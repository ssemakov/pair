package main

import (
	"database/sql"
	"path/filepath"
	"sort"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PAIR_DATA_DIR", dir)
	// openIndex reads PAIR_DATA_DIR.
	db, err := openIndex()
	if err != nil {
		t.Fatalf("openIndex: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// Sanity check: index file lives in our temp dir.
	if _, err := db.Exec("SELECT 1"); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if got, _ := indexPath(); filepath.Dir(got) != dir {
		t.Fatalf("indexPath %q not under %q", got, dir)
	}
	return db
}

// seed inserts a session and returns its id. Time is offset minutes from now.
func seed(t *testing.T, db *sql.DB, cli, repo, branch string, offsetMin int) string {
	t.Helper()
	s := Session{
		ID:           cli + "-" + repo + "-" + branch + "-" + time.Now().Format("150405.000000000"),
		CLI:          cli,
		CLISessionID: cli + "-cli-" + branch + "-" + time.Now().Format("150405.000000000"),
		Repo:         repo,
		Branch:       branch,
		StartedAt:    time.Now().Add(time.Duration(offsetMin) * time.Minute),
	}
	if err := insertSession(db, s); err != nil {
		t.Fatalf("insertSession: %v", err)
	}
	return s.ID
}

func ids(rows []Session) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	sort.Strings(out)
	return out
}

func TestListSessionsFilters(t *testing.T) {
	db := openTestDB(t)
	a := seed(t, db, "claude", "/r1", "main", -10)
	b := seed(t, db, "codex", "/r1", "main", -5)
	c := seed(t, db, "claude", "/r1", "feat", -3)
	d := seed(t, db, "claude", "/r2", "main", -1)

	cases := []struct {
		name string
		f    listFilter
		want []string
	}{
		{"all", listFilter{}, []string{a, b, c, d}},
		{"repo r1", listFilter{Repo: "/r1"}, []string{a, b, c}},
		{"repo r1 main", listFilter{Repo: "/r1", Branch: "main"}, []string{a, b}},
		{"cli claude", listFilter{CLI: "claude"}, []string{a, c, d}},
		{"r1 main claude", listFilter{Repo: "/r1", Branch: "main", CLI: "claude"}, []string{a}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := listSessions(db, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			got := ids(rows)
			sort.Strings(tc.want)
			if !equalStrings(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListSessionsOrderDesc(t *testing.T) {
	db := openTestDB(t)
	old := seed(t, db, "claude", "/r1", "main", -30)
	mid := seed(t, db, "claude", "/r1", "main", -10)
	newest := seed(t, db, "claude", "/r1", "main", -1)
	rows, err := listSessions(db, listFilter{Repo: "/r1", Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].ID != newest || rows[1].ID != mid || rows[2].ID != old {
		t.Fatalf("ordering wrong: %+v", rows)
	}
}

func TestPruneKeepsLatestPerRepoBranchCli(t *testing.T) {
	db := openTestDB(t)
	// Group A: r1/main/claude — three sessions, expect newest to survive
	aOld := seed(t, db, "claude", "/r1", "main", -30)
	aMid := seed(t, db, "claude", "/r1", "main", -20)
	aNew := seed(t, db, "claude", "/r1", "main", -1)
	// Group B: r1/main/codex — distinct cli, must be preserved
	bOnly := seed(t, db, "codex", "/r1", "main", -15)
	// Group C: r1/feat/claude — different branch, preserved
	cOnly := seed(t, db, "claude", "/r1", "feat", -5)
	// Group D: r2/main/claude — different repo, preserved
	dOnly := seed(t, db, "claude", "/r2", "main", -2)

	pruned, err := pruneSessions(db, listFilter{})
	if err != nil {
		t.Fatal(err)
	}
	gotPruned := ids(pruned)
	wantPruned := []string{aOld, aMid}
	sort.Strings(wantPruned)
	if !equalStrings(gotPruned, wantPruned) {
		t.Errorf("pruned %v, want %v", gotPruned, wantPruned)
	}

	rows, err := listSessions(db, listFilter{})
	if err != nil {
		t.Fatal(err)
	}
	gotKept := ids(rows)
	wantKept := []string{aNew, bOnly, cOnly, dOnly}
	sort.Strings(wantKept)
	if !equalStrings(gotKept, wantKept) {
		t.Errorf("kept %v, want %v", gotKept, wantKept)
	}
}

func TestPruneScopedToRepoBranch(t *testing.T) {
	db := openTestDB(t)
	keep1 := seed(t, db, "claude", "/r1", "main", -1)
	drop1 := seed(t, db, "claude", "/r1", "main", -10)
	// Outside scope — must not be touched.
	otherBranch := seed(t, db, "claude", "/r1", "feat", -20)
	otherRepo := seed(t, db, "claude", "/r2", "main", -20)

	pruned, err := pruneSessions(db, listFilter{Repo: "/r1", Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(pruned); !equalStrings(got, []string{drop1}) {
		t.Errorf("pruned %v, want [%s]", got, drop1)
	}

	rows, _ := listSessions(db, listFilter{})
	gotKept := ids(rows)
	wantKept := []string{keep1, otherBranch, otherRepo}
	sort.Strings(wantKept)
	if !equalStrings(gotKept, wantKept) {
		t.Errorf("kept %v, want %v", gotKept, wantKept)
	}
}

func TestPruneEmpty(t *testing.T) {
	db := openTestDB(t)
	pruned, err := pruneSessions(db, listFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 0 {
		t.Errorf("expected nothing pruned, got %v", pruned)
	}
}

func TestFindSessionByEitherID(t *testing.T) {
	db := openTestDB(t)
	id := seed(t, db, "claude", "/r1", "main", -1)
	s, err := findSession(db, id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := findSession(db, s.CLISessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id {
		t.Errorf("by cli_session_id: got %s, want %s", got.ID, id)
	}
	if _, err := findSession(db, "no-such-id"); err == nil {
		t.Error("expected error for unknown id")
	}
}

func TestDeleteSession(t *testing.T) {
	db := openTestDB(t)
	id := seed(t, db, "claude", "/r1", "main", -1)
	n, err := deleteSession(db, id)
	if err != nil || n != 1 {
		t.Fatalf("delete: n=%d err=%v", n, err)
	}
	if _, err := findSession(db, id); err == nil {
		t.Error("session still present after delete")
	}
}

func TestMigrationAddsUpdatedAtColumn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PAIR_DATA_DIR", dir)
	path, err := indexPath()
	if err != nil {
		t.Fatal(err)
	}

	// Build an "old" DB without updated_at.
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		cli TEXT NOT NULL,
		cli_session_id TEXT NOT NULL,
		repo TEXT NOT NULL,
		branch TEXT NOT NULL,
		pr_url TEXT,
		started_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	startTs := time.Now().Add(-2 * time.Hour).Unix()
	if _, err := old.Exec(
		`INSERT INTO sessions (id, cli, cli_session_id, repo, branch, started_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"old1", "claude", "ccs1", "/r1", "main", startTs,
	); err != nil {
		t.Fatal(err)
	}
	old.Close()

	// Open via openIndex to trigger migration.
	db, err := openIndex()
	if err != nil {
		t.Fatalf("openIndex post-migration: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cols, err := tableColumns(db, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if !cols["updated_at"] {
		t.Fatal("updated_at column missing after migration")
	}

	s, err := findSession(db, "old1")
	if err != nil {
		t.Fatal(err)
	}
	if s.UpdatedAt.Unix() != startTs {
		t.Errorf("updated_at = %d, want %d (backfilled to started_at)", s.UpdatedAt.Unix(), startTs)
	}

	// Re-running migration should be a no-op (idempotent).
	if err := migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestInsertSessionDefaultsUpdatedAtToStartedAt(t *testing.T) {
	db := openTestDB(t)
	st := time.Now().Add(-1 * time.Hour)
	s := Session{
		ID: "x", CLI: "claude", CLISessionID: "cs", Repo: "/r", Branch: "main",
		StartedAt: st,
	}
	if err := insertSession(db, s); err != nil {
		t.Fatal(err)
	}
	got, err := findSession(db, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt.Unix() != st.Unix() {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, st)
	}
}

func TestTouchSessionBumpsUpdatedAt(t *testing.T) {
	db := openTestDB(t)
	id := seed(t, db, "claude", "/r", "main", -60)
	before, err := findSession(db, id)
	if err != nil {
		t.Fatal(err)
	}
	bump := time.Now()
	if err := touchSession(db, id, bump); err != nil {
		t.Fatal(err)
	}
	after, err := findSession(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.UpdatedAt.Unix() != bump.Unix() {
		t.Errorf("UpdatedAt = %v, want %v", after.UpdatedAt, bump)
	}
	if !after.StartedAt.Equal(before.StartedAt) {
		t.Errorf("StartedAt drifted: %v -> %v", before.StartedAt, after.StartedAt)
	}
}

func TestListSessionsOrderByUpdated(t *testing.T) {
	db := openTestDB(t)
	// Three sessions: A is newest by started_at, B oldest, C in the middle.
	a := seed(t, db, "claude", "/r", "main", -1)
	b := seed(t, db, "claude", "/r", "main", -30)
	c := seed(t, db, "claude", "/r", "main", -10)

	// Now bump B to be the newest by updated_at.
	if err := touchSession(db, b, time.Now()); err != nil {
		t.Fatal(err)
	}
	// And bump C to a slightly-earlier point.
	if err := touchSession(db, c, time.Now().Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}

	rowsByStarted, err := listSessions(db, listFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsByStarted) != 3 || rowsByStarted[0].ID != a {
		t.Fatalf("started ordering wrong: %+v", ids(rowsByStarted))
	}

	rowsByUpdated, err := listSessions(db, listFilter{OrderByUpdated: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsByUpdated) != 3 ||
		rowsByUpdated[0].ID != b ||
		rowsByUpdated[1].ID != c ||
		rowsByUpdated[2].ID != a {
		t.Fatalf("updated ordering wrong: got %v, want [%s %s %s]",
			ids(rowsByUpdated), b, c, a)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
