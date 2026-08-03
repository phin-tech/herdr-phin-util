package open

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/gh"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/setup"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// fakeLayout records the layout calls in the order they were made, which is
// the thing worth asserting about: a setup is an ordering as much as it is a
// list of panes.
type fakeLayout struct {
	calls []string

	createTabErr error
	splitErr     error
	// splitErrAtCall fails only the Nth SplitPane call (1-based), succeeding
	// on every other one -- how a test expresses "this one split failed, but
	// the layout otherwise built fine" rather than every split failing alike.
	splitErrAtCall int
	splitCalls     int
	runErr         error
	promptErr      error
	// promptErrUntilCall makes PromptAgent fail for every call before this
	// one, which is how a prompt that lands only on the retry is expressed.
	promptErrUntilCall int
	promptCalls        int

	nextPane int
}

func (f *fakeLayout) pane() herdr.Pane {
	f.nextPane++
	return herdr.Pane{PaneID: fmt.Sprintf("p%d", f.nextPane), TabID: fmt.Sprintf("t%d", f.nextPane)}
}

func (f *fakeLayout) CreateTab(workspaceID, cwd, label string, env map[string]string, focus bool) (herdr.Pane, string, error) {
	f.calls = append(f.calls, fmt.Sprintf("tab %s cwd=%s focus=%v env=%v", label, cwd, focus, env))
	if f.createTabErr != nil {
		return herdr.Pane{}, "", f.createTabErr
	}
	p := f.pane()
	return p, p.TabID, nil
}

func (f *fakeLayout) SplitPane(target, direction string, ratio float64, cwd string, env map[string]string, focus bool) (herdr.Pane, error) {
	f.calls = append(f.calls, fmt.Sprintf("split %s %s ratio=%v cwd=%s env=%v", target, direction, ratio, cwd, env))
	f.splitCalls++
	if f.splitErr != nil {
		return herdr.Pane{}, f.splitErr
	}
	if f.splitErrAtCall != 0 && f.splitCalls == f.splitErrAtCall {
		return herdr.Pane{}, errors.New("no room")
	}
	return f.pane(), nil
}

func (f *fakeLayout) RunCommand(paneID, command string) error {
	f.calls = append(f.calls, fmt.Sprintf("run %s %s", paneID, command))
	return f.runErr
}

func (f *fakeLayout) PromptAgent(paneID, text string) error {
	f.calls = append(f.calls, fmt.Sprintf("prompt %s %s", paneID, text))
	f.promptCalls++
	if f.promptCalls < f.promptErrUntilCall {
		return errors.New("agent is not accepting input yet")
	}
	return f.promptErr
}

func (f *fakeLayout) RenamePane(paneID, label string) error {
	f.calls = append(f.calls, fmt.Sprintf("label %s %s", paneID, label))
	return nil
}

func (f *fakeLayout) RenameTab(tabID, label string) error {
	f.calls = append(f.calls, fmt.Sprintf("rename-tab %s %s", tabID, label))
	return nil
}

func (f *fakeLayout) FocusPane(paneID string) error {
	f.calls = append(f.calls, "focus "+paneID)
	return nil
}

func (f *fakeLayout) transcript() string { return strings.Join(f.calls, "\n") }

// reviewSetup is the setup this whole feature exists for, used by several
// tests below.
func reviewSetup() setup.Setup {
	return setup.Setup{
		Name: "pr-review",
		Tabs: []setup.Tab{
			{Name: "review", Panes: []setup.Pane{
				{Label: "orchestrator", Agent: "claude", Prompt: "orchestrate #{{.Number}}"},
				{Split: "right", Agent: "claude", Skill: "/code-review", Submit: true},
				{Split: "down", Ratio: 0.25, Command: "roborev review-branch", WaitFor: &setup.WaitFor{Match: "queued", TimeoutMs: 1000}},
			}},
			{Name: "shell"},
		},
	}
}

func rootPane() herdr.Pane { return herdr.Pane{PaneID: "root", TabID: "root-tab"} }

func TestApplySetupBuildsTheWholeLayout(t *testing.T) {
	s := &fakeSession{}
	l := &fakeLayout{}
	cfg := &config.Settings{}

	plan, panes, problems, err := applySetup(Deps{Session: s, Layout: l}, cfg, target.Target{}, reviewSetup(), rootPane(), "w1", "/repo", "/repo", map[string]string{"Number": "42"})
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none", problems)
	}

	if plan.Name != "pr-review" {
		t.Errorf("plan name = %q", plan.Name)
	}
	if len(panes) != 4 {
		t.Fatalf("panes = %v, want one per pane in the file", panes)
	}
	// The first pane is the Space's own: making a second would leave the
	// original tab sitting there empty.
	if panes[0] != "root" {
		t.Errorf("first pane = %q, want the Space's root pane reused", panes[0])
	}

	got := l.transcript()
	for _, want := range []string{
		"rename-tab root-tab review",
		"split root right",
		"ratio=0.25",
		"tab shell",
		"run p2 HERDR_PANE_ID='p2' HERDR_PANE_ORCHESTRATOR='root' HERDR_TAB_ID='root-tab' HERDR_WORKSPACE_ID='w1' roborev review-branch",
		"prompt p1 /code-review",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q:\n%s", want, got)
		}
	}

	// Splits are relative to the pane before them, so the second split targets
	// the first split's pane rather than the root again.
	if !strings.Contains(got, "split p1 down") {
		t.Errorf("second split does not chain off the first:\n%s", got)
	}
}

// Every pane exists before anything runs in one. Splitting a tab after a TUI
// has started in it resizes a running program.
func TestApplySetupCreatesEveryPaneBeforeFillingAny(t *testing.T) {
	l := &fakeLayout{}
	if _, _, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, reviewSetup(), rootPane(), "w1", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	lastBuild, firstFill := -1, len(l.calls)
	for i, call := range l.calls {
		switch {
		case strings.HasPrefix(call, "split "), strings.HasPrefix(call, "tab "):
			lastBuild = i
		case strings.HasPrefix(call, "run "), strings.HasPrefix(call, "prompt "):
			if i < firstFill {
				firstFill = i
			}
		}
	}
	if lastBuild > firstFill {
		t.Errorf("a pane was filled before the layout finished building:\n%s", l.transcript())
	}
}

func TestApplySetupTypesUnsubmittedPromptsAndSendsSubmittedOnes(t *testing.T) {
	s := &fakeSession{}
	l := &fakeLayout{}

	if _, _, _, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, reviewSetup(), rootPane(), "w1", "/repo", "/repo", map[string]string{"Number": "42"}); err != nil {
		t.Fatal(err)
	}

	// The orchestrator's prompt is typed, not sent: it is the one a person
	// reads before firing.
	if len(s.sendTextCalls) != 1 || s.sendTextCalls[0].text != "orchestrate #42" {
		t.Errorf("send_text calls = %+v, want the unsubmitted prompt only", s.sendTextCalls)
	}
	// And the submitted one went through agent.prompt instead.
	if !strings.Contains(l.transcript(), "prompt p1 /code-review") {
		t.Errorf("the submit:true pane was not sent:\n%s", l.transcript())
	}
	for _, call := range s.sendTextCalls {
		if strings.Contains(call.text, "code-review") {
			t.Error("a submit:true prompt was typed instead of sent")
		}
	}
}

func TestApplySetupWaitsBeforeContinuing(t *testing.T) {
	s := &fakeSession{}
	l := &fakeLayout{}

	if _, _, _, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, reviewSetup(), rootPane(), "w1", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	var waited bool
	for _, call := range s.waitOutputCalls {
		if call.value == "queued" && call.timeoutMs == 1000 {
			waited = true
		}
	}
	if !waited {
		t.Errorf("wait_for never ran: %+v", s.waitOutputCalls)
	}
}

// A wrong guess at a match string must not strand a Space that is otherwise
// fully built.
func TestApplySetupWaitTimeoutIsNotFatal(t *testing.T) {
	s := &fakeSession{waitOutputErr: errors.New("timed out")}
	l := &fakeLayout{}

	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{Command: "roborev", WaitFor: &setup.WaitFor{Match: "never", TimeoutMs: 1}},
		{Split: "down", Command: "echo done"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil); err != nil {
		t.Fatalf("a wait timeout aborted the layout: %v", err)
	}
	if !strings.Contains(l.transcript(), "echo done") {
		t.Errorf("the layout stopped at the failed wait:\n%s", l.transcript())
	}
}

// codex clears launch_pending on process detection, seconds before its input
// exists, and reports interactive_ready on its own first-run screens -- so the
// on-screen marker is the only thing standing between a prompt and a startup
// screen. It has to be waited for before anything is sent.
func TestApplySetupWaitsForTheCodexInputBeforePrompting(t *testing.T) {
	s := &fakeSession{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Agent: "codex", Prompt: "review this", Submit: true},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: &fakeLayout{}}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}

	var waited bool
	for _, call := range s.waitOutputCalls {
		if call.value == readyMarkers["codex"] {
			waited = true
		}
	}
	if !waited {
		t.Errorf("never waited for codex's input marker: %+v", s.waitOutputCalls)
	}
}

// The bug this closes: the prompt went out into codex's first-run screen,
// agent.prompt answered ok because delivery is not verified, and the pane sat
// there empty with no warning. An input that never arrives must cost the step
// loudly rather than swallow the prompt.
func TestApplySetupDoesNotPromptACodexStuckOnItsStartupScreen(t *testing.T) {
	s := &fakeSession{waitOutputErr: errors.New("timed out")}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Label: "codex-reviewer", Agent: "codex", Prompt: "review this", Submit: true},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if l.promptCalls != 0 || len(s.sendTextCalls) != 0 {
		t.Errorf("the prompt was sent into a startup screen: agent.prompt calls=%d sendText=%+v", l.promptCalls, s.sendTextCalls)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "render its prompt") {
		t.Errorf("problems = %v, want the dropped pane reported", problems)
	}
	if !strings.Contains(l.transcript(), "failed: codex-reviewer") {
		t.Errorf("the pane was not marked failed:\n%s", l.transcript())
	}
}

// #18: a positive marker rendering is not proof the pane is promptable -- a
// trust dialog can render whatever readyMarkers gates on too, in scrollback
// or otherwise. blockedMarkers is the check that catches it: a pane showing
// known modal text must fail visibly rather than get its prompt typed into
// the dialog.
func TestApplySetupFailsOnACodexTrustPrompt(t *testing.T) {
	s := &fakeSession{readPaneText: "Do you trust the contents of this directory?\n\n1. Yes, continue\n2. No, quit"}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Label: "codex-reviewer", Agent: "codex", Prompt: "review this", Submit: true},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if l.promptCalls != 0 || len(s.sendTextCalls) != 0 {
		t.Errorf("the prompt was sent into the trust dialog: agent.prompt calls=%d sendText=%+v", l.promptCalls, s.sendTextCalls)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "Do you trust") {
		t.Errorf("problems = %v, want the on-screen modal text named", problems)
	}
	if !strings.Contains(l.transcript(), "failed: codex-reviewer") {
		t.Errorf("the pane was not marked failed:\n%s", l.transcript())
	}
}

// The same modal is reachable with no setup at all: `open <pr-url>` cuts a
// worktree the agent has never seen, which is the whole trigger. That path
// has no on_launch to answer it -- that is a pane field, and there is no file
// here -- so the most it can do is refuse to type into the dialog. Refusing
// is still the difference between a visible failure and a lost prompt, and
// leaving the single-agent path without the check would have made the fix
// depend on whether you happened to use --setup.
func TestWaitAgentDrawnRefusesAModalOnTheSingleAgentPath(t *testing.T) {
	s := &fakeSession{readPaneText: "Do you trust the contents of this directory?\n\n1. Yes, continue"}

	err := waitAgentDrawn(s, "p1", "codex")
	if err == nil {
		t.Fatal("want an error when a modal is on screen, so nothing is typed into it")
	}
	if !strings.Contains(err.Error(), "Do you trust") {
		t.Errorf("error = %q, want the on-screen modal text named", err)
	}
}

// A kind with no positive marker still gets the blocked check -- the lookup
// that used to return early sat in front of it, so this pins the ordering.
func TestWaitAgentDrawnChecksModalsForAKindWithNoReadyMarker(t *testing.T) {
	if _, hasMarker := readyMarkers["gemini"]; hasMarker {
		t.Skip("gemini gained a ready marker; pick another markerless kind")
	}
	s := &fakeSession{readPaneText: "Do you trust the contents of this directory?"}

	// gemini has no blockedMarkers entry either, so it must pass cleanly --
	// the check is per-kind, not a global screen scrape.
	if err := waitAgentDrawn(s, "p1", "gemini"); err != nil {
		t.Errorf("waitAgentDrawn on a kind with no markers = %v, want nil", err)
	}
}

// The common case: nothing blocked, so nothing about the existing behaviour
// changes.
func TestApplySetupPromptsNormallyWhenNoBlockedMarkerIsOnScreen(t *testing.T) {
	s := &fakeSession{readPaneText: "codex\n>  \n\nopus-4 · /repo"}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Agent: "codex", Prompt: "review this", Submit: true},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none", problems)
	}
	if l.promptCalls != 1 {
		t.Errorf("agent.prompt calls = %d, want 1", l.promptCalls)
	}
}

// on_launch's whole point: a modal that does come up gets answered.
func TestOnLaunchSendsKeysWhenItsMatchAppears(t *testing.T) {
	s := &fakeSession{}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Agent: "codex", Prompt: "review this", Submit: true, OnLaunch: []setup.OnLaunchStep{
			{Match: "Do you trust", Keys: []string{"1", "Enter"}},
		}},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	if len(s.sendKeysCalls) != 1 {
		t.Fatalf("SendKeys calls = %+v, want exactly one", s.sendKeysCalls)
	}
	if got := s.sendKeysCalls[0].keys; len(got) != 2 || got[0] != "1" || got[1] != "Enter" {
		t.Errorf("keys sent = %v, want [1 Enter] verbatim", got)
	}
}

// The common case, and the one that must never regress: no modal shows up,
// so on_launch's wait times out and the pane is not failed over it.
func TestOnLaunchThatNeverMatchesDoesNotFailThePane(t *testing.T) {
	s := &fakeSession{waitOutputErrFor: map[string]error{
		"Do you trust": errors.New("timed out"),
	}}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Agent: "claude", Prompt: "review this", Submit: true, OnLaunch: []setup.OnLaunchStep{
			{Match: "Do you trust", Keys: []string{"1", "Enter"}},
		}},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none -- a modal that never showed up must not fail the pane", problems)
	}
	if len(s.sendKeysCalls) != 0 {
		t.Errorf("SendKeys was called for a match that never appeared: %+v", s.sendKeysCalls)
	}
	if l.promptCalls != 1 {
		t.Errorf("agent.prompt calls = %d, want 1 -- the pane should still get its prompt", l.promptCalls)
	}
}

// Several entries have to run in the order the file lists them: the first
// modal has to be cleared before the second one, if there is one, would even
// be on screen.
func TestOnLaunchEntriesRunInOrder(t *testing.T) {
	s := &fakeSession{}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Agent: "codex", OnLaunch: []setup.OnLaunchStep{
			{Match: "update available", Keys: []string{"Enter"}},
			{Match: "Do you trust", Keys: []string{"1", "Enter"}},
		}},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	if len(s.sendKeysCalls) != 2 {
		t.Fatalf("SendKeys calls = %+v, want two", s.sendKeysCalls)
	}
	if s.sendKeysCalls[0].keys[0] != "Enter" || s.sendKeysCalls[1].keys[0] != "1" {
		t.Errorf("entries ran out of order: %+v", s.sendKeysCalls)
	}
	// And the wait for the second entry's match happened after the wait for
	// the first's, not the other way round or concurrently.
	var firstIdx, secondIdx = -1, -1
	for i, call := range s.waitOutputCalls {
		if call.value == "update available" && firstIdx == -1 {
			firstIdx = i
		}
		if call.value == "Do you trust" && secondIdx == -1 {
			secondIdx = i
		}
	}
	if firstIdx == -1 || secondIdx == -1 || firstIdx > secondIdx {
		t.Errorf("waitOutputCalls = %+v, want the first entry's wait before the second's", s.waitOutputCalls)
	}
}

// A pane that answers a modal via on_launch still has to wait for its real
// input before being prompted -- readiness is re-checked after on_launch
// runs, not skipped because something was sent.
func TestReadinessIsRecheckedAfterOnLaunchAnswersAModal(t *testing.T) {
	s := &fakeSession{readPaneText: "opus-4 · /repo"} // the modal cleared
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Agent: "codex", Prompt: "review this", Submit: true, OnLaunch: []setup.OnLaunchStep{
			{Match: "Do you trust", Keys: []string{"1", "Enter"}},
		}},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none", problems)
	}
	if len(s.sendKeysCalls) != 1 {
		t.Errorf("on_launch never ran: %+v", s.sendKeysCalls)
	}
	if l.promptCalls != 1 {
		t.Errorf("agent.prompt calls = %d, want 1 -- the pane cleared the modal and should be prompted", l.promptCalls)
	}
}

// on_launch answering a modal is only an attempt, not a guarantee -- if the
// pane is still showing the same modal afterward (a wrong keypress, a second
// dialog), that has to fail visibly the same as if on_launch had not run at
// all.
func TestStillBlockedAfterOnLaunchFailsVisibly(t *testing.T) {
	s := &fakeSession{readPaneText: "Do you trust the contents of this directory?"}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Label: "codex-reviewer", Agent: "codex", Prompt: "review this", Submit: true, OnLaunch: []setup.OnLaunchStep{
			{Match: "Do you trust", Keys: []string{"1", "Enter"}},
		}},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(s.sendKeysCalls) != 1 {
		t.Errorf("on_launch never ran: %+v", s.sendKeysCalls)
	}
	if l.promptCalls != 0 {
		t.Errorf("the prompt was sent even though the modal never cleared")
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "Do you trust") {
		t.Errorf("problems = %v, want the still-on-screen modal named", problems)
	}
}

// timeout_ms is per entry, and a blank one falls back to
// setup.DefaultOnLaunchTimeoutMs -- both have to reach WaitPaneOutput, since
// that is what actually bounds how long a pane sits waiting for a modal that
// may never show.
func TestOnLaunchTimeoutIsPerEntryWithADefaultWhenOmitted(t *testing.T) {
	s := &fakeSession{}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Agent: "codex", OnLaunch: []setup.OnLaunchStep{
			{Match: "explicit timeout", Keys: []string{"Enter"}, TimeoutMs: 750},
			{Match: "default timeout", Keys: []string{"Enter"}},
		}},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	var sawExplicit, sawDefault bool
	for _, call := range s.waitOutputCalls {
		if call.value == "explicit timeout" && call.timeoutMs == 750 {
			sawExplicit = true
		}
		if call.value == "default timeout" && call.timeoutMs == setup.DefaultOnLaunchTimeoutMs {
			sawDefault = true
		}
	}
	if !sawExplicit {
		t.Errorf("explicit timeout_ms was not honoured: %+v", s.waitOutputCalls)
	}
	if !sawDefault {
		t.Errorf("omitted timeout_ms did not fall back to the default: %+v", s.waitOutputCalls)
	}
}

func TestApplySetupFocusesTheMarkedPane(t *testing.T) {
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{},
		{Split: "down", Focus: true},
	}}}}

	_, panes, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatal(err)
	}

	calls := l.calls
	if last := calls[len(calls)-1]; last != "focus "+panes[1] {
		t.Errorf("last call = %q, want focus on the marked pane (%s) once nothing is left to build", last, panes[1])
	}
}

func TestApplySetupFocusesTheFirstPaneWhenNoneIsMarked(t *testing.T) {
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{{}, {Split: "down"}}}}}

	if _, _, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}
	if last := l.calls[len(l.calls)-1]; last != "focus root" {
		t.Errorf("last call = %q, want focus on the first pane", last)
	}
}

func TestApplySetupRejectsAnUnknownAgentKind(t *testing.T) {
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{{Agent: "clod"}}}}}

	_, _, problems, err := applySetup(Deps{Session: &fakeSession{}, Layout: &fakeLayout{}}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "clod") {
		t.Errorf("problems = %v, want one naming the agent Herdr does not know", problems)
	}
}

// A failed step names where it happened and leaves the rest standing, rather
// than tearing down a Space the user can already see.
func TestApplySetupFailureNamesTheTab(t *testing.T) {
	l := &fakeLayout{splitErr: errors.New("no room")}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{{}, {Split: "down"}}}}}

	_, panes, problems, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want one", problems)
	}
	if !strings.Contains(problems[0], "review") || !strings.Contains(problems[0], "no room") {
		t.Errorf("problem = %q, want the tab and the cause", problems[0])
	}
	if panes[0] != "root" {
		t.Errorf("panes = %v, want what was built before the failure", panes)
	}
}

// The defect this whole shape exists for: one pane that will not start is not
// a reason for the panes after it to be left as bare shells.
func TestApplySetupCarriesOnPastAFailedPane(t *testing.T) {
	s := &fakeSession{startAgentErr: errors.New("agent would not start")}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{
		{Label: "reviewer", Agent: "claude", Prompt: "review it", Submit: true},
		{Split: "down", Label: "checks", Command: "roborev review-branch"},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "agent would not start") {
		t.Errorf("problems = %v, want the one failure named", problems)
	}

	got := l.transcript()
	if !strings.Contains(got, "roborev review-branch") {
		t.Errorf("the pane after the failed one never ran:\n%s", got)
	}
	// And the Space says which pane it was, rather than leaving a bare shell
	// under a label that claims it is fine.
	if !strings.Contains(got, "label root failed: reviewer") {
		t.Errorf("the failed pane was not marked:\n%s", got)
	}
}

// A tab that could not be created takes its own panes with it: splitting on
// the previous tab's pane would put them in a tab the file never asked for.
func TestApplySetupSkipsTheRestOfATabItCouldNotCreate(t *testing.T) {
	l := &fakeLayout{createTabErr: errors.New("no tab")}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{
		{Name: "first", Panes: []setup.Pane{{Command: "one"}}},
		{Name: "second", Panes: []setup.Pane{{Command: "two"}, {Split: "down", Command: "three"}}},
	}}

	_, _, problems, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "second") {
		t.Errorf("problems = %v, want the one tab named once", problems)
	}

	got := l.transcript()
	if !strings.Contains(got, "run root HERDR_PANE_ID='root' HERDR_TAB_ID='root-tab' HERDR_WORKSPACE_ID='w1' one") {
		t.Errorf("the tab before the failure did not run:\n%s", got)
	}
	if strings.Contains(got, "three") {
		t.Errorf("a pane of the abandoned tab was built anyway:\n%s", got)
	}
}

// A wait_for on a pane whose work never started is skipped: what it listens
// for is that work, so it could only ever spend its whole timeout.
func TestApplySetupSkipsTheWaitOfAFailedPane(t *testing.T) {
	s := &fakeSession{}
	l := &fakeLayout{runErr: errors.New("no shell")}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{Command: "roborev", WaitFor: &setup.WaitFor{Match: "queued", TimeoutMs: 30000}},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}
	for _, call := range s.waitOutputCalls {
		if call.value == "queued" {
			t.Errorf("waited on a pane whose command never ran: %+v", s.waitOutputCalls)
		}
	}
}

// A prompt that does not land first time gets one more go: the readiness
// checks are a good guess at "ready to be typed into", not a guarantee.
func TestApplySetupRetriesAPromptOnce(t *testing.T) {
	l := &fakeLayout{promptErrUntilCall: 2}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{
		{Agent: "claude", Prompt: "review it", Submit: true},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want the retry to have covered it", problems)
	}
	if strings.Count(l.transcript(), "prompt root review it") != 2 {
		t.Errorf("want two attempts:\n%s", l.transcript())
	}
}

// The whole point of the launch gate: a submitted prompt waits for the agent
// to finish launching, and only then goes out. Without it the prompt is sent
// into an agent Herdr will refuse -- which is the bug that lost two reviewer
// prompts in a four-pane run.
func TestApplySetupWaitsForLaunchBeforeSubmittingAPrompt(t *testing.T) {
	s := &fakeSession{launchedAfterCall: 4}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{
		{Agent: "claude", Prompt: "review it", Submit: true},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if s.launchedCalls < 4 {
		t.Errorf("polled launch state %d times, want it to keep looking until launched", s.launchedCalls)
	}
	if !strings.Contains(l.transcript(), "prompt root review it") {
		t.Errorf("the prompt never went out:\n%s", l.transcript())
	}
}

// An agent stuck on its own first-run UI never launches. That is a real
// failure and it should say so, rather than surfacing as a bare rejection from
// the prompt step.
func TestApplySetupReportsAnAgentThatNeverLaunches(t *testing.T) {
	s := &fakeSession{launchNever: true}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{
		{Agent: "codex", Prompt: "review it", Submit: true},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "never finished launching") {
		t.Fatalf("problems = %v, want one naming the launch", problems)
	}
	if strings.Contains(l.transcript(), "prompt ") {
		t.Errorf("prompted an agent that never launched:\n%s", l.transcript())
	}
	if !strings.Contains(l.transcript(), "label root failed:") {
		t.Errorf("the pane was not marked failed:\n%s", l.transcript())
	}
}

// A typed prompt goes through pane.send_text, which does not care whether the
// agent has registered. An agent that is slow to launch must not cost that
// pane its prompt.
func TestApplySetupStillTypesIntoAnAgentThatNeverLaunches(t *testing.T) {
	s := &fakeSession{launchNever: true}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{
		{Agent: "claude", Prompt: "read this first"},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: &fakeLayout{}}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none -- typing needs no launch", problems)
	}
	if len(s.sendTextCalls) != 1 || s.sendTextCalls[0].text != "read this first" {
		t.Errorf("send_text calls = %+v, want the prompt typed anyway", s.sendTextCalls)
	}
}

func TestApplySetupAgentNamesAreUniqueAndValid(t *testing.T) {
	s := &fakeSession{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{
		{Agent: "claude"},
		{Split: "down", Agent: "claude"},
		{Split: "down", Label: "Worker #2!", Agent: "claude"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: s, Layout: &fakeLayout{}}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, call := range s.startAgentCalls {
		if seen[call.name] {
			t.Errorf("agent name %q used twice -- agent.start requires unique names", call.name)
		}
		seen[call.name] = true
		if !validAgentName(call.name) {
			t.Errorf("agent name %q would be rejected by agent.start", call.name)
		}
	}
	if len(seen) != 3 {
		t.Errorf("started %d agents, want 3", len(seen))
	}
}

// Agent names are global to Herdr, not scoped to a Space, so a setup opened a
// second time while the first is still up asks for names another Space holds.
// That must not leave a bare shell where an agent belongs: the name is
// qualified by this Space and retried.
func TestApplySetupRetriesATakenAgentNameQualifiedByTheSpace(t *testing.T) {
	s := &fakeSession{takenNames: map[string]bool{"codex-reviewer-1": true}}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Label: "codex-reviewer", Agent: "codex"},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w14", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none -- the collision is recoverable", problems)
	}
	if len(s.startAgentCalls) != 2 {
		t.Fatalf("StartAgent calls = %+v, want 2 (the taken name, then the qualified one)", s.startAgentCalls)
	}
	if got := s.startAgentCalls[1].name; got != "codex-reviewer-1-w14" {
		t.Errorf("retried under %q, want %q", got, "codex-reviewer-1-w14")
	}
	// The label is what a person reads off the pane, and it was never the
	// thing that collided.
	if strings.Contains(l.transcript(), "failed:") || !strings.Contains(l.transcript(), "label root codex-reviewer") {
		t.Errorf("the pane label should be untouched and unfailed:\n%s", l.transcript())
	}
}

// A qualified name that is also taken is a real failure: retrying it a third
// way would just be guessing.
func TestApplySetupReportsAnAgentNameTakenTwice(t *testing.T) {
	s := &fakeSession{takenNames: map[string]bool{"codex-reviewer-1": true, "codex-reviewer-1-w14": true}}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Label: "codex-reviewer", Agent: "codex"},
	}}}}

	_, _, problems, err := applySetup(Deps{Session: s, Layout: &fakeLayout{}}, &config.Settings{}, target.Target{}, def, rootPane(), "w14", "/repo", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "already used") {
		t.Errorf("problems = %v, want the collision reported", problems)
	}
	if len(s.startAgentCalls) != 2 {
		t.Errorf("StartAgent calls = %d, want 2 -- no third guess", len(s.startAgentCalls))
	}
}

// validAgentName mirrors agent.start's rule: lowercase letter first, then
// lowercase letters, digits, '-' or '_', to 32 characters.
func validAgentName(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func TestApplySetupInheritsTheSpaceDirectory(t *testing.T) {
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{
		{Name: "a", Panes: []setup.Pane{{}, {Split: "down", Cwd: "web"}}},
		{Name: "b", Cwd: "docs"},
	}}

	if _, _, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	got := l.transcript()
	if !strings.Contains(got, "cwd=/repo/web") {
		t.Errorf("pane cwd not resolved against the Space:\n%s", got)
	}
	if !strings.Contains(got, "tab b cwd=/repo/docs") {
		t.Errorf("tab cwd not resolved against the Space:\n%s", got)
	}
}

// The whole point of the seam in runAgentStep: with no setup picked, nothing
// about the ordinary path changes.
func TestRunAgentStepWithoutASetupIsUnchanged(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}
	l := &fakeLayout{}
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}}
	cfg.Prompts.Plain = "{{.Text}}"

	tgt := target.Target{Kind: target.KindPlain, Text: "scratch"}
	out, err := runAgentStep(Deps{Session: s, Layout: l}, cfg, tgt, Options{}, herdr.Pane{PaneID: "p1"}, map[string]string{"Text": "scratch"}, Outcome{})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.calls) != 0 {
		t.Errorf("the layout API was touched without a setup: %v", l.calls)
	}
	if out.SetupName != "" {
		t.Errorf("SetupName = %q, want empty", out.SetupName)
	}
	if !out.AgentStarted || out.PromptSent != "scratch" {
		t.Errorf("outcome = %+v, want the ordinary single-agent result", out)
	}
}

// The panes of a worktree Space belong in the worktree, not in the checkout it
// was cut from. Building them in the source checkout is how a pull-request
// review setup ends up reviewing main.
func TestApplySetupBuildsInTheWorktreeNotTheSourceCheckout(t *testing.T) {
	repo, cfg := existingRepo(t)
	worktree := filepath.Join(t.TempDir(), "worktrees", "fix-thing")

	sess := &fakeSession{pane: herdr.Pane{PaneID: "wZ:p1", TabID: "wZ:t1", Cwd: worktree}, workspaceID: "wZ"}
	l := &fakeLayout{}
	prs := &fakePRLookup{info: gh.PRInfo{Branch: "fix-thing", Title: "Fix the thing"}}

	def := reviewSetup()
	out, err := Run(Deps{Session: sess, Layout: l, PRs: prs, Git: &fakeFetcher{}}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/42", Options{Setup: &def})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", out.Warnings)
	}

	got := l.transcript()
	if !strings.Contains(got, "cwd="+worktree) {
		t.Errorf("panes were not built in the worktree %s:\n%s", worktree, got)
	}
	if strings.Contains(got, "cwd="+repo) {
		t.Errorf("a pane was built in the source checkout %s:\n%s", repo, got)
	}
}

// worktree.create failing and worktree.open landing on the source checkout is
// the silent degradation: the Space looks right and is on the wrong branch.
func TestWorktreeFallbackToTheSourceCheckoutIsReported(t *testing.T) {
	repo, cfg := existingRepo(t)
	sess := &fakeSession{
		createWorktreeErr: errors.New("branch is already checked out"),
		pane:              herdr.Pane{PaneID: "wZ:p1", Cwd: repo},
		workspaceID:       "wZ",
	}

	out, err := Run(Deps{Session: sess, PRs: &fakePRLookup{info: gh.PRInfo{Branch: "fix-thing"}}, Git: &fakeFetcher{}}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/42", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one", out.Warnings)
	}
	for _, want := range []string{"already checked out", repo, "fix-thing"} {
		if !strings.Contains(out.Warnings[0], want) {
			t.Errorf("warning %q does not mention %q", out.Warnings[0], want)
		}
	}
}

// Reusing a worktree that already exists is the good outcome of the same
// fallback, and still worth a line -- but it must not read as the bad one.
func TestWorktreeFallbackToAnExistingWorktreeIsReportedGently(t *testing.T) {
	repo, cfg := existingRepo(t)
	worktree := filepath.Join(t.TempDir(), "fix-thing")
	sess := &fakeSession{
		createWorktreeErr: errors.New("worktree already exists"),
		pane:              herdr.Pane{PaneID: "wZ:p1", Cwd: worktree},
		workspaceID:       "wZ",
	}

	out, err := Run(Deps{Session: sess, PRs: &fakePRLookup{info: gh.PRInfo{Branch: "fix-thing"}}, Git: &fakeFetcher{}}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/42", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "reused the worktree") {
		t.Errorf("warnings = %v, want the reuse said plainly", out.Warnings)
	}
	if strings.Contains(out.Warnings[0], repo) {
		t.Errorf("warning claims the source checkout: %q", out.Warnings[0])
	}
}

// A command pane's typed line carries its own workspace/tab/pane ids -- #9's
// core ask -- so a `command:` pane never has to poll `herdr pane list` to
// find itself.
func TestFillPanePrefixesACommandWithItsOwnIds(t *testing.T) {
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{Command: "./discover.py"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w2H", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	got := l.transcript()
	want := "run root HERDR_PANE_ID='root' HERDR_TAB_ID='root-tab' HERDR_WORKSPACE_ID='w2H' ./discover.py"
	if !strings.Contains(got, want) {
		t.Errorf("transcript missing %q:\n%s", want, got)
	}
}

// A labelled sibling pane -- anywhere in the plan, not just the same tab --
// becomes HERDR_PANE_<FOLDED LABEL> in a command pane's environment, which is
// the polling loop issue #9 exists to remove.
func TestFillPaneAddsALabelledSiblingAsAnHerdrPaneVariable(t *testing.T) {
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{Label: "meta-orchestrator", Agent: "claude"},
		{Split: "down", Command: "./discover.py"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w2H", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	got := l.transcript()
	// The first pane of the first tab reuses the Space's own root pane, so the
	// labelled agent -- pane 0 -- is "root"; the split after it is the first
	// pane fakeLayout actually creates, "p1".
	if !strings.Contains(got, "HERDR_PANE_META_ORCHESTRATOR='root'") {
		t.Errorf("transcript missing the sibling's env var:\n%s", got)
	}
}

// Label folding: punctuation and spaces fold to a single underscore, a label
// that folds to nothing (pure punctuation) is skipped rather than emitting a
// broken variable name, and so is one that would start with a digit.
func TestFillPaneFoldsLabelsIntoEnvNamesAndSkipsIllegalOnes(t *testing.T) {
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{Label: "Worker #2!", Agent: "claude"},
		{Split: "down", Label: "!!!", Agent: "codex"},
		{Split: "down", Label: "2fast", Agent: "codex"},
		{Split: "down", Command: "./discover.py"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w2H", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	got := l.transcript()
	// "Worker #2!" is pane 0, which reuses the Space's own root pane.
	if !strings.Contains(got, "HERDR_PANE_WORKER_2='root'") {
		t.Errorf("transcript missing the folded label's env var:\n%s", got)
	}
	if strings.Contains(got, "HERDR_PANE_2FAST") || strings.Contains(got, "=''") {
		t.Errorf("an illegal folded name leaked into the command:\n%s", got)
	}
	// Only one HERDR_PANE_ var beyond the reserved HERDR_PANE_ID: the other
	// two labels ("!!!"  and "2fast") fold to nothing usable.
	if n := strings.Count(got, "HERDR_PANE_"); n != 2 {
		t.Errorf("HERDR_PANE_ count = %d, want 2 (the reserved id and the one legal label):\n%s", n, got)
	}
}

// Two labels that fold to the same name resolve deterministically: the first
// one in plan order wins, and the second is silently dropped rather than
// left to whichever order a map iteration produced.
func TestFillPaneResolvesAFoldedLabelCollisionByPlanOrder(t *testing.T) {
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{Label: "meta orchestrator", Agent: "claude"},
		{Split: "down", Label: "META-ORCHESTRATOR", Agent: "codex"},
		{Split: "down", Command: "./discover.py"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w2H", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	got := l.transcript()
	// "meta orchestrator" is pane 0 (the Space's own root pane, reused); the
	// colliding "META-ORCHESTRATOR" is the split after it, "p1" -- and must
	// lose.
	if !strings.Contains(got, "HERDR_PANE_META_ORCHESTRATOR='root'") {
		t.Errorf("transcript does not show the first-in-plan-order pane winning:\n%s", got)
	}
	if strings.Contains(got, "HERDR_PANE_META_ORCHESTRATOR='p1'") {
		t.Errorf("the later label overwrote the earlier one instead of losing the collision:\n%s", got)
	}
}

// A label folding to "ID" would collide with the step's own reserved
// HERDR_PANE_ID -- that reserved key must win, never be displaced by a
// labelled sibling that happens to fold onto it.
func TestFillPaneReservesHerdrPaneIDAgainstACollidingLabel(t *testing.T) {
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{Label: "id", Agent: "claude"},
		{Split: "down", Command: "./discover.py"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w2H", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	got := l.transcript()
	// The label "id" is pane 0, "root"; the command pane -- pane 1, split
	// down -- is "p1". HERDR_PANE_ID must stay the command's own id, "p1",
	// not be displaced by the sibling whose label folds onto the same name.
	if !strings.Contains(got, "HERDR_PANE_ID='p1'") {
		t.Errorf("the command pane's own id was not the winner of HERDR_PANE_ID:\n%s", got)
	}
	if strings.Contains(got, "HERDR_PANE_ID='root'") {
		t.Errorf("the colliding label displaced the reserved HERDR_PANE_ID:\n%s", got)
	}
}

// An agent pane's prompt is never prefixed: the issue explicitly has no use
// for these vars on an agent (it can run `herdr pane current` itself), and a
// prompt is not a shell command to prefix an assignment onto.
func TestFillPaneDoesNotPrefixAnAgentPrompt(t *testing.T) {
	s := &fakeSession{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{
		{Agent: "claude", Prompt: "review this"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: s, Layout: &fakeLayout{}}, &config.Settings{}, target.Target{}, def, rootPane(), "w2H", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}
	if len(s.sendTextCalls) != 1 || s.sendTextCalls[0].text != "review this" {
		t.Errorf("send_text calls = %+v, want the prompt untouched", s.sendTextCalls)
	}
}

// A pane whose creation failed contributes no HERDR_PANE_* variable to
// anything else in the plan: there is no pane id to hand out. The failure is
// made to land on exactly one split (via splitErrAtCall), so a second split
// after it can still succeed and be checked for what it did not receive.
func TestFillPaneOmitsAFailedPaneFromSiblingEnv(t *testing.T) {
	l := &fakeLayout{splitErrAtCall: 1}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{Label: "orchestrator", Agent: "claude"},
		{Split: "down", Label: "worker", Agent: "codex"},
		{Split: "down", Command: "./discover.py"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w2H", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(l.transcript(), "HERDR_PANE_WORKER") {
		t.Errorf("a var was emitted for a pane that never got built:\n%s", l.transcript())
	}
	// The command pane itself still built and ran (off the pane before the
	// failed split, since a failed split leaves prev untouched), which is
	// what makes the absence above meaningful rather than the whole tab
	// having been abandoned.
	if !strings.Contains(l.transcript(), "./discover.py") {
		t.Fatalf("the command pane after the failed split never ran:\n%s", l.transcript())
	}
}

// The typed prefix's keys are always sorted, so a run is reproducible and
// RunCommand's echo probe -- which matches a short leading fragment of
// whatever was actually typed -- sees the same fragment every time.
func TestFillPaneEmitsHerdrKeysInSortedOrder(t *testing.T) {
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{Label: "meta-orchestrator", Agent: "claude"},
		{Split: "down", Command: "./discover.py"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w2H", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	got := l.transcript()
	// Pane 0 ("meta-orchestrator") reuses the root pane; the command split
	// after it is "p1". Alphabetically: HERDR_PANE_ID, then
	// HERDR_PANE_META_ORCHESTRATOR ('I' < 'M'), then HERDR_TAB_ID ('P' <
	// 'T'), then HERDR_WORKSPACE_ID ('T' < 'W').
	want := "HERDR_PANE_ID='p1' HERDR_PANE_META_ORCHESTRATOR='root' HERDR_TAB_ID='root-tab' HERDR_WORKSPACE_ID='w2H' ./discover.py"
	if !strings.Contains(got, want) {
		t.Errorf("keys were not emitted sorted:\n%s\nwant substring:\n%s", got, want)
	}
}

// Values are single-quoted, the one quoting form sh, bash, zsh and fish all
// agree on -- an id containing a character that would otherwise need
// escaping still comes through as one shell word.
func TestFillPaneQuotesHerdrValues(t *testing.T) {
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{Command: "./discover.py"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: &fakeSession{}, Layout: l}, &config.Settings{}, target.Target{}, def, rootPane(), "w2H", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(l.transcript(), "HERDR_WORKSPACE_ID='w2H'") {
		t.Errorf("value was not single-quoted:\n%s", l.transcript())
	}
}

// A pane's model and args are the agent's command line, so they have to reach
// agent.start -- the whole point of the field is a guardrail the prompt cannot
// provide.
func TestApplySetupPassesTheAgentCommandLine(t *testing.T) {
	s := &fakeSession{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{
		{Agent: "claude", Model: "opus", Args: []string{"--permission-mode", "plan"}},
		{Split: "down", Agent: "codex"},
	}}}}

	if _, _, _, err := applySetup(Deps{Session: s, Layout: &fakeLayout{}}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", "/repo", nil); err != nil {
		t.Fatal(err)
	}
	if len(s.startAgentCalls) != 2 {
		t.Fatalf("start calls = %+v", s.startAgentCalls)
	}
	if got := strings.Join(s.startAgentCalls[0].args, " "); got != "--model opus --permission-mode plan" {
		t.Errorf("args = %q, want the model flag then the pane's own", got)
	}
	// And a pane that asked for neither is started exactly as before: an empty
	// args is not the same as an absent one for some agent kinds.
	if s.startAgentCalls[1].args != nil {
		t.Errorf("args = %v, want nil for a pane that named none", s.startAgentCalls[1].args)
	}
}

// worktreePathFor mirrors the default naming scheme (internal/config's
// ResolveTabWorktreePath with no [worktrees].path configured) so a test can
// say ahead of time what path applySetup's pre-pass is going to compute,
// without reaching into internal/config itself.
func worktreePathFor(repoRoot, ref string) string {
	return repoRoot + "/.herdr-worktrees/" + ref
}

// The missing case: nothing is at the deterministic path yet, so the
// pre-pass creates it, detached, before the tab that needs it is built.
func TestApplySetupCreatesAMissingWorktreeBeforeOpeningItsTab(t *testing.T) {
	repoRoot := t.TempDir()
	fetch := &fakeFetcher{}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{
		{Name: "shell"},
		{Name: "baseline", Worktree: &setup.WorktreeSpec{Ref: "main"}, Panes: []setup.Pane{{}}},
	}}

	_, _, problems, err := applySetup(Deps{Session: &fakeSession{}, Layout: l, Git: fetch}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", repoRoot, repoRoot, nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none", problems)
	}

	want := worktreePathFor(repoRoot, "main")
	if len(fetch.worktreeAddCalls) != 1 || fetch.worktreeAddCalls[0] != want+" main" {
		t.Errorf("WorktreeAdd calls = %v, want one detached add at %q", fetch.worktreeAddCalls, want)
	}
	if len(fetch.worktreeAddBranchCalls) != 0 {
		t.Errorf("WorktreeAddBranch calls = %v, want none -- detach is the default", fetch.worktreeAddBranchCalls)
	}
	if !strings.Contains(l.transcript(), "tab baseline cwd="+want) {
		t.Errorf("the tab was not created at the worktree path:\n%s", l.transcript())
	}
}

// The reuse case: a worktree already sits at the deterministic path, and its
// HEAD already matches the ref -- a no-op, not a fresh checkout.
func TestApplySetupReusesAnExistingWorktreeWhoseHeadMatches(t *testing.T) {
	repoRoot := t.TempDir()
	path := worktreePathFor(repoRoot, "main")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	// The default fakeFetcher.ResolveRef answers "sha-<ref>" for any ref;
	// matching that here is what makes this the reuse case rather than the
	// collision case below.
	fetch := &fakeFetcher{headCommitSHA: "sha-main"}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{
		{Name: "baseline", Worktree: &setup.WorktreeSpec{Ref: "main"}, Panes: []setup.Pane{{}}},
	}}

	_, _, problems, err := applySetup(Deps{Session: &fakeSession{}, Layout: l, Git: fetch}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", repoRoot, repoRoot, nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none -- a matching HEAD is a silent reuse", problems)
	}
	if len(fetch.worktreeAddCalls) != 0 || len(fetch.worktreeAddBranchCalls) != 0 {
		t.Errorf("a worktree was (re)created instead of reused: add=%v addBranch=%v", fetch.worktreeAddCalls, fetch.worktreeAddBranchCalls)
	}
}

// The collision case, confirmed by the repo owner during #12's design: a
// worktree already at the path, checked out at a *different* commit than the
// ref asks for, is reported and that tab is skipped -- never force-removed
// and recreated.
func TestApplySetupReportsAndSkipsAWorktreeCheckedOutAtADifferentCommit(t *testing.T) {
	repoRoot := t.TempDir()
	path := worktreePathFor(repoRoot, "main")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	fetch := &fakeFetcher{headCommitSHA: "some-other-commit"}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{
		{Name: "shell"},
		{Name: "baseline", Worktree: &setup.WorktreeSpec{Ref: "main"}, Panes: []setup.Pane{{}}},
	}}

	_, _, problems, err := applySetup(Deps{Session: &fakeSession{}, Layout: l, Git: fetch}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", repoRoot, repoRoot, nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want one naming the collision", problems)
	}
	for _, want := range []string{"baseline", path, "git worktree remove --force " + path} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("problem %q missing %q", problems[0], want)
		}
	}
	// Never a force-remove-and-recreate, ever: the interface applySetup's
	// pre-pass talks to (WorktreeGit) does not even have a Remove method, so
	// there is no call here to assert the absence of -- that guarantee is
	// structural, not behavioural.
	if len(fetch.worktreeAddCalls) != 0 || len(fetch.worktreeAddBranchCalls) != 0 {
		t.Errorf("the mismatched worktree was rebuilt instead of left alone: add=%v addBranch=%v", fetch.worktreeAddCalls, fetch.worktreeAddBranchCalls)
	}
	if strings.Contains(l.transcript(), "baseline") {
		t.Errorf("the tab whose worktree collided was still built:\n%s", l.transcript())
	}
}

// A failed worktree abandons only its own tab -- the rest of the layout,
// including a tab that comes after it in the file, still gets built. This is
// the ordinary (not-first) tab case: baseline is second, so it goes through
// buildPanes' NewTab/CreateTab path rather than reusing the Space's own root
// pane -- see the FirstTab-specific test below for that one.
func TestApplySetupFailedWorktreeAbandonsOnlyItsOwnTab(t *testing.T) {
	repoRoot := t.TempDir()
	fetch := &fakeFetcher{fetchRefErr: errors.New("network is down")}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{
		{Name: "shell", Command: "echo hi"},
		{Name: "baseline", Worktree: &setup.WorktreeSpec{Ref: "main"}, Panes: []setup.Pane{{}}},
	}}

	_, _, problems, err := applySetup(Deps{Session: &fakeSession{}, Layout: l, Git: fetch}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", repoRoot, repoRoot, nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "baseline") {
		t.Errorf("problems = %v, want one naming the failed tab", problems)
	}
	if !strings.Contains(l.transcript(), "echo hi") {
		t.Errorf("the tab after the failed worktree never built:\n%s", l.transcript())
	}
	if strings.Contains(l.transcript(), "tab baseline") {
		t.Errorf("a tab was created for the failed worktree:\n%s", l.transcript())
	}
}

// A worktree: tab that is also the Space's own first tab has no "reuse the
// Space's own tab" fallback to lose when its worktree fails -- the root pane
// and tab already exist regardless. It still gets reported, and the rest of
// that one tab (its splits) is what gets abandoned, not the tab itself.
func TestApplySetupReportsAFailedWorktreeOnTheFirstTabWithoutLosingTheRootPane(t *testing.T) {
	repoRoot := t.TempDir()
	fetch := &fakeFetcher{fetchRefErr: errors.New("network is down")}
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{
		{Name: "baseline", Worktree: &setup.WorktreeSpec{Ref: "main"}, Panes: []setup.Pane{
			{},
			{Split: "down", Command: "echo skip-me"},
		}},
	}}

	_, panes, problems, err := applySetup(Deps{Session: &fakeSession{}, Layout: l, Git: fetch}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", repoRoot, repoRoot, nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "baseline") {
		t.Errorf("problems = %v, want one naming the failed tab", problems)
	}
	// The Space's own root pane is still what the first step used -- there
	// was never a "reuse the Space's own tab" fallback to lose.
	if panes[0] != "root" {
		t.Errorf("panes[0] = %q, want the Space's own root pane reused despite the failed worktree", panes[0])
	}
	if strings.Contains(l.transcript(), "skip-me") {
		t.Errorf("the split after the failed worktree still ran:\n%s", l.transcript())
	}
}

// detach: false checks ref out as a branch rather than leaving HEAD
// detached, through WorktreeAddBranch rather than WorktreeAdd.
func TestApplySetupDetachFalseChecksOutABranch(t *testing.T) {
	repoRoot := t.TempDir()
	fetch := &fakeFetcher{}
	no := false
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{
		{Name: "baseline", Worktree: &setup.WorktreeSpec{Ref: "fix-thing", Detach: &no}, Panes: []setup.Pane{{}}},
	}}

	_, _, problems, err := applySetup(Deps{Session: &fakeSession{}, Layout: &fakeLayout{}, Git: fetch}, &config.Settings{}, target.Target{}, def, rootPane(), "w1", repoRoot, repoRoot, nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none", problems)
	}
	if len(fetch.worktreeAddBranchCalls) != 1 {
		t.Fatalf("WorktreeAddBranch calls = %v, want one", fetch.worktreeAddBranchCalls)
	}
	if len(fetch.worktreeAddCalls) != 0 {
		t.Errorf("WorktreeAdd (detached) was called for a detach:false tab: %v", fetch.worktreeAddCalls)
	}
}

// PreviewSetup -- what --dry-run runs -- must never create a tab's worktree,
// the same promise it already makes for the whole-Space case. cfg here has
// no [worktrees].path configured, so the printed path is the default
// {repo_root}/.herdr-worktrees/{ref} scheme.
// [worktrees].path expands {host}, {owner} and {repo} from the target, and
// those are exactly the placeholders [repos].templates already uses -- so
// "~/wt/{host}/{owner}/{repo}/{ref}" is the obvious thing to configure here.
// A synthesised stand-in target instead of the real one would expand {host}
// and {owner} to nothing and quietly produce a path with empty segments,
// which is the bug this pins.
func TestWorktreePathUsesTheRealTargetsHostAndOwner(t *testing.T) {
	cfg := &config.Settings{WorktreePath: "/wt/{host}/{owner}/{repo}/{ref}"}
	tgt := target.Target{
		Kind: target.KindGitHubPR, Host: "github.com",
		Owner: "phin-tech", Repo: "herdr-phin-util", Number: 42,
	}

	fn := worktreePathFn(cfg, tgt, "/src/herdr-phin-util")
	if fn == nil {
		t.Fatal("want a path function for a target with a repo root")
	}
	got := fn("main")
	want := "/wt/github.com/phin-tech/herdr-phin-util/main"
	if got != want {
		t.Errorf("worktree path = %q, want %q", got, want)
	}
}

// A target that names no repository of its own -- a plain Space opened inside
// a checkout -- still has to fill {repo} with something, and the checkout's
// own directory name is the fallback every other path in the plugin uses.
func TestWorktreePathFallsBackToTheCheckoutNameForRepo(t *testing.T) {
	cfg := &config.Settings{WorktreePath: "/wt/{repo}/{ref}"}

	fn := worktreePathFn(cfg, target.Target{Kind: target.KindPlain}, "/src/roux-next-gen")
	if fn == nil {
		t.Fatal("want a path function for a target with a repo root")
	}
	if got, want := fn("v1.2.0"), "/wt/roux-next-gen/v1.2.0"; got != want {
		t.Errorf("worktree path = %q, want %q", got, want)
	}
}

func TestPreviewSetupDescribesATabWorktreeWithoutTouchingDiskOrGit(t *testing.T) {
	repoRoot := t.TempDir()
	fetch := &fakeFetcher{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{
		{Name: "baseline", Worktree: &setup.WorktreeSpec{Ref: "{{.Branch}}"}, Panes: []setup.Pane{{}}},
	}}
	cfg := &config.Settings{}

	plan, _, err := PreviewSetup(Deps{Cwd: repoRoot, Git: fetch}, cfg, "https://linear.app/phin/issue/ENG-9/do-a-thing", def)
	if err != nil {
		t.Fatalf("PreviewSetup: %v", err)
	}

	out := strings.Join(plan.Describe(), "\n")
	want := worktreePathFor(repoRoot, "eng-9-do-a-thing")
	for _, s := range []string{want, "eng-9-do-a-thing", "not created yet"} {
		if !strings.Contains(out, s) {
			t.Errorf("Describe() missing %q:\n%s", s, out)
		}
	}
	if fetch.calls != 0 || len(fetch.fetchRefCalls) != 0 || len(fetch.worktreeAddCalls) != 0 {
		t.Errorf("a preview touched git: %+v", fetch)
	}
	if _, err := os.Stat(want); err == nil {
		t.Errorf("a preview created %s on disk", want)
	}
}
