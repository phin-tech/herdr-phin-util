package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write drops a file at path, creating the directories it needs.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// minimal is the shortest thing that validates, for tests that care about
// where a setup came from rather than what is in it.
func minimal(name, extra string) string {
	return "name: " + name + "\n" + extra + "tabs:\n  - name: one\n"
}

func TestLoadFindsAllThreeSources(t *testing.T) {
	config := t.TempDir()
	repo := t.TempDir()

	write(t, filepath.Join(config, "setups", "generic.yaml"), minimal("generic", ""))
	write(t, filepath.Join(repo, RepoFileName), "setups:\n  - "+strings.ReplaceAll(minimal("shared", ""), "\n", "\n    ")+"\n")
	write(t, filepath.Join(config, "repos", filepath.Base(repo), "local.yml"), minimal("local", ""))

	setups, problems := Load(SourcesFor(config, repo, "", "", ""))
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}

	got := map[string]Origin{}
	for _, s := range setups {
		got[s.Name] = s.Origin
	}
	want := map[string]Origin{"generic": OriginGeneric, "shared": OriginShared, "local": OriginRepo}
	for name, origin := range want {
		if got[name] != origin {
			t.Errorf("setup %q: origin %v, want %v (loaded %v)", name, got[name], origin, got)
		}
	}
	if len(setups) != 3 {
		t.Errorf("loaded %d setups, want 3", len(setups))
	}
}

// The precedence rule is the whole reason there are three sources, so it gets
// tested by name rather than by count.
func TestLoadPrecedenceRepoBeatsSharedBeatsGeneric(t *testing.T) {
	config := t.TempDir()
	repo := t.TempDir()
	name := filepath.Base(repo)

	write(t, filepath.Join(config, "setups", "review.yaml"), minimal("review", "description: generic\n"))
	write(t, filepath.Join(repo, RepoFileName), "setups:\n  - name: review\n    description: shared\n    tabs:\n      - name: one\n")
	write(t, filepath.Join(config, "repos", name, "review.yaml"), minimal("review", "description: repo\n"))

	setups, problems := Load(SourcesFor(config, repo, "", "", ""))
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(setups) != 1 {
		t.Fatalf("got %d setups, want 1 after dedupe: %+v", len(setups), setups)
	}
	if setups[0].Description != "repo" {
		t.Errorf("description %q, want the repos/ one to win", setups[0].Description)
	}

	// And without the repos/ copy, the shared file beats the generic one.
	if err := os.Remove(filepath.Join(config, "repos", name, "review.yaml")); err != nil {
		t.Fatal(err)
	}
	setups, _ = Load(SourcesFor(config, repo, "", "", ""))
	if len(setups) != 1 || setups[0].Description != "shared" {
		t.Errorf("without repos/: got %+v, want the shared one", setups)
	}
}

func TestLoadRepoDirNestedUnderOwner(t *testing.T) {
	config := t.TempDir()
	repo := filepath.Join(t.TempDir(), "roux")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(config, "repos", "phin-tech", "roux", "deep.yaml"), minimal("deep", ""))

	setups, problems := Load(SourcesFor(config, repo, "", "", ""))
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(setups) != 1 {
		t.Fatalf("got %d setups, want 1", len(setups))
	}
	if setups[0].ScopedRepo != "phin-tech/roux" {
		t.Errorf("ScopedRepo %q, want phin-tech/roux", setups[0].ScopedRepo)
	}
}

// A repos/ directory for some other repository must not leak into this one.
func TestLoadIgnoresOtherReposDirectories(t *testing.T) {
	config := t.TempDir()
	repo := t.TempDir()

	write(t, filepath.Join(config, "repos", "someone-elses-repo", "nope.yaml"), minimal("nope", ""))

	setups, problems := Load(SourcesFor(config, repo, "", "", ""))
	if len(setups) != 0 {
		t.Errorf("loaded %+v, want nothing", setups)
	}
	if len(problems) != 0 {
		t.Errorf("problems %v, want none", problems)
	}
}

func TestLoadMissingDirectoriesAreNotProblems(t *testing.T) {
	setups, problems := Load(SourcesFor(t.TempDir(), t.TempDir(), "", "", ""))
	if len(setups) != 0 || len(problems) != 0 {
		t.Errorf("got %v / %v, want a quiet empty result", setups, problems)
	}
}

func TestLoadReportsBadFilesWithoutLosingGoodOnes(t *testing.T) {
	config := t.TempDir()

	write(t, filepath.Join(config, "setups", "good.yaml"), minimal("good", ""))
	write(t, filepath.Join(config, "setups", "unparseable.yaml"), "name: broken\n  tabs: [\n")
	write(t, filepath.Join(config, "setups", "stray-key.yaml"), "name: typo\nprompt_: nope\ntabs:\n  - name: one\n")
	write(t, filepath.Join(config, "setups", "invalid.yaml"), "name: empty\ntabs: []\n")

	setups, problems := Load(SourcesFor(config, "", "", "", ""))

	if len(setups) != 1 || setups[0].Name != "good" {
		t.Errorf("setups %+v, want only the good one", setups)
	}
	if len(problems) != 3 {
		t.Fatalf("problems %v, want one per bad file", problems)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"unparseable.yaml", "stray-key.yaml", "invalid.yaml", "prompt_", "no tabs"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems do not mention %q:\n%s", want, joined)
		}
	}
}

func TestLoadIgnoresNonYAMLFiles(t *testing.T) {
	config := t.TempDir()
	write(t, filepath.Join(config, "setups", "notes.md"), "not a setup")
	write(t, filepath.Join(config, "setups", "real.yml"), minimal("real", ""))

	setups, problems := Load(SourcesFor(config, "", "", "", ""))
	if len(problems) != 0 {
		t.Fatalf("problems %v, want none", problems)
	}
	if len(setups) != 1 || setups[0].Name != "real" {
		t.Errorf("setups %+v, want only real", setups)
	}
}

func TestSourcesForOverrides(t *testing.T) {
	config := "/cfg"
	src := SourcesFor(config, "/repo", "layouts", "/abs/repos", ".setups.yaml")

	if src.Dir != filepath.Join(config, "layouts") {
		t.Errorf("Dir %q, want a relative override joined to the config dir", src.Dir)
	}
	if src.ReposDir != "/abs/repos" {
		t.Errorf("ReposDir %q, want the absolute override left alone", src.ReposDir)
	}
	if src.RepoFile != ".setups.yaml" {
		t.Errorf("RepoFile %q", src.RepoFile)
	}
}
