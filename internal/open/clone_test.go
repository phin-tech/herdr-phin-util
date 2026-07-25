package open

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

type fakeCloner struct {
	calls []cloneCall
	err   error
	// makeDir mimics a real clone leaving a checkout behind, so the Space can
	// then be opened on it.
	makeDir bool
}

type cloneCall struct{ owner, repo, dest string }

func (f *fakeCloner) Clone(owner, repo, dest string) error {
	f.calls = append(f.calls, cloneCall{owner, repo, dest})
	if f.err != nil {
		return f.err
	}
	if f.makeDir {
		return os.MkdirAll(dest, 0o755)
	}
	return nil
}

// cloneSettings points the templates at a temp directory, so a clone lands
// somewhere the test can inspect.
func cloneSettings(root string) *config.Settings {
	return &config.Settings{
		RepoTemplates: []string{filepath.Join(root, "{host}", "{owner}", "{repo}")},
		Agent:         config.AgentSettings{Enabled: false},
	}
}

func repoTarget(t *testing.T) target.Target {
	t.Helper()
	return target.Parse("https://github.com/phin-tech/roux")
}

func TestRunCloneFetchesThenOpensASpace(t *testing.T) {
	root := t.TempDir()
	cloner := &fakeCloner{makeDir: true}
	s := &fakeSession{pane: herdr.Pane{PaneID: "w1:p1"}, workspaceID: "w1"}

	out, err := RunClone(Deps{Session: s, Clone: cloner}, cloneSettings(root), repoTarget(t), Options{})
	if err != nil {
		t.Fatal(err)
	}

	if len(cloner.calls) != 1 {
		t.Fatalf("expected one clone, got %d", len(cloner.calls))
	}
	want := filepath.Join(root, "github.com", "phin-tech", "roux")
	got := cloner.calls[0]
	if got.owner != "phin-tech" || got.repo != "roux" || got.dest != want {
		t.Errorf("clone = %+v, want dest %q", got, want)
	}

	// The Space is opened on what was just cloned.
	if len(s.createWorkspaceCalls) != 1 {
		t.Fatalf("expected a workspace, got %d", len(s.createWorkspaceCalls))
	}
	if s.createWorkspaceCalls[0].cwd != want {
		t.Errorf("cwd = %q, want %q", s.createWorkspaceCalls[0].cwd, want)
	}
	if out.RepoPath != want {
		t.Errorf("RepoPath = %q, want %q", out.RepoPath, want)
	}
}

// The clone destination has to be somewhere ResolveRepo would later look, or
// the paste-a-link flow could not find what was just cloned.
func TestClonedRepoIsFoundByResolveRepo(t *testing.T) {
	root := t.TempDir()
	cfg := cloneSettings(root)
	tgt := repoTarget(t)

	dest, err := cfg.ClonePath(tgt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	found, _, err := cfg.ResolveRepo(tgt)
	if err != nil {
		t.Fatalf("a repo cloned to %s should be findable: %v", dest, err)
	}
	if found != dest {
		t.Errorf("ResolveRepo = %q, want the clone destination %q", found, dest)
	}
}

// A checkout that is already there is opened rather than cloned over.
func TestRunCloneOpensAnExistingCheckout(t *testing.T) {
	root := t.TempDir()
	cfg := cloneSettings(root)
	tgt := repoTarget(t)

	dest, _ := cfg.ClonePath(tgt)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	cloner := &fakeCloner{}
	s := &fakeSession{pane: herdr.Pane{PaneID: "w2:p1"}, workspaceID: "w2"}

	if _, err := RunClone(Deps{Session: s, Clone: cloner}, cfg, tgt, Options{}); err != nil {
		t.Fatal(err)
	}

	if len(cloner.calls) != 0 {
		t.Error("an existing checkout should not be cloned over")
	}
	if len(s.createWorkspaceCalls) != 1 {
		t.Error("the existing checkout should still get a Space")
	}
}

// gh clones into the destination, but a brand new machine has no owner
// directory for it to land in.
func TestRunCloneCreatesTheParentDirectory(t *testing.T) {
	root := t.TempDir()
	cloner := &fakeCloner{makeDir: true}
	s := &fakeSession{pane: herdr.Pane{PaneID: "w3:p1"}, workspaceID: "w3"}

	if _, err := RunClone(Deps{Session: s, Clone: cloner}, cloneSettings(root), repoTarget(t), Options{}); err != nil {
		t.Fatal(err)
	}

	parent := filepath.Join(root, "github.com", "phin-tech")
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		t.Errorf("expected %s to have been created", parent)
	}
}

func TestRunCloneReportsAFailedClone(t *testing.T) {
	root := t.TempDir()
	cloner := &fakeCloner{err: errors.New("repository not found")}
	s := &fakeSession{}

	_, err := RunClone(Deps{Session: s, Clone: cloner}, cloneSettings(root), repoTarget(t), Options{})
	if err == nil {
		t.Fatal("expected the clone failure to surface")
	}
	if len(s.createWorkspaceCalls) != 0 {
		t.Error("no Space should be made when the clone failed")
	}
}

// A clone that reports success but leaves nothing behind must not produce a
// Space pointed at a directory that is not there.
func TestRunCloneFailsWhenNothingArrived(t *testing.T) {
	root := t.TempDir()
	cloner := &fakeCloner{makeDir: false}
	s := &fakeSession{}

	if _, err := RunClone(Deps{Session: s, Clone: cloner}, cloneSettings(root), repoTarget(t), Options{}); err == nil {
		t.Fatal("expected an error when the checkout is missing after cloning")
	}
}

func TestRunCloneRejectsIncompleteInput(t *testing.T) {
	root := t.TempDir()
	s := &fakeSession{}

	if _, err := RunClone(Deps{Session: s, Clone: &fakeCloner{}}, cloneSettings(root), target.Parse("scratch"), Options{}); err == nil {
		t.Error("expected an error with no repository to clone")
	}
	if _, err := RunClone(Deps{Session: s}, cloneSettings(root), repoTarget(t), Options{}); err == nil {
		t.Error("expected an error with no cloner configured")
	}
}

func TestClonePathUsesTheFirstTemplate(t *testing.T) {
	cfg := &config.Settings{RepoTemplates: []string{"/first/{owner}/{repo}", "/second/{owner}/{repo}"}}

	got, err := cfg.ClonePath(repoTarget(t))
	if err != nil {
		t.Fatal(err)
	}
	if got != "/first/phin-tech/roux" {
		t.Errorf("got %q, want the first template expanded", got)
	}
}

func TestClonePathNeedsATemplate(t *testing.T) {
	cfg := &config.Settings{}
	if _, err := cfg.ClonePath(repoTarget(t)); err == nil {
		t.Error("expected an error with no templates configured")
	}
}
