package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/gitcmd"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/session"
)

type stubWorktrees struct {
	worktrees []herdr.Worktree
	source    herdr.WorktreeSource
	err       error
}

func (s *stubWorktrees) Worktrees(string) ([]herdr.Worktree, herdr.WorktreeSource, error) {
	return s.worktrees, s.source, s.err
}

type stubBrancher struct {
	branches  []gitcmd.Branch
	fallback  string
	refreshed int
	err       error
}

func (s *stubBrancher) Branches(string) ([]gitcmd.Branch, error) { return s.branches, s.err }
func (s *stubBrancher) DefaultBranch(string) string              { return s.fallback }
func (s *stubBrancher) Refresh(string) error {
	s.refreshed++
	return s.err
}

// navPicker wires a picker whose descent resolves against stubs.
func navPicker(t *testing.T, wt *stubWorktrees, git *stubBrancher, candidates ...session.Candidate) *Picker {
	t.Helper()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}}
	deps := session.Deps{Worktrees: wt, Git: git}
	return NewPicker(cfg, deps, nil, candidates)
}

// run drains the command a key press returned, feeding its message back in the
// way the bubbletea runtime would.
func run(p *Picker, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if msg := cmd(); msg != nil {
		p.Update(msg)
	}
}

func press(p *Picker, key tea.KeyMsg) {
	_, cmd := p.Update(key)
	run(p, cmd)
}

func defaultWorktrees() (*stubWorktrees, *stubBrancher) {
	wt := &stubWorktrees{
		source:    herdr.WorktreeSource{RepoName: "acme", RepoRoot: "/src/acme"},
		worktrees: []herdr.Worktree{{Path: "/src/acme", Branch: "main"}},
	}
	git := &stubBrancher{fallback: "main", branches: []gitcmd.Branch{{Name: "feature"}}}
	return wt, git
}

func TestPickerDescendsIntoARepo(t *testing.T) {
	wt, git := defaultWorktrees()
	p := navPicker(t, wt, git, project("acme", "/src/acme"))

	press(p, tea.KeyMsg{Type: tea.KeyTab})

	if p.level != levelWorktrees {
		t.Fatal("expected the picker to descend")
	}
	if p.repo.Name != "acme" {
		t.Errorf("repo = %q, want acme", p.repo.Name)
	}
	if len(p.filtered) != 2 {
		t.Errorf("got %v, want the worktree and the branch", labels(p.filtered))
	}
	if !strings.Contains(p.View(), "acme") {
		t.Error("the breadcrumb should name the repo")
	}
}

// Descending from an open Space works too: its path is a checkout like any
// other.
func TestPickerDescendsFromASpace(t *testing.T) {
	wt, git := defaultWorktrees()
	p := navPicker(t, wt, git, space("acme", "/src/acme"))

	press(p, tea.KeyMsg{Type: tea.KeyTab})

	if p.level != levelWorktrees {
		t.Fatal("expected a Space row to descend")
	}
}

func TestPickerCannotDescendWithoutAPath(t *testing.T) {
	wt, git := defaultWorktrees()
	p := navPicker(t, wt, git, session.Candidate{Kind: session.KindSpace, Label: "scratch"})

	press(p, tea.KeyMsg{Type: tea.KeyTab})

	if p.level != levelProjects {
		t.Error("a Space with no directory has no repo to descend into")
	}
}

// The top level is restored exactly, so descending is cheap to try.
func TestPickerAscendRestoresTheProjectLevel(t *testing.T) {
	wt, git := defaultWorktrees()
	p := navPicker(t, wt, git,
		project("alpha", "/src/alpha"),
		project("acme", "/src/acme"),
		project("beta", "/src/beta"),
	)

	// A filter that matches one row by name and no other by path either.
	typeInto(p, "acme")
	if len(p.filtered) != 1 {
		t.Fatalf("got %v, want just acme before descending", labels(p.filtered))
	}

	press(p, tea.KeyMsg{Type: tea.KeyTab})
	if p.level != levelWorktrees {
		t.Fatal("expected to descend")
	}
	// The filter is cleared for the new level's contents.
	if p.filter.Value() != "" {
		t.Errorf("filter = %q, want it cleared on descent", p.filter.Value())
	}

	press(p, tea.KeyMsg{Type: tea.KeyEsc})

	if p.level != levelProjects {
		t.Fatal("esc should go back a level, not quit")
	}
	if p.quitting {
		t.Error("esc inside a repo must not quit the popup")
	}
	if p.filter.Value() != "acme" {
		t.Errorf("filter = %q, want the typed filter restored", p.filter.Value())
	}
	if len(p.filtered) != 1 || p.filtered[0].Label != "acme" {
		t.Errorf("got %v, want the filter reapplied", labels(p.filtered))
	}
}

func TestPickerShiftTabAscends(t *testing.T) {
	wt, git := defaultWorktrees()
	p := navPicker(t, wt, git, project("acme", "/src/acme"))

	press(p, tea.KeyMsg{Type: tea.KeyTab})
	press(p, tea.KeyMsg{Type: tea.KeyShiftTab})

	if p.level != levelProjects {
		t.Error("shift+tab should go back a level")
	}
}

// A failed descent leaves you where you were, with the reason on screen.
func TestPickerFailedDescentStaysPut(t *testing.T) {
	wt := &stubWorktrees{err: errors.New("not a git repository")}
	p := navPicker(t, wt, &stubBrancher{}, project("acme", "/src/acme"))

	press(p, tea.KeyMsg{Type: tea.KeyTab})

	if p.level != levelProjects {
		t.Error("a failed descent should not change level")
	}
	if p.err == nil {
		t.Error("the failure should be reported")
	}
	if p.running {
		t.Error("the picker should not be left in the running state")
	}
}

// Text naming no branch becomes an offer to create it.
func TestPickerOffersToCreateANewBranch(t *testing.T) {
	wt, git := defaultWorktrees()
	p := navPicker(t, wt, git, project("acme", "/src/acme"))
	press(p, tea.KeyMsg{Type: tea.KeyTab})

	typeInto(p, "brand-new")

	if len(p.filtered) != 1 {
		t.Fatalf("got %v, want just the create row", labels(p.filtered))
	}
	c := p.filtered[0]
	if c.Kind != session.KindNewBranch || c.Branch != "brand-new" {
		t.Errorf("candidate = %+v, want a new-branch row", c)
	}
	if !strings.Contains(c.Detail, "main") {
		t.Errorf("detail = %q, want it to name the base branch", c.Detail)
	}
}

// Typing the exact name of an existing branch must not also offer to create it.
func TestPickerDoesNotOfferToCreateAnExistingBranch(t *testing.T) {
	wt, git := defaultWorktrees()
	p := navPicker(t, wt, git, project("acme", "/src/acme"))
	press(p, tea.KeyMsg{Type: tea.KeyTab})

	typeInto(p, "feature")

	for _, c := range p.filtered {
		if c.Kind == session.KindNewBranch {
			t.Errorf("should not offer to create the existing branch %q", c.Label)
		}
	}
}

// The create row only exists at the worktree level.
func TestPickerNoCreateRowAtTheProjectLevel(t *testing.T) {
	wt, git := defaultWorktrees()
	p := navPicker(t, wt, git, project("acme", "/src/acme"))

	typeInto(p, "nothing-matches-this")

	for _, c := range p.filtered {
		if c.Kind == session.KindNewBranch {
			t.Error("the project level has no branches to create")
		}
	}
}

func TestPickerRefreshFetchesAndReloads(t *testing.T) {
	wt, git := defaultWorktrees()
	p := navPicker(t, wt, git, project("acme", "/src/acme"))
	press(p, tea.KeyMsg{Type: tea.KeyTab})

	press(p, tea.KeyMsg{Type: tea.KeyCtrlR})

	if git.refreshed != 1 {
		t.Errorf("refreshed %d times, want 1", git.refreshed)
	}
	if p.running {
		t.Error("the picker should have settled after the refresh")
	}
}

// Fetching only makes sense once a repository is selected.
func TestPickerRefreshIsInertAtTheProjectLevel(t *testing.T) {
	wt, git := defaultWorktrees()
	p := navPicker(t, wt, git, project("acme", "/src/acme"))

	press(p, tea.KeyMsg{Type: tea.KeyCtrlR})

	if git.refreshed != 0 {
		t.Error("ctrl+r should do nothing at the project level")
	}
}

// A picker opened straight into a repo has nothing underneath, so esc quits.
func TestWorktreePickerEscQuitsRatherThanAscending(t *testing.T) {
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}}
	repo := session.RepoContext{Name: "acme", Root: "/src/acme", DefaultBranch: "main"}
	p := NewWorktreePicker(cfg, session.Deps{}, nil, repo, []session.Candidate{
		{Kind: session.KindBranch, Label: "feature", Branch: "feature"},
	})

	if p.level != levelWorktrees {
		t.Fatal("expected to start at the worktree level")
	}

	press(p, tea.KeyMsg{Type: tea.KeyEsc})

	if !p.quitting {
		t.Error("esc should quit when there is no level to go back to")
	}
	if p.level != levelWorktrees {
		t.Error("esc must not drop into a project list that was never loaded")
	}
}

func TestWorktreePickerHintSaysCancelNotBack(t *testing.T) {
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}}
	repo := session.RepoContext{Name: "acme", Root: "/src/acme"}
	p := NewWorktreePicker(cfg, session.Deps{}, nil, repo, nil)

	if got := p.viewPickerHint(); !strings.Contains(got, "cancel") {
		t.Errorf("hint = %q, want it to say cancel", got)
	}
}

// Descending needs collaborators; without them the key is inert rather than a
// nil dereference.
func TestPickerDescendWithoutDepsIsInert(t *testing.T) {
	p := testPicker(t, project("acme", "/src/acme"))

	press(p, tea.KeyMsg{Type: tea.KeyTab})

	if p.level != levelWorktrees && p.running {
		t.Error("descending with no deps should not leave the picker running")
	}
	if p.level == levelWorktrees {
		t.Error("expected no descent without a worktree lister")
	}
}

func TestPickerRowTagsCoverEveryKind(t *testing.T) {
	kinds := []session.Kind{
		session.KindSpace, session.KindProject, session.KindWorktree,
		session.KindBranch, session.KindRemoteBranch, session.KindNewBranch,
		session.KindPrunable,
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		tag := strings.TrimSpace(stripStyle(rowTag(k)))
		if tag == "" {
			t.Errorf("kind %q has no tag", k)
		}
		if seen[tag] {
			t.Errorf("kind %q reuses the tag %q", k, tag)
		}
		seen[tag] = true
	}
}

// stripStyle drops ANSI so a tag can be compared as text.
func stripStyle(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}
