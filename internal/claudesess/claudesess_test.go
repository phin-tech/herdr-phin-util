package claudesess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSlugReplacesSeparatorsAndDots(t *testing.T) {
	got := Slug("/Users/sphinizy/src/github.com/phin-tech/herdr-phin-util")
	want := "-Users-sphinizy-src-github-com-phin-tech-herdr-phin-util"
	if got != want {
		t.Errorf("Slug = %q, want %q", got, want)
	}
}

func TestProjectDirIsUnderClaudeProjects(t *testing.T) {
	got := ProjectDir("/home/me", "/src/app")
	want := filepath.Join("/home/me", ".claude", "projects", "-src-app")
	if got != want {
		t.Errorf("ProjectDir = %q, want %q", got, want)
	}
}

func TestFromEnvReadsTheSessionVariable(t *testing.T) {
	t.Setenv(EnvVar, "  f73cb238-9ee4  ")
	if got := FromEnv(); got != "f73cb238-9ee4" {
		t.Errorf("FromEnv = %q, want the trimmed id", got)
	}
}

func TestFromEnvIsEmptyOutsideASession(t *testing.T) {
	t.Setenv(EnvVar, "")
	if got := FromEnv(); got != "" {
		t.Errorf("FromEnv = %q, want empty", got)
	}
}

// writeTranscript creates one transcript with a controlled modification time,
// so "newest wins" can be asserted without sleeping between writes. The body
// mimics the real shape: a couple of entries before the one carrying cwd.
func writeTranscript(t *testing.T, home, cwd, id string, age time.Duration) {
	t.Helper()
	writeTranscriptAt(t, ProjectDir(home, cwd), cwd, id, age)
}

func writeTranscriptAt(t *testing.T, dir, cwd, id string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"last-prompt","sessionId":"` + id + `"}
{"type":"summary"}
{"type":"user","cwd":"` + cwd + `","gitBranch":"main"}
`
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestLatestPicksTheMostRecentTranscript(t *testing.T) {
	home := t.TempDir()
	const cwd = "/src/app"
	writeTranscript(t, home, cwd, "older", 2*time.Hour)
	writeTranscript(t, home, cwd, "newest", time.Minute)
	writeTranscript(t, home, cwd, "oldest", 48*time.Hour)

	got, err := Latest(home, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "newest" {
		t.Errorf("Latest = %q, want %q", got.ID, "newest")
	}
	// The directory selected the file, so it is what the session reports --
	// no need to have read it back out of the transcript.
	if got.Cwd != cwd {
		t.Errorf("Cwd = %q, want %q", got.Cwd, cwd)
	}
}

func TestLatestIgnoresNonTranscripts(t *testing.T) {
	home := t.TempDir()
	const cwd = "/src/app"
	writeTranscript(t, home, cwd, "real", time.Hour)

	// Claude keeps sidecar directories beside the transcripts; neither those
	// nor stray files should ever be offered as a session id.
	dir := ProjectDir(home, cwd)
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Latest(home, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "real" {
		t.Errorf("Latest = %q, want %q", got.ID, "real")
	}
}

func TestLatestFailsWhenTheDirectoryIsMissing(t *testing.T) {
	if _, err := Latest(t.TempDir(), "/src/never-opened"); err == nil {
		t.Fatal("expected an error for a directory Claude has never seen")
	}
}

func TestLatestAnySearchesEveryProject(t *testing.T) {
	home := t.TempDir()
	writeTranscript(t, home, "/src/one", "stale", 6*time.Hour)
	writeTranscript(t, home, "/src/two", "freshest", time.Minute)
	writeTranscript(t, home, "/src/three", "old", 72*time.Hour)

	got, err := LatestAny(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "freshest" {
		t.Errorf("LatestAny = %q, want %q", got.ID, "freshest")
	}
	if got.Cwd != "/src/two" {
		t.Errorf("Cwd = %q, want the directory the transcript recorded", got.Cwd)
	}
}

// The folder name cannot answer this: Claude's slug turns "." and "/" alike
// into "-", so a directory with a dot in it is unrecoverable from the name
// and has to come from inside the transcript.
func TestLatestAnyRecoversADirectoryTheSlugCannotEncode(t *testing.T) {
	home := t.TempDir()
	const cwd = "/src/foo.bar/app"
	writeTranscript(t, home, cwd, "dotted", time.Minute)

	got, err := LatestAny(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cwd != cwd {
		t.Errorf("Cwd = %q, want %q", got.Cwd, cwd)
	}
}

func TestLatestAnySkipsTranscriptsWithNoDirectory(t *testing.T) {
	home := t.TempDir()
	writeTranscript(t, home, "/src/usable", "usable", time.Hour)

	// Newer, but it never says where it was: resuming it would mean guessing
	// a directory, so it is passed over rather than opened in the wrong place.
	dir := ProjectDir(home, "/src/mystery")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mystery.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"summary"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LatestAny(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "usable" {
		t.Errorf("LatestAny = %q, want it to skip the transcript with no cwd", got.ID)
	}
}

func TestLatestAnyReadsPastAHugeFirstEntry(t *testing.T) {
	home := t.TempDir()
	dir := ProjectDir(home, "/src/big")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A single transcript line carries whole tool results and can run to
	// megabytes, well past a line scanner's default buffer.
	huge := strings.Repeat("x", 2<<20)
	body := `{"type":"assistant","text":"` + huge + `"}` + "\n" +
		`{"type":"user","cwd":"/src/big"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "big.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LatestAny(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cwd != "/src/big" {
		t.Errorf("Cwd = %q, want it read past the huge entry", got.Cwd)
	}
}

func TestLatestAnyFailsOnAnEmptyHome(t *testing.T) {
	if _, err := LatestAny(t.TempDir()); err == nil {
		t.Fatal("expected an error when there are no transcripts anywhere")
	}
}

func TestLatestFailsWhenNoTranscriptsExist(t *testing.T) {
	home := t.TempDir()
	const cwd = "/src/app"
	if err := os.MkdirAll(ProjectDir(home, cwd), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Latest(home, cwd); err == nil {
		t.Fatal("expected an error for a directory with no transcripts")
	}
}
