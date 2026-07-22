package promote

import (
	"errors"
	"os"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/herdr"
)

// The tests that exercise the focus fallback pass an empty target, which makes
// Run consult the injected plugin context first. Running the suite from inside
// a plugin action would otherwise let a real pane id decide the result.
func TestMain(m *testing.M) {
	os.Unsetenv("HERDR_PLUGIN_CONTEXT_JSON")
	os.Exit(m.Run())
}

type fakeSession struct {
	panes []herdr.Pane
	err   error

	movedPane  string
	movedLabel string
	moveCalls  int
}

func (f *fakeSession) Panes() ([]herdr.Pane, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.panes, nil
}

func (f *fakeSession) MoveToNewWorkspace(paneID, label string, _ bool) (herdr.Pane, error) {
	f.moveCalls++
	f.movedPane = paneID
	f.movedLabel = label
	// Herdr renumbers a pane when it changes workspace, so the fake does too.
	return herdr.Pane{PaneID: "wZ:p1"}, nil
}

func twoPaneSession() *fakeSession {
	return &fakeSession{panes: []herdr.Pane{
		{PaneID: "w1:p1", WorkspaceID: "w1", Cwd: "/src/alpha"},
		{PaneID: "w1:p2", WorkspaceID: "w1", Cwd: "/src/beta", Focused: true},
	}}
}

func TestRunPromotesExplicitTarget(t *testing.T) {
	s := twoPaneSession()
	out, err := Run(s, "w1:p1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.Moved {
		t.Fatal("expected the pane to move")
	}
	if s.movedPane != "w1:p1" {
		t.Errorf("moved %q, want the explicit target w1:p1", s.movedPane)
	}
	// An explicit target must win over focus, which points at w1:p2.
	if s.movedLabel != "alpha" {
		t.Errorf("label %q, want alpha from the target's cwd", s.movedLabel)
	}
	if out.PaneID != "wZ:p1" {
		t.Errorf("PaneID %q, want the post-move id wZ:p1", out.PaneID)
	}
}

func TestRunFallsBackToFocusedPane(t *testing.T) {
	s := twoPaneSession()
	out, err := Run(s, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.Moved || s.movedPane != "w1:p2" {
		t.Errorf("moved %q, want the focused pane w1:p2", s.movedPane)
	}
}

func TestRunIsNoOpForLonePane(t *testing.T) {
	s := &fakeSession{panes: []herdr.Pane{
		{PaneID: "w1:p1", WorkspaceID: "w1", Cwd: "/src/alpha", Focused: true},
	}}
	out, err := Run(s, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Moved {
		t.Error("a pane alone in its Space should not move")
	}
	if s.moveCalls != 0 {
		t.Errorf("made %d move calls, want none", s.moveCalls)
	}
}

// Panes in other workspaces must not count as siblings, or a lone pane would
// look promotable whenever any other Space happened to be split.
func TestRunCountsSiblingsPerWorkspace(t *testing.T) {
	s := &fakeSession{panes: []herdr.Pane{
		{PaneID: "w1:p1", WorkspaceID: "w1", Cwd: "/src/alpha", Focused: true},
		{PaneID: "w2:p1", WorkspaceID: "w2", Cwd: "/src/beta"},
		{PaneID: "w2:p2", WorkspaceID: "w2", Cwd: "/src/beta"},
	}}
	out, err := Run(s, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Moved {
		t.Error("w1:p1 is alone in w1 and should not move")
	}
}

func TestRunRejectsUnknownPane(t *testing.T) {
	s := twoPaneSession()
	if _, err := Run(s, "w9:p9"); err == nil {
		t.Fatal("expected an error for a pane that does not exist")
	}
	if s.moveCalls != 0 {
		t.Errorf("made %d move calls, want none", s.moveCalls)
	}
}

func TestRunPropagatesListError(t *testing.T) {
	s := &fakeSession{err: errors.New("socket down")}
	if _, err := Run(s, ""); err == nil {
		t.Fatal("expected the list error to propagate")
	}
}

func TestSpaceLabel(t *testing.T) {
	cases := map[string]string{
		"/Users/me/src/herdr-phin-util": "herdr-phin-util",
		"/src/alpha/":                   "alpha",
		"/":                             "promoted",
		"":                              "promoted",
	}
	for cwd, want := range cases {
		if got := SpaceLabel(cwd); got != want {
			t.Errorf("SpaceLabel(%q) = %q, want %q", cwd, got, want)
		}
	}
}
