package open

import (
	"errors"
	"strings"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
)

func worktreeDeps(s *fakeSession, f *fakeFetcher) Deps {
	return Deps{Session: s, Git: f}
}

func noAgent() *config.Settings {
	return &config.Settings{Agent: config.AgentSettings{Enabled: false}}
}

func TestRunWorktreeCreatesFromALocalBranch(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "w5:p1"}, workspaceID: "w5"}
	f := &fakeFetcher{}

	out, err := RunWorktree(worktreeDeps(s, f), noAgent(), WorktreeRequest{
		RepoRoot: "/src/acme",
		Branch:   "feature",
		Label:    "acme/feature",
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if len(s.createWorktreeCalls) != 1 {
		t.Fatalf("expected one worktree.create, got %d", len(s.createWorktreeCalls))
	}
	req := s.createWorktreeCalls[0]
	if req.Cwd != "/src/acme" || req.Branch != "feature" {
		t.Errorf("request = %+v", req)
	}
	// A branch that already exists locally needs no base.
	if req.Base != "" {
		t.Errorf("Base = %q, want empty for an existing local branch", req.Base)
	}
	if req.Label != "acme/feature" {
		t.Errorf("Label = %q", req.Label)
	}
	if !req.Focus {
		t.Error("a picked worktree should be focused")
	}
	// Nothing to fetch for a branch already on disk.
	if f.calls != 0 {
		t.Errorf("fetched %d times, want none", f.calls)
	}
	if out.Branch != "feature" || out.WorkspaceID != "w5" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestRunWorktreeFetchesARemoteBranch(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "w6:p1"}, workspaceID: "w6"}
	f := &fakeFetcher{}

	_, err := RunWorktree(worktreeDeps(s, f), noAgent(), WorktreeRequest{
		RepoRoot: "/src/acme",
		Branch:   "upstream-only",
		Base:     "origin/upstream-only",
		Fetch:    true,
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if f.calls != 1 || f.branch != "upstream-only" || f.path != "/src/acme" {
		t.Errorf("fetch = %d calls, branch %q, path %q", f.calls, f.branch, f.path)
	}
	if got := s.createWorktreeCalls[0].Base; got != "origin/upstream-only" {
		t.Errorf("Base = %q, want the fetched remote ref", got)
	}
}

// A fetch that fails must stop before a worktree is built on a ref that is not
// there.
func TestRunWorktreeStopsOnFetchFailure(t *testing.T) {
	s := &fakeSession{}
	f := &fakeFetcher{err: errors.New("offline")}

	_, err := RunWorktree(worktreeDeps(s, f), noAgent(), WorktreeRequest{
		RepoRoot: "/src/acme", Branch: "x", Fetch: true,
	}, Options{})
	if err == nil {
		t.Fatal("expected the fetch failure to surface")
	}
	if len(s.createWorktreeCalls) != 0 {
		t.Error("nothing should be created after a failed fetch")
	}
}

// An existing worktree is opened, never created -- creating one that exists
// fails with a worse message than not trying.
func TestRunWorktreeOpensAnExistingWorktree(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "w7:p1"}, workspaceID: "w7"}

	_, err := RunWorktree(worktreeDeps(s, &fakeFetcher{}), noAgent(), WorktreeRequest{
		RepoRoot: "/src/acme",
		Branch:   "feature",
		Path:     "/src/acme/.wt/feature",
		Existing: true,
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if len(s.createWorktreeCalls) != 0 {
		t.Error("an existing worktree should not be created")
	}
	if len(s.openWorktreeCalls) != 1 {
		t.Fatalf("expected one worktree.open, got %d", len(s.openWorktreeCalls))
	}
	if got := s.openWorktreeCalls[0].Path; got != "/src/acme/.wt/feature" {
		t.Errorf("Path = %q, want where the worktree already lives", got)
	}
}

// baseAwareSession accepts a create only for one specific base, which is what
// a repository that has never been fetched actually does: origin/main is not a
// valid reference there, but main is.
type baseAwareSession struct {
	*fakeSession
	acceptBase string
}

func (b *baseAwareSession) CreateWorktree(req herdr.WorktreeRequest) (herdr.Pane, string, error) {
	b.createWorktreeCalls = append(b.createWorktreeCalls, req)
	if req.Base != b.acceptBase {
		return herdr.Pane{}, "", errors.New("invalid reference: " + req.Base)
	}
	return b.pane, b.workspaceID, nil
}

// origin/main does not resolve in a repo with no remote; the local branch is a
// slightly staler but perfectly good base.
func TestRunWorktreeFallsBackToALocalBase(t *testing.T) {
	inner := &fakeSession{
		pane:        herdr.Pane{PaneID: "w8:p1"},
		workspaceID: "w8",
		// worktree.open cannot rescue this: the worktree does not exist yet.
		openWorktreeErr: errors.New("no such worktree"),
	}
	s := &baseAwareSession{fakeSession: inner, acceptBase: "main"}

	out, err := RunWorktree(Deps{Session: s, Git: &fakeFetcher{}}, noAgent(), WorktreeRequest{
		RepoRoot:     "/src/acme",
		Branch:       "brand-new",
		Base:         "origin/main",
		FallbackBase: "main",
	}, Options{})
	if err != nil {
		t.Fatalf("expected the fallback to succeed: %v", err)
	}

	if len(inner.createWorktreeCalls) != 2 {
		t.Fatalf("expected two create attempts, got %d", len(inner.createWorktreeCalls))
	}
	if got := inner.createWorktreeCalls[0].Base; got != "origin/main" {
		t.Errorf("first base = %q, want the remote ref tried first", got)
	}
	if got := inner.createWorktreeCalls[1].Base; got != "main" {
		t.Errorf("second base = %q, want the local fallback", got)
	}
	if out.WorkspaceID != "w8" {
		t.Errorf("outcome = %+v, want the Space the retry made", out)
	}
}

// When both bases fail, the error names the base that was actually asked for
// rather than the fallback nobody chose.
func TestRunWorktreeReportsTheOriginalBaseOnTotalFailure(t *testing.T) {
	inner := &fakeSession{openWorktreeErr: errors.New("no such worktree")}
	s := &baseAwareSession{fakeSession: inner, acceptBase: "nothing-matches"}

	_, err := RunWorktree(Deps{Session: s, Git: &fakeFetcher{}}, noAgent(), WorktreeRequest{
		RepoRoot:     "/src/acme",
		Branch:       "brand-new",
		Base:         "origin/main",
		FallbackBase: "main",
	}, Options{})
	if err == nil {
		t.Fatal("expected an error when neither base works")
	}
	if !strings.Contains(err.Error(), "origin/main") {
		t.Errorf("error = %q, want it to name the requested base", err)
	}
}

// With no fallback configured there is nothing to retry.
func TestRunWorktreeDoesNotRetryWithoutAFallback(t *testing.T) {
	inner := &fakeSession{openWorktreeErr: errors.New("no such worktree")}
	s := &baseAwareSession{fakeSession: inner, acceptBase: "nothing-matches"}

	_, err := RunWorktree(Deps{Session: s, Git: &fakeFetcher{}}, noAgent(), WorktreeRequest{
		RepoRoot: "/src/acme", Branch: "x", Base: "origin/main",
	}, Options{})
	if err == nil {
		t.Fatal("expected the failure to surface")
	}
	if len(inner.createWorktreeCalls) != 1 {
		t.Errorf("expected one attempt, got %d", len(inner.createWorktreeCalls))
	}
}

func TestRunWorktreeRejectsIncompleteRequests(t *testing.T) {
	s := &fakeSession{}

	if _, err := RunWorktree(worktreeDeps(s, &fakeFetcher{}), noAgent(), WorktreeRequest{Branch: "x"}, Options{}); err == nil {
		t.Error("expected an error with no repository")
	}
	if _, err := RunWorktree(worktreeDeps(s, &fakeFetcher{}), noAgent(), WorktreeRequest{RepoRoot: "/src/x"}, Options{}); err == nil {
		t.Error("expected an error with no branch")
	}
}

// Without a label the Space is named after the branch, which is better than
// leaving Herdr to invent one.
func TestRunWorktreeDefaultsTheLabelToTheBranch(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "w9:p1"}, workspaceID: "w9"}

	out, err := RunWorktree(worktreeDeps(s, &fakeFetcher{}), noAgent(), WorktreeRequest{
		RepoRoot: "/src/acme", Branch: "feature",
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.createWorktreeCalls[0].Label != "feature" {
		t.Errorf("Label = %q, want the branch name", s.createWorktreeCalls[0].Label)
	}
	if out.Label != "feature" {
		t.Errorf("Outcome.Label = %q", out.Label)
	}
}

// The worktree path template resolves {repo_root} and {branch}; a worktree
// picked from the list has no URL, so {host}/{owner} render empty.
func TestRunWorktreeUsesTheConfiguredPathTemplate(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "wA:p1"}, workspaceID: "wA"}
	cfg := &config.Settings{
		Agent:        config.AgentSettings{Enabled: false},
		WorktreePath: "{repo_root}/.worktrees/{branch}",
	}

	_, err := RunWorktree(worktreeDeps(s, &fakeFetcher{}), cfg, WorktreeRequest{
		RepoRoot: "/src/acme", Branch: "feature",
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	want := "/src/acme/.worktrees/feature"
	if got := s.createWorktreeCalls[0].Path; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

// The agent step is shared with every other kind of Space, so the toggle
// behaves identically here.
func TestRunWorktreeRunsTheAgentStep(t *testing.T) {
	s := &fakeSession{pane: herdr.Pane{PaneID: "wB:p1"}, workspaceID: "wB"}
	cfg := &config.Settings{
		Agent:   config.AgentSettings{Enabled: true, Kind: "codex"},
		Prompts: config.PromptSettings{Project: "on {{.Branch}} in {{.Repo}}"},
	}

	out, err := RunWorktree(worktreeDeps(s, &fakeFetcher{}), cfg, WorktreeRequest{
		RepoRoot: "/src/acme", Branch: "feature",
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if len(s.startAgentCalls) != 1 {
		t.Fatalf("expected the agent to start, got %d", len(s.startAgentCalls))
	}
	if len(s.sendTextCalls) != 1 {
		t.Fatalf("expected the prompt to be typed, got %d", len(s.sendTextCalls))
	}
	if got, want := s.sendTextCalls[0].text, "on feature in acme"; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
	if !out.AgentStarted {
		t.Error("AgentStarted should be set")
	}
}
