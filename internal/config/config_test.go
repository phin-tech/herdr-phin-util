package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/target"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A missing file is the normal first run, not an error: the plugin has to work
// before anyone has written any settings.
func TestLoadFromUsesDefaultsWhenFileMissing(t *testing.T) {
	got, err := LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(got.RepoTemplates) == 0 {
		t.Error("want at least one default repo template")
	}
	if got.Agent.Kind != DefaultAgentKind {
		t.Errorf("Agent.Kind = %q, want %q", got.Agent.Kind, DefaultAgentKind)
	}
	if len(got.Problems) != 0 {
		t.Errorf("Problems = %v, want none for a missing file", got.Problems)
	}
}

func TestLoadFromReadsSettings(t *testing.T) {
	dir := writeConfig(t, `
[repos]
templates = ["~/work/{owner}/{repo}", "~/src/{host}/{owner}/{repo}"]

[agent]
enabled = false
kind = "codex"

[agent.prompts]
github_pr = "PR {{.Number}}"
linear = "Issue {{.Issue}}"
plain = "{{.Text}}"
`)
	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(got.RepoTemplates) != 2 || got.RepoTemplates[0] != "~/work/{owner}/{repo}" {
		t.Errorf("RepoTemplates = %v", got.RepoTemplates)
	}
	if got.Agent.Enabled {
		t.Error("Agent.Enabled should honour an explicit false")
	}
	if got.Agent.Kind != "codex" {
		t.Errorf("Agent.Kind = %q, want codex", got.Agent.Kind)
	}
	if got.Prompts.For(target.KindGitHubPR) != "PR {{.Number}}" {
		t.Errorf("github_pr prompt = %q", got.Prompts.For(target.KindGitHubPR))
	}
}

// enabled is a pointer internally so that an omitted key keeps the default
// rather than silently reading as false.
func TestOmittedEnabledKeepsDefault(t *testing.T) {
	dir := writeConfig(t, "[agent]\nkind = \"claude\"\n")
	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !got.Agent.Enabled {
		t.Error("omitting agent.enabled should leave it at the default of true")
	}
}

// A typo should not stop the plugin working, but it must not pass silently
// either -- an unreported bad value looks like the setting was ignored.
func TestBrokenFileIsReportedNotFatal(t *testing.T) {
	dir := writeConfig(t, "this is not = valid toml [[[")
	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("a broken file should not be fatal: %v", err)
	}
	if len(got.Problems) == 0 {
		t.Error("want the parse failure reported in Problems")
	}
	if got.Agent.Kind != DefaultAgentKind {
		t.Errorf("Agent.Kind = %q, want the default after a parse failure", got.Agent.Kind)
	}
}

func TestUnknownAgentKindIsReported(t *testing.T) {
	dir := writeConfig(t, "[agent]\nkind = \"not-an-agent\"\n")
	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(got.Problems) == 0 {
		t.Error("want an unknown agent kind reported")
	}
	if got.Agent.Kind != DefaultAgentKind {
		t.Errorf("Agent.Kind = %q, want a fallback to the default", got.Agent.Kind)
	}
}

func TestResolveRepoPicksFirstExistingTemplate(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, "src", "github.com", "phin-tech", "herdr-phin-util")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}

	s := &Settings{
		RepoTemplates: []string{
			// The first does not exist, so resolution must fall through.
			filepath.Join(home, "work", "{owner}", "{repo}"),
			filepath.Join(home, "src", "{host}", "{owner}", "{repo}"),
		},
	}
	tgt := target.Parse("https://github.com/phin-tech/herdr-phin-util/pull/1")
	got, tried, err := s.ResolveRepo(tgt)
	if err != nil {
		t.Fatalf("ResolveRepo: %v (tried %v)", err, tried)
	}
	if got != want {
		t.Errorf("ResolveRepo = %q, want %q", got, want)
	}
}

// When nothing matches, the paths that were tried are the whole diagnostic --
// without them the failure is unactionable on a machine with a layout the
// templates do not describe.
func TestResolveRepoReportsWhatItTried(t *testing.T) {
	home := t.TempDir()
	s := &Settings{RepoTemplates: []string{filepath.Join(home, "src", "{owner}", "{repo}")}}
	tgt := target.Parse("https://github.com/phin-tech/nope/pull/1")

	_, tried, err := s.ResolveRepo(tgt)
	if err == nil {
		t.Fatal("want an error when no template matches")
	}
	if len(tried) != 1 || !strings.Contains(tried[0], "phin-tech") {
		t.Errorf("tried = %v, want the expanded candidate path", tried)
	}
}

func TestResolveRepoExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, "src", "phin-tech", "herdr-phin-util")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}

	s := &Settings{RepoTemplates: []string{"~/src/{owner}/{repo}"}}
	tgt := target.Parse("https://github.com/phin-tech/herdr-phin-util/pull/1")
	got, tried, err := s.ResolveRepo(tgt)
	if err != nil {
		t.Fatalf("ResolveRepo: %v (tried %v)", err, tried)
	}
	if got != want {
		t.Errorf("ResolveRepo = %q, want %q", got, want)
	}
}

// A Space named from a pasted string has no repository behind it, so asking
// where it lives is a programming error rather than a lookup miss.
func TestResolveRepoRejectsNonRepoTarget(t *testing.T) {
	s := &Settings{RepoTemplates: []string{"~/src/{owner}/{repo}"}}
	if _, _, err := s.ResolveRepo(target.Parse("just a name")); err == nil {
		t.Fatal("want an error for a target with no repository")
	}
}

func TestPromptsFallBackToDefaults(t *testing.T) {
	got, err := LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []target.Kind{target.KindGitHubPR, target.KindLinear, target.KindPlain} {
		if got.Prompts.For(k) == "" {
			t.Errorf("no default prompt for kind %q", k)
		}
	}
}

// Omitting [worktrees] entirely must stay a no-op: Herdr's own default
// placement under ~/.herdr/worktrees/... is what most machines want.
func TestWorktreePathUnsetByDefault(t *testing.T) {
	got, err := LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.WorktreePath != "" {
		t.Errorf("WorktreePath = %q, want empty when [worktrees] is absent", got.WorktreePath)
	}
	tgt := target.Parse("https://github.com/phin-tech/herdr-phin-util/pull/1")
	if _, ok := got.ResolveWorktreePath(tgt, "/repo", "some-branch"); ok {
		t.Error("ResolveWorktreePath should report ok=false when no template is configured")
	}
}

func TestWorktreePathReadFromFile(t *testing.T) {
	dir := writeConfig(t, "[worktrees]\npath = \"~/wt/{repo}/{branch}\"\n")
	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.WorktreePath != "~/wt/{repo}/{branch}" {
		t.Errorf("WorktreePath = %q", got.WorktreePath)
	}
}

// The branch is sanitized before it becomes a path segment: a PR's branch can
// contain slashes, which git tolerates in a ref but which would otherwise
// collide with the template's own directory separators unpredictably.
func TestResolveWorktreePathExpandsPlaceholdersAndTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := &Settings{WorktreePath: "~/wt/{owner}/{repo}/{branch}"}
	tgt := target.Parse("https://github.com/phin-tech/herdr-phin-util/pull/1")

	got, ok := s.ResolveWorktreePath(tgt, "/repo/root", "Feature/Thing")
	if !ok {
		t.Fatal("want ok=true when a template is configured")
	}
	want := filepath.Join(home, "wt", "phin-tech", "herdr-phin-util", "feature/thing")
	if got != want {
		t.Errorf("ResolveWorktreePath = %q, want %q", got, want)
	}
}

// {repo_root} lets a template nest the worktree inside the checkout it came
// from, which is the natural layout for anyone who has not opted into a
// central worktree directory.
func TestResolveWorktreePathRepoRootPlaceholder(t *testing.T) {
	s := &Settings{WorktreePath: "{repo_root}/.worktrees/{branch}"}
	tgt := target.Parse("https://linear.app/phin/issue/ENG-9/do-the-thing")

	got, ok := s.ResolveWorktreePath(tgt, "/repo/root", tgt.Branch())
	if !ok {
		t.Fatal("want ok=true when a template is configured")
	}
	want := "/repo/root/.worktrees/eng-9-do-the-thing"
	if got != want {
		t.Errorf("ResolveWorktreePath = %q, want %q", got, want)
	}
}

// With no [worktrees].path configured, a tab's own worktree still needs
// somewhere concrete to live -- unlike ResolveWorktreePath, there is no Herdr
// call that would pick it instead -- so this falls back to a default keyed on
// repo root and ref alone.
func TestResolveTabWorktreePathDefaultsWhenUnconfigured(t *testing.T) {
	s := &Settings{}
	tgt := target.Target{}

	got := s.ResolveTabWorktreePath(tgt, "/repo/root", "main")
	want := "/repo/root/.herdr-worktrees/main"
	if got != want {
		t.Errorf("ResolveTabWorktreePath = %q, want %q", got, want)
	}
}

// Keyed on repo root and ref alone -- no setup name, no run id, no
// timestamp -- is what makes a re-run reuse the same worktree rather than
// accumulate a fresh one every time (#12's explicit preference).
func TestResolveTabWorktreePathIsDeterministicAcrossCalls(t *testing.T) {
	s := &Settings{}
	tgt := target.Target{}

	first := s.ResolveTabWorktreePath(tgt, "/repo/root", "main")
	second := s.ResolveTabWorktreePath(tgt, "/repo/root", "main")
	if first != second {
		t.Errorf("two resolutions disagreed: %q vs %q", first, second)
	}
}

// A configured [worktrees].path is reused for a tab's own worktree too,
// through the new {ref} placeholder -- one configured notion of where this
// plugin's worktrees live, not two.
func TestResolveTabWorktreePathUsesConfiguredTemplate(t *testing.T) {
	s := &Settings{WorktreePath: "{repo_root}/.worktrees/{ref}"}
	tgt := target.Target{}

	got := s.ResolveTabWorktreePath(tgt, "/repo/root", "release/v2")
	want := "/repo/root/.worktrees/release/v2"
	if got != want {
		t.Errorf("ResolveTabWorktreePath = %q, want %q", got, want)
	}
}

// A ref with slashes (a SHA never has one, but a branch name used with
// detach: false can) is sanitized the same way {branch} already is, so it
// becomes safe nested directories rather than colliding with the template's
// own separators unpredictably.
func TestResolveTabWorktreePathSanitizesARefWithSlashes(t *testing.T) {
	s := &Settings{}
	tgt := target.Target{}

	got := s.ResolveTabWorktreePath(tgt, "/repo/root", "Feature/Thing")
	want := "/repo/root/.herdr-worktrees/feature/thing"
	if got != want {
		t.Errorf("ResolveTabWorktreePath = %q, want %q", got, want)
	}
}

// Linear issues resolve from the URL alone today, with no API call. The key
// is still accepted and carried through so a later enrichment step has
// somewhere to read it from without a config format change.
func TestLinearAPIKeyReadFromFile(t *testing.T) {
	dir := writeConfig(t, "[linear]\napi_key = \"lin_abc123\"\n")
	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.LinearAPIKey != "lin_abc123" {
		t.Errorf("LinearAPIKey = %q, want lin_abc123", got.LinearAPIKey)
	}
}

func TestLinearAPIKeyEmptyByDefault(t *testing.T) {
	got, err := LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.LinearAPIKey != "" {
		t.Errorf("LinearAPIKey = %q, want empty when [linear] is absent", got.LinearAPIKey)
	}
}
