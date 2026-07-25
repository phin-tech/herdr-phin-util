package open

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phin-tech/herdr-phin-util/internal/claudesess"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
)

// handoffSession is the fake wired the way every handoff test needs it, so
// the tests below differ only in what they are actually asserting.
func handoffSession() *fakeSession {
	return &fakeSession{pane: herdr.Pane{PaneID: "w7:p1"}, workspaceID: "w7"}
}

// writeTranscript lays down a transcript the way Claude does: the working
// directory arrives a couple of entries in, not on the first line.
func writeTranscript(t *testing.T, home, cwd, id string) {
	t.Helper()
	dir := claudesess.ProjectDir(home, cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"last-prompt","sessionId":"` + id + `"}
{"type":"user","cwd":"` + cwd + `"}
`
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// touchOlder backdates a transcript, so "newer elsewhere" can be set up
// without sleeping between writes.
func touchOlder(t *testing.T, home, cwd, id string) {
	t.Helper()
	path := filepath.Join(claudesess.ProjectDir(home, cwd), id+".jsonl")
	when := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestRunHandoffResumesTheSessionFromTheEnvironment(t *testing.T) {
	t.Setenv(claudesess.EnvVar, "f73cb238-9ee4")
	sess := handoffSession()

	out, err := RunHandoff(Deps{Session: sess}, HandoffOptions{Cwd: "/src/app"})
	if err != nil {
		t.Fatal(err)
	}

	if len(sess.startAgentCalls) != 1 {
		t.Fatalf("StartAgent calls = %+v, want 1", sess.startAgentCalls)
	}
	got := sess.startAgentCalls[0]
	if want := []string{"--resume", "f73cb238-9ee4"}; !reflect.DeepEqual(got.args, want) {
		t.Errorf("agent args = %q, want %q", got.args, want)
	}
	if got.kind != "claude" {
		t.Errorf("agent kind = %q, want claude", got.kind)
	}
	if got.paneID != "w7:p1" {
		t.Errorf("agent pane = %q, want the new Space's root pane", got.paneID)
	}
	if !out.AgentStarted {
		t.Error("AgentStarted = false, want true")
	}
	if out.WorkspaceID != "w7" {
		t.Errorf("WorkspaceID = %q, want w7", out.WorkspaceID)
	}
}

func TestRunHandoffNeverTypesAPrompt(t *testing.T) {
	t.Setenv(claudesess.EnvVar, "abc")
	sess := handoffSession()

	if _, err := RunHandoff(Deps{Session: sess}, HandoffOptions{Cwd: "/src/app"}); err != nil {
		t.Fatal(err)
	}

	// A resumed session already has its content. Typing into it would put
	// words into a conversation that is mid-flow.
	if len(sess.sendTextCalls) != 0 {
		t.Errorf("SendText calls = %+v, want none", sess.sendTextCalls)
	}
}

func TestRunHandoffNamesTheSpaceAfterTheDirectory(t *testing.T) {
	t.Setenv(claudesess.EnvVar, "abc")
	sess := handoffSession()

	if _, err := RunHandoff(Deps{Session: sess}, HandoffOptions{Cwd: "/src/herdr-phin-util"}); err != nil {
		t.Fatal(err)
	}

	if len(sess.createWorkspaceCalls) != 1 {
		t.Fatalf("CreateWorkspace calls = %+v, want 1", sess.createWorkspaceCalls)
	}
	call := sess.createWorkspaceCalls[0]
	if call.label != "herdr-phin-util" {
		t.Errorf("label = %q, want the directory's base name", call.label)
	}
	if call.cwd != "/src/herdr-phin-util" {
		t.Errorf("cwd = %q, want the source directory", call.cwd)
	}
	if !call.focus {
		t.Error("focus = false; a handoff you have to go and find is not a handoff")
	}
}

func TestRunHandoffPrefersAnExplicitLabelAndSession(t *testing.T) {
	t.Setenv(claudesess.EnvVar, "from-env")
	sess := handoffSession()

	_, err := RunHandoff(Deps{Session: sess}, HandoffOptions{
		Cwd:       "/src/app",
		SessionID: "explicit",
		Label:     "rescued",
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := sess.startAgentCalls[0].args[1]; got != "explicit" {
		t.Errorf("resumed %q, want the explicitly passed session", got)
	}
	if got := sess.createWorkspaceCalls[0].label; got != "rescued" {
		t.Errorf("label = %q, want the explicitly passed label", got)
	}
}

func TestRunHandoffFallsBackToTheNewestTranscriptOnDisk(t *testing.T) {
	t.Setenv(claudesess.EnvVar, "")
	home := t.TempDir()
	const cwd = "/src/app"
	dir := claudesess.ProjectDir(home, cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "on-disk.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sess := handoffSession()
	out, err := RunHandoff(Deps{Session: sess}, HandoffOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}

	if got := sess.startAgentCalls[0].args[1]; got != "on-disk" {
		t.Errorf("resumed %q, want the transcript found on disk", got)
	}
	// Found where the caller stands, so nothing surprising to report.
	if out.SessionWidened {
		t.Error("SessionWidened = true for a session in the caller's own directory")
	}
}

func TestRunHandoffWidensToTheNewestSessionAnywhere(t *testing.T) {
	t.Setenv(claudesess.EnvVar, "")
	home := t.TempDir()
	writeTranscript(t, home, "/src/elsewhere", "far-away")

	sess := handoffSession()
	// The caller is standing somewhere Claude has never been.
	out, err := RunHandoff(Deps{Session: sess}, HandoffOptions{Cwd: "/src/empty", Home: home})
	if err != nil {
		t.Fatal(err)
	}

	if got := sess.startAgentCalls[0].args[1]; got != "far-away" {
		t.Errorf("resumed %q, want the session found elsewhere", got)
	}
	// The Space has to follow the session. Opening a conversation about one
	// repository in the directory of another would be worse than not finding
	// it at all.
	call := sess.createWorkspaceCalls[0]
	if call.cwd != "/src/elsewhere" {
		t.Errorf("cwd = %q, want the session's own directory", call.cwd)
	}
	if call.label != "elsewhere" {
		t.Errorf("label = %q, want it named after the session's directory", call.label)
	}
	if !out.SessionWidened {
		t.Error("SessionWidened = false; a pick from another directory has to be reportable")
	}
	if out.SessionID != "far-away" {
		t.Errorf("SessionID = %q, want it carried out for reporting", out.SessionID)
	}
}

func TestRunHandoffPrefersASessionInTheCallersDirectory(t *testing.T) {
	t.Setenv(claudesess.EnvVar, "")
	home := t.TempDir()
	// Newer, but somewhere else: standing in a directory with its own session
	// is a strong statement about which conversation you mean.
	writeTranscript(t, home, "/src/elsewhere", "newer-elsewhere")
	writeTranscript(t, home, "/src/here", "older-here")
	touchOlder(t, home, "/src/here", "older-here")

	sess := handoffSession()
	out, err := RunHandoff(Deps{Session: sess}, HandoffOptions{Cwd: "/src/here", Home: home})
	if err != nil {
		t.Fatal(err)
	}

	if got := sess.startAgentCalls[0].args[1]; got != "older-here" {
		t.Errorf("resumed %q, want the session in the caller's directory", got)
	}
	if out.SessionWidened {
		t.Error("SessionWidened = true, want false when the directory had its own session")
	}
}

func TestRunHandoffFailsWhenNoSessionCanBeFound(t *testing.T) {
	t.Setenv(claudesess.EnvVar, "")
	sess := handoffSession()

	_, err := RunHandoff(Deps{Session: sess}, HandoffOptions{Cwd: "/src/app", Home: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error when there is no session to resume")
	}
	if !strings.Contains(err.Error(), "--session") {
		t.Errorf("error = %q, want it to point at the escape hatch", err)
	}
	// Failing before the Space is built is the point: a Space with a fresh
	// agent in it is not what was asked for, and is worse than nothing.
	if len(sess.createWorkspaceCalls) != 0 {
		t.Errorf("CreateWorkspace calls = %+v, want none", sess.createWorkspaceCalls)
	}
}

func TestRunHandoffRetriesThroughPaneBusy(t *testing.T) {
	t.Setenv(claudesess.EnvVar, "abc")
	sess := handoffSession()
	sess.startAgentBusyUntilCall = 3

	if _, err := RunHandoff(Deps{Session: sess}, HandoffOptions{Cwd: "/src/app"}); err != nil {
		t.Fatal(err)
	}

	// Same race as everywhere else: the Space and the agent are created in
	// one breath, so the pane is often not a registered shell yet.
	if len(sess.startAgentCalls) != 3 {
		t.Errorf("StartAgent calls = %d, want 3 (two busy, one that succeeds)", len(sess.startAgentCalls))
	}
}

func TestRunHandoffReportsTheSpaceWhenTheAgentStepFails(t *testing.T) {
	t.Setenv(claudesess.EnvVar, "abc")
	sess := handoffSession()
	sess.waitErr = errors.New("agent never reported idle")

	out, err := RunHandoff(Deps{Session: sess}, HandoffOptions{Cwd: "/src/app"})
	if err == nil {
		t.Fatal("expected the wait failure to surface")
	}
	// The Space exists by this point. Reporting it as absent would send the
	// caller looking for something that is sitting right there.
	if out.WorkspaceID != "w7" {
		t.Errorf("WorkspaceID = %q, want the Space that was created", out.WorkspaceID)
	}
	if out.AgentStarted {
		t.Error("AgentStarted = true, want false when the agent never came up")
	}
}

func TestRunHandoffFallsBackToDepsCwd(t *testing.T) {
	t.Setenv(claudesess.EnvVar, "abc")
	sess := handoffSession()

	if _, err := RunHandoff(Deps{Session: sess, Cwd: "/src/from-deps"}, HandoffOptions{}); err != nil {
		t.Fatal(err)
	}

	if got := sess.createWorkspaceCalls[0].cwd; got != "/src/from-deps" {
		t.Errorf("cwd = %q, want the invocation directory", got)
	}
}
