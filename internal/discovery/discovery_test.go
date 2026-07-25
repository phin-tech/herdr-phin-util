package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// mkrepo makes a directory under root and gives it .git, so discovery counts
// it as a checkout.
func mkrepo(t *testing.T, root string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func mkdir(t *testing.T, root string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestListGitOnlyFindsCheckoutsAtDepth(t *testing.T) {
	root := t.TempDir()
	a := mkrepo(t, root, "src", "alpha")
	b := mkrepo(t, root, "src", "beta")
	mkdir(t, root, "src", "not-a-repo")

	got := List([]string{filepath.Join(root, "src")}, Options{GitOnly: true, Depth: 1})

	want := []string{a, b}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestListGitOnlyDescendsToDepth(t *testing.T) {
	root := t.TempDir()
	deep := mkrepo(t, root, "src", "github.com", "owner", "repo")

	// Depth 1 stops before the checkout.
	if got := List([]string{filepath.Join(root, "src")}, Options{GitOnly: true, Depth: 1}); len(got) != 0 {
		t.Fatalf("depth 1 should not reach %s, got %v", deep, got)
	}

	got := List([]string{filepath.Join(root, "src")}, Options{GitOnly: true, Depth: 3})
	if !reflect.DeepEqual(got, []string{deep}) {
		t.Fatalf("got %v, want %v", got, []string{deep})
	}
}

// A repository containing its own nested checkouts (submodules, worktrees)
// should still read as one project, not several.
func TestListDoesNotDescendIntoACheckout(t *testing.T) {
	root := t.TempDir()
	repo := mkrepo(t, root, "src", "alpha")
	mkrepo(t, repo, "vendor", "nested")

	got := List([]string{filepath.Join(root, "src")}, Options{GitOnly: true, Depth: 5})
	if !reflect.DeepEqual(got, []string{repo}) {
		t.Fatalf("got %v, want just %v", got, repo)
	}
}

// A linked worktree's .git is a file, not a directory.
func TestListAcceptsGitFile(t *testing.T) {
	root := t.TempDir()
	repo := mkdir(t, root, "src", "linked")
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := List([]string{filepath.Join(root, "src")}, Options{GitOnly: true, Depth: 1})
	if !reflect.DeepEqual(got, []string{repo}) {
		t.Fatalf("got %v, want %v", got, []string{repo})
	}
}

func TestListWithoutGitOnlyTakesEveryChild(t *testing.T) {
	root := t.TempDir()
	a := mkdir(t, root, "src", "alpha")
	b := mkrepo(t, root, "src", "beta")
	mkdir(t, root, "src", ".hidden")

	got := List([]string{filepath.Join(root, "src")}, Options{GitOnly: false})
	if !reflect.DeepEqual(got, []string{a, b}) {
		t.Fatalf("got %v, want %v", got, []string{a, b})
	}
}

func TestListExpandsSingleStarGlob(t *testing.T) {
	root := t.TempDir()
	a := mkrepo(t, root, "src", "github.com", "owner-one", "repo-a")
	b := mkrepo(t, root, "src", "github.com", "owner-two", "repo-b")
	// A different host, which the pattern should not reach.
	mkrepo(t, root, "src", "gitlab.com", "owner-three", "repo-c")

	pattern := filepath.Join(root, "src", "github.com", "*")
	got := List([]string{pattern}, Options{GitOnly: true, Depth: 1})

	if !reflect.DeepEqual(got, []string{a, b}) {
		t.Fatalf("got %v, want %v", got, []string{a, b})
	}
}

func TestListExpandsDoubleStarGlob(t *testing.T) {
	root := t.TempDir()
	shallow := mkrepo(t, root, "src", "flat")
	deep := mkrepo(t, root, "src", "a", "b", "c", "nested")

	pattern := filepath.Join(root, "src", "**")
	got := List([]string{pattern}, Options{GitOnly: true, Depth: 1})

	want := []string{deep, shallow}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestListDeduplicatesOverlappingRoots(t *testing.T) {
	root := t.TempDir()
	repo := mkrepo(t, root, "src", "alpha")

	src := filepath.Join(root, "src")
	got := List([]string{src, src, filepath.Join(root, "*")}, Options{GitOnly: true, Depth: 1})

	if !reflect.DeepEqual(got, []string{repo}) {
		t.Fatalf("got %v, want %v", got, []string{repo})
	}
}

func TestListSkipsMissingRoots(t *testing.T) {
	root := t.TempDir()
	repo := mkrepo(t, root, "src", "alpha")

	roots := []string{
		filepath.Join(root, "does-not-exist"),
		filepath.Join(root, "also", "missing", "*"),
		filepath.Join(root, "src"),
	}
	got := List(roots, Options{GitOnly: true, Depth: 1})

	if !reflect.DeepEqual(got, []string{repo}) {
		t.Fatalf("got %v, want %v", got, []string{repo})
	}
}

// A symlink pointing back up its own tree must not send the walk in circles.
func TestListSurvivesSymlinkCycle(t *testing.T) {
	root := t.TempDir()
	src := mkdir(t, root, "src")
	repo := mkrepo(t, root, "src", "alpha")
	loop := mkdir(t, root, "src", "loop")
	if err := os.Symlink(src, filepath.Join(loop, "back")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	done := make(chan []string, 1)
	go func() { done <- List([]string{src}, Options{GitOnly: true, Depth: 6}) }()

	got := <-done
	found := false
	for _, path := range got {
		if path == repo {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected %s in %v", repo, got)
	}
}

func TestListEmptyRoots(t *testing.T) {
	if got := List(nil, Options{GitOnly: true, Depth: 1}); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
	if got := List([]string{"", "   "}, Options{GitOnly: true, Depth: 1}); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := expandHome("~"); got != home {
		t.Fatalf("got %q, want %q", got, home)
	}
	if got, want := expandHome("~/src"), filepath.Join(home, "src"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// A ~ that is not a leading path segment is just a character.
	if got := expandHome("/tmp/~x"); got != "/tmp/~x" {
		t.Fatalf("got %q, want unchanged", got)
	}
}
