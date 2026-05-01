package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// cliPattern is the regex used to scan the wrapped CLI's stdout for its
// session id. Each pattern must contain exactly one capture group.
//
// These are reasonable defaults — both codex and claude print a session id
// somewhere in their startup/exit banner. They can be overridden via env:
//
//	PAIR_CODEX_PATTERN=...
//	PAIR_CLAUDE_PATTERN=...
//
// The capture group is what gets stored as cli_session_id.
var cliPattern = map[string]string{
	"codex":  `(?i)session(?:[ _-]?id)?[:\s=]+([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`,
	"claude": `(?i)session(?:[ _-]?id)?[:\s=]+([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`,
}

func patternFor(cli string) (*regexp.Regexp, error) {
	if v := os.Getenv("PAIR_" + cli + "_PATTERN"); v != "" {
		return regexp.Compile(v)
	}
	if v := os.Getenv("PAIR_" + toUpperASCII(cli) + "_PATTERN"); v != "" {
		return regexp.Compile(v)
	}
	p, ok := cliPattern[cli]
	if !ok {
		return nil, fmt.Errorf("no session-id pattern configured for cli %q", cli)
	}
	return regexp.Compile(p)
}

func toUpperASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

// runWrapped spawns `cli args...` under a PTY, tees its output to the user's
// terminal while scanning for the CLI's session id, and returns it on exit.
func runWrapped(cli string, args []string) (string, error) {
	if _, err := exec.LookPath(cli); err != nil {
		return "", fmt.Errorf("%s: not found in PATH", cli)
	}
	pat, err := patternFor(cli)
	if err != nil {
		return "", err
	}

	cmd := exec.Command(cli, args...)
	cmd.Env = os.Environ()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return "", err
	}
	defer ptmx.Close()

	// Forward window-size changes.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	winch <- syscall.SIGWINCH // initial size
	defer func() {
		signal.Stop(winch)
		close(winch)
	}()

	// Set stdin to raw mode if it's a tty so the child sees keystrokes raw.
	var oldState *term.State
	if term.IsTerminal(int(os.Stdin.Fd())) {
		oldState, err = term.MakeRaw(int(os.Stdin.Fd()))
		if err == nil {
			defer term.Restore(int(os.Stdin.Fd()), oldState)
		}
	}

	// stdin → pty
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()

	// pty → (stdout + scanner)
	scanner := newSessionScanner(pat)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.MultiWriter(os.Stdout, scanner), ptmx)
	}()

	werr := cmd.Wait()
	wg.Wait() // drain remaining pty output

	if oldState != nil {
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}

	id := scanner.ID()
	if werr != nil {
		// Surface the child's exit error but still return any captured id.
		return id, werr
	}
	return id, nil
}

// sessionScanner is an io.Writer that buffers the tail of the stream and
// extracts the first regex capture-group match it sees, or the most recent if
// the CLI prints the id multiple times. We keep scanning until EOF since
// some CLIs print the id only on exit.
type sessionScanner struct {
	mu      sync.Mutex
	pat     *regexp.Regexp
	tail    []byte
	maxTail int
	last    string
}

func newSessionScanner(pat *regexp.Regexp) *sessionScanner {
	return &sessionScanner{pat: pat, maxTail: 64 * 1024}
}

func (s *sessionScanner) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tail = append(s.tail, p...)
	if len(s.tail) > s.maxTail {
		s.tail = s.tail[len(s.tail)-s.maxTail:]
	}
	// Find the last match in the current buffer.
	if m := s.pat.FindAllSubmatch(s.tail, -1); len(m) > 0 {
		s.last = string(m[len(m)-1][1])
	}
	return len(p), nil
}

func (s *sessionScanner) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}
