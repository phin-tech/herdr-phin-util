package session

import (
	"strings"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/open"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// KindLinearBase is a row at the worktree level while a Linear ticket is
// pending: the ref the ticket's branch would be cut from.
//
// It exists because a Linear issue names no repository. A pull request URL
// carries its owner and repo, so the checkout is knowable from the link alone;
// a ticket carries neither, and the branch it implies could belong to any
// repository on this machine. The repository is therefore chosen the way every
// other repository is chosen -- from the project list -- and the level
// underneath, which already lists this repository's refs, answers the only
// question left.
const KindLinearBase Kind = "linear_base"

// LinearBaseRows turns the worktree level's list into the bases a pending
// ticket's branch could start from.
//
// The branch name is not asked for: target.Branch derives it from the URL
// slug, so by the time these rows are drawn the only undecided thing is where
// it starts. That is why the rows are bases rather than branches -- offering to
// name a branch that Linear has already named would be a question with one
// right answer.
//
// The repository's default branch leads, since it is the right answer nearly
// every time and the one KindNewBranch takes without asking.
func LinearBaseRows(repo RepoContext, tgt target.Target, all []Candidate) []Candidate {
	branch := tgt.Branch()
	if branch == "" {
		return nil
	}

	row := func(base, detail string) Candidate {
		return Candidate{
			Kind:   KindLinearBase,
			Label:  "from " + base,
			Base:   base,
			Branch: branch,
			Path:   repo.Root,
			Target: tgt,
			Detail: detail,
		}
	}

	var rows []Candidate
	seen := map[string]bool{}
	add := func(base, detail string) {
		if base == "" || seen[base] {
			return
		}
		seen[base] = true
		rows = append(rows, row(base, detail))
	}

	if repo.DefaultBranch != "" {
		// Prefer the remote's copy for the same reason KindNewBranch does: a
		// local main left unpulled for a fortnight is a worse starting point,
		// and the fallback downstream makes preferring it free.
		add("origin/"+repo.DefaultBranch, "default branch")
	}

	for _, c := range all {
		switch c.Kind {
		case KindWorktree, KindBranch:
			add(c.Branch, "local branch")
		case KindRemoteBranch:
			add("origin/"+c.Branch, "remote branch")
		}
	}
	return rows
}

// LinearBaseFallback is the second base to try when the first does not
// resolve, by the same rule the rest of the plugin follows: origin's copy of a
// branch is the better starting point, and its absence -- an unfetched
// repository, or one with no remote at all -- is not a reason to fail when the
// local branch is right there.
func LinearBaseFallback(base string) string {
	if local := strings.TrimPrefix(base, "origin/"); local != base {
		return local
	}
	return ""
}

// OpenLinearBranch cuts the ticket's branch from the chosen base and opens a
// Space on it.
//
// The ticket's own target is carried through rather than a synthesised project
// one, so the prompt renders against {{.Issue}} and {{.URL}} and a setup with
// `applies_to: [linear]` still matches -- which is the whole point of routing a
// ticket through the picker rather than treating it as an ordinary new branch
// that happens to be named after one.
func OpenLinearBranch(deps Deps, cfg *config.Settings, repo RepoContext, c Candidate, opts open.Options) (open.Outcome, error) {
	tgt := c.Target
	return open.RunWorktree(deps.Open, cfg, open.WorktreeRequest{
		RepoRoot:     repo.Root,
		Branch:       c.Branch,
		Base:         c.Base,
		FallbackBase: LinearBaseFallback(c.Base),
		Label:        tgt.Label(),
		Target:       &tgt,
	}, opts)
}
