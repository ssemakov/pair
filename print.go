package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func printSessions(rows []Session) {
	if len(rows) == 0 {
		fmt.Println("(no sessions)")
		return
	}
	for i, s := range rows {
		fmt.Printf("%2d  %-7s  %s  %s@%s  %s%s\n",
			i+1,
			s.CLI,
			s.StartedAt.Local().Format(time.RFC3339),
			shortRepo(s.Repo),
			s.Branch,
			truncID(s.CLISessionID),
			prSuffix(s.PRURL),
		)
	}
}

func shortRepo(p string) string {
	if p == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && len(rel) < len(p) && rel != "." && rel[0] != '.' {
			return "~/" + rel
		}
	}
	return filepath.Base(p)
}

func truncID(s string) string {
	if len(s) > 12 {
		return s[:8] + "…"
	}
	return s
}

func prSuffix(url string) string {
	if url == "" {
		return ""
	}
	return "  " + url
}
