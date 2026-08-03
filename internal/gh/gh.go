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

// StackPR is one pull request in a stack, as reconstructed by Stack or
// Stacks -- from a native PullRequestStack or from the baseRefName walk,
// both map onto the same fields (see stackNative's doc comment).
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

// stackGraphQLQuery asks GitHub's own PullRequestStack API for number's
// stack. This is queried directly rather than adding a second CommandRunner:
// `gh api graphql` is still a `gh` subprocess, so it goes through the exact
// same c.run every other method here uses, and gets the exact same
// fakeability in tests.
//
// entries(first: 50) is a deliberate bound, not a guess at "enough": a
// PullRequestStack is a chain of open pull requests a person is actively
// reviewing, and a stack fifty deep is not a real shape for that -- if one
// ever shows up, the native path simply stops being the fast path for it and
// falls back to the walk below, same as every other native failure mode.
//
// position is documented by GitHub as "this entry's position in the stack,
// where 1 is the closest to the base branch" -- i.e. already the bottom-first
// order this package's own walk produces, so sorting by it needs no
// reinterpretation to match StackPR's contract.
const stackGraphQLQuery = `
query($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      stack {
        id
        number
        size
        baseRefName
        entries(first: 50) {
          nodes {
            position
            pullRequest {
              number
              title
              url
              headRefName
              baseRefName
              headRefOid
            }
          }
        }
      }
    }
  }
}`

// stackNative tries GitHub's real stack API before falling back to the
// baseRefName walk. It is a fast path, never a replacement for the walk:
// I verified against the live API that pr.stack comes back **null** for a
// stack built with plain git -- tested against phin-tech/herdr-phin-util#16,
// a real stack whose base is another open PR's head, and PullRequestStack
// was null there. A PullRequestStack has its own id and repo-scoped number,
// which means it is a persisted entity GitHub creates when a stack is built
// through its own tooling, not a view computed on demand from
// baseRefName/headRefName. Issue #13's warning about `gh stack view` --
// "do not trust it, it says 'not part of a stack' for branches not created
// through it" -- applies here too, for the same underlying reason.
//
// So this returns ok=false, deferring to the walk, on every failure mode:
// a command error (old gh, missing scopes, no network), JSON this package
// does not recognise, or a stack field that is null. None of those are
// fatal -- a user on an older gh must see no behaviour change at all, which
// is the entire point of keeping the walk rather than deleting it as
// "redundant" now that a native answer exists for some stacks.
//
// When it does succeed, a PullRequestStack is linear by construction --
// GitHub does not let its own stacking tool fork -- so the answer is always
// exactly one path with no fork handling needed.
func (c *Client) stackNative(owner, repo string, number int) ([]StackPR, bool) {
	out, err := c.run("", "gh", "api", "graphql",
		"-f", "query="+stackGraphQLQuery,
		"-F", "owner="+owner,
		"-F", "repo="+repo,
		"-F", "number="+strconv.Itoa(number),
	)
	if err != nil {
		return nil, false
	}

	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					Stack *struct {
						Entries struct {
							Nodes []struct {
								Position    int `json:"position"`
								PullRequest struct {
									Number      int    `json:"number"`
									Title       string `json:"title"`
									URL         string `json:"url"`
									HeadRefName string `json:"headRefName"`
									BaseRefName string `json:"baseRefName"`
									HeadRefOid  string `json:"headRefOid"`
								} `json:"pullRequest"`
							} `json:"nodes"`
						} `json:"entries"`
					} `json:"stack"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, false
	}

	stack := resp.Data.Repository.PullRequest.Stack
	if stack == nil {
		return nil, false
	}

	nodes := stack.Entries.Nodes
	if len(nodes) == 0 {
		return nil, false
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Position < nodes[j].Position })

	prs := make([]StackPR, len(nodes))
	for i, n := range nodes {
		prs[i] = StackPR{
			Number: n.PullRequest.Number, Title: n.PullRequest.Title, URL: n.PullRequest.URL,
			HeadBranch: n.PullRequest.HeadRefName, BaseBranch: n.PullRequest.BaseRefName,
			HeadSHA: n.PullRequest.HeadRefOid,
		}
	}
	return prs, true
}

// resolveStacks is the shared engine behind both Stacks and Stack: try the
// native fast path first, and fall back to the baseRefName walk when it
// declines. forkErr is non-nil only when the walk found more than one path
// above number -- carrying the exact error Stack has always returned in that
// case, computed once here so Stacks and Stack cannot disagree about what
// "the" fork looks like.
func (c *Client) resolveStacks(owner, repo string, number int) (paths [][]StackPR, forkErr error, err error) {
	if native, ok := c.stackNative(owner, repo, number); ok {
		return [][]StackPR{native}, nil, nil
	}
	return c.stackWalk(owner, repo, number)
}

// Stacks returns every path from the bottom of number's chain to a tip, each
// bottom-first. A chain with no fork -- the common case, and the only shape
// a native PullRequestStack can ever have -- yields exactly one path. A
// git-built fork (two or more open PRs sharing a base) yields one path per
// tip, each sharing whatever trunk-ward prefix the tips have in common:
// Stack's downward walk is shared by every path, since "what is this based
// on" always has exactly one answer (see Stack's doc comment for why only
// the upward walk can fork).
//
// This is what #14's picker needs and Stack deliberately does not give it:
// building a for_each layout needs exactly one chain, so Stack still refuses
// on a fork, but a picker showing every stack as a row needs the opposite --
// every path, named, so each can become its own row.
func (c *Client) Stacks(owner, repo string, number int) ([][]StackPR, error) {
	paths, _, err := c.resolveStacks(owner, repo, number)
	return paths, err
}

// Stack reconstructs the single chain of open pull requests that number
// belongs to, bottom of the stack (the layer based on the trunk) first,
// ending with number's own topmost layer. number is always included, even
// when it belongs to no stack at all -- that case is a correct one-element
// chain, not an error (see the "no PRs" and "not among them" checks in
// stackWalk for what actually is one).
//
// It is a thin wrapper over Stacks: exactly one path is returned as-is, and
// more than one path (a fork) is refused with an error naming every path to
// a tip, so a person can pick the right number and retry rather than this
// silently guessing which branch is "the" stack.
//
// Stacks' own doc comment covers the native-fast-path/walk split and the
// up-only fork asymmetry; both apply here unchanged, since Stack is defined
// entirely in terms of Stacks' answer.
func (c *Client) Stack(owner, repo string, number int) ([]StackPR, error) {
	paths, forkErr, err := c.resolveStacks(owner, repo, number)
	if err != nil {
		return nil, err
	}
	if len(paths) == 1 {
		return paths[0], nil
	}
	return nil, forkErr
}

// stackWalk is the baseRefName walk stackNative falls back to: reconstruct
// the chain(s) by hand, across a single gh pr list, rather than asking gh
// directly. `gh stack view` answers "not part of a stack" for any branch
// that was not created through its own tracking, which is most stacks
// reviewed here -- built with plain git, rebase-based tooling, or another
// editor entirely. baseRefName/headRefName across the open pull requests is
// true regardless of how the branches were made, so that is what this walks.
//
// The walk goes in both directions from number, and both matter: walking
// only upward (whatever is based on number) misses that a *lower* layer
// bases on number in turn, and walking only downward (what number is based
// on) misses everything built on top of it. Doing only one of the two was
// the original version of this bug -- a stack's bottom layer bases on the
// trunk, so "base != trunk" alone reads a five-layer stack as standalone the
// moment you ask about its bottom PR, however tall the rest of it is.
//
// It returns every path (see enumerateUp) so Stacks can hand back a fork
// as multiple rows, plus, separately, the single fork error Stack has
// always returned when there is more than one path -- computed once here
// so both callers agree on it. err is non-nil only for the command/decode/
// lookup failures below; a fork is not one of them, by design (see Stacks).
func (c *Client) stackWalk(owner, repo string, number int) ([][]StackPR, error, error) {
	out, err := c.run("", "gh", "pr", "list",
		"--repo", owner+"/"+repo,
		"--state", "open",
		"--limit", "200",
		"--json", "number,title,url,headRefName,baseRefName,headRefOid")
	if err != nil {
		return nil, nil, fmt.Errorf("gh pr list %s/%s: %w", owner, repo, err)
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
		return nil, nil, fmt.Errorf("decode gh pr list output for %s/%s: %w", owner, repo, err)
	}
	if len(raw) == 0 {
		return nil, nil, fmt.Errorf("gh pr list %s/%s: no open pull requests", owner, repo)
	}

	byNumber := make(map[int]StackPR, len(raw))
	// byHead answers "which open PR's head is this branch", used walking
	// toward the trunk. Keyed deterministically by the lowest PR number in
	// the unlikely event two open PRs share a head branch name, so this
	// never depends on the order gh's JSON happened to list them in.
	byHead := make(map[string]int, len(raw))
	// byBase is the reverse: which open PRs are built directly on top of
	// this branch. More than one entry here at a step of the upward walk is
	// a fork -- see enumerateUp and detectFirstFork below.
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

	tgt, ok := byNumber[number]
	if !ok {
		return nil, nil, fmt.Errorf("gh pr list %s/%s: #%d is not among the %d open pull requests", owner, repo, number, len(raw))
	}

	// Walk down toward the trunk: the current PR's base is another open
	// PR's head, until it isn't -- that PR is the bottom of the stack. This
	// prefix is shared by every path Stacks returns, which is exactly the
	// asymmetry the package doc keeps repeating: "what is this based on"
	// has one answer, so there is nothing to enumerate on the way down.
	// visited guards a malformed loop (A based on B based on A) rather than
	// hanging on it.
	visited := map[int]bool{number: true}
	down := []StackPR{tgt}
	cur := tgt
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

	// Detect the first fork directly above the target, if any, purely to
	// reproduce Stack's historical error verbatim -- this mirrors
	// enumerateUp's first step rather than driving it; enumerateUp below is
	// what actually walks (and branches across) every path. Runs against a
	// clone of visited so its own bookkeeping cannot affect enumerateUp's.
	forkErr := detectFirstFork(owner, repo, tgt, byNumber, byBase, cloneVisited(visited))

	tails := enumerateUp(tgt, byNumber, byBase, visited)
	paths := make([][]StackPR, 0, len(tails))
	for _, tail := range tails {
		full := make([]StackPR, 0, len(down)+len(tail))
		full = append(full, down...)
		full = append(full, tail...)
		paths = append(paths, full)
	}

	return paths, forkErr, nil
}

// detectFirstFork walks upward from target exactly the way Stack's upward
// walk always has, stopping the instant it finds two or more open PRs
// sharing a base and describing that point the same way forkErr always has.
// It returns nil when the walk reaches a tip with no fork at all -- number
// itself might be above every fork in its tree, which is not an error (see
// TestStackFromAboveAForkResolvesToThatPath).
func detectFirstFork(owner, repo string, target StackPR, byNumber map[int]StackPR, byBase map[string][]int, visited map[int]bool) error {
	cur := target
	for {
		children := byBase[cur.HeadBranch]
		if len(children) == 0 {
			return nil
		}
		if len(children) > 1 {
			return forkErr(owner, repo, cur, children, byNumber, byBase, visited)
		}
		childNum := children[0]
		if visited[childNum] {
			return nil
		}
		visited[childNum] = true
		cur = byNumber[childNum]
	}
}

// enumerateUp returns every continuation upward from cur, exclusive of cur
// itself: one []StackPR per path to a tip. A tip (no open PR based on cur's
// head) yields a single empty continuation -- "this path ends here" -- and a
// cycle (a child already in visited) is treated the same way, terminating
// that branch rather than hanging on it. Two or more open PRs sharing a
// base is where a path splits; unlike Stack's old single walk, this does not
// refuse there -- it recurses into every child and returns all of them,
// which is the entire difference between Stacks and Stack.
func enumerateUp(cur StackPR, byNumber map[int]StackPR, byBase map[string][]int, visited map[int]bool) [][]StackPR {
	children := byBase[cur.HeadBranch]
	if len(children) == 0 {
		return [][]StackPR{{}}
	}
	sort.Ints(children)

	var results [][]StackPR
	for _, childNum := range children {
		if visited[childNum] {
			results = append(results, []StackPR{})
			continue
		}
		// Cloned per child, not shared across siblings: two children of the
		// same fork must each see their own branch independently, not mark
		// nodes visited on behalf of a sibling path they never walked.
		childVisited := cloneVisited(visited)
		childVisited[childNum] = true
		child := byNumber[childNum]
		for _, sub := range enumerateUp(child, byNumber, byBase, childVisited) {
			tail := make([]StackPR, 0, 1+len(sub))
			tail = append(tail, child)
			tail = append(tail, sub...)
			results = append(results, tail)
		}
	}
	return results
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
