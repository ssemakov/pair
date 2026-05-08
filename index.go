package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Session struct {
	ID           string
	CLI          string
	CLISessionID string
	Repo         string
	Branch       string
	PRURL        string
	StartedAt    time.Time
	UpdatedAt    time.Time
}

func openIndex() (*sql.DB, error) {
	path, err := indexPath()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,
    cli             TEXT NOT NULL,
    cli_session_id  TEXT NOT NULL,
    repo            TEXT NOT NULL,
    branch          TEXT NOT NULL,
    pr_url          TEXT,
    started_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_sessions_repo_branch ON sessions(repo, branch, started_at DESC);
`

// migrate applies idempotent schema upgrades to existing index files. Each
// step checks whether it's needed before running.
func migrate(db *sql.DB) error {
	cols, err := tableColumns(db, "sessions")
	if err != nil {
		return err
	}
	if !cols["updated_at"] {
		fmt.Fprintln(os.Stderr, "pair: migrating index: adding updated_at column to sessions")
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("add updated_at column: %w", err)
		}
		if _, err := db.Exec("UPDATE sessions SET updated_at = started_at WHERE updated_at = 0"); err != nil {
			return fmt.Errorf("backfill updated_at: %w", err)
		}
	}
	return nil
}

func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func insertSession(db *sql.DB, s Session) error {
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.StartedAt
	}
	_, err := db.Exec(
		`INSERT INTO sessions (id, cli, cli_session_id, repo, branch, pr_url, started_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.CLI, s.CLISessionID, s.Repo, s.Branch, nullIfEmpty(s.PRURL), s.StartedAt.Unix(), s.UpdatedAt.Unix(),
	)
	return err
}

// touchSession bumps updated_at on the session with the given pair id.
func touchSession(db *sql.DB, pairID string, t time.Time) error {
	_, err := db.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, t.Unix(), pairID)
	return err
}

func deleteSession(db *sql.DB, id string) (int64, error) {
	res, err := db.Exec(`DELETE FROM sessions WHERE id = ? OR cli_session_id = ?`, id, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type listFilter struct {
	Repo           string
	Branch         string
	CLI            string
	OrderByUpdated bool
}

const sessionCols = `id, cli, cli_session_id, repo, branch, COALESCE(pr_url, ''), started_at, COALESCE(NULLIF(updated_at, 0), started_at)`

func scanSession(scanner interface {
	Scan(dest ...any) error
}) (Session, error) {
	var s Session
	var startedTs, updatedTs int64
	if err := scanner.Scan(&s.ID, &s.CLI, &s.CLISessionID, &s.Repo, &s.Branch, &s.PRURL, &startedTs, &updatedTs); err != nil {
		return Session{}, err
	}
	s.StartedAt = time.Unix(startedTs, 0)
	s.UpdatedAt = time.Unix(updatedTs, 0)
	return s, nil
}

func listSessions(db *sql.DB, f listFilter) ([]Session, error) {
	q := `SELECT ` + sessionCols + ` FROM sessions`
	args := []any{}
	where := ""
	add := func(clause string, v any) {
		if where == "" {
			where = " WHERE "
		} else {
			where += " AND "
		}
		where += clause
		args = append(args, v)
	}
	if f.Repo != "" {
		add("repo = ?", f.Repo)
	}
	if f.Branch != "" {
		add("branch = ?", f.Branch)
	}
	if f.CLI != "" {
		add("cli = ?", f.CLI)
	}
	order := " ORDER BY started_at DESC, id DESC"
	if f.OrderByUpdated {
		order = " ORDER BY COALESCE(NULLIF(updated_at, 0), started_at) DESC, started_at DESC, id DESC"
	}
	q += where + order
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func findSession(db *sql.DB, idOrCLISession string) (Session, error) {
	row := db.QueryRow(
		`SELECT `+sessionCols+`
		   FROM sessions WHERE id = ? OR cli_session_id = ? LIMIT 1`,
		idOrCLISession, idOrCLISession,
	)
	s, err := scanSession(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Session{}, fmt.Errorf("no session with id %q", idOrCLISession)
		}
		return Session{}, err
	}
	return s, nil
}

// pruneSessions keeps the most-recent session per (repo, branch, cli) within
// the given filter scope and deletes the rest. Returns the sessions that
// were removed, in the same order list-style commands would print them
// (most recent first).
func pruneSessions(db *sql.DB, f listFilter) ([]Session, error) {
	args := []any{}
	where := ""
	add := func(clause string, v any) {
		if where == "" {
			where = " WHERE "
		} else {
			where += " AND "
		}
		where += clause
		args = append(args, v)
	}
	if f.Repo != "" {
		add("repo = ?", f.Repo)
	}
	if f.Branch != "" {
		add("branch = ?", f.Branch)
	}
	if f.CLI != "" {
		add("cli = ?", f.CLI)
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	selectQ := `WITH ranked AS (
	    SELECT id, ROW_NUMBER() OVER (PARTITION BY repo, branch, cli ORDER BY started_at DESC, id DESC) AS rn
	    FROM sessions` + where + `
	)
	SELECT ` + sessionCols + `
	FROM sessions
	WHERE id IN (SELECT id FROM ranked WHERE rn > 1)
	ORDER BY started_at DESC`
	rows, err := tx.Query(selectQ, args...)
	if err != nil {
		return nil, err
	}
	var pruned []Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		pruned = append(pruned, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(pruned) > 0 {
		ids := make([]any, len(pruned))
		var placeholders strings.Builder
		for i, s := range pruned {
			if i > 0 {
				placeholders.WriteByte(',')
			}
			placeholders.WriteByte('?')
			ids[i] = s.ID
		}
		if _, err := tx.Exec("DELETE FROM sessions WHERE id IN ("+placeholders.String()+")", ids...); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return pruned, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
