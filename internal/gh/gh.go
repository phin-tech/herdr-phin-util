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
	"sort"
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

// StackPR is one pull request in a stack, as reconstructed by Stack.
type StackPR struct {
	Number int
	Title  string
	URL    string
	// HeadBranch and BaseBranch are exactly gh's own headRefName/baseRefName,
	// used verbatim for the same reason PRInfo.Branch is: guessing either
	// would be actively wrong.
	HeadBranch string
	BaseBranch string
	HeadSHA    string
}

// Stack reconstructs the chain of open pull requests that number belongs to,
// bottom of the stack (the layer based on the trunk) first, ending with
// number's own topmost layer. number is always included, even when it
// belongs to no stack at all -- that case is a correct one-element chain,
// not an error (see the "no PRs" and "not among them" checks below for what
// actually is one).
//
// This is walked by hand, across a single gh pr list, rather than asked of
// gh directly: `gh stack view` answers "not part of a stack" for any branch
// that was not created through its own tracking, which is most stacks
// reviewed here -- built with plain git, rebase-based tooling, or another
// editor entirely. baseRefName/headRefName across the open pull requests is
// true regardless of how the branches were made, so that is what this walks.
//
// The walk goes in both directions from number, and both matter: walking only
// upward (whatever is based on number) misses that a *lower* layer bases on
// number in turn, and walking only downward (what number is based on) misses
// everything built on top of it. Doing only one of the two was the original
// version of this bug -- a stack's bottom layer bases on the trunk, so
// "base != trunk" alone reads a five-layer stack as standalone the moment you
// ask about its bottom PR, however tall the rest of it is.
func (c *Client) Stack(owner, repo string, number int) ([]StackPR, error) {
	out, err := c.run("", "gh", "pr", "list",
		"--repo", owner+"/"+repo,
		"--state", "open",
		"--limit", "200",
		"--json", "number,title,url,headRefName,baseRefName,headRefOid")
	if err != nil {
		return nil, fmt.Errorf("gh pr list %s/%s: %w", owner, repo, err)
	}

	var raw []struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		HeadRefName string `json:"headRefName"`
		BaseRefName string `json:"baseRefName"`
		HeadRefOid  string `json:"headRefOid"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("decode gh pr list output for %s/%s: %w", owner, repo, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("gh pr list %s/%s: no open pull requests", owner, repo)
	}

	byNumber := make(map[int]StackPR, len(raw))
	// byHead answers "which open PR's head is this branch", used walking
	// toward the trunk. Keyed deterministically by the lowest PR number in
	// the unlikely event two open PRs share a head branch name, so this
	// never depends on the order gh's JSON happened to list them in.
	byHead := make(map[string]int, len(raw))
	// byBase is the reverse: which open PRs are built directly on top of
	// this branch. More than one entry here at a step of the upward walk is
	// a fork -- see below -- and is refused rather than resolved by picking
	// one arbitrarily.
	byBase := make(map[string][]int, len(raw))

	for _, r := range raw {
		pr := StackPR{
			Number: r.Number, Title: r.Title, URL: r.URL,
			HeadBranch: r.HeadRefName, BaseBranch: r.BaseRefName, HeadSHA: r.HeadRefOid,
		}
		byNumber[r.Number] = pr
		if existing, ok := byHead[r.HeadRefName]; !ok || r.Number < existing {
			byHead[r.HeadRefName] = r.Number
		}
		byBase[r.BaseRefName] = append(byBase[r.BaseRefName], r.Number)
	}

	target, ok := byNumber[number]
	if !ok {
		return nil, fmt.Errorf("gh pr list %s/%s: #%d is not among the %d open pull requests", owner, repo, number, len(raw))
	}

	// Walk down toward the trunk: the current PR's base is another open
	// PR's head, until it isn't -- that PR is the bottom of the stack.
	// visited guards a malformed loop (A based on B based on A) rather than
	// hanging on it.
	visited := map[int]bool{number: true}
	down := []StackPR{target}
	cur := target
	for {
		parentNum, ok := byHead[cur.BaseBranch]
		if !ok || visited[parentNum] {
			break
		}
		visited[parentNum] = true
		cur = byNumber[parentNum]
		down = append(down, cur)
	}
	// down was built target-to-trunk; reverse it to bottom-first.
	for i, j := 0, len(down)-1; i < j; i, j = i+1, j-1 {
		down[i], down[j] = down[j], down[i]
	}

	// Walk up from the target: whichever open PR bases on the current one's
	// head. Two open PRs sharing a base makes this ambiguous -- the set of
	// open PRs is a tree at that point, not a chain -- and rather than guess
	// which branch is "the" stack (silently building a layout for a stack
	// that is not the one on screen), this refuses and names every path to a
	// tip so a person can pick the right number and retry.
	//
	// Only the upward walk can hit this, and the asymmetry is deliberate
	// rather than an oversight: walking down asks "what is this based on",
	// which has exactly one answer however many siblings share that parent,
	// so it never has to choose. Concretely, with #11 and #12 both based on
	// #10, asking about #12 succeeds and gives #10 -> #12 -- the path is not
	// ambiguous for that question -- while asking about #10 refuses, because
	// that is the question with two answers. Naming a tip is how you say
	// which branch of the tree you meant.
	up := []StackPR{}
	cur = target
	for {
		children := byBase[cur.HeadBranch]
		if len(children) == 0 {
			break
		}
		if len(children) > 1 {
			return nil, forkErr(owner, repo, cur, children, byNumber, byBase, visited)
		}
		childNum := children[0]
		if visited[childNum] {
			break
		}
		visited[childNum] = true
		cur = byNumber[childNum]
		up = append(up, cur)
	}

	return append(down, up...), nil
}

// forkErr describes a branch point as every path from it to a tip, rather
// than silently picking one child. base is the PR whose head branch two or
// more open PRs are based on; children are their numbers.
func forkErr(owner, repo string, base StackPR, children []int, byNumber map[int]StackPR, byBase map[string][]int, visited map[int]bool) error {
	sort.Ints(children)
	paths := make([]string, 0, len(children))
	for _, childNum := range children {
		paths = append(paths, describePath(childNum, byNumber, byBase, cloneVisited(visited)))
	}
	return fmt.Errorf(
		"gh pr list %s/%s: #%d has %d open pull requests based on it (%s) -- that is a tree, not a stack; pick the tip you mean and retry against its own number",
		owner, repo, base.Number, len(children), strings.Join(paths, "; "))
}

// describePath walks upward from start the same way Stack's own upward walk
// does -- lowest PR number wins at any further fork along this one path,
// purely to give a readable, terminated example of where this branch leads,
// not a second attempt to resolve the ambiguity Stack already refused.
func describePath(start int, byNumber map[int]StackPR, byBase map[string][]int, visited map[int]bool) string {
	nums := []int{start}
	visited[start] = true
	cur := byNumber[start]
	for {
		children := byBase[cur.HeadBranch]
		if len(children) == 0 {
			break
		}
		sort.Ints(children)
		next := children[0]
		if visited[next] {
			break
		}
		visited[next] = true
		cur = byNumber[next]
		nums = append(nums, next)
	}
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("#%d", n)
	}
	return strings.Join(parts, "->")
}

func cloneVisited(v map[int]bool) map[int]bool {
	out := make(map[int]bool, len(v))
	for k, val := range v {
		out[k] = val
	}
	return out
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
