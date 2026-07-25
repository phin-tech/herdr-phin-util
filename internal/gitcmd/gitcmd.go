// Package gitcmd shells out to git for what Herdr's own API does not answer:
// which branches a repository has, what a new one should be based on, and
// making a remote branch present locally before a worktree is built on it.
//
// Nothing here writes to the working tree. Fetching updates remote-tracking
// refs and branch listing only reads, so none of it can disturb whatever the
// source checkout happens to have checked out.
package gitcmd

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// CommandRunner executes one command in dir and returns its stdout. It exists
// so tests can fake git's behaviour without a real repository or network.
type CommandRunner func(dir, name string, args ...string) ([]byte, error)

// Runner fetches branches via the real git binary.
type Runner struct {
	run CommandRunner
}

// New builds a Runner backed by the real git binary.
func New() *Runner {
	return &Runner{run: runCommand}
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

// Branch is a ref a worktree could be built on.
type Branch struct {
	// Name is the branch to check out. For a remote ref this is the local
	// name it would get: "origin/feature/x" is offered as "feature/x", since
	// that is the branch the worktree ends up on.
	Name string
	// Remote is true for a remote-tracking ref with no local branch yet.
	// Those need a base of "origin/<name>" when the worktree is created.
	Remote bool
}

// defaultBranchGuesses are tried when the remote does not publish a HEAD,
// which happens with a repo that has never been pushed.
var defaultBranchGuesses = []string{"main", "master"}

// Branches lists every local branch, plus every remote-tracking branch that
// has no local counterpart.
//
// Remote refs are read as they are on disk rather than fetched first. That
// makes the picker instant at the cost of missing a branch pushed since the
// last fetch, which is the right trade for a popup -- Refresh is there for
// when you know it is stale.
func (r *Runner) Branches(repoPath string) ([]Branch, error) {
	out, err := r.run(repoPath, "git", "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref (in %s): %w", repoPath, err)
	}

	local := map[string]bool{}
	var remotes []string

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		ref := strings.TrimSpace(line)
		switch {
		case ref == "":
			continue
		case strings.HasPrefix(ref, "refs/heads/"):
			local[strings.TrimPrefix(ref, "refs/heads/")] = true
		case strings.HasPrefix(ref, "refs/remotes/"):
			rest := strings.TrimPrefix(ref, "refs/remotes/")
			// refs/remotes/<remote>/<branch>: drop the remote name to get the
			// branch a worktree would actually be on.
			parts := strings.SplitN(rest, "/", 2)
			if len(parts) != 2 || parts[1] == "" {
				continue
			}
			// origin/HEAD is a symbolic alias for the default branch, not a
			// branch of its own -- offering it would duplicate a real row.
			if parts[1] == "HEAD" {
				continue
			}
			remotes = append(remotes, parts[1])
		}
	}

	branches := make([]Branch, 0, len(local)+len(remotes))
	for name := range local {
		branches = append(branches, Branch{Name: name})
	}

	// A remote branch that already exists locally is the same branch; the
	// local one is what a worktree would use, so the remote row is dropped.
	seen := map[string]bool{}
	for _, name := range remotes {
		if local[name] || seen[name] {
			continue
		}
		seen[name] = true
		branches = append(branches, Branch{Name: name, Remote: true})
	}

	// Local before remote, alphabetical within each -- map iteration order is
	// random, and a picker whose list reshuffles between openings is unusable.
	sort.SliceStable(branches, func(i, j int) bool {
		if branches[i].Remote != branches[j].Remote {
			return !branches[i].Remote
		}
		return branches[i].Name < branches[j].Name
	})
	return branches, nil
}

// DefaultBranch resolves what a brand new branch should be based on.
//
// origin/HEAD is the authority when the remote publishes one. Without it this
// falls back to whichever conventional name actually exists, and finally to
// the empty string -- which callers treat as "let git decide", i.e. branch
// from whatever HEAD already is.
func (r *Runner) DefaultBranch(repoPath string) string {
	if out, err := r.run(repoPath, "git", "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(string(out))
		if name := strings.TrimPrefix(ref, "refs/remotes/origin/"); name != ref && name != "" {
			return name
		}
	}
	for _, guess := range defaultBranchGuesses {
		if _, err := r.run(repoPath, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+guess); err == nil {
			return guess
		}
	}
	return ""
}

// Refresh updates every remote-tracking ref, for the explicit "my list is
// stale" key. Unlike FetchBranch this touches the whole remote, which is why
// it is never on the path that just opens the picker.
func (r *Runner) Refresh(repoPath string) error {
	if _, err := r.run(repoPath, "git", "fetch", "--prune", "origin"); err != nil {
		return fmt.Errorf("git fetch origin (in %s): %w", repoPath, err)
	}
	return nil
}

// FetchBranch fetches a single branch from origin into repoPath's
// remote-tracking refs, without touching whatever is currently checked out
// there. The worktree that gets built afterward reads origin/<branch>, so
// nothing here needs to switch or merge anything.
func (r *Runner) FetchBranch(repoPath, branch string) error {
	if branch == "" {
		return fmt.Errorf("no branch to fetch")
	}
	if _, err := r.run(repoPath, "git", "fetch", "origin", branch); err != nil {
		return fmt.Errorf("git fetch origin %s (in %s): %w", branch, repoPath, err)
	}
	return nil
}
