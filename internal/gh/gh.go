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
