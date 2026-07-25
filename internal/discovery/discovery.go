// Package discovery finds project directories under a set of configured
// roots.
//
// This is the "folder full of checkouts" half of a sessionizer: given
// ~/src/github.com/*/* it answers with every repository underneath, so the
// picker has something to offer without anyone maintaining a list by hand.
//
// Everything here is filesystem-only -- no Herdr, no network -- so it tests
// against a temp directory rather than a live session.
package discovery

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Options tunes what counts as a project.
type Options struct {
	// GitOnly keeps only directories that carry .git metadata, descending
	// through anything that does not. With it off, every immediate child of a
	// root is a project.
	GitOnly bool
	// Depth is how many levels below a root to search for .git when GitOnly is
	// set. It is ignored otherwise, since the no-git case never descends.
	Depth int
}

// List enumerates project directories under roots, which may be plain paths or
// glob patterns. The result is sorted and deduplicated: two roots that overlap
// are a configuration accident, not a reason to show the same repo twice.
func List(roots []string, opts Options) []string {
	seen := map[string]bool{}

	for _, root := range roots {
		for _, base := range expand(root) {
			if opts.GitOnly {
				walkForGit(base, opts.Depth, seen)
				continue
			}
			for _, child := range childDirs(base) {
				seen[child] = true
			}
		}
	}

	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// walkForGit descends from base looking for checkouts. A directory carrying
// .git is a project and is not descended into: the worktrees and submodules
// inside a repository are not separate projects, and treating them as such
// would bury the real ones.
func walkForGit(base string, depth int, seen map[string]bool) {
	visited := map[string]bool{}

	var visit func(path string, level int)
	visit = func(path string, level int) {
		if level > depth || !isDir(path) {
			return
		}
		if isDir(filepath.Join(path, ".git")) || isFile(filepath.Join(path, ".git")) {
			seen[path] = true
			return
		}
		// A symlink that points back up its own tree would otherwise recurse
		// until the depth budget ran out, once per entry point.
		if real, err := filepath.EvalSymlinks(path); err == nil {
			if visited[real] {
				return
			}
			visited[real] = true
		}
		for _, child := range childDirs(path) {
			visit(child, level+1)
		}
	}

	for _, child := range childDirs(base) {
		visit(child, 1)
	}
}

// expand turns one configured root into the concrete directories it names.
// Expansion happens here, at use time, rather than when the config is loaded,
// so a repository cloned after the popup last opened still shows up.
func expand(root string) []string {
	pattern := expandHome(strings.TrimSpace(root))
	if pattern == "" {
		return nil
	}
	if !hasMeta(pattern) {
		if isDir(pattern) {
			return []string{filepath.Clean(pattern)}
		}
		return nil
	}
	return expandGlob(pattern)
}

// expandGlob walks the pattern one segment at a time, carrying forward the set
// of directories matched so far. filepath.Glob would cover most of this, but
// not "**", and a recursive root is the whole point for anyone whose checkouts
// nest deeper than host/owner/repo.
func expandGlob(pattern string) []string {
	cleaned := filepath.Clean(pattern)
	segments := strings.Split(cleaned, string(os.PathSeparator))

	current := []string{"."}
	if filepath.IsAbs(cleaned) {
		current = []string{string(os.PathSeparator)}
		segments = segments[1:]
	}

	for _, segment := range segments {
		if segment == "" || segment == "." {
			continue
		}

		var next []string
		switch {
		case segment == "**":
			next = descendants(current)
		case hasMeta(segment):
			for _, dir := range current {
				for _, child := range childDirs(dir) {
					if ok, err := filepath.Match(segment, filepath.Base(child)); err == nil && ok {
						next = append(next, child)
					}
				}
			}
		default:
			for _, dir := range current {
				joined := filepath.Join(dir, segment)
				if isDir(joined) {
					next = append(next, joined)
				}
			}
		}

		if len(next) == 0 {
			// A pattern that matches nothing contributes nothing. It is not an
			// error: a root for a machine you are not currently on is a normal
			// thing to have in a config shared between two of them.
			return nil
		}
		current = next
	}
	return current
}

// descendants is "**": every directory at or below each starting point.
func descendants(roots []string) []string {
	var out []string
	visited := map[string]bool{}

	var walk func(path string)
	walk = func(path string) {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			if visited[real] {
				return
			}
			visited[real] = true
		}
		out = append(out, path)
		for _, child := range childDirs(path) {
			walk(child)
		}
	}

	for _, root := range roots {
		walk(root)
	}
	return out
}

// childDirs lists the immediate subdirectories of path, quietly returning
// nothing for a path that cannot be read. A root the user cannot list is not
// worth failing the whole picker over.
func childDirs(path string) []string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		// Dotted directories are noise here: ~/.cache and friends are never
		// what someone means by "my projects folder".
		if strings.HasPrefix(name, ".") {
			continue
		}
		joined := filepath.Join(path, name)
		if isDir(joined) {
			out = append(out, joined)
		}
	}
	return out
}

// isDir follows symlinks, so a roots entry pointing at a symlinked tree
// behaves like the real one.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isFile exists for .git, which is a file rather than a directory inside a
// linked worktree or a submodule.
func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func hasMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// expandHome resolves a leading ~, which the shell would have done for a path
// typed at a prompt but nobody does for one read out of a TOML file.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}
