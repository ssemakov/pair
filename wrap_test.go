package main

import (
	"strings"
	"testing"
)

func TestPatternForBuiltinClaude(t *testing.T) {
	t.Setenv("PAIR_claude_PATTERN", "")
	t.Setenv("PAIR_CLAUDE_PATTERN", "")
	pat, err := patternFor("claude")
	if err != nil {
		t.Fatalf("patternFor(claude): %v", err)
	}
	cases := []struct {
		in   string
		want string
	}{
		{"Session ID: aa8007cb-fe50-4fcc-a523-4bdccc301334", "aa8007cb-fe50-4fcc-a523-4bdccc301334"},
		{"session_id=aa8007cb-fe50-4fcc-a523-4bdccc301334", "aa8007cb-fe50-4fcc-a523-4bdccc301334"},
		{"Resume this session with:\nclaude --resume aa8007cb-fe50-4fcc-a523-4bdccc301334\n", "aa8007cb-fe50-4fcc-a523-4bdccc301334"},
	}
	for _, tc := range cases {
		m := pat.FindStringSubmatch(tc.in)
		if len(m) < 2 || m[1] != tc.want {
			t.Errorf("input %q: got %v, want %s", tc.in, m, tc.want)
		}
	}
}

func TestPatternForBuiltinCodex(t *testing.T) {
	t.Setenv("PAIR_codex_PATTERN", "")
	t.Setenv("PAIR_CODEX_PATTERN", "")
	pat, err := patternFor("codex")
	if err != nil {
		t.Fatalf("patternFor(codex): %v", err)
	}
	cases := []struct {
		in   string
		want string
	}{
		{"Session ID: 019de826-c84c-7041-b2af-b74d45c8c2cb", "019de826-c84c-7041-b2af-b74d45c8c2cb"},
		{"To continue this session, run codex resume 019de826-c84c-7041-b2af-b74d45c8c2cb\n", "019de826-c84c-7041-b2af-b74d45c8c2cb"},
	}
	for _, tc := range cases {
		m := pat.FindStringSubmatch(tc.in)
		if len(m) < 2 || m[1] != tc.want {
			t.Errorf("input %q: got %v, want %s", tc.in, m, tc.want)
		}
	}
}

func TestPatternForEnvOverride(t *testing.T) {
	t.Setenv("PAIR_CLAUDE_PATTERN", `custom=([0-9]+)`)
	pat, err := patternFor("claude")
	if err != nil {
		t.Fatalf("patternFor: %v", err)
	}
	m := pat.FindStringSubmatch("custom=42")
	if len(m) < 2 || m[1] != "42" {
		t.Fatalf("override not used: got %v", m)
	}
}

func TestPatternForUnknownCLI(t *testing.T) {
	if _, err := patternFor("nope"); err == nil {
		t.Fatal("expected error for unknown cli")
	}
}

func TestSessionScannerCapturesLastMatch(t *testing.T) {
	pat, err := patternFor("claude")
	if err != nil {
		t.Fatal(err)
	}
	s := newSessionScanner(pat, nil)
	chunks := []string{
		"banner Session ID: 11111111-1111-1111-1111-111111111111\n",
		"some other output\n",
		"Resume this session with:\nclaude --resume 22222222-2222-2222-2222-222222222222\n",
	}
	for _, c := range chunks {
		if _, err := s.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.ID(); got != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("got %q, want last match", got)
	}
}

func TestSessionScannerHandlesSplitWrites(t *testing.T) {
	pat, err := patternFor("codex")
	if err != nil {
		t.Fatal(err)
	}
	s := newSessionScanner(pat, nil)
	full := "To continue this session, run codex resume 019de826-c84c-7041-b2af-b74d45c8c2cb\n"
	for i := 0; i < len(full); i++ {
		if _, err := s.Write([]byte{full[i]}); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.ID(); got != "019de826-c84c-7041-b2af-b74d45c8c2cb" {
		t.Fatalf("got %q, byte-by-byte writes lost the match", got)
	}
}

func TestSessionScannerTailTruncation(t *testing.T) {
	pat, err := patternFor("claude")
	if err != nil {
		t.Fatal(err)
	}
	s := newSessionScanner(pat, nil)
	// Push more than maxTail of noise, then a match. The match should still be captured.
	noise := strings.Repeat("x", 128*1024)
	if _, err := s.Write([]byte(noise)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("Session ID: aa8007cb-fe50-4fcc-a523-4bdccc301334\n")); err != nil {
		t.Fatal(err)
	}
	if got := s.ID(); got != "aa8007cb-fe50-4fcc-a523-4bdccc301334" {
		t.Fatalf("got %q, want match after truncation", got)
	}
}

func TestToUpperASCII(t *testing.T) {
	if got := toUpperASCII("claude"); got != "CLAUDE" {
		t.Errorf("got %q", got)
	}
	if got := toUpperASCII("Codex123"); got != "CODEX123" {
		t.Errorf("got %q", got)
	}
}
