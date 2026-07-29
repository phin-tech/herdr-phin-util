package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/herdr"

	"github.com/phin-tech/herdr-phin-util/internal/session"
)

const ticketURL = "https://linear.app/phin/issue/ENG-123/fix-the-thing"

func worktreePickerWithBranches(t *testing.T) *Picker {
	t.Helper()
	repo := session.RepoContext{Root: "/src/app", Name: "app", DefaultBranch: "main"}
	p := testPicker(t)
	return NewWorktreePicker(p.cfg, session.Deps{}, nil, repo, []session.Candidate{
		{Kind: session.KindWorktree, Label: "main", Branch: "main"},
		{Kind: session.KindRemoteBranch, Label: "feature-x", Branch: "feature-x"},
	})
}

// A ticket names no repository, so it cannot collapse the list to one row the
// way a PR link does -- the project list is the question it still has to ask.
func TestPickerTicketKeepsTheProjectList(t *testing.T) {
	p := testPicker(t,
		project("app", "/src/app"),
		project("other", "/src/other"),
	)

	typeInto(p, ticketURL)
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if p.linkMode {
		t.Error("a ticket should not collapse the list to one row")
	}
	if len(p.filtered) != 2 {
		t.Errorf("filtered = %v, want both projects", labels(p.filtered))
	}
	if p.ticket.Issue != "ENG-123" {
		t.Errorf("ticket = %q, want it held", p.ticket.Issue)
	}
}

// The URL is taken out of the box, or it would be filtering the project list
// against itself at exactly the moment you want to type a repository's name.
func TestPickerTicketClearsTheFilterBox(t *testing.T) {
	p := testPicker(t, project("app", "/src/app"), project("other", "/src/other"))

	typeInto(p, ticketURL)
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := p.filter.Value(); got != "" {
		t.Errorf("filter = %q, want it emptied", got)
	}

	// And the box is usable again for what it is now for.
	typeInto(p, "other")
	if len(p.filtered) != 1 || p.filtered[0].Label != "other" {
		t.Errorf("got %v, want just other", labels(p.filtered))
	}
	if p.ticket.Issue != "ENG-123" {
		t.Error("typing a filter should not drop the ticket")
	}
}

// Descending with a ticket held turns the branch list into bases for it.
func TestPickerTicketTurnsWorktreeRowsIntoBases(t *testing.T) {
	p := worktreePickerWithBranches(t)

	typeInto(p, ticketURL)
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(p.all) == 0 {
		t.Fatal("no rows")
	}
	for _, c := range p.all {
		if c.Kind != session.KindLinearBase {
			t.Fatalf("row %q is %q, want every row to be a base", c.Label, c.Kind)
		}
		if c.Branch != "eng-123-fix-the-thing" {
			t.Errorf("row %q cuts %q", c.Label, c.Branch)
		}
	}
	if got := p.all[0].Label; got != "from origin/main" {
		t.Errorf("first row = %q, want the default branch", got)
	}
}

// The branch is already named by the ticket, so the row that offers to invent
// one from the filter text has nothing left to offer.
func TestPickerTicketSuppressesTheNewBranchRow(t *testing.T) {
	p := worktreePickerWithBranches(t)
	typeInto(p, ticketURL)
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	typeInto(p, "some-other-name")

	for _, c := range p.filtered {
		if c.Kind == session.KindNewBranch {
			t.Errorf("offered to create %q while a ticket was held", c.Label)
		}
	}
}

// esc gives the ticket up before it gives the popup up.
func TestPickerEscDropsTheTicketFirst(t *testing.T) {
	p := worktreePickerWithBranches(t)
	typeInto(p, ticketURL)
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	p.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if p.ticket.Kind != "" {
		t.Error("esc should drop the ticket")
	}
	if p.quitting {
		t.Error("esc should not also quit")
	}
	// The branches are back, without re-reading the repository.
	for _, c := range p.all {
		if c.Kind == session.KindLinearBase {
			t.Fatalf("row %q is still a base", c.Label)
		}
	}

	// And a second esc means what it usually does.
	p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !p.quitting {
		t.Error("a second esc should quit")
	}
}

// The popup has to say what it is holding, or a cleared filter box looks like
// the paste was simply lost.
func TestPickerTicketShowsInTheTitle(t *testing.T) {
	p := testPicker(t, project("app", "/src/app"))
	p.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	typeInto(p, ticketURL)
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	view := p.View()
	if !strings.Contains(view, "ENG-123") {
		t.Errorf("the view does not name the held ticket:\n%s", view)
	}
	if !strings.Contains(view, "eng-123-fix-the-thing") {
		t.Errorf("the view does not name the branch it would cut:\n%s", view)
	}
}

// Enter on a project row while a ticket is held has to mean "this is the
// repository", not "open this" -- opening it would switch away and drop the
// ticket with no sign it had been holding one.
func TestPickerTicketMakesEnterChooseTheRepository(t *testing.T) {
	p := testPicker(t, project("app", "/src/app"))
	typeInto(p, ticketURL)
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Descending needs the deps a real picker has; without them the command is
	// nil, which is enough to show Enter did not go and open anything.
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if p.done || p.picked.Label != "" {
		t.Errorf("enter opened %q instead of choosing a repository", p.picked.Label)
	}
	if p.ticket.Kind == "" {
		t.Error("the ticket was dropped")
	}
}

// A row with no repository behind it cannot answer "which repository", so it
// is not offered while a ticket is held.
func TestPickerTicketHidesRowsWithNoRepository(t *testing.T) {
	shapeOnly := session.Candidate{Kind: session.KindSpace, Label: "scratch", Detail: "2 panes"}
	p := testPicker(t, project("app", "/src/app"), shapeOnly)

	typeInto(p, ticketURL)
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	for _, c := range p.all {
		if c.Label == "scratch" {
			t.Error("a Space with no directory was offered as a repository")
		}
	}
	if len(p.all) != 1 {
		t.Errorf("rows = %v, want just the checkout", labels(p.all))
	}

	// And dropping the ticket puts it back.
	p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if len(p.all) != 2 {
		t.Errorf("rows = %v, want the full list back", labels(p.all))
	}
}

// A ticket that already has a Space is answered outright: there is nothing to
// choose a repository for when the thing is already running.
func TestPickerTicketAlreadyOpenStaysALinkRow(t *testing.T) {
	p := testPicker(t, project("app", "/src/app")).
		WithWorkspaces([]herdr.Workspace{{WorkspaceID: "w7", Label: "ENG-123"}})

	typeInto(p, ticketURL)

	if p.ticket.Kind != "" {
		t.Error("an open ticket should not be taken as pending work")
	}
	if !p.linkMode || len(p.filtered) != 1 {
		t.Fatalf("want the one open Space, got %v", labels(p.filtered))
	}
	if p.filtered[0].Kind != session.KindSpace {
		t.Errorf("kind = %q, want the existing Space", p.filtered[0].Kind)
	}
}
