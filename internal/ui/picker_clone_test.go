package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/gitcmd"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/open"
	"github.com/phin-tech/herdr-phin-util/internal/session"
)

// clonePicker points the repo templates at a temp directory, so "is it already
// cloned?" is answerable in a test.
func clonePicker(t *testing.T, root string, workspaces []herdr.Workspace, candidates ...session.Candidate) *Picker {
	t.Helper()
	cfg := &config.Settings{
		RepoTemplates: []string{filepath.Join(root, "{host}", "{owner}", "{repo}")},
		Agent:         config.AgentSettings{Enabled: true, Kind: "claude"},
	}
	return NewPicker(cfg, session.Deps{}, nil, candidates).WithWorkspaces(workspaces)
}

func TestRepoURLThatIsNotClonedOffersToClone(t *testing.T) {
	p := clonePicker(t, t.TempDir(), nil, project("other", "/src/other"))

	typeInto(p, "https://github.com/phin-tech/roux")

	if len(p.filtered) != 1 {
		t.Fatalf("got %v, want one row", labels(p.filtered))
	}
	c := p.filtered[0]
	if c.Kind != session.KindClone {
		t.Fatalf("kind = %q, want %q", c.Kind, session.KindClone)
	}
	if c.Label != "roux" {
		t.Errorf("label = %q, want roux", c.Label)
	}
	if !strings.Contains(c.Detail, "clone to") {
		t.Errorf("detail = %q, want it to name the destination", c.Detail)
	}
}

// The same rule as everywhere else: what is already here is not offered as
// something to fetch.
func TestRepoURLThatIsAlreadyClonedOffersToOpen(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "github.com", "phin-tech", "roux")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	p := clonePicker(t, root, nil)
	typeInto(p, "https://github.com/phin-tech/roux")

	c := p.filtered[0]
	if c.Kind != session.KindProject {
		t.Fatalf("kind = %q, want %q for a checkout that exists", c.Kind, session.KindProject)
	}
	if c.Path != dest {
		t.Errorf("path = %q, want %q", c.Path, dest)
	}
	if !strings.Contains(c.Detail, "already cloned") {
		t.Errorf("detail = %q", c.Detail)
	}
}

func TestRepoURLThatIsAlreadyOpenOffersToSwitch(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "github.com", "phin-tech", "roux")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	p := clonePicker(t, root, []herdr.Workspace{{WorkspaceID: "w5", Label: "roux"}})
	typeInto(p, "https://github.com/phin-tech/roux")

	c := p.filtered[0]
	if c.Kind != session.KindSpace || c.WorkspaceID != "w5" {
		t.Errorf("got %+v, want the open Space", c)
	}
}

func TestSSHRemoteAlsoOffersToClone(t *testing.T) {
	p := clonePicker(t, t.TempDir(), nil)

	typeInto(p, "git@github.com:phin-tech/roux.git")

	if p.filtered[0].Kind != session.KindClone {
		t.Errorf("kind = %q, want a clone row", p.filtered[0].Kind)
	}
}

func TestOwnerRepoShorthandOffersToClone(t *testing.T) {
	p := clonePicker(t, t.TempDir(), nil, project("other", "/src/other"))

	typeInto(p, "phin-tech/roux")

	if len(p.filtered) != 1 || p.filtered[0].Kind != session.KindClone {
		t.Fatalf("got %v, want a clone row", labels(p.filtered))
	}
	if p.filtered[0].Target.Owner != "phin-tech" {
		t.Errorf("target = %+v", p.filtered[0].Target)
	}
}

// The collision that makes the shorthand dangerous: one level down, that shape
// is a branch name, and must stay one.
func TestShorthandIsNotRecognisedAtTheWorktreeLevel(t *testing.T) {
	cfg := &config.Settings{
		RepoTemplates: []string{filepath.Join(t.TempDir(), "{host}", "{owner}", "{repo}")},
		Agent:         config.AgentSettings{Enabled: true, Kind: "claude"},
	}
	repo := session.RepoContext{Name: "roux-next-gen", Root: "/src/roux-next-gen", DefaultBranch: "main"}
	p := NewWorktreePicker(cfg, session.Deps{}, nil, repo, []session.Candidate{
		{Kind: session.KindBranch, Label: "codex/iterm-split", Branch: "codex/iterm-split"},
	})

	typeInto(p, "codex/iterm-split")

	for _, c := range p.filtered {
		if c.Kind == session.KindClone {
			t.Fatal("a branch name must not be read as a repository to clone")
		}
	}
	if len(p.filtered) == 0 {
		t.Fatal("expected the branch to still match")
	}
	if p.filtered[0].Kind != session.KindBranch {
		t.Errorf("kind = %q, want the branch row", p.filtered[0].Kind)
	}
}

// A brand-new branch named like owner/repo must still be creatable.
func TestShorthandShapedNewBranchStillOffersCreate(t *testing.T) {
	cfg := &config.Settings{
		RepoTemplates: []string{filepath.Join(t.TempDir(), "{host}", "{owner}", "{repo}")},
		Agent:         config.AgentSettings{Enabled: true, Kind: "claude"},
	}
	repo := session.RepoContext{Name: "roux", Root: "/src/roux", DefaultBranch: "main"}
	p := NewWorktreePicker(cfg, session.Deps{}, nil, repo, nil)

	typeInto(p, "sam/new-idea")

	if len(p.filtered) != 1 {
		t.Fatalf("got %v, want the create row", labels(p.filtered))
	}
	if p.filtered[0].Kind != session.KindNewBranch {
		t.Errorf("kind = %q, want a new branch", p.filtered[0].Kind)
	}
}

// Ordinary filter text with no slash is untouched by any of this.
func TestPlainFilterIsUnaffectedByCloneSupport(t *testing.T) {
	p := clonePicker(t, t.TempDir(), nil, project("roux", "/src/roux"), project("other", "/src/other"))

	typeInto(p, "roux")

	if len(p.filtered) != 1 || p.filtered[0].Kind != session.KindProject {
		t.Errorf("got %v, want the ordinary filtered list", labels(p.filtered))
	}
}

// A clone row starts an agent once the checkout is open, so the prompt editor
// applies to it.
func TestCloneRowOffersThePromptEditor(t *testing.T) {
	p := clonePicker(t, t.TempDir(), nil)
	typeInto(p, "phin-tech/roux")

	p.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if !p.editing {
		t.Error("a clone row ends in a Space with an agent, so it has a prompt")
	}
}

// A repo reference that is already on disk behaves like any other project
// row, so tab descends into its branches.
func TestTabDescendsFromAnAlreadyClonedRepoRef(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "github.com", "phin-tech", "roux")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	wt := &stubWorktrees{
		source:    herdr.WorktreeSource{RepoName: "roux", RepoRoot: dest},
		worktrees: []herdr.Worktree{{Path: dest, Branch: "main"}},
	}
	git := &stubBrancher{fallback: "main", branches: []gitcmd.Branch{{Name: "feature"}}}

	cfg := &config.Settings{
		RepoTemplates: []string{filepath.Join(root, "{host}", "{owner}", "{repo}")},
		Agent:         config.AgentSettings{Enabled: true, Kind: "claude"},
	}
	p := NewPicker(cfg, session.Deps{Worktrees: wt, Git: git}, nil, nil).WithWorkspaces(nil)

	typeInto(p, "https://github.com/phin-tech/roux")
	if p.filtered[0].Kind != session.KindProject {
		t.Fatalf("kind = %q, want a project row", p.filtered[0].Kind)
	}

	press(p, tea.KeyMsg{Type: tea.KeyTab})

	if p.level != levelWorktrees {
		t.Fatal("tab should descend into an already-cloned repo")
	}
	if p.repo.Name != "roux" {
		t.Errorf("repo = %q, want roux", p.repo.Name)
	}
	if len(p.filtered) != 2 {
		t.Errorf("got %v, want the worktree and the branch", labels(p.filtered))
	}
}

// tab on a repository you do not have fetches it first, then shows its
// branches -- one pass from "owner/repo" to a brand new branch.
func TestTabOnACloneRowClonesThenDescends(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "github.com", "phin-tech", "roux")

	cloner := &stubCloner{makeDir: dest}
	wt := &stubWorktrees{
		source:    herdr.WorktreeSource{RepoName: "roux", RepoRoot: dest},
		worktrees: []herdr.Worktree{{Path: dest, Branch: "main"}},
	}
	git := &stubBrancher{fallback: "main"}

	cfg := &config.Settings{
		RepoTemplates: []string{filepath.Join(root, "{host}", "{owner}", "{repo}")},
		Agent:         config.AgentSettings{Enabled: true, Kind: "claude"},
	}
	deps := session.Deps{
		Worktrees: wt,
		Git:       git,
		Open:      open.Deps{Clone: cloner},
	}
	p := NewPicker(cfg, deps, nil, nil).WithWorkspaces(nil)

	typeInto(p, "phin-tech/roux")
	if p.filtered[0].Kind != session.KindClone {
		t.Fatalf("kind = %q, want a clone row", p.filtered[0].Kind)
	}

	press(p, tea.KeyMsg{Type: tea.KeyTab})

	if len(cloner.calls) != 1 {
		t.Fatalf("expected one clone, got %d", len(cloner.calls))
	}
	if p.level != levelWorktrees {
		t.Fatalf("expected to land at the worktree level, err=%v", p.err)
	}
	if p.repo.Name != "roux" {
		t.Errorf("repo = %q, want roux", p.repo.Name)
	}

	// And the whole point: a custom branch is now one more line of typing.
	typeInto(p, "my-new-thing")
	if len(p.filtered) != 1 || p.filtered[0].Kind != session.KindNewBranch {
		t.Fatalf("got %v, want a create row", labels(p.filtered))
	}
	if p.filtered[0].Branch != "my-new-thing" {
		t.Errorf("branch = %q", p.filtered[0].Branch)
	}
}

// A clone that fails leaves the picker where it was, with the reason showing.
func TestFailedCloneDescentStaysPut(t *testing.T) {
	root := t.TempDir()
	cloner := &stubCloner{err: errors.New("repository not found")}
	cfg := &config.Settings{
		RepoTemplates: []string{filepath.Join(root, "{host}", "{owner}", "{repo}")},
		Agent:         config.AgentSettings{Enabled: true, Kind: "claude"},
	}
	deps := session.Deps{
		Worktrees: &stubWorktrees{},
		Git:       &stubBrancher{},
		Open:      open.Deps{Clone: cloner},
	}
	p := NewPicker(cfg, deps, nil, nil).WithWorkspaces(nil)

	typeInto(p, "phin-tech/nope")
	press(p, tea.KeyMsg{Type: tea.KeyTab})

	if p.level != levelProjects {
		t.Error("a failed clone should not change level")
	}
	if p.err == nil {
		t.Error("the failure should be reported")
	}
	if p.running {
		t.Error("the picker should not be left running")
	}
}

// The hint has to say what tab would actually do on this row.
func TestHintSaysCloneAndBranchOnACloneRow(t *testing.T) {
	p := clonePicker(t, t.TempDir(), nil)
	typeInto(p, "phin-tech/roux")

	if got := p.viewPickerHint(); !strings.Contains(got, "clone & branch") {
		t.Errorf("hint = %q, want it to name the clone", got)
	}
}

type stubCloner struct {
	calls   []string
	makeDir string
	err     error
}

func (s *stubCloner) Clone(owner, repo, dest string) error {
	s.calls = append(s.calls, owner+"/"+repo)
	if s.err != nil {
		return s.err
	}
	if s.makeDir != "" {
		return os.MkdirAll(s.makeDir, 0o755)
	}
	return nil
}
