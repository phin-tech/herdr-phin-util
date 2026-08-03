package session

import (
	"fmt"
	"path/filepath"

	"github.com/phin-tech/herdr-phin-util/internal/open"
	"github.com/phin-tech/herdr-phin-util/internal/setup"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// SetupLoader reads the setups that could apply to a checkout. It is a
// function rather than an interface because there is exactly one thing to ask
// it, and the picker's tests want to answer that question with a literal.
type SetupLoader func(repoPath string) []setup.Setup

// DefaultSetupLabel is the row that means "no setup": one agent and one
// prompt, which is what the picker did before setups existed and what it
// still does on Enter.
const DefaultSetupLabel = "default"

// SetupRows builds the setup level's list for a row that is about to be
// opened: the default first, then every setup that applies to it.
//
// The default leads because it is the common answer and the one Enter already
// gives you -- the setup level exists to offer the alternatives, not to make
// you re-choose the norm.
//
// prs resolves whether c's target is stacked (see stackedSubject) -- it is
// open.PRLookup rather than a session-local interface because the picker
// already has one wired up as deps.Open.PRs, and a second interface saying
// the same thing would just be more surface to keep in sync. Pass nil from a
// caller with no PR lookup available; a stack-aware setup then simply never
// matches, the same way it would not match a repository this plugin knows
// nothing about.
func SetupRows(load SetupLoader, prs open.PRLookup, cfg AgentKindNamer, c Candidate) []Candidate {
	rows := []Candidate{{
		Kind:   KindSetup,
		Label:  DefaultSetupLabel,
		Detail: "one " + cfg.AgentKind() + ", prompt typed not sent",
	}}
	if load == nil {
		return rows
	}

	setups := load(c.Path)
	for _, s := range setup.Match(setups, stackedSubject(prs, setups, c)) {
		row := Candidate{
			Kind:   KindSetup,
			Label:  s.Name,
			Detail: s.Description,
			Setup:  &s,
		}
		if row.Detail == "" {
			row.Detail = fmt.Sprintf("%d tab%s", len(s.Tabs), pluralS(len(s.Tabs)))
		}
		// Where a setup came from is worth saying on the row: two setups with
		// the same name resolve by precedence, which is otherwise invisible at
		// exactly the moment you are choosing between them.
		row.Detail = fmt.Sprintf("%s — %s", row.Detail, s.Origin)
		rows = append(rows, row)
	}
	return rows
}

// AgentKindNamer is the sliver of the config the default row's text needs.
// config.Settings satisfies it.
type AgentKindNamer interface {
	AgentKind() string
}

// SetupSubject describes a row for matching. A link row carries a parsed
// target, so its kind, owner and repo are known before anything is fetched;
// a checkout row knows only where it is, which is enough for a repos/ scope
// and a branch glob.
func SetupSubject(c Candidate) setup.Subject {
	sub := setup.Subject{Branch: c.Branch}
	if c.Path != "" {
		sub.RepoName = filepath.Base(c.Path)
	}

	switch c.Kind {
	case KindLink, KindClone:
		sub.Kind = c.Target.Kind
		sub.Owner = c.Target.Owner
		sub.Repo = c.Target.Repo
	case KindLinearBase:
		// Both halves are known here and neither would be on its own: the kind
		// comes from the ticket, and the repository from the checkout that was
		// picked for it. This is the only row where a linear setup can also be
		// scoped by repos:.
		sub.Kind = c.Target.Kind
		sub.Repo = sub.RepoName
	case KindProject, KindSpace:
		sub.Kind = target.KindProject
		sub.Repo = sub.RepoName
	default:
		// A worktree or branch row is a checkout being opened, which is the
		// project kind as far as a setup is concerned -- the branch is carried
		// separately.
		sub.Kind = target.KindProject
		sub.Repo = sub.RepoName
	}
	return sub
}

// stackedSubject is SetupSubject plus the one field it cannot fill in on its
// own: Stacked, resolved here rather than there because resolving it can mean
// a `gh` round trip, and SetupSubject is used nowhere else that would want to
// pay for one.
//
// The gate is "did anyone ask for this?", the same shape resolveLists and
// ForEachNames already apply to for_each's list sources (see
// internal/open/stack.go): only when candidates -- the setups actually being
// matched against -- has at least one applies_to: [github_stack] entry is
// gh consulted at all. A machine with no stack setups pays nothing, on every
// keystroke that opens the setup level, for every pull request it ever
// visits.
func stackedSubject(prs open.PRLookup, candidates []setup.Setup, c Candidate) setup.Subject {
	sub := SetupSubject(c)
	if sub.Kind != target.KindGitHubPR || prs == nil || !anyWantsStack(candidates) {
		return sub
	}
	sub.Stacked = isStacked(prs, c.Target)
	return sub
}

// anyWantsStack reports whether any candidate setup's applies_to mentions
// github_stack -- the question that decides whether stackedSubject is worth
// a network call at all.
func anyWantsStack(candidates []setup.Setup) bool {
	for _, s := range candidates {
		for _, kind := range s.AppliesTo {
			if kind == string(target.KindGitHubStack) {
				return true
			}
		}
	}
	return false
}

// isStacked answers "does tgt's chain have 2 or more layers on any path to a
// tip", using gh.Client.Stacks (plural) rather than Stack (singular)
// precisely because Stacks never refuses on a fork -- see #14's Q4. Matching
// is a yes/no question, and a fork must not make it error out from under an
// otherwise ordinary github_pr row; whichever single chain matters is a
// question only for_each's "layers" list (internal/open/stack.go) has to
// answer, and only Stack refuses there.
//
// A gh failure -- rate limit, no network, an old gh with neither the native
// nor the walk path working -- degrades to "not stacked" rather than
// propagating an error SetupRows has nowhere to put: the setup level would
// otherwise have to fail outright over a network blip that has nothing to do
// with whether the row can be opened at all. Falling back to "not stacked"
// costs exactly what it would cost before this feature existed -- the
// github_stack setup is silently not offered, and github_pr ones still are.
func isStacked(prs open.PRLookup, tgt target.Target) bool {
	paths, err := prs.Stacks(tgt.Owner, tgt.Repo, tgt.Number)
	if err != nil {
		return false
	}
	for _, path := range paths {
		if len(path) >= 2 {
			return true
		}
	}
	return false
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
