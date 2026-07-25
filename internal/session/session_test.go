package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/open"
)

type fakeLister struct {
	workspaces []herdr.Workspace
	panes      []herdr.Pane
	wsErr      error
	paneErr    error
}

func (f *fakeLister) Workspaces() ([]herdr.Workspace, error) {
	return f.workspaces, f.wsErr
}

func (f *fakeLister) Panes() ([]herdr.Pane, error) {
	return f.panes, f.paneErr
}

type fakeFocuser struct {
	focused []string
	err     error
}

func (f *fakeFocuser) FocusWorkspace(id string) error {
	f.focused = append(f.focused, id)
	return f.err
}

// repoAt makes a checkout under root that discovery will find.
func repoAt(t *testing.T, root string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func settingsFor(root string) *config.Settings {
	return &config.Settings{
		Projects: config.ProjectSettings{
			Roots:   []string{root},
			GitOnly: true,
			Depth:   1,
		},
	}
}

func TestListPutsOpenSpacesBeforeProjects(t *testing.T) {
	root := t.TempDir()
	alpha := repoAt(t, root, "alpha")
	repoAt(t, root, "beta")

	l := &fakeLister{
		workspaces: []herdr.Workspace{
			{WorkspaceID: "w1", Label: "alpha", TabCount: 1, PaneCount: 2},
		},
		panes: []herdr.Pane{{PaneID: "w1:p1", WorkspaceID: "w1", Cwd: alpha}},
	}

	got, err := List(l, settingsFor(root))
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(got), got)
	}
	if got[0].Kind != KindSpace || got[0].WorkspaceID != "w1" {
		t.Errorf("first row should be the open Space, got %+v", got[0])
	}
	if got[1].Kind != KindProject || got[1].Label != "beta" {
		t.Errorf("second row should be the unopened checkout, got %+v", got[1])
	}
}

// The rule that makes the list safe to act on: a checkout with a Space is
// offered once, as the Space.
func TestListHidesProjectsThatAlreadyHaveASpace(t *testing.T) {
	root := t.TempDir()
	alpha := repoAt(t, root, "alpha")

	l := &fakeLister{
		workspaces: []herdr.Workspace{{WorkspaceID: "w1", Label: "alpha"}},
		panes:      []herdr.Pane{{WorkspaceID: "w1", Cwd: alpha}},
	}

	got, err := List(l, settingsFor(root))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range got {
		if c.Kind == KindProject && c.Path == alpha {
			t.Fatalf("%s has a Space and should not also be offered as a project", alpha)
		}
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want just the Space: %+v", len(got), got)
	}
}

// A trailing slash on a pane's cwd must not defeat the "already open" match.
func TestListMatchesUncleanPaneCwd(t *testing.T) {
	root := t.TempDir()
	alpha := repoAt(t, root, "alpha")

	l := &fakeLister{
		workspaces: []herdr.Workspace{{WorkspaceID: "w1", Label: "alpha"}},
		panes:      []herdr.Pane{{WorkspaceID: "w1", Cwd: alpha + "/"}},
	}

	got, err := List(l, settingsFor(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want just the Space: %+v", len(got), got)
	}
}

func TestListSortsTheCurrentSpaceLast(t *testing.T) {
	root := t.TempDir()

	l := &fakeLister{
		workspaces: []herdr.Workspace{
			{WorkspaceID: "w1", Label: "here", Focused: true},
			{WorkspaceID: "w2", Label: "there"},
			{WorkspaceID: "w3", Label: "elsewhere"},
		},
	}

	got, err := List(l, settingsFor(root))
	if err != nil {
		t.Fatal(err)
	}

	if got[len(got)-1].WorkspaceID != "w1" {
		t.Errorf("the focused Space should sort last, got %+v", got)
	}
	// The unfocused ones keep the order Herdr reported.
	if got[0].WorkspaceID != "w2" || got[1].WorkspaceID != "w3" {
		t.Errorf("unfocused Spaces should keep their order, got %+v", got)
	}
	if !got[len(got)-1].Focused {
		t.Error("Focused should be carried through to the candidate")
	}
}

func TestListTakesTheFirstPaneCwdForASpace(t *testing.T) {
	root := t.TempDir()
	first := repoAt(t, root, "first")
	second := repoAt(t, root, "second")

	l := &fakeLister{
		workspaces: []herdr.Workspace{{WorkspaceID: "w1", Label: "mixed"}},
		panes: []herdr.Pane{
			{WorkspaceID: "w1", Cwd: ""},
			{WorkspaceID: "w1", Cwd: first},
			{WorkspaceID: "w1", Cwd: second},
		},
	}

	got, err := List(l, settingsFor(root))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Path != first {
		t.Errorf("path = %q, want the first pane reporting one (%q)", got[0].Path, first)
	}
	// Only the matched directory is suppressed; the other stays a project.
	var sawSecond bool
	for _, c := range got {
		if c.Kind == KindProject && c.Path == second {
			sawSecond = true
		}
	}
	if !sawSecond {
		t.Errorf("%s has no Space of its own and should still be offered", second)
	}
}

// A Space whose panes report no directory still needs something in the detail
// column.
func TestListDescribesASpaceWithNoDirectory(t *testing.T) {
	l := &fakeLister{
		workspaces: []herdr.Workspace{
			{WorkspaceID: "w1", Label: "scratch", TabCount: 2, PaneCount: 5},
		},
	}

	got, err := List(l, &config.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Detail != "2 tabs · 5 panes" {
		t.Errorf("detail = %q, want a shape summary", got[0].Detail)
	}
}

func TestListLabelsAnUnlabelledSpace(t *testing.T) {
	root := t.TempDir()
	alpha := repoAt(t, root, "alpha")

	l := &fakeLister{
		workspaces: []herdr.Workspace{
			{WorkspaceID: "w1"},
			{WorkspaceID: "w2"},
		},
		panes: []herdr.Pane{{WorkspaceID: "w1", Cwd: alpha}},
	}

	got, err := List(l, settingsFor(root))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Label != "alpha" {
		t.Errorf("label = %q, want the directory name", got[0].Label)
	}
	if got[1].Label != "w2" {
		t.Errorf("label = %q, want the workspace id as a last resort", got[1].Label)
	}
}

func TestListPropagatesAPIErrors(t *testing.T) {
	boom := errors.New("socket closed")

	if _, err := List(&fakeLister{wsErr: boom}, &config.Settings{}); err == nil {
		t.Error("expected a workspace.list failure to surface")
	}
	if _, err := List(&fakeLister{paneErr: boom}, &config.Settings{}); err == nil {
		t.Error("expected a pane.list failure to surface")
	}
}

func TestListWithNoRootsStillReturnsSpaces(t *testing.T) {
	l := &fakeLister{workspaces: []herdr.Workspace{{WorkspaceID: "w1", Label: "only"}}}

	got, err := List(l, &config.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindSpace {
		t.Fatalf("got %+v, want the one Space", got)
	}
}

func TestOpenFocusesAnExistingSpace(t *testing.T) {
	f := &fakeFocuser{}
	c := Candidate{Kind: KindSpace, Label: "alpha", WorkspaceID: "w1", Path: "/src/alpha"}

	out, err := Open(Deps{}, f, &config.Settings{}, c, open.Options{})
	if err != nil {
		t.Fatal(err)
	}

	if len(f.focused) != 1 || f.focused[0] != "w1" {
		t.Errorf("focused = %v, want [w1]", f.focused)
	}
	if out.WorkspaceID != "w1" || out.Label != "alpha" {
		t.Errorf("outcome = %+v, want it to name the focused Space", out)
	}
	// Switching to a running Space must never start a second agent in it.
	if out.AgentStarted {
		t.Error("focusing should not start an agent")
	}
}

func TestOpenReportsAFocusFailure(t *testing.T) {
	f := &fakeFocuser{err: errors.New("gone")}
	c := Candidate{Kind: KindSpace, Label: "alpha", WorkspaceID: "w1"}

	if _, err := Open(Deps{}, f, &config.Settings{}, c, open.Options{}); err == nil {
		t.Fatal("expected the focus failure to surface")
	}
}

func TestOpenRejectsAnUnknownKind(t *testing.T) {
	if _, err := Open(Deps{}, &fakeFocuser{}, &config.Settings{}, Candidate{Kind: "nonsense"}, open.Options{}); err == nil {
		t.Fatal("expected an error for an unknown candidate kind")
	}
}

func TestShortenHome(t *testing.T) {
	homeDir = func() (string, error) { return "/Users/sam", nil }
	t.Cleanup(func() { homeDir = os.UserHomeDir })

	cases := []struct{ in, want string }{
		{"/Users/sam/src/repo", "~/src/repo"},
		{"/Users/sam", "~"},
		{"/opt/elsewhere", "/opt/elsewhere"},
		// A path that merely starts with the same characters is not inside it.
		{"/Users/samantha/src", "/Users/samantha/src"},
	}
	for _, tc := range cases {
		if got := shortenHome(tc.in); got != tc.want {
			t.Errorf("shortenHome(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
