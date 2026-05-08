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
	now := time.Now()
	for i, s := range rows {
		stamp := s.StartedAt.Local().Format(time.RFC3339) + " (" + humanAge(now.Sub(s.StartedAt))
		if !s.UpdatedAt.IsZero() && s.UpdatedAt.After(s.StartedAt.Add(time.Second)) {
			stamp += ", resumed " + humanAge(now.Sub(s.UpdatedAt))
		}
		stamp += ")"
		fmt.Printf("%2d  %-7s  %s  %s@%s  %s%s\n",
			i+1,
			s.CLI,
			stamp,
			shortRepo(s.Repo),
			s.Branch,
			s.CLISessionID,
			prSuffix(s.PRURL),
		)
	}
}

// humanAge returns a short, human-friendly string for the duration `d`.
// Negative durations (clock skew, etc.) are treated as "now".
func humanAge(d time.Duration) string {
	if d < time.Second {
		return "now"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
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

func prSuffix(url string) string {
	if url == "" {
		return ""
	}
	return "  " + url
}
