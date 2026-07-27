package open

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/config"
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
	runErr       error
	promptErr    error

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
	if f.splitErr != nil {
		return herdr.Pane{}, f.splitErr
	}
	return f.pane(), nil
}

func (f *fakeLayout) RunCommand(paneID, command string) error {
	f.calls = append(f.calls, fmt.Sprintf("run %s %s", paneID, command))
	return f.runErr
}

func (f *fakeLayout) PromptAgent(paneID, text string) error {
	f.calls = append(f.calls, fmt.Sprintf("prompt %s %s", paneID, text))
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

	plan, panes, err := applySetup(s, l, cfg, reviewSetup(), rootPane(), "w1", "/repo", map[string]string{"Number": "42"})
	if err != nil {
		t.Fatalf("applySetup: %v", err)
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
		"run p2 roborev review-branch",
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
	if _, _, err := applySetup(&fakeSession{}, l, &config.Settings{}, reviewSetup(), rootPane(), "w1", "/repo", nil); err != nil {
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

	if _, _, err := applySetup(s, l, &config.Settings{}, reviewSetup(), rootPane(), "w1", "/repo", map[string]string{"Number": "42"}); err != nil {
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

	if _, _, err := applySetup(s, l, &config.Settings{}, reviewSetup(), rootPane(), "w1", "/repo", nil); err != nil {
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

	if _, _, err := applySetup(s, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil); err != nil {
		t.Fatalf("a wait timeout aborted the layout: %v", err)
	}
	if !strings.Contains(l.transcript(), "echo done") {
		t.Errorf("the layout stopped at the failed wait:\n%s", l.transcript())
	}
}

func TestApplySetupFocusesTheMarkedPane(t *testing.T) {
	l := &fakeLayout{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{
		{},
		{Split: "down", Focus: true},
	}}}}

	_, panes, err := applySetup(&fakeSession{}, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
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

	if _, _, err := applySetup(&fakeSession{}, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil); err != nil {
		t.Fatal(err)
	}
	if last := l.calls[len(l.calls)-1]; last != "focus root" {
		t.Errorf("last call = %q, want focus on the first pane", last)
	}
}

func TestApplySetupRejectsAnUnknownAgentKind(t *testing.T) {
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{{Agent: "clod"}}}}}

	_, _, err := applySetup(&fakeSession{}, &fakeLayout{}, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
	if err == nil || !strings.Contains(err.Error(), "clod") {
		t.Errorf("err = %v, want it to name the agent Herdr does not know", err)
	}
}

// A failure names where it stopped and leaves the rest standing, rather than
// tearing down a Space the user can already see.
func TestApplySetupFailureNamesTheTab(t *testing.T) {
	l := &fakeLayout{splitErr: errors.New("no room")}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{{}, {Split: "down"}}}}}

	_, panes, err := applySetup(&fakeSession{}, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "review") || !strings.Contains(err.Error(), "no room") {
		t.Errorf("err = %v, want the tab and the cause", err)
	}
	if panes[0] != "root" {
		t.Errorf("panes = %v, want what was built before the failure", panes)
	}
}

func TestApplySetupAgentNamesAreUniqueAndValid(t *testing.T) {
	s := &fakeSession{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{
		{Agent: "claude"},
		{Split: "down", Agent: "claude"},
		{Split: "down", Label: "Worker #2!", Agent: "claude"},
	}}}}

	if _, _, err := applySetup(s, &fakeLayout{}, &config.Settings{}, def, rootPane(), "w1", "/repo", nil); err != nil {
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

	if _, _, err := applySetup(&fakeSession{}, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil); err != nil {
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
