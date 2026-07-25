package open

import (
	"fmt"
	"path/filepath"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// WorktreeRequest describes a worktree Space to open, in the picker's terms
// rather than Herdr's.
type WorktreeRequest struct {
	// RepoRoot is the source checkout git resolves the repository from.
	RepoRoot string
	Branch   string
	// Base is the starting point for a branch that does not exist yet.
	Base string
	// FallbackBase is tried when Base does not resolve. It exists for the
	// "origin/main" case: a repository with no remote, or one that has never
	// been fetched, still has a perfectly good local main to branch from.
	FallbackBase string
	// Path is where the worktree already lives. Set only for Existing; a new
	// worktree's path comes from the config template, or from Herdr.
	Path string
	// Label names the Space.
	Label string
	// Fetch brings origin/<Branch> up to date first, for a branch that only
	// exists on the remote.
	Fetch bool
	// Existing skips worktree.create for a worktree already on disk. Creating
	// one that exists fails, and the error is less clear than not trying.
	Existing bool
}

// RunWorktree opens a Space on a worktree, creating the worktree and its
// branch when they do not exist yet.
//
// This is the same plumbing the pasted-PR-link flow uses, reached from the
// picker instead of from a URL. Keeping it in this package is what stops the
// two front ends growing separate ideas about what a worktree Space is.
func RunWorktree(deps Deps, cfg *config.Settings, req WorktreeRequest, opts Options) (Outcome, error) {
	if req.RepoRoot == "" {
		return Outcome{}, fmt.Errorf("no repository to build a worktree in")
	}
	if req.Branch == "" {
		return Outcome{}, fmt.Errorf("no branch to check out")
	}

	if req.Fetch {
		if err := deps.Git.FetchBranch(req.RepoRoot, req.Branch); err != nil {
			return Outcome{}, err
		}
	}

	label := req.Label
	if label == "" {
		label = req.Branch
	}

	path := req.Path
	if path == "" {
		// A worktree reached from the picker carries no host or owner -- there
		// is no URL behind it -- so a path template using {host} or {owner}
		// resolves those empty. {repo}, {repo_root} and {branch} all work.
		tgt := target.Target{Kind: target.KindProject, Repo: filepath.Base(req.RepoRoot)}
		path, _ = cfg.ResolveWorktreePath(tgt, req.RepoRoot, req.Branch)
	}

	hreq := herdr.WorktreeRequest{
		Cwd:    req.RepoRoot,
		Branch: req.Branch,
		Base:   req.Base,
		Path:   path,
		Label:  label,
		Focus:  true,
	}

	var (
		pane        herdr.Pane
		workspaceID string
		err         error
	)
	if req.Existing {
		pane, workspaceID, err = deps.Session.OpenWorktree(hreq)
	} else {
		pane, workspaceID, err = createWorktreeWithFallback(deps.Session, hreq, req.FallbackBase)
	}
	if err != nil {
		return Outcome{}, err
	}

	tgt := target.Target{Kind: target.KindProject, Text: label}
	out := Outcome{
		Kind:        tgt.Kind,
		Label:       label,
		Branch:      req.Branch,
		RepoPath:    req.RepoRoot,
		WorkspaceID: workspaceID,
		PaneID:      pane.PaneID,
	}

	data := promptData(tgt, req.Branch, "")
	data["Repo"] = filepath.Base(req.RepoRoot)
	data["Path"] = req.RepoRoot

	return runAgentStep(deps.Session, cfg, tgt, opts, pane.PaneID, data, out)
}

// createWorktreeWithFallback retries with a second base before giving up.
//
// "origin/main" is the right base when the remote has been fetched, and does
// not resolve at all when it has not -- or when the repository has no remote.
// Retrying with the plain local branch turns that from a failure into a
// slightly staler starting point, which is the better of the two.
func createWorktreeWithFallback(s Session, req herdr.WorktreeRequest, fallbackBase string) (herdr.Pane, string, error) {
	pane, workspaceID, err := createOrOpenWorktree(s, req)
	if err == nil {
		return pane, workspaceID, nil
	}
	if fallbackBase == "" || fallbackBase == req.Base {
		return herdr.Pane{}, "", err
	}

	retry := req
	retry.Base = fallbackBase
	pane, workspaceID, retryErr := createOrOpenWorktree(s, retry)
	if retryErr != nil {
		// The first error is the informative one: it names the base that was
		// actually asked for.
		return herdr.Pane{}, "", err
	}
	return pane, workspaceID, nil
}
