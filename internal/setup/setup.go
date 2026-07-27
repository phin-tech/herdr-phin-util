// Package setup holds workspace recipes: what tabs, panes, agents and
// commands a Space should be built with once the target it is for has been
// resolved.
//
// A setup is a recipe applied to a target, not a workspace of its own. The
// worktree, the branch and the label are already decided by the time one of
// these runs; all it describes is what fills the Space -- which is why a
// prompt here renders against the same fields an [agent.prompts] template
// does.
//
// Nothing in this package talks to Herdr or to git. It reads files, decides
// which of them apply, and resolves one into a flat list of steps; executing
// those steps is internal/open's job. That split is what lets the interesting
// parts -- precedence between three sources, cwd and env inheritance, what
// applies to what -- be tested without a running session.
package setup

import (
	"fmt"
	"path"
	"strings"

	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// Origin records which of the three sources a setup came from. It doubles as
// the precedence order (higher wins) and as what the picker shows on a row,
// since two setups sharing a name is otherwise invisible at the moment you
// pick one.
type Origin int

const (
	// OriginGeneric is setups/ in the plugin config directory: offered for
	// every repository.
	OriginGeneric Origin = iota
	// OriginShared is .herdr-setups.yaml inside a checkout -- committed, so
	// everyone working on that repo has it.
	OriginShared
	// OriginRepo is repos/<repo>/ in the plugin config directory: this
	// machine's layouts for one repository. It wins, so a shared setup can be
	// overridden locally without editing a tracked file.
	OriginRepo
)

// String is the one-word label a row or a listing shows.
func (o Origin) String() string {
	switch o {
	case OriginRepo:
		return "repo"
	case OriginShared:
		return "shared"
	default:
		return "generic"
	}
}

// WaitFor holds a pane up until its output says it is ready.
type WaitFor struct {
	Match     string `yaml:"match"`
	TimeoutMs int    `yaml:"timeout_ms"`
}

// DefaultWaitTimeoutMs is used when a wait_for names a match but no timeout.
const DefaultWaitTimeoutMs = 30000

// Pane is one pane in a tab.
//
// The three shapes it can take are mutually exclusive: an agent (with a prompt
// or a skill), a command, or neither -- which is a plain shell, and a
// perfectly good thing to want.
type Pane struct {
	Label string `yaml:"label"`
	// Split is "right" or "down", relative to the pane before it. The first
	// pane in a tab is the tab itself and takes no split.
	Split string `yaml:"split"`
	// Ratio is the new pane's share of the space it is splitting, 0 < r < 1.
	// Zero leaves Herdr's own even split alone.
	Ratio float64 `yaml:"ratio"`
	// Cwd is resolved against the tab's, which is resolved against the
	// setup's, which defaults to the Space's own directory.
	Cwd string            `yaml:"cwd"`
	Env map[string]string `yaml:"env"`

	// Agent is an agent kind for Herdr to start here.
	Agent string `yaml:"agent"`
	// Model is the model to launch that agent with, passed through as
	// "--model <value>". It is not validated: model names change faster than
	// this file could keep up with, and the agent's own rejection is a better
	// error than a stale allowlist's.
	Model string `yaml:"model"`
	// Args are extra command-line arguments for the agent, a list rather than
	// a string so nothing has to be shell-quoted. This is the escape hatch
	// Model is the ergonomic case of: --permission-mode, --sandbox, --add-dir
	// and whatever the agent grows next all go here.
	Args []string `yaml:"args"`
	// Prompt is a Go text/template rendered against the target.
	Prompt string `yaml:"prompt"`
	// Skill is shorthand for a prompt that is just a slash command.
	Skill string `yaml:"skill"`
	// Submit sends the prompt with Enter. Omitted means type it and leave it,
	// which is what the rest of the plugin does with a prompt.
	Submit bool `yaml:"submit"`

	// Command runs in a plain shell pane.
	Command string `yaml:"command"`

	// Focus marks the pane to land on once the layout is built.
	Focus bool `yaml:"focus"`

	WaitFor *WaitFor `yaml:"wait_for"`
}

// Tab is one tab of a setup.
type Tab struct {
	Name string            `yaml:"name"`
	Cwd  string            `yaml:"cwd"`
	Env  map[string]string `yaml:"env"`
	// Command is the single-pane shorthand: a tab that is one command and
	// nothing else. It cannot be combined with Panes.
	Command string `yaml:"command"`
	Panes   []Pane `yaml:"panes"`
}

// EffectivePanes normalises the two ways of writing a tab into the one the
// rest of the code works with. A tab with neither a command nor panes is a
// single empty shell, which is how herdr-plus reads it too.
func (t Tab) EffectivePanes() []Pane {
	if len(t.Panes) > 0 {
		return t.Panes
	}
	return []Pane{{Command: t.Command}}
}

// Setup is one recipe.
type Setup struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// AppliesTo restricts the setup to certain target kinds ("github_pr",
	// "project", ...). Empty means every kind.
	AppliesTo []string `yaml:"applies_to"`
	// Repos are globs over "owner/repo" or a bare repo name. Empty means every
	// repository -- and is the normal case for a file under repos/, where the
	// path has already said which repository this is for.
	Repos []string `yaml:"repos"`
	// Branches are globs over the branch the target resolved to. Empty means
	// every branch. This is how a worktree-specific layout is expressed: a
	// worktree is a branch.
	Branches []string `yaml:"branches"`

	Cwd  string            `yaml:"cwd"`
	Env  map[string]string `yaml:"env"`
	Tabs []Tab             `yaml:"tabs"`

	// Source is the file this was read from, and Origin which of the three
	// sources that file was. Neither is settable from the file itself.
	Source string `yaml:"-"`
	Origin Origin `yaml:"-"`
	// ScopedRepo is the repository a repos/<repo>/ directory named, empty for
	// the other two origins.
	ScopedRepo string `yaml:"-"`
}

// file is the shape of a document that holds several setups -- the in-repo
// .herdr-setups.yaml. A file under setups/ or repos/ is a single setup at the
// top level instead, so both are tried when decoding.
type file struct {
	Setups []Setup `yaml:"setups"`
}

// splitDirections are the two Herdr understands.
var splitDirections = map[string]bool{"right": true, "down": true}

// DefaultSplit is what a pane that asks for a split without saying which way
// gets. Down matches herdr-plus, and stacking is the safer guess: a terminal
// is usually wider than it is tall, but a third of a stack is still readable
// where a quarter of a row is not.
const DefaultSplit = "down"

// Validate reports everything wrong with a setup, as a list rather than a
// first error: someone fixing a file they just wrote wants to hear about all
// of it, not to rerun the command four times.
func (s Setup) Validate() []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(s.Name) == "" {
		add("no name")
	}
	for _, kind := range s.AppliesTo {
		if !knownKind(kind) {
			add("applies_to %q is not a target kind (%s)", kind, strings.Join(kindNames(), ", "))
		}
	}
	if len(s.Tabs) == 0 {
		add("no tabs")
	}

	focused := 0
	for i, tab := range s.Tabs {
		where := fmt.Sprintf("tab %d", i+1)
		if tab.Name != "" {
			where = fmt.Sprintf("tab %q", tab.Name)
		}
		if tab.Command != "" && len(tab.Panes) > 0 {
			add("%s has both a command and panes -- use one or the other", where)
		}

		for j, pane := range tab.EffectivePanes() {
			at := fmt.Sprintf("%s pane %d", where, j+1)

			if j == 0 && pane.Split != "" {
				add("%s is the tab's first pane, so it cannot split", at)
			}
			if j > 0 && pane.Split != "" && !splitDirections[pane.Split] {
				add("%s: split %q is not \"right\" or \"down\"", at, pane.Split)
			}
			if pane.Ratio != 0 && (pane.Ratio <= 0 || pane.Ratio >= 1) {
				add("%s: ratio %v is not between 0 and 1", at, pane.Ratio)
			}
			if pane.Agent == "" && (pane.Prompt != "" || pane.Skill != "" || pane.Submit) {
				add("%s has a prompt but no agent to type it into", at)
			}
			if pane.Agent == "" && pane.Model != "" {
				add("%s sets a model but has no agent to launch -- a command pane spells its own flags out", at)
			}
			if pane.Agent == "" && len(pane.Args) > 0 {
				add("%s sets args but has no agent to pass them to -- a command pane spells its own flags out", at)
			}
			if pane.Prompt != "" && pane.Skill != "" {
				add("%s has both a prompt and a skill -- use one or the other", at)
			}
			if pane.Agent != "" && pane.Command != "" {
				add("%s has both an agent and a command -- use one or the other", at)
			}
			if pane.WaitFor != nil && strings.TrimSpace(pane.WaitFor.Match) == "" {
				add("%s: wait_for has no match to wait for", at)
			}
			if pane.Focus {
				focused++
			}
		}
	}
	if focused > 1 {
		add("%d panes are marked focus -- only one can be", focused)
	}

	return problems
}

// knownKind reports whether a string names a target kind. The list lives in
// internal/target; this only has to agree with it.
func knownKind(kind string) bool {
	for _, k := range allKinds {
		if string(k) == kind {
			return true
		}
	}
	return false
}

var allKinds = []target.Kind{
	target.KindGitHubPR,
	target.KindGitHubIssue,
	target.KindGitHubRepo,
	target.KindLinear,
	target.KindPlain,
	target.KindProject,
}

func kindNames() []string {
	out := make([]string, 0, len(allKinds))
	for _, k := range allKinds {
		out = append(out, string(k))
	}
	return out
}

// Subject is what a setup is matched against: the target that has already been
// resolved, plus where it landed.
type Subject struct {
	Kind   target.Kind
	Owner  string
	Repo   string
	Branch string
	// RepoName is the checkout's directory name, which is what a repos/<name>/
	// directory is keyed by. It is often but not always the same as Repo --
	// a worktree directory can be named for its branch.
	RepoName string
}

// Matches reports whether a setup should be offered for a subject.
func (s Setup) Matches(sub Subject) bool {
	if len(s.AppliesTo) > 0 && sub.Kind != "" && !contains(s.AppliesTo, string(sub.Kind)) {
		return false
	}
	if s.ScopedRepo != "" && !matchesRepoName(s.ScopedRepo, sub) {
		return false
	}
	if len(s.Repos) > 0 && !matchAny(s.Repos, repoNames(sub)) {
		return false
	}
	if len(s.Branches) > 0 {
		if sub.Branch == "" || !matchAny(s.Branches, []string{sub.Branch}) {
			return false
		}
	}
	return true
}

// matchesRepoName checks a repos/<...>/ directory's implied scope. The
// directory is either "repo" or "owner/repo", and matching is exact rather
// than glob: a directory name is a name, and someone who wants a pattern has
// the repos key for it.
func matchesRepoName(scope string, sub Subject) bool {
	for _, name := range repoNames(sub) {
		if strings.EqualFold(scope, name) {
			return true
		}
	}
	return false
}

// repoNames is every way of naming the subject's repository that a setup could
// reasonably be written against.
func repoNames(sub Subject) []string {
	var out []string
	if sub.Owner != "" && sub.Repo != "" {
		out = append(out, sub.Owner+"/"+sub.Repo)
	}
	if sub.Repo != "" {
		out = append(out, sub.Repo)
	}
	if sub.RepoName != "" && sub.RepoName != sub.Repo {
		out = append(out, sub.RepoName)
	}
	return out
}

// matchAny reports whether any of the candidates matches any of the patterns.
// Patterns are shell globs, which is the same vocabulary [projects].roots
// already uses.
func matchAny(patterns, candidates []string) bool {
	for _, pattern := range patterns {
		for _, candidate := range candidates {
			if ok, err := path.Match(pattern, candidate); err == nil && ok {
				return true
			}
			if strings.EqualFold(pattern, candidate) {
				return true
			}
		}
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// Match filters a loaded list down to the setups that apply to a subject,
// keeping the order Load produced.
func Match(setups []Setup, sub Subject) []Setup {
	out := make([]Setup, 0, len(setups))
	for _, s := range setups {
		if s.Matches(sub) {
			out = append(out, s)
		}
	}
	return out
}

// Find returns the setup with a given name, or false.
func Find(setups []Setup, name string) (Setup, bool) {
	for _, s := range setups {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return Setup{}, false
}
