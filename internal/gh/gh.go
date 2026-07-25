// Package gh looks up a pull request's branch and title through the gh CLI.
//
// This deliberately does not talk to GitHub's API directly: gh already
// carries the user's auth, so there is nothing to configure here.
package gh

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// CommandRunner executes one command and returns its stdout. It exists so
// tests can fake gh's output without shelling out or hitting the network.
type CommandRunner func(dir, name string, args ...string) ([]byte, error)

// Client looks up pull requests via the gh CLI.
type Client struct {
	run CommandRunner
}

// New builds a Client that shells out to the real gh binary.
func New() *Client {
	return &Client{run: runCommand}
}

func runCommand(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// PRInfo is what LookupPR reports about a pull request.
type PRInfo struct {
	// Branch is the head branch, used verbatim: a pull request's branch is
	// whatever GitHub says it is, and guessing one would be actively wrong.
	Branch string
	Title  string
}

// LookupPR fetches a pull request's head branch and title.
func (c *Client) LookupPR(owner, repo string, number int) (PRInfo, error) {
	out, err := c.run("", "gh", "pr", "view", strconv.Itoa(number),
		"--repo", owner+"/"+repo,
		"--json", "headRefName,title")
	if err != nil {
		return PRInfo{}, fmt.Errorf("gh pr view %s/%s#%d: %w", owner, repo, number, err)
	}

	var raw struct {
		HeadRefName string `json:"headRefName"`
		Title       string `json:"title"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return PRInfo{}, fmt.Errorf("decode gh pr view output for %s/%s#%d: %w", owner, repo, number, err)
	}
	return PRInfo{Branch: raw.HeadRefName, Title: raw.Title}, nil
}

// IssueInfo is what LookupIssue reports about an issue.
//
// There is no branch here, unlike a pull request: an issue names work that has
// not started, so the branch is derived from the title rather than read off
// the remote.
type IssueInfo struct {
	Title string
}

// LookupIssue fetches an issue's title, which is what turns a branch called
// "issue-99" into one called "99-fix-the-flaky-test".
func (c *Client) LookupIssue(owner, repo string, number int) (IssueInfo, error) {
	out, err := c.run("", "gh", "issue", "view", strconv.Itoa(number),
		"--repo", owner+"/"+repo,
		"--json", "title")
	if err != nil {
		return IssueInfo{}, fmt.Errorf("gh issue view %s/%s#%d: %w", owner, repo, number, err)
	}

	var raw struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return IssueInfo{}, fmt.Errorf("decode gh issue view output for %s/%s#%d: %w", owner, repo, number, err)
	}
	return IssueInfo{Title: raw.Title}, nil
}

// Clone fetches a repository to dest.
//
// This shells out to gh rather than git so that a private repository works
// with no extra configuration: gh already holds the user's credentials, which
// is the same reason the lookups above go through it.
func (c *Client) Clone(owner, repo, dest string) error {
	if dest == "" {
		return fmt.Errorf("no destination to clone %s/%s into", owner, repo)
	}
	if _, err := c.run("", "gh", "repo", "clone", owner+"/"+repo, dest); err != nil {
		return fmt.Errorf("gh repo clone %s/%s: %w", owner, repo, err)
	}
	return nil
}
