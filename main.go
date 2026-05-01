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
  pair codex [args...]                 wrap codex, capture session
  pair claude [args...]                wrap claude, capture session

  pair list [--here|--repo|--branch=N] list sessions
  pair last [n]                        resume the n-th most-recent session on this repo+branch (default 1)
  pair resume <id>                     resume by pair-id or cli-session-id
  pair register --cli C --session S    insert a session manually
  pair forget <id>                     remove a session from the index

env:
  PAIR_DATA_DIR        override storage dir (default: $XDG_DATA_HOME/pair or ~/.local/share/pair)
  PAIR_<CLI>_PATTERN   override the regex used to scrape the session id from <CLI>'s stdout
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

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

	startedAt := time.Now()
	cliSessionID, runErr := runWrapped(cli, args)

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
	if len(args) > 0 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1 {
			return fmt.Errorf("last: expected a positive integer, got %q", args[0])
		}
		n = v
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

	rows, err := listSessions(db, listFilter{Repo: info.Repo, Branch: info.Branch})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("no sessions on %s@%s", shortRepo(info.Repo), info.Branch)
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

// resumeExec replaces the current process with `<cli> resume <cli_session_id>`.
func resumeExec(s Session) error {
	bin, err := exec.LookPath(s.CLI)
	if err != nil {
		return fmt.Errorf("%s: not found in PATH", s.CLI)
	}
	argv := []string{s.CLI, "resume", s.CLISessionID}
	return syscall.Exec(bin, argv, os.Environ())
}

// execPassthrough runs cli with stdio inherited; used when we can't index.
func execPassthrough(cli string, args []string) error {
	bin, err := exec.LookPath(cli)
	if err != nil {
		return fmt.Errorf("%s: not found in PATH", cli)
	}
	return syscall.Exec(bin, append([]string{cli}, args...), os.Environ())
}
