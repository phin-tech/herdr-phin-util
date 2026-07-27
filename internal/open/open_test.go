package open

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/gh"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// A test importing this package should not have its decisions swayed by
// whatever plugin action happens to be invoking the test binary.
func TestMain(m *testing.M) {
	os.Unsetenv("HERDR_PLUGIN_CONTEXT_JSON")
	// The retry backoff is real time.Sleep in production; tests swap it out
	// so a retry test finishes in milliseconds rather than seconds. The clock
	// goes with it, advanced by every skipped sleep: a bounded wait still has
	// to reach its deadline, it just gets there without anyone waiting.
	clock := time.Now()
	sleep = func(d time.Duration) { clock = clock.Add(d) }
	now = func() time.Time { return clock }
	os.Exit(m.Run())
}

type fakeSession struct {
	createWorktreeErr   error
	createWorktreeCalls []herdr.WorktreeRequest
	openWorktreeErr     error
	openWorktreeCalls   []herdr.WorktreeRequest

	createWorkspaceCalls []createWorkspaceCall

	pane        herdr.Pane
	workspaceID string

	startAgentCalls []startAgentCall
	startAgentErr   error
	// startAgentBusyUntilCall makes StartAgent fail with agent_pane_busy for
	// every call before this one (1-indexed), then succeed from then on.
	// Zero disables this and just returns startAgentErr immediately.
	startAgentBusyUntilCall int
	waitCalls               []string
	waitErr                 error
	waitOutputCalls         []waitOutputCall
	waitOutputErr           error
	sendTextCalls           []sendTextCall
	sendTextErr             error

	// launchedAfterCall makes AgentLaunched report "still launching" for every
	// call before this one (1-indexed), which is how the gap between a rendered
	// prompt and an agent that will accept one is expressed. Zero means the
	// agent is launched from the first look, which is what most tests want.
	launchedAfterCall int
	launchedCalls     int
	// launchNever holds the agent at "still launching" forever, standing in for
	// one stuck on its own first-run UI.
	launchNever bool
	launchedErr error
}

type createWorkspaceCall struct {
	cwd, label string
	focus      bool
}
type startAgentCall struct {
	paneID, name, kind string
	args               []string
}
type waitOutputCall struct {
	paneID, value string
	timeoutMs     int
}
type sendTextCall struct{ paneID, text string }

func (f *fakeSession) CreateWorktree(req herdr.WorktreeRequest) (herdr.Pane, string, error) {
	f.createWorktreeCalls = append(f.createWorktreeCalls, req)
	if f.createWorktreeErr != nil {
		return herdr.Pane{}, "", f.createWorktreeErr
	}
	return f.pane, f.workspaceID, nil
}

func (f *fakeSession) OpenWorktree(req herdr.WorktreeRequest) (herdr.Pane, string, error) {
	f.openWorktreeCalls = append(f.openWorktreeCalls, req)
	if f.openWorktreeErr != nil {
		return herdr.Pane{}, "", f.openWorktreeErr
	}
	return f.pane, f.workspaceID, nil
}

func (f *fakeSession) CreateWorkspace(cwd, label string, focus bool) (herdr.Pane, string, error) {
	f.createWorkspaceCalls = append(f.createWorkspaceCalls, createWorkspaceCall{cwd, label, focus})
	return f.pane, f.workspaceID, nil
}

func (f *fakeSession) StartAgent(paneID, name, kind string, args []string) error {
	f.startAgentCalls = append(f.startAgentCalls, startAgentCall{paneID, name, kind, args})
	if f.startAgentBusyUntilCall > 0 && len(f.startAgentCalls) < f.startAgentBusyUntilCall {
		return &herdr.APIError{Method: "agent.start", Code: "agent_pane_busy", Message: "agent target pane is not an available shell"}
	}
	return f.startAgentErr
}

func (f *fakeSession) WaitAgentIdle(paneID string) error {
	f.waitCalls = append(f.waitCalls, paneID)
	return f.waitErr
}

func (f *fakeSession) WaitPaneOutput(paneID, value string, timeoutMs int) error {
	f.waitOutputCalls = append(f.waitOutputCalls, waitOutputCall{paneID, value, timeoutMs})
	return f.waitOutputErr
}

func (f *fakeSession) AgentLaunched(paneID string) (bool, error) {
	f.launchedCalls++
	if f.launchedErr != nil {
		return false, f.launchedErr
	}
	if f.launchNever {
		return false, nil
	}
	return f.launchedCalls >= f.launchedAfterCall, nil
}

func (f *fakeSession) SendText(paneID, text string) error {
	f.sendTextCalls = append(f.sendTextCalls, sendTextCall{paneID, text})
	return f.sendTextErr
}

type fakePRLookup struct {
	info  gh.PRInfo
	err   error
	calls int

	issue      gh.IssueInfo
	issueErr   error
	issueCalls int
}

func (f *fakePRLookup) LookupIssue(owner, repo string, number int) (gh.IssueInfo, error) {
	f.issueCalls++
	if f.issueErr != nil {
		return gh.IssueInfo{}, f.issueErr
	}
	return f.issue, nil
}

func (f *fakePRLookup) LookupPR(owner, repo string, number int) (gh.PRInfo, error) {
	f.calls++
	if f.err != nil {
		return gh.PRInfo{}, f.err
	}
	return f.info, nil
}

type fakeFetcher struct {
	err    error
	calls  int
	branch string
	path   string
}

func (f *fakeFetcher) FetchBranch(repoPath, branch string) error {
	f.calls++
	f.path, f.branch = repoPath, branch
	return f.err
}

// existingRepo creates a directory so config.ResolveRepo can find it, and
// returns Settings pointed only at that template.
func existingRepo(t *testing.T) (dir string, cfg *config.Settings) {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "src", "github.com", "phin-tech", "herdr-phin-util")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	return repo, &config.Settings{
		RepoTemplates: []string{filepath.Join(home, "src", "{host}", "{owner}", "{repo}")},
		Agent:         config.AgentSettings{Enabled: true, Kind: "claude"},
		Prompts: config.PromptSettings{
			GithubPR: "PR #{{.Number}} {{.Title}}",
			Linear:   "Issue {{.Issue}} {{.Title}}",
			Plain:    "{{.Text}}",
		},
	}
}

func TestRunGitHubPRCreatesWorktreeAndPromptsAgent(t *testing.T) {
	repo, cfg := existingRepo(t)
	sess := &fakeSession{pane: herdr.Pane{PaneID: "wZ:p1"}, workspaceID: "wZ"}
	prs := &fakePRLookup{info: gh.PRInfo{Branch: "fix-thing", Title: "Fix the thing"}}
	fetch := &fakeFetcher{}

	out, err := Run(Deps{Session: sess, PRs: prs, Git: fetch}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/42", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if prs.calls != 1 {
		t.Fatalf("PR lookup called %d times, want 1", prs.calls)
	}
	if fetch.calls != 1 || fetch.branch != "fix-thing" || fetch.path != repo {
		t.Errorf("fetch = %+v, want branch fix-thing in %s", fetch, repo)
	}

	if len(sess.createWorktreeCalls) != 1 {
		t.Fatalf("CreateWorktree called %d times, want 1", len(sess.createWorktreeCalls))
	}
	req := sess.createWorktreeCalls[0]
	if req.Cwd != repo || req.Branch != "fix-thing" || req.Base != "origin/fix-thing" {
		t.Errorf("CreateWorktree req = %+v", req)
	}
	if req.Label != "herdr-phin-util#42" {
		t.Errorf("Label = %q, want herdr-phin-util#42", req.Label)
	}
	if req.Path != "" {
		t.Errorf("Path = %q, want empty (no worktree template configured)", req.Path)
	}

	if len(sess.startAgentCalls) != 1 || sess.startAgentCalls[0].kind != "claude" {
		t.Fatalf("StartAgent calls = %+v", sess.startAgentCalls)
	}
	if len(sess.waitCalls) != 1 || sess.waitCalls[0] != "wZ:p1" {
		t.Fatalf("WaitAgentIdle calls = %v", sess.waitCalls)
	}
	if len(sess.sendTextCalls) != 1 {
		t.Fatalf("SendText calls = %+v", sess.sendTextCalls)
	}
	if want := "PR #42 Fix the thing"; sess.sendTextCalls[0].text != want {
		t.Errorf("prompt = %q, want %q", sess.sendTextCalls[0].text, want)
	}

	if out.Branch != "fix-thing" || out.WorkspaceID != "wZ" || out.PaneID != "wZ:p1" {
		t.Errorf("Outcome = %+v", out)
	}
	if !out.AgentStarted {
		t.Error("Outcome.AgentStarted should be true")
	}
}

// Resolving the repo happens before any network call: there is no point
// asking GitHub about a PR when the checkout is not even findable locally.
func TestRunGitHubPRMissingRepoStopsBeforeLookup(t *testing.T) {
	cfg := &config.Settings{RepoTemplates: []string{filepath.Join(t.TempDir(), "{host}", "{owner}", "{repo}")}}
	prs := &fakePRLookup{}
	sess := &fakeSession{}

	_, err := Run(Deps{Session: sess, PRs: prs, Git: &fakeFetcher{}}, cfg,
		"https://github.com/phin-tech/nope/pull/1", Options{})
	if err == nil {
		t.Fatal("want an error when the repo cannot be resolved")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error %q should mention the repo that could not be found", err)
	}
	if prs.calls != 0 {
		t.Errorf("PR lookup called %d times, want 0", prs.calls)
	}
	if len(sess.createWorktreeCalls) != 0 {
		t.Error("should not attempt to create a worktree without a resolved repo")
	}
}

func TestRunGitHubPRPropagatesLookupError(t *testing.T) {
	_, cfg := existingRepo(t)
	prs := &fakePRLookup{err: errors.New("gh: not found")}
	_, err := Run(Deps{Session: &fakeSession{}, PRs: prs, Git: &fakeFetcher{}}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/1", Options{})
	if err == nil {
		t.Fatal("want the gh lookup error to propagate")
	}
}

func TestRunGitHubPRPropagatesFetchError(t *testing.T) {
	_, cfg := existingRepo(t)
	prs := &fakePRLookup{info: gh.PRInfo{Branch: "b", Title: "t"}}
	fetch := &fakeFetcher{err: errors.New("no such remote branch")}
	_, err := Run(Deps{Session: &fakeSession{}, PRs: prs, Git: fetch}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/1", Options{})
	if err == nil {
		t.Fatal("want the fetch error to propagate")
	}
}

// worktree.open is for a branch that is already checked out somewhere;
// CreateWorktree failing is the signal to try that instead of giving up.
func TestRunGitHubPRFallsBackToOpenWorktree(t *testing.T) {
	_, cfg := existingRepo(t)
	cfg.Agent.Enabled = false
	sess := &fakeSession{
		createWorktreeErr: errors.New("branch already checked out"),
		pane:              herdr.Pane{PaneID: "wQ:p1"},
		workspaceID:       "wQ",
	}
	prs := &fakePRLookup{info: gh.PRInfo{Branch: "fix-thing", Title: "Fix the thing"}}

	out, err := Run(Deps{Session: sess, PRs: prs, Git: &fakeFetcher{}}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/42", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sess.openWorktreeCalls) != 1 {
		t.Fatalf("OpenWorktree called %d times, want 1", len(sess.openWorktreeCalls))
	}
	if sess.openWorktreeCalls[0].Branch != "fix-thing" {
		t.Errorf("OpenWorktree branch = %q", sess.openWorktreeCalls[0].Branch)
	}
	if out.WorkspaceID != "wQ" {
		t.Errorf("Outcome.WorkspaceID = %q, want wQ", out.WorkspaceID)
	}
}

func TestRunGitHubPRBothWorktreeCallsFail(t *testing.T) {
	_, cfg := existingRepo(t)
	sess := &fakeSession{
		createWorktreeErr: errors.New("nope"),
		openWorktreeErr:   errors.New("still nope"),
	}
	prs := &fakePRLookup{info: gh.PRInfo{Branch: "b", Title: "t"}}

	_, err := Run(Deps{Session: sess, PRs: prs, Git: &fakeFetcher{}}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/1", Options{})
	if err == nil {
		t.Fatal("want an error when both create and open fail")
	}
}

// A Linear URL carries no repository, so the worktree is made wherever the
// caller already is; there is no gh lookup and no fetch involved at all.
func TestRunLinearUsesCwdAndDerivedBranchNoNetwork(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{
		Agent: config.AgentSettings{Enabled: false},
		Prompts: config.PromptSettings{
			Linear: "Issue {{.Issue}}",
		},
	}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "wL:p1"}, workspaceID: "wL"}
	prs := &fakePRLookup{}
	fetch := &fakeFetcher{}

	out, err := Run(Deps{Session: sess, PRs: prs, Git: fetch, Cwd: cwd}, cfg,
		"https://linear.app/phin/issue/ENG-123/fix-the-flaky-test", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prs.calls != 0 || fetch.calls != 0 {
		t.Errorf("Linear must not touch gh or git: prs=%d fetch=%d", prs.calls, fetch.calls)
	}
	if len(sess.createWorktreeCalls) != 1 {
		t.Fatalf("CreateWorktree calls = %d, want 1", len(sess.createWorktreeCalls))
	}
	req := sess.createWorktreeCalls[0]
	if req.Cwd != cwd {
		t.Errorf("Cwd = %q, want %q", req.Cwd, cwd)
	}
	if req.Branch != "eng-123-fix-the-flaky-test" {
		t.Errorf("Branch = %q", req.Branch)
	}
	if req.Base != "" {
		t.Errorf("Base = %q, want empty for a brand new branch", req.Base)
	}
	if out.Branch != "eng-123-fix-the-flaky-test" {
		t.Errorf("Outcome.Branch = %q", out.Branch)
	}
}

func TestRunLinearWithoutCwdErrors(t *testing.T) {
	cfg := &config.Settings{}
	_, err := Run(Deps{Session: &fakeSession{}, PRs: &fakePRLookup{}, Git: &fakeFetcher{}}, cfg,
		"https://linear.app/phin/issue/ENG-1", Options{})
	if err == nil {
		t.Fatal("want an error when there is no working directory to build a worktree in")
	}
}

func TestRunPlainCreatesSpaceOnlyNoWorktree(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: false}}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "wP:p1"}, workspaceID: "wP"}

	out, err := Run(Deps{Session: sess, PRs: &fakePRLookup{}, Git: &fakeFetcher{}, Cwd: cwd}, cfg,
		"scratch notes", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sess.createWorktreeCalls) != 0 {
		t.Error("a plain target must not create a worktree")
	}
	if len(sess.createWorkspaceCalls) != 1 {
		t.Fatalf("CreateWorkspace calls = %d, want 1", len(sess.createWorkspaceCalls))
	}
	call := sess.createWorkspaceCalls[0]
	if call.cwd != cwd || call.label != "scratch notes" {
		t.Errorf("CreateWorkspace call = %+v", call)
	}
	if out.PaneID != "wP:p1" || out.WorkspaceID != "wP" {
		t.Errorf("Outcome = %+v", out)
	}
}

// --- agent toggle ---

// A pane just spawned by worktree/workspace create is not "available" the
// instant its process exists -- the shell needs a moment to settle -- so
// agent.start can fail with agent_pane_busy on the first try. That is
// transient, not a real failure, and must be retried rather than surfaced.
func TestRunRetriesStartAgentThroughPaneBusyWindow(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "codex"}, Prompts: config.PromptSettings{Plain: "hi"}}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1", startAgentBusyUntilCall: 3}

	_, err := Run(Deps{Session: sess, PRs: &fakePRLookup{}, Git: &fakeFetcher{}, Cwd: cwd}, cfg, "notes", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sess.startAgentCalls) != 3 {
		t.Errorf("StartAgent calls = %d, want 3 (two busy, one that succeeds)", len(sess.startAgentCalls))
	}
	if len(sess.sendTextCalls) != 1 {
		t.Error("should still reach the prompt step once the pane settles")
	}
}

// A kind of error other than the pane's transient busy window is a real
// failure and must not be masked by retrying it into oblivion.
func TestRunDoesNotRetryOtherStartAgentErrors(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "codex"}}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1", startAgentErr: errors.New("no such agent kind")}

	_, err := Run(Deps{Session: sess, PRs: &fakePRLookup{}, Git: &fakeFetcher{}, Cwd: cwd}, cfg, "notes", Options{})
	if err == nil {
		t.Fatal("want the non-retryable error to propagate")
	}
	if len(sess.startAgentCalls) != 1 {
		t.Errorf("StartAgent calls = %d, want 1 (no retry for a non-busy error)", len(sess.startAgentCalls))
	}
}

// If the pane never settles, this must give up rather than retry forever --
// an action that hangs indefinitely is worse than one that reports failure.
func TestRunGivesUpAfterMaxStartAgentRetries(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "codex"}}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1", startAgentBusyUntilCall: startAgentAttempts + 100}

	_, err := Run(Deps{Session: sess, PRs: &fakePRLookup{}, Git: &fakeFetcher{}, Cwd: cwd}, cfg, "notes", Options{})
	if err == nil {
		t.Fatal("want an error once retries are exhausted")
	}
	if len(sess.startAgentCalls) != startAgentAttempts {
		t.Errorf("StartAgent calls = %d, want %d", len(sess.startAgentCalls), startAgentAttempts)
	}
}

// agent_status can report idle during a startup lull before the agent has
// actually drawn its prompt, so a kind with a verified on-screen marker gets
// an extra, concrete check before anything is typed.
func TestRunAgentWithKnownMarkerWaitsForItAfterIdle(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}, Prompts: config.PromptSettings{Plain: "hi"}}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}

	_, err := Run(Deps{Session: sess, PRs: &fakePRLookup{}, Git: &fakeFetcher{}, Cwd: cwd}, cfg, "notes", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sess.waitCalls) != 1 {
		t.Fatalf("WaitAgentIdle calls = %d, want 1", len(sess.waitCalls))
	}
	if len(sess.waitOutputCalls) != 1 {
		t.Fatalf("WaitPaneOutput calls = %d, want 1", len(sess.waitOutputCalls))
	}
	if got := sess.waitOutputCalls[0]; got.paneID != "p1" || got.value != readyMarkers["claude"] {
		t.Errorf("WaitPaneOutput call = %+v", got)
	}
}

// A kind with no verified marker must not wait for one -- guessing text that
// never appears would hang the action instead of merely mistiming it.
func TestRunAgentWithUnknownKindSkipsMarkerWait(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "codex"}, Prompts: config.PromptSettings{Plain: "hi"}}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}

	_, err := Run(Deps{Session: sess, PRs: &fakePRLookup{}, Git: &fakeFetcher{}, Cwd: cwd}, cfg, "notes", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sess.waitOutputCalls) != 0 {
		t.Errorf("WaitPaneOutput calls = %d, want 0 for a kind with no known marker", len(sess.waitOutputCalls))
	}
}

func TestRunPropagatesWaitPaneOutputError(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}, Prompts: config.PromptSettings{Plain: "hi"}}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1", waitOutputErr: errors.New("timed out")}

	_, err := Run(Deps{Session: sess, PRs: &fakePRLookup{}, Git: &fakeFetcher{}, Cwd: cwd}, cfg, "notes", Options{})
	if err == nil {
		t.Fatal("want an error when the ready marker never appears")
	}
	if len(sess.sendTextCalls) != 0 {
		t.Error("must not type the prompt when the ready marker never appeared")
	}
}

func TestRunAgentDisabledByConfigSkipsAgent(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: false, Kind: "claude"}}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}

	out, err := Run(Deps{Session: sess, PRs: &fakePRLookup{}, Git: &fakeFetcher{}, Cwd: cwd}, cfg, "notes", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sess.startAgentCalls) != 0 || len(sess.sendTextCalls) != 0 {
		t.Error("agent should not be started when disabled")
	}
	if out.AgentStarted {
		t.Error("Outcome.AgentStarted should be false")
	}
	if out.PromptSent != "" {
		t.Error("Outcome.PromptSent should be empty when the agent did not run")
	}
}

func TestRunAgentOptionOverridesConfigOn(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: false, Kind: "claude"}, Prompts: config.PromptSettings{Plain: "{{.Text}}"}}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}
	on := true

	_, err := Run(Deps{Session: sess, PRs: &fakePRLookup{}, Git: &fakeFetcher{}, Cwd: cwd}, cfg, "notes", Options{Agent: &on})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sess.startAgentCalls) != 1 {
		t.Error("Options.Agent=true should start the agent even when config disables it")
	}
}

func TestRunAgentOptionOverridesConfigOff(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}
	off := false

	_, err := Run(Deps{Session: sess, PRs: &fakePRLookup{}, Git: &fakeFetcher{}, Cwd: cwd}, cfg, "notes", Options{Agent: &off})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sess.startAgentCalls) != 0 {
		t.Error("Options.Agent=false should skip the agent even when config enables it")
	}
}

// The prompt option is the popup's editable box: whatever text is in it wins
// outright over the template, or editing it would be pointless.
func TestRunPromptOptionOverridesTemplate(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}, Prompts: config.PromptSettings{Plain: "{{.Text}}"}}
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}

	out, err := Run(Deps{Session: sess, PRs: &fakePRLookup{}, Git: &fakeFetcher{}, Cwd: cwd}, cfg, "notes",
		Options{Prompt: "custom prompt text"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sess.sendTextCalls) != 1 || sess.sendTextCalls[0].text != "custom prompt text" {
		t.Errorf("SendText calls = %+v, want the override text", sess.sendTextCalls)
	}
	if out.PromptSent != "custom prompt text" {
		t.Errorf("Outcome.PromptSent = %q", out.PromptSent)
	}
}

// --- worktree path template ---

func TestRunWorktreePathOverrideAppliesToCreateWorktree(t *testing.T) {
	repo, cfg := existingRepo(t)
	cfg.WorktreePath = "{repo_root}/.worktrees/{branch}"
	cfg.Agent.Enabled = false
	sess := &fakeSession{pane: herdr.Pane{PaneID: "p1"}, workspaceID: "w1"}
	prs := &fakePRLookup{info: gh.PRInfo{Branch: "fix-thing", Title: "t"}}

	_, err := Run(Deps{Session: sess, PRs: prs, Git: &fakeFetcher{}}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := filepath.Join(repo, ".worktrees", "fix-thing")
	if got := sess.createWorktreeCalls[0].Path; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

// --- prompt rendering ---

func TestRenderPromptMissingFieldDoesNotError(t *testing.T) {
	got, err := renderPrompt("{{.NotAField}} stays empty", map[string]string{"Text": "x"})
	if err != nil {
		t.Fatalf("renderPrompt: %v", err)
	}
	if got != " stays empty" {
		t.Errorf("got %q", got)
	}
}

func TestRenderPromptSubstitutesKnownFields(t *testing.T) {
	got, err := renderPrompt("PR #{{.Number}} — {{.Title}}\n{{.URL}}", map[string]string{
		"Number": "7", "Title": "Fix it", "URL": "https://example.com/7",
	})
	if err != nil {
		t.Fatalf("renderPrompt: %v", err)
	}
	if want := "PR #7 — Fix it\nhttps://example.com/7"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderPromptInvalidTemplateErrors(t *testing.T) {
	if _, err := renderPrompt("{{.Broken", nil); err == nil {
		t.Fatal("want an error for unparsable template text")
	}
}

// --- PreviewPrompt (used by the popup for a live, pre-submit preview) ---

func TestPreviewPromptLinearFillsBranchFromURLAlone(t *testing.T) {
	cfg := &config.Settings{Prompts: config.PromptSettings{Linear: "Work {{.Issue}} on {{.Branch}}"}}
	tgt := target.Parse("https://linear.app/phin/issue/ENG-123/fix-the-flaky-test")
	got, err := PreviewPrompt(cfg, tgt)
	if err != nil {
		t.Fatalf("PreviewPrompt: %v", err)
	}
	if want := "Work ENG-123 on eng-123-fix-the-flaky-test"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A PR's branch and title are not knowable without gh, so the preview must
// not block on it -- it renders those fields empty rather than erroring.
func TestPreviewPromptGitHubPRBlanksUnknownFields(t *testing.T) {
	cfg := &config.Settings{Prompts: config.PromptSettings{GithubPR: "PR #{{.Number}} branch={{.Branch}} title={{.Title}}"}}
	tgt := target.Parse("https://github.com/phin-tech/herdr-phin-util/pull/42")
	got, err := PreviewPrompt(cfg, tgt)
	if err != nil {
		t.Fatalf("PreviewPrompt: %v", err)
	}
	if want := "PR #42 branch= title="; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The paths that were tried are the whole diagnostic when no template matches,
// but they must be said once. Both ResolveRepo and its caller used to append
// them, producing "(tried [...]) (tried [...])" -- noise in exactly the error a
// new install is most likely to hit.
func TestRunGitHubPRMissingRepoSaysTriedOnce(t *testing.T) {
	cfg := &config.Settings{RepoTemplates: []string{"/definitely/nowhere/{owner}/{repo}"}}
	_, err := Run(Deps{Session: &fakeSession{}, PRs: &fakePRLookup{}, Git: &fakeFetcher{}}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/1", Options{})
	if err == nil {
		t.Fatal("want an error when no template matches")
	}
	if got := strings.Count(err.Error(), "tried"); got != 1 {
		t.Errorf("error mentions \"tried\" %d times, want 1: %v", got, err)
	}
}

// A failure in the agent step comes after the Space already exists, so the
// Outcome has to carry it: without these ids the caller cannot tell a Space
// that was never made from one that is sitting there missing its agent, and
// would report the wrong thing either way.
func TestRunReportsSpaceEvenWhenAgentFails(t *testing.T) {
	repo, cfg := existingRepo(t)
	sess := &fakeSession{
		pane:          herdr.Pane{PaneID: "wZ:p1"},
		workspaceID:   "wZ",
		startAgentErr: errors.New("agent kind unavailable"),
	}
	prs := &fakePRLookup{info: gh.PRInfo{Branch: "feature/x", Title: "Add a thing"}}

	out, err := Run(Deps{Session: sess, PRs: prs, Git: &fakeFetcher{}, Cwd: repo}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/1", Options{})
	if err == nil {
		t.Fatal("want the agent failure reported")
	}
	if out.WorkspaceID != "wZ" || out.PaneID != "wZ:p1" {
		t.Errorf("Outcome lost the Space it created: %+v", out)
	}
	if out.AgentStarted {
		t.Error("AgentStarted should stay false when the agent failed")
	}
}

// agent.start rejects a pane Herdr has only just created: the shell is spawned
// but not yet registered as an available target. That is a timing artifact of
// building the Space and starting the agent in one breath, not a real failure,
// so it is worth retrying before giving up on the prompt.
func TestRunRetriesStartAgentWhilePaneIsBusy(t *testing.T) {
	repo, cfg := existingRepo(t)
	sess := &fakeSession{
		pane:                    herdr.Pane{PaneID: "wZ:p1"},
		workspaceID:             "wZ",
		startAgentBusyUntilCall: 3, // fails twice, succeeds on the third
	}
	prs := &fakePRLookup{info: gh.PRInfo{Branch: "feature/x", Title: "Add a thing"}}

	out, err := Run(Deps{Session: sess, PRs: prs, Git: &fakeFetcher{}, Cwd: repo}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sess.startAgentCalls) != 3 {
		t.Errorf("StartAgent called %d times, want 3", len(sess.startAgentCalls))
	}
	if !out.AgentStarted || len(sess.sendTextCalls) != 1 {
		t.Errorf("the prompt should still be typed after a retry: %+v", out)
	}
}

// Only the busy code is a timing artifact. Retrying anything else would just
// repeat a real rejection several times over before reporting it.
func TestRunDoesNotRetryOtherAgentErrors(t *testing.T) {
	repo, cfg := existingRepo(t)
	sess := &fakeSession{
		pane:          herdr.Pane{PaneID: "wZ:p1"},
		workspaceID:   "wZ",
		startAgentErr: &herdr.APIError{Method: "agent.start", Code: "unknown_agent_kind", Message: "nope"},
	}
	prs := &fakePRLookup{info: gh.PRInfo{Branch: "feature/x", Title: "t"}}

	_, err := Run(Deps{Session: sess, PRs: prs, Git: &fakeFetcher{}, Cwd: repo}, cfg,
		"https://github.com/phin-tech/herdr-phin-util/pull/1", Options{})
	if err == nil {
		t.Fatal("want the error reported")
	}
	if len(sess.startAgentCalls) != 1 {
		t.Errorf("StartAgent called %d times, want exactly 1", len(sess.startAgentCalls))
	}
}
