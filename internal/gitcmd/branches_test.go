package gitcmd

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeGit records invocations and replays canned stdout per subcommand.
type fakeGit struct {
	out    map[string]string
	errs   map[string]error
	called [][]string
}

func (f *fakeGit) runner() CommandRunner {
	return func(dir, name string, args ...string) ([]byte, error) {
		f.called = append(f.called, append([]string{dir, name}, args...))
		key := args[0]
		if err, ok := f.errs[key]; ok {
			return nil, err
		}
		return []byte(f.out[key]), nil
	}
}

func newFake(f *fakeGit) *Runner { return &Runner{run: f.runner()} }

func TestBranchesSplitsLocalAndRemote(t *testing.T) {
	f := &fakeGit{out: map[string]string{
		"for-each-ref": strings.Join([]string{
			"refs/heads/main",
			"refs/heads/feature/x",
			"refs/remotes/origin/main",
			"refs/remotes/origin/upstream-only",
			"refs/remotes/origin/HEAD",
		}, "\n"),
	}}

	got, err := newFake(f).Branches("/src/acme")
	if err != nil {
		t.Fatal(err)
	}

	want := []Branch{
		{Name: "feature/x"},
		{Name: "main"},
		{Name: "upstream-only", Remote: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// origin/main duplicates the local main; only the local one can back a
// worktree, so the remote row is dropped.
func TestBranchesDropsRemotesThatExistLocally(t *testing.T) {
	f := &fakeGit{out: map[string]string{
		"for-each-ref": "refs/heads/main\nrefs/remotes/origin/main",
	}}

	got, err := newFake(f).Branches("/src/acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "main" || got[0].Remote {
		t.Errorf("got %+v, want just the local main", got)
	}
}

// origin/HEAD is an alias for the default branch, not a branch of its own.
func TestBranchesSkipsRemoteHead(t *testing.T) {
	f := &fakeGit{out: map[string]string{
		"for-each-ref": "refs/remotes/origin/HEAD",
	}}

	got, err := newFake(f).Branches("/src/acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestBranchesHandlesSeveralRemotes(t *testing.T) {
	f := &fakeGit{out: map[string]string{
		"for-each-ref": "refs/remotes/origin/shared\nrefs/remotes/fork/shared\nrefs/remotes/fork/only-there",
	}}

	got, err := newFake(f).Branches("/src/acme")
	if err != nil {
		t.Fatal(err)
	}
	// "shared" exists on two remotes but is one branch to check out.
	want := []Branch{
		{Name: "only-there", Remote: true},
		{Name: "shared", Remote: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestBranchesEmptyRepository(t *testing.T) {
	got, err := newFake(&fakeGit{out: map[string]string{"for-each-ref": "\n"}}).Branches("/src/new")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestBranchesReportsGitFailure(t *testing.T) {
	f := &fakeGit{errs: map[string]error{"for-each-ref": errors.New("not a repository")}}
	if _, err := newFake(f).Branches("/tmp"); err == nil {
		t.Fatal("expected the git failure to surface")
	}
}

func TestDefaultBranchPrefersOriginHead(t *testing.T) {
	f := &fakeGit{out: map[string]string{"symbolic-ref": "refs/remotes/origin/trunk\n"}}
	if got := newFake(f).DefaultBranch("/src/acme"); got != "trunk" {
		t.Errorf("got %q, want trunk", got)
	}
}

// A repository that has never been pushed has no origin/HEAD.
func TestDefaultBranchFallsBackToConvention(t *testing.T) {
	f := &fakeGit{
		errs: map[string]error{"symbolic-ref": errors.New("no such ref")},
		out:  map[string]string{"show-ref": ""},
	}
	// show-ref succeeds for the first guess, so main wins.
	if got := newFake(f).DefaultBranch("/src/acme"); got != "main" {
		t.Errorf("got %q, want main", got)
	}
}

func TestDefaultBranchGivesUpCleanly(t *testing.T) {
	f := &fakeGit{errs: map[string]error{
		"symbolic-ref": errors.New("no such ref"),
		"show-ref":     errors.New("no such ref"),
	}}
	// Empty means "let git decide", which is a usable answer rather than an
	// error the picker would have to render.
	if got := newFake(f).DefaultBranch("/src/acme"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestRefreshPrunes(t *testing.T) {
	f := &fakeGit{out: map[string]string{"fetch": ""}}
	if err := newFake(f).Refresh("/src/acme"); err != nil {
		t.Fatal(err)
	}

	if len(f.called) != 1 {
		t.Fatalf("expected one git call, got %d", len(f.called))
	}
	args := strings.Join(f.called[0], " ")
	if !strings.Contains(args, "fetch --prune origin") {
		t.Errorf("called %q, want a pruning fetch", args)
	}
	if !strings.HasPrefix(args, "/src/acme ") {
		t.Errorf("called %q, want it to run in the repo", args)
	}
}

func TestRefreshReportsFailure(t *testing.T) {
	f := &fakeGit{errs: map[string]error{"fetch": errors.New("offline")}}
	if err := newFake(f).Refresh("/src/acme"); err == nil {
		t.Fatal("expected the fetch failure to surface")
	}
}
