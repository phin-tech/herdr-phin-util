package session

import (
	"fmt"
	"strings"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/open"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// KindClone is a repository that is not on this machine yet. Picking it
// fetches it and opens a Space on it.
const KindClone Kind = "clone"

// KindLink is a pasted reference -- a pull request, an issue, or plain text --
// that has no Space yet. Picking it builds one.
const KindLink Kind = "link"

// ResolveLink turns typed input into the single row it describes, when that
// input is a reference rather than a filter.
//
// The trick that makes this instant: a link's Space label is derived from the
// URL and nothing else, so "roux#42" or "ENG-123" is knowable before any
// network call. Matching that against the open Spaces answers "am I already in
// this?" for free -- no gh, no branch resolution, no fetch.
//
// It is a heuristic, and deliberately so. Renaming a Space defeats it, and the
// answer is then merely the one we have today: create, discover the worktree
// already exists, and focus it. Fast and usually right beats slow and certain
// for something that redraws on every keystroke.
func ResolveLink(workspaces []herdr.Workspace, cfg *config.Settings, tgt target.Target) (Candidate, bool) {
	switch tgt.Kind {
	case target.KindGitHubRepo:
		return resolveRepoRef(workspaces, cfg, tgt), true
	case target.KindGitHubPR, target.KindGitHubIssue, target.KindLinear:
	default:
		// Plain text is a filter first and a Space name second, so it stays
		// with the ordinary list rather than collapsing it to one row.
		return Candidate{}, false
	}

	label := tgt.Label()
	if existing, ok := findByLabel(workspaces, label); ok {
		return Candidate{
			Kind:        KindSpace,
			Label:       label,
			WorkspaceID: existing.WorkspaceID,
			Focused:     existing.Focused,
			Detail:      "already open — switch to it",
		}, true
	}

	return Candidate{
		Kind:   KindLink,
		Label:  label,
		Branch: tgt.Branch(),
		Detail: linkDetail(tgt),
		Target: tgt,
	}, true
}

// resolveRepoRef answers the one question a repository reference raises: is it
// already here?
//
// Three outcomes, in the same vocabulary as every other row -- switch to the
// Space, open the checkout that exists, or clone the one that does not. The
// answer costs a single os.Stat, because the templates already say where the
// checkout would be.
func resolveRepoRef(workspaces []herdr.Workspace, cfg *config.Settings, tgt target.Target) Candidate {
	if cfg != nil {
		if path, _, err := cfg.ResolveRepo(tgt); err == nil {
			// It is on disk. If a Space is already pointed at it, that is the
			// answer; otherwise it is an ordinary project row.
			if ws, ok := findByLabel(workspaces, tgt.Repo); ok {
				return Candidate{
					Kind:        KindSpace,
					Label:       tgt.Repo,
					Path:        path,
					WorkspaceID: ws.WorkspaceID,
					Focused:     ws.Focused,
					Detail:      "already open — switch to it",
				}
			}
			return Candidate{
				Kind:   KindProject,
				Label:  tgt.Repo,
				Path:   path,
				Detail: "already cloned — " + shortenHome(path),
			}
		}
	}

	detail := "clone from github.com/" + tgt.Owner + "/" + tgt.Repo
	if cfg != nil {
		if dest, err := cfg.ClonePath(tgt); err == nil {
			detail = "clone to " + shortenHome(dest)
		}
	}
	return Candidate{
		Kind:   KindClone,
		Label:  tgt.Repo,
		Detail: detail,
		Target: tgt,
	}
}

// findByLabel matches case-insensitively: Herdr preserves the label it was
// given, but a Space made by hand may not match the exact casing a URL
// produces.
func findByLabel(workspaces []herdr.Workspace, label string) (herdr.Workspace, bool) {
	for _, w := range workspaces {
		if strings.EqualFold(w.Label, label) {
			return w, true
		}
	}
	return herdr.Workspace{}, false
}

// linkDetail says what picking the row would actually do, in the same voice as
// the branch rows a level down.
func linkDetail(tgt target.Target) string {
	switch tgt.Kind {
	case target.KindGitHubPR:
		return fmt.Sprintf("pull request in %s/%s — worktree on its branch", tgt.Owner, tgt.Repo)
	case target.KindGitHubIssue:
		return fmt.Sprintf("issue in %s/%s — new branch", tgt.Owner, tgt.Repo)
	case target.KindLinear:
		// A ticket names no repository, so unlike every other reference this
		// row cannot say what it would build -- only what it would build once
		// it is told where.
		if b := tgt.Branch(); b != "" {
			return "Linear issue — choose a repository for " + b
		}
		return "Linear issue — choose a repository"
	default:
		return ""
	}
}

// OpenClone fetches a repository and opens a Space on it.
func OpenClone(deps Deps, cfg *config.Settings, c Candidate, opts open.Options) (open.Outcome, error) {
	return open.RunClone(deps.Open, cfg, c.Target, opts)
}

// CloneAndList fetches a repository that is not here yet, then lists what is
// inside it -- the "get me this repo and start a branch on it" path.
//
// The clone is the only slow part; once it lands this is the ordinary worktree
// level, so the branch you invent next goes through exactly the same code as
// one invented in a repo you have had for months.
func CloneAndList(deps Deps, cfg *config.Settings, tgt target.Target) ([]Candidate, RepoContext, error) {
	dest, err := open.EnsureCloned(deps.Open, cfg, tgt)
	if err != nil {
		return nil, RepoContext{}, err
	}
	return ListWorktrees(deps.Worktrees, deps.Git, dest)
}

// OpenLink builds the Space a pasted reference describes, by handing straight
// back to the package that already knows how: the picker is a second front end
// onto open.Run, not a second implementation of it.
func OpenLink(deps Deps, cfg *config.Settings, c Candidate, opts open.Options) (open.Outcome, error) {
	input := c.Target.URL
	if input == "" {
		input = c.Target.Text
	}
	if input == "" {
		return open.Outcome{}, fmt.Errorf("nothing to open")
	}
	return open.Run(deps.Open, cfg, input, opts)
}
