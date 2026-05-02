package main

import (
	"database/sql"
	"path/filepath"
	"sort"
	"testing"
	"time"
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
