package open

import (
	"errors"
	"fmt"
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
	runErr       error
	promptErr    error
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

	plan, panes, problems, err := applySetup(s, l, cfg, reviewSetup(), rootPane(), "w1", "/repo", map[string]string{"Number": "42"})
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
	if _, _, _, err := applySetup(&fakeSession{}, l, &config.Settings{}, reviewSetup(), rootPane(), "w1", "/repo", nil); err != nil {
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

	if _, _, _, err := applySetup(s, l, &config.Settings{}, reviewSetup(), rootPane(), "w1", "/repo", map[string]string{"Number": "42"}); err != nil {
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

	if _, _, _, err := applySetup(s, l, &config.Settings{}, reviewSetup(), rootPane(), "w1", "/repo", nil); err != nil {
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

	if _, _, _, err := applySetup(s, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil); err != nil {
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

	_, panes, _, err := applySetup(&fakeSession{}, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
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

	if _, _, _, err := applySetup(&fakeSession{}, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil); err != nil {
		t.Fatal(err)
	}
	if last := l.calls[len(l.calls)-1]; last != "focus root" {
		t.Errorf("last call = %q, want focus on the first pane", last)
	}
}

func TestApplySetupRejectsAnUnknownAgentKind(t *testing.T) {
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "a", Panes: []setup.Pane{{Agent: "clod"}}}}}

	_, _, problems, err := applySetup(&fakeSession{}, &fakeLayout{}, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
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

	_, panes, problems, err := applySetup(&fakeSession{}, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
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

	_, _, problems, err := applySetup(s, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
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

	_, _, problems, err := applySetup(&fakeSession{}, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "second") {
		t.Errorf("problems = %v, want the one tab named once", problems)
	}

	got := l.transcript()
	if !strings.Contains(got, "run root one") {
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

	if _, _, _, err := applySetup(s, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil); err != nil {
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

	_, _, problems, err := applySetup(&fakeSession{}, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
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

	_, _, problems, err := applySetup(s, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
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

	_, _, problems, err := applySetup(s, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
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

	_, _, problems, err := applySetup(s, &fakeLayout{}, &config.Settings{}, def, rootPane(), "w1", "/repo", nil)
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

	if _, _, _, err := applySetup(s, &fakeLayout{}, &config.Settings{}, def, rootPane(), "w1", "/repo", nil); err != nil {
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

	if _, _, _, err := applySetup(&fakeSession{}, l, &config.Settings{}, def, rootPane(), "w1", "/repo", nil); err != nil {
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

// A pane's model and args are the agent's command line, so they have to reach
// agent.start -- the whole point of the field is a guardrail the prompt cannot
// provide.
func TestApplySetupPassesTheAgentCommandLine(t *testing.T) {
	s := &fakeSession{}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "review", Panes: []setup.Pane{
		{Agent: "claude", Model: "opus", Args: []string{"--permission-mode", "plan"}},
		{Split: "down", Agent: "codex"},
	}}}}

	if _, _, _, err := applySetup(s, &fakeLayout{}, &config.Settings{}, def, rootPane(), "w1", "/repo", nil); err != nil {
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
