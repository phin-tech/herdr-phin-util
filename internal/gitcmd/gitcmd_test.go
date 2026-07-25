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
