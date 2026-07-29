package session

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/gitcmd"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/open"
)

// The worktree level's own kinds. They extend the project level's vocabulary
// rather than replacing it, so one row renderer and one dispatch cover both.
const (
	// KindWorktree is a worktree that exists on disk but has no Space.
	// Picking it opens one.
	KindWorktree Kind = "worktree"
	// KindBranch is a local branch with no worktree. Picking it makes one.
	KindBranch Kind = "branch"
	// KindRemoteBranch is a remote-tracking branch with no local counterpart.
	// Picking it fetches, then makes a worktree based on origin/<name>.
	KindRemoteBranch Kind = "remote_branch"
	// KindNewBranch is the row offering to create the name you typed. It is
	// synthesised from the filter text rather than found anywhere.
	KindNewBranch Kind = "new_branch"
)

// WorktreeLister is the slice of the Herdr API the worktree level needs.
type WorktreeLister interface {
	Worktrees(cwd string) ([]herdr.Worktree, herdr.WorktreeSource, error)
}

// Brancher is the slice of git the worktree level needs.
type Brancher interface {
	Branches(repoPath string) ([]gitcmd.Branch, error)
	DefaultBranch(repoPath string) string
	Refresh(repoPath string) error
}

// RepoContext is the repository a worktree list belongs to, carried alongside
// the rows so the picker can title itself and so acting on a row knows which
// checkout to run git against.
type RepoContext struct {
	Name string
	Root string
	// DefaultBranch is what a brand new branch is based on. Empty means the
	// repository has no discoverable default, in which case a new branch
	// starts from whatever HEAD already is.
	DefaultBranch string
}

// ListWorktrees builds the worktree level's rows for one repository:
// worktrees that exist, then local branches without one, then remote branches
// without one.
//
// The ordering is the same principle as the project level -- what exists
// before what would have to be made -- and the same dedup rule applies twice
// over: a branch that already has a worktree is not offered as a branch, and a
// remote branch that already exists locally is not offered as remote.
func ListWorktrees(l WorktreeLister, g Brancher, repoPath string) ([]Candidate, RepoContext, error) {
	worktrees, source, err := l.Worktrees(repoPath)
	if err != nil {
		return nil, RepoContext{}, fmt.Errorf("list worktrees: %w", err)
	}

	repo := RepoContext{Name: source.RepoName, Root: source.RepoRoot}
	if repo.Name == "" {
		repo.Name = filepath.Base(repoPath)
	}
	if repo.Root == "" {
		repo.Root = repoPath
	}
	repo.DefaultBranch = g.DefaultBranch(repo.Root)

	out := make([]Candidate, 0, len(worktrees))
	checkedOut := map[string]bool{}

	for _, w := range worktrees {
		if w.IsBare {
			// A bare repository has no working tree to open.
			continue
		}
		if w.Branch != "" {
			checkedOut[w.Branch] = true
		}

		c := Candidate{
			Kind:        KindWorktree,
			Label:       worktreeLabel(w),
			Path:        w.Path,
			Branch:      w.Branch,
			WorkspaceID: w.OpenWorkspaceID,
			Detail:      shortenHome(w.Path),
		}
		if w.OpenWorkspaceID != "" {
			// Already has a Space: this is a switch, not an open, and it is
			// the same KindSpace the project level uses so that Open needs no
			// extra case.
			c.Kind = KindSpace
		}
		if w.IsPrunable {
			// The directory is gone, so opening it would fail. Saying so is
			// more useful than silently dropping the row, since a prunable
			// worktree is usually a surprise.
			c.Kind = KindPrunable
			c.Detail = "missing on disk — git worktree prune"
		}
		out = append(out, c)
	}

	branches, err := g.Branches(repo.Root)
	if err != nil {
		return nil, repo, fmt.Errorf("list branches: %w", err)
	}
	for _, b := range branches {
		if checkedOut[b.Name] {
			// Already backed by a worktree above.
			continue
		}
		kind := KindBranch
		detail := "no worktree yet"
		if b.Remote {
			kind = KindRemoteBranch
			detail = "origin — fetched on open"
		}
		out = append(out, Candidate{
			Kind:   kind,
			Label:  b.Name,
			Branch: b.Name,
			Path:   repo.Root,
			Detail: detail,
		})
	}

	return out, repo, nil
}

// KindPrunable is a worktree whose directory has gone missing.
const KindPrunable Kind = "prunable"

// NewBranchCandidate is the row offered when the filter text names no existing
// branch: it turns what you typed into a branch to create.
//
// It is built by the UI rather than found by ListWorktrees, because it depends
// on the filter rather than on the repository.
func NewBranchCandidate(repo RepoContext, name string) Candidate {
	detail := "new branch"
	if repo.DefaultBranch != "" {
		detail = "new branch from " + repo.DefaultBranch
	}
	return Candidate{
		Kind:   KindNewBranch,
		Label:  name,
		Branch: name,
		Path:   repo.Root,
		Detail: detail,
	}
}

func worktreeLabel(w herdr.Worktree) string {
	switch {
	case w.Branch != "":
		return w.Branch
	case w.IsDetached:
		return "(detached) " + filepath.Base(w.Path)
	case w.Label != "":
		return w.Label
	default:
		return filepath.Base(w.Path)
	}
}

// OpenWorktree acts on a row from the worktree level.
//
// The three creating kinds differ only in what the new branch is based on: an
// existing local branch needs no base, a remote one is based on its fetched
// origin ref, and a brand new one on the repository's default branch. That
// single difference is why they are separate kinds rather than one.
func OpenWorktree(deps Deps, cfg *config.Settings, repo RepoContext, c Candidate, opts open.Options) (open.Outcome, error) {
	switch c.Kind {
	case KindPrunable:
		return open.Outcome{}, fmt.Errorf("worktree %s is missing on disk; run 'git worktree prune' in %s", c.Label, repo.Root)

	case KindWorktree:
		// Exists on disk with no Space: open it where it already is.
		return open.RunWorktree(deps.Open, cfg, open.WorktreeRequest{
			RepoRoot: repo.Root,
			Branch:   c.Branch,
			Path:     c.Path,
			Label:    worktreeSpaceLabel(repo, c.Label),
			Existing: true,
		}, opts)

	case KindBranch:
		return open.RunWorktree(deps.Open, cfg, open.WorktreeRequest{
			RepoRoot: repo.Root,
			Branch:   c.Branch,
			Label:    worktreeSpaceLabel(repo, c.Label),
		}, opts)

	case KindRemoteBranch:
		return open.RunWorktree(deps.Open, cfg, open.WorktreeRequest{
			RepoRoot: repo.Root,
			Branch:   c.Branch,
			Base:     "origin/" + c.Branch,
			Fetch:    true,
			Label:    worktreeSpaceLabel(repo, c.Label),
		}, opts)

	case KindLinearBase:
		return OpenLinearBranch(deps, cfg, repo, c, opts)

	case KindNewBranch:
		base := repo.DefaultBranch
		if base != "" {
			// Prefer the remote's copy of the default branch: a local main
			// that has not been pulled in a fortnight is a worse starting
			// point than origin's, and falling back is handled downstream.
			base = "origin/" + base
		}
		return open.RunWorktree(deps.Open, cfg, open.WorktreeRequest{
			RepoRoot:     repo.Root,
			Branch:       c.Branch,
			Base:         base,
			FallbackBase: repo.DefaultBranch,
			Label:        worktreeSpaceLabel(repo, c.Label),
		}, opts)

	default:
		return open.Outcome{}, fmt.Errorf("unknown worktree candidate kind %q", c.Kind)
	}
}

// worktreeSpaceLabel names the Space "repo/branch", since a bare branch name
// is ambiguous once several repositories have a "main" open at once.
func worktreeSpaceLabel(repo RepoContext, branch string) string {
	name := repo.Name
	if name == "" {
		return branch
	}
	if branch == "" {
		return name
	}
	// The source checkout reports the repo's own name as its branch label in
	// some layouts; "roux/roux" reads worse than "roux".
	if strings.EqualFold(name, branch) {
		return name
	}
	return name + "/" + branch
}
