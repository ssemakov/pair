package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const usage = `pair — link agentic-coding sessions to git branches

usage:
  pair [-v|--verbose] codex [args...]  wrap codex, capture session
  pair [-v|--verbose] claude [args...] wrap claude, capture session

  pair list [--here|--repo|--branch=N] list sessions
  pair last [n] [claude|codex]         resume the n-th most-recent session on this repo+branch (default 1),
                                       optionally filtered by agent
  pair resume <id>                     resume by pair-id or cli-session-id
  pair register --cli C --session S    insert a session manually
  pair forget <id>                     remove a session from the index

global flags:
  -v, --verbose        emit debug output to stderr (also: PAIR_VERBOSE=1)

env:
  PAIR_DATA_DIR        override storage dir (default: $XDG_DATA_HOME/pair or ~/.local/share/pair)
  PAIR_<CLI>_PATTERN   override the regex used to scrape the session id from <CLI>'s stdout
  PAIR_VERBOSE         if set to a non-empty value, behaves like --verbose
`

var verbose bool

func debugf(format string, args ...any) {
	if !verbose {
		return
	}
	fmt.Fprintf(os.Stderr, "pair[debug]: "+format+"\n", args...)
}

func main() {
	if v := os.Getenv("PAIR_VERBOSE"); v != "" && v != "0" {
		verbose = true
	}
	rest := make([]string, 0, len(os.Args)-1)
	for _, a := range os.Args[1:] {
		if a == "-v" || a == "--verbose" {
			verbose = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) < 1 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := rest[0], rest[1:]
	debugf("cmd=%q args=%v", cmd, args)

	var err error
	switch cmd {
	case "codex", "claude":
		err = cmdWrap(cmd, args)
	case "list":
		err = cmdList(args)
	case "last":
		err = cmdLast(args)
	case "resume":
		err = cmdResume(args)
	case "register":
		err = cmdRegister(args)
	case "forget":
		err = cmdForget(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "pair: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "pair: %v\n", err)
		os.Exit(1)
	}
}

func cmdWrap(cli string, args []string) error {
	info, err := snapshotRepo()
	if err != nil {
		// Not in a repo? Run the CLI anyway, just don't index.
		fmt.Fprintf(os.Stderr, "pair: %v — running %s without indexing\n", err, cli)
		return execPassthrough(cli, args)
	}
	debugf("repo=%s branch=%s pr_url=%q", info.Repo, info.Branch, info.PRURL)

	startedAt := time.Now()
	debugf("launching %s with %d args", cli, len(args))
	cliSessionID, runErr := runWrapped(cli, args)
	debugf("wrapped %s exited: session_id=%q err=%v elapsed=%s", cli, cliSessionID, runErr, time.Since(startedAt))

	if cliSessionID == "" {
		fmt.Fprintf(os.Stderr, "pair: could not detect %s session id (set PAIR_%s_PATTERN if its output format changed); not indexing this run\n", cli, toUpperASCII(cli))
		return runErr
	}

	db, err := openIndex()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pair: open index: %v (session id %s left unindexed)\n", err, cliSessionID)
		return runErr
	}
	defer db.Close()

	s := Session{
		ID:           uuid.NewString(),
		CLI:          cli,
		CLISessionID: cliSessionID,
		Repo:         info.Repo,
		Branch:       info.Branch,
		PRURL:        info.PRURL,
		StartedAt:    startedAt,
	}
	if err := insertSession(db, s); err != nil {
		fmt.Fprintf(os.Stderr, "pair: insert session: %v\n", err)
		return runErr
	}
	debugf("inserted session pair_id=%s", s.ID)
	fmt.Fprintf(os.Stderr, "pair: indexed %s session %s on %s@%s\n", cli, cliSessionID, shortRepo(info.Repo), info.Branch)
	return runErr
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	here := fs.Bool("here", false, "current repo + current branch")
	repoOnly := fs.Bool("repo", false, "current repo, any branch")
	branch := fs.String("branch", "", "filter by branch")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()

	f := listFilter{}
	if *here || *repoOnly || *branch != "" {
		info, gerr := snapshotRepo()
		if *here || *repoOnly {
			if gerr != nil {
				return gerr
			}
			f.Repo = info.Repo
		}
		if *here {
			f.Branch = info.Branch
		}
		if *branch != "" {
			f.Branch = *branch
		}
	}
	rows, err := listSessions(db, f)
	if err != nil {
		return err
	}
	printSessions(rows)
	return nil
}

func cmdLast(args []string) error {
	n := 1
	cli := ""
	for _, a := range args {
		switch a {
		case "claude", "codex":
			if cli != "" {
				return fmt.Errorf("last: agent specified twice (%q and %q)", cli, a)
			}
			cli = a
		default:
			v, err := strconv.Atoi(a)
			if err != nil || v < 1 {
				return fmt.Errorf("last: expected a positive integer or agent (claude|codex), got %q", a)
			}
			n = v
		}
	}
	info, err := snapshotRepo()
	if err != nil {
		return err
	}
	db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := listSessions(db, listFilter{Repo: info.Repo, Branch: info.Branch, CLI: cli})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		where := fmt.Sprintf("%s@%s", shortRepo(info.Repo), info.Branch)
		if cli != "" {
			where = cli + " on " + where
		}
		return fmt.Errorf("no sessions for %s", where)
	}
	if n > len(rows) {
		return fmt.Errorf("only %d session(s) on this branch", len(rows))
	}
	printSessions(rows)
	pick := rows[n-1]
	fmt.Fprintf(os.Stderr, "\npair: resuming #%d → %s %s\n", n, pick.CLI, pick.CLISessionID)
	return resumeExec(pick)
}

func cmdResume(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("resume: expected exactly one id")
	}
	db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	s, err := findSession(db, args[0])
	if err != nil {
		return err
	}
	return resumeExec(s)
}

func cmdRegister(args []string) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	cli := fs.String("cli", "", "cli name (codex, claude, ...)")
	cliSession := fs.String("session", "", "the cli's own session id")
	repo := fs.String("repo", "", "absolute repo path (defaults to current repo)")
	branch := fs.String("branch", "", "branch (defaults to current branch)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cli == "" || *cliSession == "" {
		return fmt.Errorf("register: --cli and --session are required")
	}
	if *repo == "" || *branch == "" {
		info, err := snapshotRepo()
		if err != nil {
			return fmt.Errorf("register: --repo and --branch required when not inside a git repo")
		}
		if *repo == "" {
			*repo = info.Repo
		}
		if *branch == "" {
			*branch = info.Branch
		}
	}
	db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	s := Session{
		ID:           uuid.NewString(),
		CLI:          *cli,
		CLISessionID: *cliSession,
		Repo:         *repo,
		Branch:       *branch,
		StartedAt:    time.Now(),
	}
	if err := insertSession(db, s); err != nil {
		return err
	}
	fmt.Printf("registered %s session %s on %s@%s\n", *cli, *cliSession, shortRepo(*repo), *branch)
	return nil
}

func cmdForget(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("forget: expected exactly one id")
	}
	db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	n, err := deleteSession(db, args[0])
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no session matched %q", args[0])
	}
	fmt.Printf("forgot %d session(s)\n", n)
	return nil
}

// resumeArgv returns the argv used to resume a session for a given CLI.
// Different CLIs spell it differently: codex uses `codex resume <id>`,
// claude uses `claude --resume <id>`.
func resumeArgv(cli, sessionID string) []string {
	switch cli {
	case "claude":
		return []string{cli, "--resume", sessionID}
	default:
		return []string{cli, "resume", sessionID}
	}
}

// resumeExec runs the CLI's resume invocation under a PTY so we can scan
// for an updated session id on exit and index it if it's not already known.
func resumeExec(s Session) error {
	if _, err := exec.LookPath(s.CLI); err != nil {
		return fmt.Errorf("%s: not found in PATH", s.CLI)
	}
	argv := resumeArgv(s.CLI, s.CLISessionID)
	debugf("wrap-resume %v", argv)

	startedAt := time.Now()
	captured, runErr := runWrapped(s.CLI, argv[1:])
	debugf("resume exited: captured_session_id=%q err=%v elapsed=%s", captured, runErr, time.Since(startedAt))

	newID := captured
	if newID == "" {
		newID = s.CLISessionID
	}

	db, err := openIndex()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pair: open index: %v (resumed session %s left unindexed)\n", err, newID)
		return runErr
	}
	defer db.Close()

	if _, err := findSession(db, newID); err == nil {
		debugf("resumed session %s already indexed", newID)
		return runErr
	}

	// By design: a resumed session stays attached to the repo+branch it was
	// originally indexed against, even if the user has since switched
	// branches or cwd. Resuming is a continuation, not a new session, so we
	// deliberately do NOT call snapshotRepo() here.
	ns := Session{
		ID:           uuid.NewString(),
		CLI:          s.CLI,
		CLISessionID: newID,
		Repo:         s.Repo,
		Branch:       s.Branch,
		StartedAt:    startedAt,
	}
	if err := insertSession(db, ns); err != nil {
		fmt.Fprintf(os.Stderr, "pair: insert resumed session: %v\n", err)
		return runErr
	}
	debugf("indexed resumed session pair_id=%s cli_session_id=%s", ns.ID, newID)
	fmt.Fprintf(os.Stderr, "pair: indexed %s session %s on %s@%s\n", s.CLI, newID, shortRepo(s.Repo), s.Branch)
	return runErr
}

// execPassthrough runs cli with stdio inherited; used when we can't index.
func execPassthrough(cli string, args []string) error {
	bin, err := exec.LookPath(cli)
	if err != nil {
		return fmt.Errorf("%s: not found in PATH", cli)
	}
	return syscall.Exec(bin, append([]string{cli}, args...), os.Environ())
}
