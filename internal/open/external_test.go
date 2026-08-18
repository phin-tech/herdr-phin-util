package open

import (
	"errors"
	"os"
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

// opens:false runs the command and makes no Space -- structurally, since
// RunExternalCommand is handed no Session at all.
func TestRunExternalCommandMakesNoSpace(t *testing.T) {
	out, err := RunExternalCommand(Deps{}, "act", []string{"/bin/sh", "-c", "exit 0"})
	if err != nil {
		t.Fatalf("RunExternalCommand: %v", err)
	}
	if out.WorkspaceID != "" || out.PaneID != "" {
		t.Errorf("no Space should be reported for a run-only row: %+v", out)
	}
	if out.Label != "act" {
		t.Errorf("Label should be carried through: %+v", out)
	}
}

// A failure carries the command's own stderr, so the reason is in the popup
// rather than in a pane the user has to go read.
func TestRunExternalCommandFailureCarriesStderr(t *testing.T) {
	_, err := RunExternalCommand(Deps{}, "act", []string{"/bin/sh", "-c", "echo 'no credentials' >&2; exit 3"})
	if err == nil {
		t.Fatal("want an error for a command that exited non-zero")
	}
	if !strings.Contains(err.Error(), "no credentials") {
		t.Errorf("error should carry the command's stderr, got %v", err)
	}
}

func TestRunExternalCommandEmptyIsAnError(t *testing.T) {
	if _, err := RunExternalCommand(Deps{}, "act", nil); err == nil {
		t.Fatal("want an error for a run-only row with no command")
	}
}

// The origin-Space identity variables are stripped, so a command cannot read
// HERDR_WORKSPACE_ID and act on a Space the user never pointed at. Other
// variables pass through untouched.
func TestEnvWithoutOriginSpaceStripsHerdrIdentity(t *testing.T) {
	t.Setenv("HERDR_WORKSPACE_ID", "w9")
	t.Setenv("HERDR_PANE_ID", "w9:p1")
	t.Setenv("HERDR_TAB_ID", "w9:t1")
	t.Setenv("HERDR_SOCK", "/tmp/herdr.sock")
	t.Setenv("PATH_MARKER_FOR_TEST", "keep-me")

	env := envWithoutOriginSpace()
	joined := strings.Join(env, "\n")

	for _, gone := range []string{"HERDR_WORKSPACE_ID=", "HERDR_PANE_ID=", "HERDR_TAB_ID="} {
		if strings.Contains(joined, gone) {
			t.Errorf("%s should be stripped from the child environment", gone)
		}
	}
	// The socket is how the command reaches Herdr at all -- stripping it would
	// break the very thing an opens:false command exists to do.
	if !strings.Contains(joined, "HERDR_SOCK=/tmp/herdr.sock") {
		t.Error("HERDR_SOCK must survive: it is how the command reaches Herdr")
	}
	if !strings.Contains(joined, "PATH_MARKER_FOR_TEST=keep-me") {
		t.Error("unrelated variables must pass through")
	}
	// Guard the assertion itself: prove the stripped var really was in os.Environ.
	if _, ok := os.LookupEnv("HERDR_WORKSPACE_ID"); !ok {
		t.Fatal("test precondition: HERDR_WORKSPACE_ID should be set")
	}
}
