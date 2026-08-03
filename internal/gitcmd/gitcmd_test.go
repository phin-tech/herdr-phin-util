package gitcmd

import (
	"errors"
	"strings"
	"testing"
)

func fakeRunner(err error, calls *[]struct {
	dir  string
	args []string
}) CommandRunner {
	return func(dir, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, struct {
			dir  string
			args []string
		}{dir, append([]string{name}, args...)})
		if err != nil {
			return nil, err
		}
		return nil, nil
	}
}

func TestFetchBranchRunsGitFetchInRepo(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: fakeRunner(nil, &calls)}

	if err := r.FetchBranch("/repo/path", "fix-thing"); err != nil {
		t.Fatalf("FetchBranch: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	if calls[0].dir != "/repo/path" {
		t.Errorf("dir = %q, want /repo/path", calls[0].dir)
	}
	if got, want := strings.Join(calls[0].args, " "), "git fetch origin fix-thing"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestFetchBranchPropagatesCommandError(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: fakeRunner(errors.New("boom"), &calls)}

	if err := r.FetchBranch("/repo", "b"); err == nil {
		t.Fatal("want an error when git fetch fails")
	}
}

// An empty branch would fetch nothing useful and produce a confusing "up to
// date" success, so it is refused up front rather than shelled out.
func TestFetchBranchRejectsEmptyBranch(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: fakeRunner(nil, &calls)}

	if err := r.FetchBranch("/repo", ""); err == nil {
		t.Fatal("want an error for an empty branch")
	}
	if len(calls) != 0 {
		t.Errorf("made %d calls, want none", len(calls))
	}
}

// sequenceRunner answers each call in order from errs, cycling the last entry
// once exhausted -- enough to script "fetch fails, then the verify that
// follows it succeeds" without a bespoke fake per test.
func sequenceRunner(errs []error, calls *[]struct {
	dir  string
	args []string
}) CommandRunner {
	i := 0
	return func(dir, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, struct {
			dir  string
			args []string
		}{dir, append([]string{name}, args...)})
		var err error
		if i < len(errs) {
			err = errs[i]
		} else if len(errs) > 0 {
			err = errs[len(errs)-1]
		}
		i++
		if err != nil {
			return nil, err
		}
		return nil, nil
	}
}

func TestFetchRefRunsGitFetchByRef(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: fakeRunner(nil, &calls)}

	if err := r.FetchRef("/repo/path", "a1b2c3d"); err != nil {
		t.Fatalf("FetchRef: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	if got, want := strings.Join(calls[0].args, " "), "git fetch origin a1b2c3d"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// The common case: a commit already present locally. git itself refuses to
// fetch a ref it already has, and that refusal must not block the run --
// so a failed fetch is checked against a local rev-parse before it is
// reported as an error.
func TestFetchRefToleratesACommitAlreadyPresentLocally(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: sequenceRunner([]error{errors.New("couldn't find remote ref"), nil}, &calls)}

	if err := r.FetchRef("/repo", "a1b2c3d"); err != nil {
		t.Fatalf("FetchRef: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("want 2 calls (fetch, then the local verify), got %d: %+v", len(calls), calls)
	}
	if !strings.Contains(strings.Join(calls[1].args, " "), "rev-parse") {
		t.Errorf("second call = %v, want a local rev-parse verify", calls[1].args)
	}
}

// A ref that is neither fetchable nor already present is a real failure.
func TestFetchRefPropagatesAGenuineFailure(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: sequenceRunner([]error{errors.New("no such ref"), errors.New("unknown revision")}, &calls)}

	if err := r.FetchRef("/repo", "nope"); err == nil {
		t.Fatal("want an error when neither fetch nor local verify succeed")
	}
}

func TestFetchRefRejectsEmptyRef(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: fakeRunner(nil, &calls)}

	if err := r.FetchRef("/repo", "  "); err == nil {
		t.Fatal("want an error for a blank ref")
	}
	if len(calls) != 0 {
		t.Errorf("made %d calls, want none", len(calls))
	}
}

func TestResolveRefRunsRevParse(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: func(dir, name string, args ...string) ([]byte, error) {
		calls = append(calls, struct {
			dir  string
			args []string
		}{dir, append([]string{name}, args...)})
		return []byte("a1b2c3d\n"), nil
	}}

	got, err := r.ResolveRef("/repo", "main")
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if got != "a1b2c3d" {
		t.Errorf("got %q, want the trimmed SHA", got)
	}
	if got, want := strings.Join(calls[0].args, " "), "git rev-parse main"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestHeadCommitRunsRevParseHeadInPath(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: func(dir, name string, args ...string) ([]byte, error) {
		calls = append(calls, struct {
			dir  string
			args []string
		}{dir, append([]string{name}, args...)})
		return []byte("deadbeef\n"), nil
	}}

	got, err := r.HeadCommit("/worktree/path")
	if err != nil {
		t.Fatalf("HeadCommit: %v", err)
	}
	if got != "deadbeef" {
		t.Errorf("got %q, want the trimmed SHA", got)
	}
	if calls[0].dir != "/worktree/path" {
		t.Errorf("dir = %q, want the worktree path, not the repo it was cut from", calls[0].dir)
	}
	if got, want := strings.Join(calls[0].args, " "), "git rev-parse HEAD"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestWorktreeAddChecksOutDetached(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: fakeRunner(nil, &calls)}

	if err := r.WorktreeAdd("/repo", "/repo/.herdr-worktrees/main", "main"); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	if calls[0].dir != "/repo" {
		t.Errorf("dir = %q, want the repo the worktree is cut from", calls[0].dir)
	}
	if got, want := strings.Join(calls[0].args, " "), "git worktree add --detach /repo/.herdr-worktrees/main main"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestWorktreeAddBranchChecksOutABranchNotDetached(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: fakeRunner(nil, &calls)}

	if err := r.WorktreeAddBranch("/repo", "/repo/.herdr-worktrees/fix-thing", "fix-thing"); err != nil {
		t.Fatalf("WorktreeAddBranch: %v", err)
	}
	if got, want := strings.Join(calls[0].args, " "), "git worktree add /repo/.herdr-worktrees/fix-thing fix-thing"; got != want {
		t.Errorf("args = %q, want %q (no --detach)", got, want)
	}
}

func TestWorktreeRemoveWithoutForce(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: fakeRunner(nil, &calls)}

	if err := r.WorktreeRemove("/repo", "/repo/.herdr-worktrees/main", false); err != nil {
		t.Fatalf("WorktreeRemove: %v", err)
	}
	if got, want := strings.Join(calls[0].args, " "), "git worktree remove /repo/.herdr-worktrees/main"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestWorktreeRemoveWithForce(t *testing.T) {
	var calls []struct {
		dir  string
		args []string
	}
	r := &Runner{run: fakeRunner(nil, &calls)}

	if err := r.WorktreeRemove("/repo", "/repo/.herdr-worktrees/main", true); err != nil {
		t.Fatalf("WorktreeRemove: %v", err)
	}
	if got, want := strings.Join(calls[0].args, " "), "git worktree remove --force /repo/.herdr-worktrees/main"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}
