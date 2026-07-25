package session

import (
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

func TestResolveLinkIgnoresPlainText(t *testing.T) {
	for _, in := range []string{"roux", "", "some scratch space", "not a url"} {
		if _, ok := ResolveLink(nil, nil, target.Parse(in)); ok {
			t.Errorf("ResolveLink(%q) claimed a link row; plain text should stay a filter", in)
		}
	}
}

func TestResolveLinkBuildsACreateRow(t *testing.T) {
	got, ok := ResolveLink(nil, nil, target.Parse("https://github.com/phin-tech/roux/pull/42"))
	if !ok {
		t.Fatal("expected a link row")
	}
	if got.Kind != KindLink {
		t.Errorf("kind = %q, want %q", got.Kind, KindLink)
	}
	if got.Label != "roux#42" {
		t.Errorf("label = %q", got.Label)
	}
	if got.Target.Number != 42 {
		t.Errorf("target not carried through: %+v", got.Target)
	}
}

// The whole point: knowing a Space already exists costs nothing, because the
// label is derived from the URL.
func TestResolveLinkFindsAnOpenSpaceByLabel(t *testing.T) {
	workspaces := []herdr.Workspace{
		{WorkspaceID: "w1", Label: "something-else"},
		{WorkspaceID: "w7", Label: "roux#42", Focused: true},
	}

	got, ok := ResolveLink(workspaces, nil, target.Parse("https://github.com/phin-tech/roux/pull/42"))
	if !ok {
		t.Fatal("expected a link row")
	}
	if got.Kind != KindSpace {
		t.Errorf("kind = %q, want %q", got.Kind, KindSpace)
	}
	if got.WorkspaceID != "w7" {
		t.Errorf("WorkspaceID = %q, want w7", got.WorkspaceID)
	}
	if !got.Focused {
		t.Error("Focused should carry through from the Space")
	}
}

func TestResolveLinkMatchesLabelsCaseInsensitively(t *testing.T) {
	workspaces := []herdr.Workspace{{WorkspaceID: "w3", Label: "ENG-123"}}

	got, _ := ResolveLink(workspaces, nil, target.Parse("https://linear.app/phin/issue/eng-123/x"))
	if got.Kind != KindSpace || got.WorkspaceID != "w3" {
		t.Errorf("got %+v, want the open Space", got)
	}
}

// A near-miss label must not be treated as the same Space.
func TestResolveLinkDoesNotMatchADifferentNumber(t *testing.T) {
	workspaces := []herdr.Workspace{{WorkspaceID: "w7", Label: "roux#4"}}

	got, _ := ResolveLink(workspaces, nil, target.Parse("https://github.com/phin-tech/roux/pull/42"))
	if got.Kind != KindLink {
		t.Errorf("kind = %q, want a create row -- roux#4 is a different PR", got.Kind)
	}
}

func TestResolveLinkLinearNamesItsBranch(t *testing.T) {
	got, _ := ResolveLink(nil, nil, target.Parse("https://linear.app/phin/issue/ENG-9/fix-it"))
	if got.Branch != "eng-9-fix-it" {
		t.Errorf("branch = %q", got.Branch)
	}
	if got.Detail == "" {
		t.Error("a link row should say what it would do")
	}
}

func TestResolveLinkGitHubIssue(t *testing.T) {
	got, ok := ResolveLink(nil, nil, target.Parse("https://github.com/phin-tech/roux/issues/99"))
	if !ok {
		t.Fatal("expected a link row for an issue")
	}
	if got.Label != "roux#99" {
		t.Errorf("label = %q", got.Label)
	}
	// Offline there is no title, so the branch is the number alone.
	if got.Branch != "issue-99" {
		t.Errorf("branch = %q, want issue-99", got.Branch)
	}
}

func TestOpenLinkNeedsSomethingToOpen(t *testing.T) {
	if _, err := OpenLink(Deps{}, nil, Candidate{Kind: KindLink}, openOptions()); err == nil {
		t.Fatal("expected an error for a link row with no target")
	}
}
