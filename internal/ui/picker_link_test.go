package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/session"
)

func linkPicker(t *testing.T, workspaces []herdr.Workspace, candidates ...session.Candidate) *Picker {
	t.Helper()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}}
	return NewPicker(cfg, session.Deps{}, nil, candidates).WithWorkspaces(workspaces)
}

const prURL = "https://github.com/phin-tech/roux/pull/42"

// The core of the unified input: a reference is not a filter that matches
// nothing, it is a query with exactly one answer.
func TestPasingALinkCollapsesTheListToOneRow(t *testing.T) {
	p := linkPicker(t, nil, project("roux", "/src/roux"), project("other", "/src/other"))

	typeInto(p, prURL)

	if len(p.filtered) != 1 {
		t.Fatalf("got %v, want exactly one row", labels(p.filtered))
	}
	c := p.filtered[0]
	if c.Kind != session.KindLink {
		t.Errorf("kind = %q, want %q", c.Kind, session.KindLink)
	}
	if c.Label != "roux#42" {
		t.Errorf("label = %q, want roux#42", c.Label)
	}
}

// "goes to that pane": a link whose Space is already open reads as a switch,
// and says so before you commit.
func TestPastingALinkThatIsAlreadyOpenOffersToSwitch(t *testing.T) {
	open := []herdr.Workspace{{WorkspaceID: "w7", Label: "roux#42"}}
	p := linkPicker(t, open, project("roux", "/src/roux"))

	typeInto(p, prURL)

	c := p.filtered[0]
	if c.Kind != session.KindSpace {
		t.Fatalf("kind = %q, want %q", c.Kind, session.KindSpace)
	}
	if c.WorkspaceID != "w7" {
		t.Errorf("WorkspaceID = %q, want w7", c.WorkspaceID)
	}
	if !strings.Contains(p.View(), "already open") {
		t.Errorf("the row should say it is already open:\n%s", p.View())
	}
}

func TestPastingALinearLinkResolves(t *testing.T) {
	p := linkPicker(t, nil)

	typeInto(p, "https://linear.app/phin/issue/ENG-123/fix-the-thing")

	c := p.filtered[0]
	if c.Label != "ENG-123" {
		t.Errorf("label = %q, want ENG-123", c.Label)
	}
	// A Linear branch is derivable offline, so the row can name it up front.
	if c.Branch != "eng-123-fix-the-thing" {
		t.Errorf("branch = %q", c.Branch)
	}
}

func TestPastingAGitHubIssueResolves(t *testing.T) {
	p := linkPicker(t, nil)

	typeInto(p, "https://github.com/phin-tech/roux/issues/99")

	c := p.filtered[0]
	if c.Kind != session.KindLink || c.Label != "roux#99" {
		t.Errorf("candidate = %+v, want a link row for roux#99", c)
	}
	if !strings.Contains(c.Detail, "issue") {
		t.Errorf("detail = %q, want it to say issue", c.Detail)
	}
}

// Plain text stays a filter. Collapsing the list every time a query matched
// nothing would make the picker useless for typing.
func TestPlainTextStillFilters(t *testing.T) {
	p := linkPicker(t, nil, project("roux", "/src/roux"), project("other", "/src/other"))

	typeInto(p, "roux")

	if len(p.filtered) != 1 || p.filtered[0].Kind != session.KindProject {
		t.Errorf("got %+v, want the ordinary filtered list", labels(p.filtered))
	}
}

// Deleting back from a URL has to restore the list, or the collapse would be a
// one-way door.
func TestClearingALinkRestoresTheList(t *testing.T) {
	p := linkPicker(t, nil, project("roux", "/src/roux"), project("other", "/src/other"))

	typeInto(p, prURL)
	if len(p.filtered) != 1 {
		t.Fatalf("expected the link row, got %v", labels(p.filtered))
	}

	for range prURL {
		p.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}

	if len(p.filtered) != 2 {
		t.Errorf("got %v, want the full list back", labels(p.filtered))
	}
}

// The bug that motivated moving navigation off the arrows: a pasted URL is
// content, and content you cannot put a cursor into is content you cannot fix.
func TestArrowKeysMoveTheFilterCursor(t *testing.T) {
	p := linkPicker(t, nil, project("roux", "/src/roux"))
	typeInto(p, "roux")

	end := p.filter.Position()
	if end != 4 {
		t.Fatalf("cursor = %d, want 4 after typing", end)
	}

	p.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got := p.filter.Position(); got != 3 {
		t.Errorf("after left, cursor = %d, want 3", got)
	}

	p.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := p.filter.Position(); got != 4 {
		t.Errorf("after right, cursor = %d, want 4", got)
	}
}

// A link row acts through open.Run rather than the focus-or-create dispatch,
// so it must not be mistaken for a project.
func TestLinkRowCarriesItsTarget(t *testing.T) {
	p := linkPicker(t, nil)
	typeInto(p, prURL)

	c := p.filtered[0]
	if c.Target.URL != prURL {
		t.Errorf("Target.URL = %q, want the pasted link", c.Target.URL)
	}
	if c.Target.Number != 42 || c.Target.Repo != "roux" {
		t.Errorf("Target = %+v", c.Target)
	}
}

// Whitespace around a pasted link is normal and must not defeat parsing.
func TestPastedLinkToleratesSurroundingSpace(t *testing.T) {
	p := linkPicker(t, nil, project("roux", "/src/roux"))

	typeInto(p, "  "+prURL+"  ")

	if len(p.filtered) != 1 || p.filtered[0].Label != "roux#42" {
		t.Errorf("got %v, want the link row", labels(p.filtered))
	}
}
