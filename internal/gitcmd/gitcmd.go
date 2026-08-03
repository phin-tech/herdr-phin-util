// Package gitcmd shells out to git for what Herdr's own API does not answer:
// which branches a repository has, what a new one should be based on, making
// a remote branch or ref present locally, and -- since #12's tab-level
// `worktree:` -- laying out and inspecting the worktrees that pins a tab to.
//
// This package now writes to disk. That is a deliberate change from what its
// doc comment used to say here, and worth being honest about rather than
// quietly stale: WorktreeAdd and WorktreeAddBranch call `git worktree add`,
// which is the first thing in this package that creates anything. It exists
// because Herdr's own worktree API cannot do what a tab's `worktree:` needs --
// `herdr worktree create` always checks out a *named branch* it makes itself
// (there is no `--detach`, and WorktreeRequest.Branch is documented as "the
// name of the branch that gets made"), and every one of its calls is
// Space-scoped: CreateWorktree returns a new Workspace, and `herdr worktree
// remove` takes a workspace id, not a path. A tab's worktree is a directory a
// tab points its cwd at, not a Space, so there is no Herdr call to reach for
// here at all -- only git itself. Everything else in this package -- fetching,
// listing branches -- still only reads.
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

// FetchRef is FetchBranch's counterpart for a tab's `worktree: {ref: ...}`,
// which can name a bare commit SHA rather than a branch -- GitHub allows
// fetching one, and FetchBranch's "git fetch origin <branch>" is not that
// call.
//
// A commit already present locally is the common case here: a re-run of a
// setup whose worktree already exists at the right ref should not have to
// reach the network to confirm that, and git itself refuses to fetch a ref it
// already has with an error, not a quiet no-op. So a failed fetch is checked
// against what is already on disk before it is reported -- rev-parse
// resolving ref locally means there was nothing to fetch, which is success,
// not a fetch that failed to do anything.
func (r *Runner) FetchRef(repoPath, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("no ref to fetch")
	}
	if _, err := r.run(repoPath, "git", "fetch", "origin", ref); err != nil {
		if _, verifyErr := r.run(repoPath, "git", "rev-parse", "--verify", "--quiet", ref+"^{commit}"); verifyErr == nil {
			return nil
		}
		return fmt.Errorf("git fetch origin %s (in %s): %w", ref, repoPath, err)
	}
	return nil
}

// ResolveRef resolves a ref to the commit SHA it currently names, in
// repoPath. This is the other half of the collision rule in
// internal/open/setup.go: comparing what a tab's `worktree:` asks for against
// what is already checked out at its path needs both sides reduced to a
// commit, since "main" and "a1b2c3d" are not comparable as strings even when
// they name the same thing.
func (r *Runner) ResolveRef(repoPath, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("no ref to resolve")
	}
	out, err := r.run(repoPath, "git", "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s (in %s): %w", ref, repoPath, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// HeadCommit reports the commit SHA checked out at path, which for a
// worktree already on disk is the other side of the collision rule's
// comparison.
func (r *Runner) HeadCommit(path string) (string, error) {
	out, err := r.run(path, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD (in %s): %w", path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// WorktreeAdd lays out a new worktree at path, detached at ref rather than on
// a branch. Detached is the default a tab's `worktree:` takes (see
// internal/setup's WorktreeSpec) precisely because a branch cannot be checked
// out in two worktrees at once and moves under you if someone pushes to it
// mid-review -- neither problem exists for a detached HEAD, which just is
// whatever commit ref named at creation time.
func (r *Runner) WorktreeAdd(repoPath, path, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("no ref to check out")
	}
	if _, err := r.run(repoPath, "git", "worktree", "add", "--detach", path, ref); err != nil {
		return fmt.Errorf("git worktree add --detach %s %s (in %s): %w", path, ref, repoPath, err)
	}
	return nil
}

// WorktreeAddBranch is WorktreeAdd's counterpart for `detach: false`: it
// checks ref out as a branch rather than leaving HEAD detached, for the
// single-tab case where the point is to commit on it. Only meaningful without
// for_each in the picture -- a for_each tab repeating a constant branch would
// have every element fight over the same checkout, which is stage two's
// validation rule, not this function's problem.
func (r *Runner) WorktreeAddBranch(repoPath, path, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("no ref to check out")
	}
	if _, err := r.run(repoPath, "git", "worktree", "add", path, ref); err != nil {
		return fmt.Errorf("git worktree add %s %s (in %s): %w", path, ref, repoPath, err)
	}
	return nil
}

// WorktreeRemove deletes a worktree Herdr's own API cannot reach: `herdr
// worktree remove` only knows a workspace id, not a bare path, and a tab's
// worktree is never a Space of its own. Wired for tests and for a future
// cleanup story, but nothing in this package or internal/open calls it
// automatically.
//
// That last part is deliberate and worth defending against a future edit: the
// collision rule in internal/open/setup.go reports a mismatched worktree as a
// failed tab rather than force-removing and recreating it, because the repo
// owner confirmed exactly that during #12's design -- "I would rather it
// accumulate predictably than get cleverly cleaned up and occasionally delete
// something someone was using." Auto-forcing here would be precisely that
// mistake. If you are tempted to wire force removal into the collision path,
// don't -- that decision was made on purpose, not by omission.
func (r *Runner) WorktreeRemove(repoPath, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	if _, err := r.run(repoPath, "git", args...); err != nil {
		return fmt.Errorf("git worktree remove (in %s): %w", repoPath, err)
	}
	return nil
}
