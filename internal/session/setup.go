package session

import (
	"fmt"
	"path/filepath"

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
func SetupRows(load SetupLoader, cfg AgentKindNamer, c Candidate) []Candidate {
	rows := []Candidate{{
		Kind:   KindSetup,
		Label:  DefaultSetupLabel,
		Detail: "one " + cfg.AgentKind() + ", prompt typed not sent",
	}}
	if load == nil {
		return rows
	}

	for _, s := range setup.Match(load(c.Path), SetupSubject(c)) {
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

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
