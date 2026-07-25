// Package claudesess finds the Claude Code session that belongs to a working
// directory.
//
// Claude keeps one JSONL transcript per session under a per-directory folder
// in ~/.claude/projects, and exports the current session's id into the
// environment. That is everything needed to say "resume this conversation
// somewhere else", so this package is deliberately just the lookup -- it
// starts nothing and talks to nothing.
package claudesess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// EnvVar is the variable Claude Code exports into every session it runs.
const EnvVar = "CLAUDE_CODE_SESSION_ID"

// maxScanEntries bounds how far into a transcript the cwd search reads.
// Claude records it within the first few entries; a transcript that has not
// said where it is by then is not going to.
const maxScanEntries = 50

// Session is one transcript on disk.
type Session struct {
	ID string
	// Cwd is the directory the session was running in, read from the
	// transcript itself. The containing folder's name cannot be trusted for
	// this: Claude's slug turns both "/" and "." into "-", so "foo.bar" and
	// "foo/bar" produce the same folder and cannot be told apart afterwards.
	Cwd     string
	ModTime time.Time
	// Path is the transcript file, kept so a caller that needs the cwd can
	// read it without reconstructing the location.
	Path string
}

// FromEnv returns the id of the session this process is running inside, or ""
// when it is not running inside one. This is the accurate answer whenever it
// is available: it names the actual session rather than inferring one.
func FromEnv() string {
	return strings.TrimSpace(os.Getenv(EnvVar))
}

// Slug is the directory name Claude derives from a working directory. Both
// separators and dots become dashes, so "~/src/foo.bar" and "~/src/foo/bar"
// deliberately collide -- that is Claude's rule, not a simplification of it.
func Slug(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
}

// ProjectDir is where the transcripts for cwd live. home is taken as an
// argument rather than read here so tests can point it at a temp directory.
func ProjectDir(home, cwd string) string {
	return filepath.Join(home, ".claude", "projects", Slug(cwd))
}

// Latest returns the most recently written transcript for cwd.
//
// This is the fallback for running from a shell beside the session rather
// than inside it, where FromEnv has nothing to say. "Most recent" is a guess,
// but it is the same guess `claude --continue` makes, and in the case this
// exists for -- one session, one directory -- it is not much of one.
//
// Cwd is set from the argument rather than read back from the transcript:
// the directory is what selected the file in the first place.
func Latest(home, cwd string) (Session, error) {
	found := scan(ProjectDir(home, cwd))
	if len(found) == 0 {
		return Session{}, fmt.Errorf("no Claude sessions for %s", cwd)
	}
	s := found[0]
	s.Cwd = cwd
	return s, nil
}

// LatestAny returns the most recently written transcript anywhere, with the
// directory it belongs to.
//
// This is the last resort, for running from somewhere that has no sessions of
// its own -- a home directory, a scratch shell. Because the answer can be a
// conversation about a repository the caller is nowhere near, the directory
// comes from the transcript and the caller is expected to say out loud which
// session it picked.
func LatestAny(home string) (Session, error) {
	dirs, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*"))
	if err != nil {
		return Session{}, fmt.Errorf("search Claude sessions: %w", err)
	}

	var found []Session
	for _, dir := range dirs {
		found = append(found, scan(dir)...)
	}
	if len(found) == 0 {
		return Session{}, fmt.Errorf("no Claude sessions found under %s", filepath.Join(home, ".claude", "projects"))
	}
	sortByRecency(found)

	// Only the transcripts actually needed get opened, newest first, so the
	// usual case reads one file rather than every file. A transcript that
	// never recorded a cwd is skipped: resuming it would mean guessing a
	// directory, and a Space in the wrong place is worse than none.
	for _, s := range found {
		cwd, err := readCwd(s.Path)
		if err != nil || cwd == "" {
			continue
		}
		s.Cwd = cwd
		return s, nil
	}
	return Session{}, fmt.Errorf("found %d Claude transcripts but none recorded a working directory", len(found))
}

// scan lists the transcripts in one project directory, newest first. A
// directory it cannot read is not an error here -- LatestAny walks many, and
// one unreadable folder should not sink the search.
func scan(dir string) []Session {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var found []Session
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			// Vanished between the listing and the stat. One unreadable
			// transcript is not a reason to fail the whole lookup.
			continue
		}
		found = append(found, Session{
			ID:      strings.TrimSuffix(e.Name(), ".jsonl"),
			ModTime: info.ModTime(),
			Path:    filepath.Join(dir, e.Name()),
		})
	}
	sortByRecency(found)
	return found
}

// sortByRecency breaks ties on the id so the answer is stable, which matters
// mostly for tests: a temp directory can easily write two files within one
// filesystem timestamp tick.
func sortByRecency(found []Session) {
	sort.Slice(found, func(i, j int) bool {
		if !found[i].ModTime.Equal(found[j].ModTime) {
			return found[i].ModTime.After(found[j].ModTime)
		}
		return found[i].ID < found[j].ID
	})
}

// readCwd pulls the working directory out of a transcript.
//
// A json.Decoder is used rather than a line scanner because transcript lines
// carry whole tool results and can run to megabytes -- well past a scanner's
// default buffer, and awkward to size for in advance.
func readCwd(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	for i := 0; i < maxScanEntries; i++ {
		var rec struct {
			Cwd string `json:"cwd"`
		}
		if err := dec.Decode(&rec); err != nil {
			break
		}
		if rec.Cwd != "" {
			return rec.Cwd, nil
		}
	}
	return "", nil
}
