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
//
// # YAML is the front end, not the model
//
// The pipeline is: a file becomes a [Setup], [Setup.Validate] says whether it
// is well-formed, and [Setup.ResolveData] turns it into a [Plan] of ordered
// steps. Only the first of those four is YAML-shaped. That is deliberate, and
// it is what a second front end -- a real language, for the setups a
// declaration cannot express -- would slot into: it would build a [Setup] and
// inherit validation and resolution unchanged, rather than building a [Plan]
// and silently bypassing every rule in Validate. Two properties keep that door
// open, and both are worth preserving on purpose:
//
//   - [Plan] and [Step] stay plain data. No yaml tags, no map[string]any,
//     nothing that only makes sense for a file.
//   - A [Setup] is constructible in memory, with no load-time side effects.
//     Source, Origin and ScopedRepo are set only by Load and read only by the
//     picker and by Matches; their zero values are meaningful, so a Setup that
//     never came from a file is still a valid one.
//
// TestSetupIsConstructibleWithoutYAML pins the second property. One caveat on
// the first: unknown-key rejection is not in Validate -- it is
// yaml.Decoder.KnownFields in load.go, so it protects the YAML front end only.
// Validate is the shared contract; strictness about a file's spelling is not.
//
// The reason this is written down rather than assumed is scope. for_each (see
// [Tab.ForEach]) is one loop, and one loop is a loop. A second control-flow
// feature -- a when:, arithmetic in a template, a loop over a loop -- would
// make this a programming language expressed in YAML, which is a bad one. That
// request is the signal to add a second front end instead of growing this
// dialect further; see issue #10 for why Starlark is the candidate.
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

// WorktreeSpec pins a tab to a git ref of its own, checked out in a worktree
// the tab's own cwd points at, rather than sharing the Space's one worktree
// (see #12). It is deliberately not folded into Cwd: a tab with both is two
// answers to the same question ("where does this tab live"), not a
// precedence rule, and Validate rejects the combination outright.
//
// This is stage one of #12 -- an ordinary tab, no for_each in sight. The
// shape is written to accept for_each without rework: Ref already renders as
// a per-iteration template (see ResolveData's tabCwd handling, which this
// follows exactly), so a for_each tab whose Ref varies per element already
// works once stage two adds the two validation rules that belong to it: a
// constant Ref on a for_each tab (every element would build an identical
// worktree, which is always a mistake), and detach: false on a for_each tab
// (every element would fight over the same branch checkout). Neither rule is
// implemented here on purpose.
type WorktreeSpec struct {
	// Ref is a Go text/template, rendered against the same per-iteration data
	// a tab's Cwd is -- a branch, a tag, or a bare commit SHA.
	Ref string `yaml:"ref"`
	// Detach is a pointer so "not set" and "set to false" are distinguishable:
	// nil means true. Detached is the default because a branch cannot be
	// checked out in two worktrees at once and moves under you if someone
	// pushes to it mid-review -- neither is a problem for a detached HEAD.
	// detach: false opts into a branch checkout instead, for the single-tab
	// case where the point is to commit on it; safe in stage one only because
	// there is no for_each to make several elements fight over the same
	// branch.
	Detach *bool `yaml:"detach"`
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
	// Worktree pins this tab to its own ref -- see WorktreeSpec -- instead of
	// the Space's own worktree. nil is the ordinary case: a tab with no
	// worktree: uses the Space's own cwd exactly as it always has.
	Worktree *WorktreeSpec `yaml:"worktree"`

	// ForEach names a list carried in a [Data] passed to ResolveData, one that
	// this tab is rendered once per element of -- and deliberately a name,
	// never a template expression. promptData is a flat map[string]string,
	// and Render's missingkey=zero only means what it means today because
	// nothing that flows through {{ }} is anything but a string; the moment
	// a template placeholder could itself be a list, Render would need to
	// become a second, richer dialect to cope with it. Resolving the name
	// outside the template layer keeps Render exactly as it was and makes
	// what for_each does readable without knowing Go's text/template rules.
	ForEach string `yaml:"for_each"`
	// As is the prefix each element's fields render under: {{.layer_pr}}, not
	// {{.layer.pr}}. Nested access would need map[string]any underneath
	// Vars, which drags missingkey=zero somewhere far less predictable for
	// the sake of prettier YAML -- see ResolveData for how the prefix is
	// built. Defaults to ForEach itself when left blank, since most setups
	// have no reason to say the same word twice.
	As string `yaml:"as"`
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

		// forEach is the trimmed name this tab repeats over, or "" for an
		// ordinary tab -- computed once so both the shape checks below and
		// the per-pane focus check agree on which kind of tab this is.
		forEach := strings.TrimSpace(tab.ForEach)
		if tab.ForEach != "" && forEach == "" {
			add("%s: for_each has nothing after it -- name the list to repeat over, or delete the key", where)
		}
		if forEach == "" && strings.TrimSpace(tab.As) != "" {
			add("%s: as %q is set without a for_each to repeat over", where, tab.As)
		}

		// cwd: and worktree: are two answers to "where does this tab live",
		// not a precedence rule -- a setup that sets both said something
		// contradictory, not something ambiguous, and should be told so
		// rather than have one silently win.
		if tab.Worktree != nil && strings.TrimSpace(tab.Cwd) != "" {
			add("%s has both cwd and worktree -- they are two answers to where the tab lives, use one", where)
		}
		if tab.Worktree != nil && strings.TrimSpace(tab.Worktree.Ref) == "" {
			add("%s: worktree has no ref to check out", where)
		}
		if forEach != "" && tab.Worktree != nil {
			// A ref that does not vary per element builds N identical
			// worktrees, which is never what was meant: the element was not
			// used. Checked the only way Validate can check it -- against the
			// unrendered template, asking whether it names this tab's own
			// element prefix -- which is exactly the bar the focus rule below
			// holds itself to. Not sound, deliberately: a ref could name
			// {{.layer_index}} and still be wrong. It catches the literal
			// mistake, which is the one that ships.
			if strings.TrimSpace(tab.Worktree.Ref) != "" && !referencesElement(tab.Worktree.Ref, elementPrefix(tab)) {
				add("%s: worktree ref %q does not vary per element -- every repetition would build the same worktree, so name {{.%s...}} in it", where, tab.Worktree.Ref, elementPrefix(tab))
			}
			// A branch cannot be checked out in two worktrees at once, so the
			// second element onwards would fail outright in git. Detached is
			// the default precisely so this is not the common shape; asking
			// for it explicitly inside a repeated tab is always a mistake.
			if tab.Worktree.Detach != nil && !*tab.Worktree.Detach {
				add("%s: detach false inside a for_each tab -- a branch cannot be checked out in two worktrees at once, so every element after the first would fail", where)
			}
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
				if forEach != "" {
					// Cardinality is the one thing a for_each tab makes
					// dynamic (see the package-level rationale), and this is
					// where that bites: every element would render this
					// pane with Focus set, marking N panes across N tabs,
					// and only the last one built would quietly end up
					// focused. That is silent enough to ship and confusing
					// enough to debug that it is worth refusing outright
					// rather than folding it into the plain multi-focus
					// count below, which only ever sees one template.
					add("%s: focus true inside a for_each tab -- every repeated instance would set it, and only the last one built would win", at)
				} else {
					focused++
				}
			}
		}
	}
	if focused > 1 {
		add("%d panes are marked focus -- only one can be", focused)
	}

	return problems
}

// elementPrefix is what each of a for_each tab's element fields renders
// under: the as: name, or the list's own name when as: was left off. It is
// the same rule tabIterations applies when it builds the per-element vars,
// and the two have to agree or a validation message would name a prefix that
// does not exist.
func elementPrefix(tab Tab) string {
	as := strings.TrimSpace(tab.As)
	if as == "" {
		as = strings.TrimSpace(tab.ForEach)
	}
	return as + "_"
}

// referencesElement reports whether a template mentions this tab's own
// per-element fields, which is how Validate answers "does this vary per
// element" without rendering anything.
//
// Deliberately loose: it looks for ".<prefix>" rather than parsing the
// template, so it errs toward saying yes. A false yes accepts a setup that
// might still be wrong; a false no would reject a setup that is right, and
// blocking valid configuration is the worse failure for a rule whose whole
// job is catching an obvious mistake.
func referencesElement(text, prefix string) bool {
	return strings.Contains(text, "."+prefix)
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

// ForEachNames returns the list names this setup's tabs repeat over --
// deduplicated, trimmed, blank names skipped, in first-seen order so a
// caller resolving them reports problems in the same order the file lists
// them.
//
// This is what makes resolving a list source lazy: internal/open calls it
// before doing anything that would fetch one, and asks only for the names it
// returns. A setup with no for_each anywhere -- most of them -- gets an empty
// slice, and its caller does no extra work at all; a setup naming a list
// nobody produces still resolves nothing and falls through to the existing
// "provides no lists" error at ResolveData time, unchanged.
func (s Setup) ForEachNames() []string {
	var out []string
	seen := make(map[string]bool)
	for _, tab := range s.Tabs {
		name := strings.TrimSpace(tab.ForEach)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
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
