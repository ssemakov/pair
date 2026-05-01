package main

import (
	"errors"
	"os/exec"
	"strings"
)

type repoInfo struct {
	Repo   string
	Branch string
	PRURL  string
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
	info := repoInfo{Repo: repo, Branch: branch}
	info.PRURL = lookupPRURL(branch) // best-effort, never blocks
	return info, nil
}

func runGit(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// lookupPRURL is best-effort. Returns "" if `gh` is missing or the call fails.
func lookupPRURL(branch string) string {
	if _, err := exec.LookPath("gh"); err != nil {
		return ""
	}
	out, err := exec.Command("gh", "pr", "list", "--head", branch, "--state", "open", "--json", "url", "--jq", ".[0].url // \"\"").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
