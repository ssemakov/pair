package main

import (
	"database/sql"
	"fmt"
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
    started_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_repo_branch ON sessions(repo, branch, started_at DESC);
`

func insertSession(db *sql.DB, s Session) error {
	_, err := db.Exec(
		`INSERT INTO sessions (id, cli, cli_session_id, repo, branch, pr_url, started_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.CLI, s.CLISessionID, s.Repo, s.Branch, nullIfEmpty(s.PRURL), s.StartedAt.Unix(),
	)
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
	Repo   string
	Branch string
}

func listSessions(db *sql.DB, f listFilter) ([]Session, error) {
	q := `SELECT id, cli, cli_session_id, repo, branch, COALESCE(pr_url, ''), started_at FROM sessions`
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
	q += where + " ORDER BY started_at DESC"
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		var ts int64
		if err := rows.Scan(&s.ID, &s.CLI, &s.CLISessionID, &s.Repo, &s.Branch, &s.PRURL, &ts); err != nil {
			return nil, err
		}
		s.StartedAt = time.Unix(ts, 0)
		out = append(out, s)
	}
	return out, rows.Err()
}

func findSession(db *sql.DB, idOrCLISession string) (Session, error) {
	row := db.QueryRow(
		`SELECT id, cli, cli_session_id, repo, branch, COALESCE(pr_url, ''), started_at
		   FROM sessions WHERE id = ? OR cli_session_id = ? LIMIT 1`,
		idOrCLISession, idOrCLISession,
	)
	var s Session
	var ts int64
	if err := row.Scan(&s.ID, &s.CLI, &s.CLISessionID, &s.Repo, &s.Branch, &s.PRURL, &ts); err != nil {
		if err == sql.ErrNoRows {
			return Session{}, fmt.Errorf("no session with id %q", idOrCLISession)
		}
		return Session{}, err
	}
	s.StartedAt = time.Unix(ts, 0)
	return s, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
