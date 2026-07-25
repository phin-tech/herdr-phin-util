package session

import (
	"errors"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/gitcmd"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/open"
)

func openOptions() open.Options { return open.Options{} }

type fakeWorktrees struct {
	worktrees []herdr.Worktree
	source    herdr.WorktreeSource
	err       error
	cwds      []string
}

func (f *fakeWorktrees) Worktrees(cwd string) ([]herdr.Worktree, herdr.WorktreeSource, error) {
	f.cwds = append(f.cwds, cwd)
	return f.worktrees, f.source, f.err
}

type fakeBrancher struct {
	branches      []gitcmd.Branch
	defaultBranch string
	branchesErr   error
	refreshErr    error
	refreshed     []string
}

func (f *fakeBrancher) Branches(string) ([]gitcmd.Branch, error) { return f.branches, f.branchesErr }
func (f *fakeBrancher) DefaultBranch(string) string              { return f.defaultBranch }
func (f *fakeBrancher) Refresh(repo string) error {
	f.refreshed = append(f.refreshed, repo)
	return f.refreshErr
}

func kinds(candidates []Candidate) []Kind {
	out := make([]Kind, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.Kind)
	}
	return out
}

func find(candidates []Candidate, label string) (Candidate, bool) {
	for _, c := range candidates {
		if c.Label == label {
			return c, true
		}
	}
	return Candidate{}, false
}

func TestListWorktreesOrdersWorktreesThenLocalThenRemote(t *testing.T) {
	l := &fakeWorktrees{
		source:    herdr.WorktreeSource{RepoName: "acme", RepoRoot: "/src/acme"},
		worktrees: []herdr.Worktree{{Path: "/src/acme", Branch: "main"}},
	}
	g := &fakeBrancher{
		defaultBranch: "main",
		branches: []gitcmd.Branch{
			{Name: "feature"},
			{Name: "upstream-only", Remote: true},
		},
	}

	got, repo, err := ListWorktrees(l, g, "/src/acme")
	if err != nil {
		t.Fatal(err)
	}

	want := []Kind{KindWorktree, KindBranch, KindRemoteBranch}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(got), got)
	}
	for i, k := range want {
		if got[i].Kind != k {
			t.Errorf("row %d kind = %q, want %q", i, got[i].Kind, k)
		}
	}
	if repo.Name != "acme" || repo.Root != "/src/acme" || repo.DefaultBranch != "main" {
		t.Errorf("repo = %+v, want it resolved from the source", repo)
	}
}

// A branch that already has a worktree must not also be offered as a branch to
// build one from.
func TestListWorktreesHidesBranchesThatHaveAWorktree(t *testing.T) {
	l := &fakeWorktrees{
		worktrees: []herdr.Worktree{
			{Path: "/src/acme", Branch: "main"},
			{Path: "/src/acme/.wt/feature", Branch: "feature"},
		},
	}
	g := &fakeBrancher{branches: []gitcmd.Branch{{Name: "main"}, {Name: "feature"}, {Name: "other"}}}

	got, _, err := ListWorktrees(l, g, "/src/acme")
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(got), kinds(got))
	}
	if c, _ := find(got, "other"); c.Kind != KindBranch {
		t.Errorf("only 'other' should remain a branch, got %+v", kinds(got))
	}
	for _, c := range got {
		if c.Kind == KindBranch && (c.Label == "main" || c.Label == "feature") {
			t.Errorf("%s already has a worktree and should not be a branch row", c.Label)
		}
	}
}

// A worktree with a Space becomes a KindSpace, so picking it switches instead
// of trying to open a second one.
func TestListWorktreesMarksOpenWorktreesAsSpaces(t *testing.T) {
	l := &fakeWorktrees{
		worktrees: []herdr.Worktree{
			{Path: "/src/acme", Branch: "main", OpenWorkspaceID: "w4"},
			{Path: "/src/acme/.wt/idle", Branch: "idle"},
		},
	}
	g := &fakeBrancher{}

	got, _, err := ListWorktrees(l, g, "/src/acme")
	if err != nil {
		t.Fatal(err)
	}

	open, ok := find(got, "main")
	if !ok {
		t.Fatal("expected a row for main")
	}
	if open.Kind != KindSpace {
		t.Errorf("kind = %q, want %q for a worktree that already has a Space", open.Kind, KindSpace)
	}
	if open.WorkspaceID != "w4" {
		t.Errorf("WorkspaceID = %q, want w4", open.WorkspaceID)
	}
	if idle, _ := find(got, "idle"); idle.Kind != KindWorktree {
		t.Errorf("kind = %q, want %q for a worktree with no Space", idle.Kind, KindWorktree)
	}
}

func TestListWorktreesFlagsAPrunableWorktree(t *testing.T) {
	l := &fakeWorktrees{
		worktrees: []herdr.Worktree{{Path: "/src/acme/.wt/gone", Branch: "gone", IsPrunable: true}},
	}

	got, _, err := ListWorktrees(l, &fakeBrancher{}, "/src/acme")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Kind != KindPrunable {
		t.Errorf("kind = %q, want %q", got[0].Kind, KindPrunable)
	}
}

// Opening a prunable worktree would fail inside git; saying so up front is
// more useful than the error that would come back.
func TestOpenWorktreeRefusesAPrunableRow(t *testing.T) {
	repo := RepoContext{Name: "acme", Root: "/src/acme"}
	c := Candidate{Kind: KindPrunable, Label: "gone", Branch: "gone"}

	_, err := OpenWorktree(Deps{}, nil, repo, c, openOptions())
	if err == nil {
		t.Fatal("expected an error for a prunable worktree")
	}
}

func TestListWorktreesSkipsBareRepositories(t *testing.T) {
	l := &fakeWorktrees{
		worktrees: []herdr.Worktree{
			{Path: "/src/acme.git", IsBare: true},
			{Path: "/src/acme", Branch: "main"},
		},
	}

	got, _, err := ListWorktrees(l, &fakeBrancher{}, "/src/acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Label != "main" {
		t.Errorf("got %+v, want only the non-bare worktree", got)
	}
}

// worktree.list must be asked about the highlighted repo, not the calling
// pane's.
func TestListWorktreesPassesTheRepoPath(t *testing.T) {
	l := &fakeWorktrees{}
	if _, _, err := ListWorktrees(l, &fakeBrancher{}, "/src/elsewhere"); err != nil {
		t.Fatal(err)
	}
	if len(l.cwds) != 1 || l.cwds[0] != "/src/elsewhere" {
		t.Errorf("cwds = %v, want [/src/elsewhere]", l.cwds)
	}
}

func TestListWorktreesFallsBackWhenSourceIsEmpty(t *testing.T) {
	_, repo, err := ListWorktrees(&fakeWorktrees{}, &fakeBrancher{}, "/src/acme")
	if err != nil {
		t.Fatal(err)
	}
	if repo.Name != "acme" || repo.Root != "/src/acme" {
		t.Errorf("repo = %+v, want it derived from the path", repo)
	}
}

func TestListWorktreesPropagatesErrors(t *testing.T) {
	boom := errors.New("no such repo")

	if _, _, err := ListWorktrees(&fakeWorktrees{err: boom}, &fakeBrancher{}, "/x"); err == nil {
		t.Error("expected a worktree.list failure to surface")
	}
	if _, _, err := ListWorktrees(&fakeWorktrees{}, &fakeBrancher{branchesErr: boom}, "/x"); err == nil {
		t.Error("expected a branch listing failure to surface")
	}
}

func TestNewBranchCandidate(t *testing.T) {
	repo := RepoContext{Name: "acme", Root: "/src/acme", DefaultBranch: "main"}

	c := NewBranchCandidate(repo, "my-thing")
	if c.Kind != KindNewBranch || c.Branch != "my-thing" || c.Path != "/src/acme" {
		t.Errorf("candidate = %+v", c)
	}
	if c.Detail != "new branch from main" {
		t.Errorf("detail = %q, want it to name the base", c.Detail)
	}

	// A repository with no discoverable default still offers the row.
	bare := NewBranchCandidate(RepoContext{Root: "/src/x"}, "thing")
	if bare.Detail != "new branch" {
		t.Errorf("detail = %q, want no base named", bare.Detail)
	}
}

func TestWorktreeSpaceLabel(t *testing.T) {
	cases := []struct {
		repo, branch, want string
	}{
		{"acme", "feature", "acme/feature"},
		// The source checkout often reports the repo's own name.
		{"acme", "acme", "acme"},
		{"acme", "", "acme"},
		{"", "feature", "feature"},
	}
	for _, tc := range cases {
		got := worktreeSpaceLabel(RepoContext{Name: tc.repo}, tc.branch)
		if got != tc.want {
			t.Errorf("worktreeSpaceLabel(%q, %q) = %q, want %q", tc.repo, tc.branch, got, tc.want)
		}
	}
}

func TestWorktreeLabelFallsBackForDetachedHead(t *testing.T) {
	got := worktreeLabel(herdr.Worktree{Path: "/src/acme/.wt/x", IsDetached: true})
	if got != "(detached) x" {
		t.Errorf("got %q, want it to say detached", got)
	}
	if got := worktreeLabel(herdr.Worktree{Path: "/src/acme/.wt/y", Label: "named"}); got != "named" {
		t.Errorf("got %q, want the label", got)
	}
}
