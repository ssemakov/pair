# pair

`pair` links agentic-coding sessions (Claude Code, Codex) to the git branch
they ran on, so you can resume the right one later instead of digging through
shell history.

It works by wrapping the underlying CLI under a PTY, scraping its session id
from the output, and indexing `(cli, cli_session_id, repo, branch, started_at)`
into a small SQLite database.

## Install

```sh
make install            # builds and copies the `pair` binary to ~/bin
# or:
go install ./...
```

Make sure `~/bin` (or wherever you installed) is on your `PATH`.

## Usage

```
pair [-v|--verbose] codex [args...]   wrap codex, capture session
pair [-v|--verbose] claude [args...]  wrap claude, capture session

pair list [--here|--repo|--branch=N] [--updated] [claude|codex]
                                      list sessions, optionally filtered by agent;
                                      --updated orders by last-resumed time
pair last [n] [--updated] [claude|codex]
                                      resume the n-th most-recent session on
                                      this repo+branch (default 1). n indexes
                                      the same filtered view list would print.
                                      --updated picks by last-resumed time.
pair resume <id>                      resume by pair-id or cli-session-id
pair register --cli C --session S     insert a session manually
pair forget <id>                      remove a session from the index
pair prune [--branch=N|--repo[=P]|--all]
                                      keep the most-recent session per
                                      repo+branch+cli and forget the rest
pair version                          print the version string
```

### Two notions of "most recent"

Sessions have two timestamps: `started_at` (when you launched it) and
`updated_at` (when you most recently exited a wrapped run of it — fresh or
resumed). `pair list` and `pair last` default to `started_at` ordering,
which is stable: pruning, exiting, or resuming never permutes the list.
Pass `--updated` to either command for the other view ("which was I just
working in?"):

```sh
pair list --here --updated
pair last --updated     # most recently used, regardless of when it started
```

Each row in `pair list` shows the start timestamp, an age in parens, and a
"resumed Xm ago" suffix when `updated_at` differs from `started_at`.

### Wrapping a session

Run `pair claude` or `pair codex` exactly as you'd run the CLI — args,
stdin/stdout, raw mode, and window resizes are all forwarded:

```sh
pair claude
pair codex --some-flag
```

As soon as the wrapped CLI prints its session id, `pair` records:

- the CLI's own session id (scraped from its output)
- the absolute repo path and current branch
- the open PR url for that branch, if `gh` is installed
- `started_at` and `updated_at` timestamps

The row is inserted **at session-id capture time, not at exit**, so a
SIGKILL (e.g. tmux server restart) doesn't lose the session. When the run
exits cleanly, `updated_at` is bumped to the exit time.

### Listing & resuming

```sh
pair list --here              # this repo + this branch
pair list --repo              # this repo, any branch
pair list --branch=feat-x     # any repo, this branch
pair list --here claude       # this repo + branch, claude only

pair last                     # resume the newest session for this repo+branch
pair last 2                   # resume the 2nd-newest
pair last claude              # only consider claude sessions
pair last 2 codex             # 2nd-newest codex session

pair resume <pair-id-or-cli-session-id>
```

The number `n` in `pair last n [agent]` always indexes into the filtered view
that `pair list --here [agent]` would print, so the row numbers you see in
`pair list --here claude` are the same numbers you can pass to
`pair last n claude`.

`resume` and `last` invoke the CLI with the spelling each tool expects:
`claude --resume <id>` and `codex resume <id>`. The resumed run is itself
wrapped, so if a new session id appears on exit it is indexed (the original
repo+branch is preserved — see `resumeExec` in `main.go`).

### Pruning

`pair prune` keeps the most-recent session per `(repo, branch, cli)` and
forgets the rest. Default scope is the current repo+branch. Other scopes:

```sh
pair prune                    # this repo + this branch
pair prune --branch=feat-x    # this repo, branch feat-x
pair prune --repo             # this repo, all branches
pair prune --repo=/path/to/r  # repo at /path/to/r, all branches
pair prune --all              # every repo + branch
```

`--branch`, `--repo`, and `--all` are mutually exclusive. Pruned sessions are
printed in the same format as `pair list`.

### Environment variables

| Variable | Purpose |
| --- | --- |
| `PAIR_DATA_DIR` | override storage dir (default: `$XDG_DATA_HOME/pair` or `~/.local/share/pair`) |
| `PAIR_<CLI>_PATTERN` | override the regex used to scrape the session id from `<CLI>`'s stdout. Must contain exactly one capture group |
| `PAIR_VERBOSE` | if non-empty, behaves like `--verbose` |

The session-id patterns ship with sensible defaults — they look both for
`Session ID: <uuid>` banners and for the resume hints (`claude --resume …`,
`codex resume …`) the tools print at the end. Override them if a future
release changes the format.

## Storage

A single SQLite file at `$PAIR_DATA_DIR/index.sqlite`:

```sql
CREATE TABLE sessions (
    id              TEXT PRIMARY KEY,            -- pair's own uuid
    cli             TEXT NOT NULL,
    cli_session_id  TEXT NOT NULL,
    repo            TEXT NOT NULL,
    branch          TEXT NOT NULL,
    pr_url          TEXT,
    started_at      INTEGER NOT NULL,            -- unix seconds
    updated_at      INTEGER NOT NULL DEFAULT 0   -- unix seconds; bumped on each clean exit
);
```

Schema upgrades are applied automatically the next time `pair` opens the
index — no manual step. The migration is idempotent, so repeated runs are
no-ops. If you go back to an older binary, it just ignores the new column.

It's safe to delete the file; the next wrapped run will recreate it.

## Development

```sh
make build              # build the binary; embeds `git describe` as the version
make test               # run tests
make fmt vet            # formatting / static checks
make tidy               # go mod tidy
```

Override the version manually with `make build VERSION=1.2.3`.

Tests live alongside the source (`*_test.go`) and use a temp `PAIR_DATA_DIR`,
so they don't touch your real session index.
