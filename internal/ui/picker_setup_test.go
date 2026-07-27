package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/session"
	"github.com/phin-tech/herdr-phin-util/internal/setup"
)

var ctrlT = tea.KeyMsg{Type: tea.KeyCtrlT}

// setupPicker wires a picker whose setup level resolves against literals
// rather than the filesystem.
func setupPicker(t *testing.T, setups []setup.Setup, candidates ...session.Candidate) *Picker {
	t.Helper()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}}
	deps := session.Deps{Setups: func(string) []setup.Setup { return setups }}
	return NewPicker(cfg, deps, nil, candidates)
}

func reviewSetups() []setup.Setup {
	return []setup.Setup{
		{Name: "pr-review", Description: "3 agents + roborev", Tabs: []setup.Tab{{Name: "review"}}, Origin: setup.OriginGeneric},
		{Name: "deep-debug", Tabs: []setup.Tab{{Name: "a"}, {Name: "b"}}, Origin: setup.OriginRepo},
	}
}

func TestPickerOpensTheSetupLevel(t *testing.T) {
	p := setupPicker(t, reviewSetups(), project("acme", "/src/acme"))

	press(p, ctrlT)

	if p.level != levelSetups {
		t.Fatal("ctrl+t did not open the setup level")
	}
	got := labels(p.filtered)
	want := []string{session.DefaultSetupLabel, "pr-review", "deep-debug"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("rows = %v, want %v -- the default leads", got, want)
	}

	view := p.View()
	if !strings.Contains(view, "acme") {
		t.Error("the breadcrumb should name the row the setup applies to")
	}
	// Where a setup came from decides which of two same-named ones wins, so it
	// belongs on the row you choose from.
	if !strings.Contains(view, "repo") {
		t.Errorf("the origin is not shown:\n%s", view)
	}
}

// Switching to a Space builds nothing, so there is no layout to pick.
func TestPickerOffersNoSetupsForAnOpenSpace(t *testing.T) {
	p := setupPicker(t, reviewSetups(), space("acme", "/src/acme"))

	press(p, ctrlT)

	if p.level == levelSetups {
		t.Error("ctrl+t opened the setup level for a Space, which starts nothing")
	}
	if strings.Contains(p.View(), "ctrl+t") {
		t.Error("the hint advertises setups on a row that cannot use one")
	}
}

func TestPickerSetupLevelRestoresWhatItInterrupted(t *testing.T) {
	p := setupPicker(t, reviewSetups(),
		project("acme", "/src/acme"),
		project("beta", "/src/beta"),
	)

	typeInto(p, "beta")
	press(p, ctrlT)
	if p.level != levelSetups {
		t.Fatal("expected the setup level")
	}

	press(p, tea.KeyMsg{Type: tea.KeyEsc})

	if p.level != levelProjects {
		t.Fatal("esc did not come back")
	}
	if p.filter.Value() != "beta" {
		t.Errorf("filter = %q, want the text that was typed before ctrl+t", p.filter.Value())
	}
	if c, ok := p.selected(); !ok || c.Label != "beta" {
		t.Errorf("selection = %+v, want the row ctrl+t was pressed on", c)
	}
}

// esc must not quit the picker from the setup level: it is a question about a
// row, and backing out of it should leave you on that row.
func TestPickerSetupLevelEscDoesNotQuit(t *testing.T) {
	p := setupPicker(t, reviewSetups(), project("acme", "/src/acme"))

	press(p, ctrlT)
	press(p, tea.KeyMsg{Type: tea.KeyEsc})

	if p.quitting {
		t.Error("esc quit the picker instead of leaving the setup level")
	}
}

func TestPickerSetupLevelFilters(t *testing.T) {
	p := setupPicker(t, reviewSetups(), project("acme", "/src/acme"))

	press(p, ctrlT)
	// "debug" rather than "deep": subsequence matching is generous, and "deep"
	// is a subsequence of the default row's own description.
	typeInto(p, "debug")

	if got := labels(p.filtered); len(got) != 1 || got[0] != "deep-debug" {
		t.Errorf("rows = %v, want just deep-debug", got)
	}
}

func TestPickerAppliesThePickedSetupToTheHeldRow(t *testing.T) {
	p := setupPicker(t, reviewSetups(), project("acme", "/src/acme"))

	press(p, ctrlT)
	press(p, tea.KeyMsg{Type: tea.KeyDown}) // off the default, onto pr-review

	c, ok := p.selected()
	if !ok || c.Kind != session.KindSetup || c.Setup == nil || c.Setup.Name != "pr-review" {
		t.Fatalf("selected %+v, want the pr-review setup row", c)
	}

	// submit is what turns the two halves back into one pick: the setup rows
	// answer "how", the held row is still "what".
	p.submit()

	if p.picked.Label != "acme" {
		t.Errorf("picked %q, want the project row the setup was chosen for", p.picked.Label)
	}
	if p.picked.Kind == session.KindSetup {
		t.Error("the setup row itself was picked, which is not a thing to open")
	}
}

// The default row is the behaviour the picker has always had, so it must carry
// no setup at all rather than an empty one.
func TestPickerDefaultRowCarriesNoSetup(t *testing.T) {
	p := setupPicker(t, reviewSetups(), project("acme", "/src/acme"))

	press(p, ctrlT)

	c, _ := p.selected()
	if c.Setup != nil {
		t.Errorf("the default row carries %+v, want nil", c.Setup)
	}
}

func TestPickerSetupLevelWithNothingDefined(t *testing.T) {
	p := setupPicker(t, nil, project("acme", "/src/acme"))

	press(p, ctrlT)

	if got := labels(p.filtered); len(got) != 1 || got[0] != session.DefaultSetupLabel {
		t.Errorf("rows = %v, want only the default", got)
	}
	if !strings.Contains(p.View(), "no setups apply") {
		t.Errorf("the empty case is not explained:\n%s", p.View())
	}
}

// Enter on a row with no setup chosen still does exactly what it did before
// setups existed. This is the regression that matters most.
func TestPickerEnterWithoutCtrlTIsUnchanged(t *testing.T) {
	p := setupPicker(t, reviewSetups(), project("acme", "/src/acme"))

	p.submit()

	if p.level == levelSetups {
		t.Error("enter went via the setup level")
	}
	if p.picked.Label != "acme" {
		t.Errorf("picked %q", p.picked.Label)
	}
}

// tab means "deeper", and what is deeper depends on the row: a repository has
// worktrees, a worktree has only the question of how to build it.
func TestPickerTabOnAWorktreeRowOpensSetups(t *testing.T) {
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}}
	wt, git := defaultWorktrees()
	deps := session.Deps{
		Worktrees: wt,
		Git:       git,
		Setups:    func(string) []setup.Setup { return reviewSetups() },
	}
	p := NewPicker(cfg, deps, nil, []session.Candidate{project("acme", "/src/acme")})

	press(p, tea.KeyMsg{Type: tea.KeyTab})
	if p.level != levelWorktrees {
		t.Fatal("tab did not descend into the repo")
	}

	press(p, tea.KeyMsg{Type: tea.KeyTab})
	if p.level != levelSetups {
		t.Fatal("tab at the worktree level did not reach the setups")
	}
	if !strings.Contains(strings.Join(labels(p.filtered), ","), "pr-review") {
		t.Errorf("rows = %v", labels(p.filtered))
	}

	// And shift+tab walks back out the way it came, one level at a time.
	press(p, tea.KeyMsg{Type: tea.KeyShiftTab})
	if p.level != levelWorktrees {
		t.Errorf("shift+tab left the setups for level %v, want the worktrees", p.level)
	}
}

// The flagship path: paste a PR, tab, pick the layout. A link row has nothing
// below it either, so tab used to do nothing there at all.
func TestPickerTabOnALinkRowOpensSetups(t *testing.T) {
	p := setupPicker(t, reviewSetups())
	typeInto(p, "https://github.com/phin-tech/roux/pull/42")

	c, ok := p.selected()
	if !ok || c.Kind != session.KindLink {
		t.Fatalf("selected %+v, want the resolved link row", c)
	}

	press(p, tea.KeyMsg{Type: tea.KeyTab})

	if p.level != levelSetups {
		t.Fatal("tab on a link row did not reach the setups")
	}
	if p.pending.Kind != session.KindLink {
		t.Errorf("pending = %+v, want the link row held for the setup to apply to", p.pending)
	}
}

// An open Space builds nothing, so tab has nowhere to go and says so by not
// offering itself.
func TestPickerTabOnASpaceWithNoPathDoesNothing(t *testing.T) {
	p := setupPicker(t, reviewSetups(), session.Candidate{Kind: session.KindSpace, Label: "scratch"})

	press(p, tea.KeyMsg{Type: tea.KeyTab})

	if p.level != levelProjects {
		t.Errorf("tab moved to level %v from a Space row", p.level)
	}
	if strings.Contains(p.View(), "tab") {
		t.Errorf("the hint offers tab where it does nothing:\n%s", p.View())
	}
}
