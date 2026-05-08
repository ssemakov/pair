package main

import (
	"errors"
	"os/exec"
	"strings"
)

type repoInfo struct {
	Repo   string
	Branch string
}

func snapshotRepo() (repoInfo, error) {
	repo, err := runGit("rev-parse", "--show-toplevel")
	if err != nil {
		return repoInfo{}, errors.New("not inside a git repository")
	}
	branch, err := runGit("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return repoInfo{}, err
	}
	return repoInfo{Repo: repo, Branch: branch}, nil
}

func runGit(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// lookupPRURL is best-effort. Returns "" if `gh` is missing, the repo isn't
// on GitHub, no open PR matches the branch, or the call fails for any other
// reason. It's safe (and intended) to run in a goroutine in parallel with
// the rest of `pair`'s startup work.
func lookupPRURL(branch string) string {
	if _, err := exec.LookPath("gh"); err != nil {
		debugf("gh not found in PATH; skipping pr lookup")
		return ""
	}
	out, err := exec.Command("gh", "pr", "list", "--head", branch, "--state", "open", "--json", "url", "--jq", ".[0].url // \"\"").Output()
	if err != nil {
		debugf("gh pr list failed: %v", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}
