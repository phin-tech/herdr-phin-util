package open

import (
	"errors"
	"strings"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
)

func TestRunExternalCreatesSpaceAndRunsCommand(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "w1:p1"}, workspaceID: "w1"}
	l := &fakeLayout{}
	deps := Deps{Session: s, Layout: l, Cwd: "/home/me"}

	out, err := RunExternal(deps, &config.Settings{}, "jim-dev2", []string{"herdr-anyscale-remote", "connect", "--name", "jim-dev2"})
	if err != nil {
		t.Fatalf("RunExternal: %v", err)
	}

	if out.WorkspaceID != "w1" || out.PaneID != "w1:p1" || out.Label != "jim-dev2" {
		t.Errorf("outcome mismatch: %+v", out)
	}
	if len(s.createWorkspaceCalls) != 1 {
		t.Fatalf("want one CreateWorkspace, got %d", len(s.createWorkspaceCalls))
	}
	if got := s.createWorkspaceCalls[0]; got.cwd != "/home/me" || got.label != "jim-dev2" || !got.focus {
		t.Errorf("CreateWorkspace called with %+v", got)
	}

	want := "run w1:p1 herdr-anyscale-remote connect --name jim-dev2"
	if l.transcript() != want {
		t.Errorf("command transcript:\n got %q\nwant %q", l.transcript(), want)
	}
}

func TestRunExternalQuotesArgvWithSpaces(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}
	l := &fakeLayout{}

	_, err := RunExternal(Deps{Session: s, Layout: l}, &config.Settings{}, "box", []string{"ssh", "host with space", "echo 'hi'"})
	if err != nil {
		t.Fatalf("RunExternal: %v", err)
	}
	// Every word is single-quoted, and the embedded quote is closed and
	// reopened -- the one form every shell agrees on.
	want := `run p1 ssh 'host with space' 'echo '\''hi'\'''`
	if l.transcript() != want {
		t.Errorf("quoting:\n got %q\nwant %q", l.transcript(), want)
	}
}

func TestRunExternalEmptyCommandIsAnError(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}
	_, err := RunExternal(Deps{Session: s, Layout: &fakeLayout{}}, &config.Settings{}, "box", nil)
	if err == nil {
		t.Fatal("want an error for an external row with no command")
	}
	if len(s.createWorkspaceCalls) != 0 {
		t.Error("built a Space for a row that had no command to run")
	}
}

// A command that fails to run leaves the Space standing, and the Space is
// reported so a half-built one is never silently called a success -- the same
// contract every other post-Space failure in this package keeps.
func TestRunExternalReportsTheSpaceWhenTheCommandFails(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}
	l := &fakeLayout{runErr: errors.New("no shell")}

	out, err := RunExternal(Deps{Session: s, Layout: l}, &config.Settings{}, "box", []string{"attach"})
	if err == nil || !strings.Contains(err.Error(), "no shell") {
		t.Fatalf("want the run error surfaced, got %v", err)
	}
	if out.WorkspaceID != "w1" || out.PaneID != "p1" {
		t.Errorf("the Space that was built should still be reported: %+v", out)
	}
}
