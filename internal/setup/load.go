package setup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Directory and file names, all overridable from config.toml but rarely worth
// overriding.
const (
	// DirName holds setups offered for every repository.
	DirName = "setups"
	// ReposDirName holds per-repository directories: repos/<repo>/*.yaml, or
	// repos/<owner>/<repo>/*.yaml when two repos share a name.
	ReposDirName = "repos"
	// RepoFileName is the in-checkout file, committed alongside the code.
	RepoFileName = ".herdr-setups.yaml"
)

// Sources says where to look. Every field is optional: an empty one is simply
// not consulted, which is what makes "no setups anywhere" a normal state
// rather than an error.
type Sources struct {
	// Dir is the generic setups directory.
	Dir string
	// ReposDir is the per-repository directory tree.
	ReposDir string
	// RepoPath is the checkout being opened, which is where the shared file is
	// looked for and what a repos/<name>/ directory is matched against.
	RepoPath string
	// RepoFile is the shared file's name inside that checkout.
	RepoFile string
}

// Load reads every setup that could apply, in precedence order: generic
// first, then the checkout's shared file, then this machine's per-repo
// directory. A later setup with the same name replaces an earlier one, so the
// list that comes back has no duplicates and the strongest source wins.
//
// Problems are returned rather than raised. A directory of setups is exactly
// the kind of thing where one bad file should not take the others down with
// it, and a plugin action has nowhere good to put a fatal error anyway.
func Load(src Sources) ([]Setup, []string) {
	var problems []string
	var ordered []Setup

	collect := func(setups []Setup, probs []string) {
		ordered = append(ordered, setups...)
		problems = append(problems, probs...)
	}

	if src.Dir != "" {
		collect(loadDir(src.Dir, OriginGeneric, ""))
	}
	if src.RepoPath != "" {
		name := src.RepoFile
		if name == "" {
			name = RepoFileName
		}
		collect(loadSharedFile(filepath.Join(src.RepoPath, name)))
	}
	if src.ReposDir != "" && src.RepoPath != "" {
		collect(loadRepoDirs(src.ReposDir, filepath.Base(filepath.Clean(src.RepoPath))))
	}

	return dedupe(ordered), problems
}

// dedupe keeps the last setup for each name -- Load walks the sources
// weakest-first, so last means strongest -- while preserving the position of
// the first appearance. Shuffling a setup up the list because a stronger
// source redefined it would make the picker's order jump around for a reason
// nobody can see.
func dedupe(setups []Setup) []Setup {
	position := map[string]int{}
	out := []Setup{}
	for _, s := range setups {
		key := strings.ToLower(s.Name)
		if i, ok := position[key]; ok {
			out[i] = s
			continue
		}
		position[key] = len(out)
		out = append(out, s)
	}
	return out
}

// loadDir reads every YAML file directly inside dir, each holding one setup.
func loadDir(dir string, origin Origin, scopedRepo string) ([]Setup, []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// The normal state for anyone who has not written a setup yet.
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("could not read %s: %v", dir, err)}
	}

	var setups []Setup
	var problems []string
	for _, entry := range entries {
		if entry.IsDir() || !isYAML(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		s, probs := loadFile(path, origin, scopedRepo)
		setups = append(setups, s...)
		problems = append(problems, probs...)
	}
	return setups, problems
}

// loadRepoDirs finds the per-repository directory for a checkout. Both
// repos/<repo>/ and repos/<owner>/<repo>/ are accepted: the second exists for
// the day two checkouts share a name, and looking for it costs one walk of a
// directory that is small by construction.
func loadRepoDirs(reposDir, repoName string) ([]Setup, []string) {
	if repoName == "" || repoName == "." || repoName == string(filepath.Separator) {
		return nil, nil
	}

	var setups []Setup
	var problems []string

	direct := filepath.Join(reposDir, repoName)
	if isDir(direct) {
		s, probs := loadDir(direct, OriginRepo, repoName)
		setups = append(setups, s...)
		problems = append(problems, probs...)
	}

	// One level down: repos/<owner>/<repo>/. Only directories are considered,
	// and only a child named for the repo, so this never wanders.
	owners, err := os.ReadDir(reposDir)
	if err != nil {
		if !os.IsNotExist(err) {
			problems = append(problems, fmt.Sprintf("could not read %s: %v", reposDir, err))
		}
		return setups, problems
	}
	for _, owner := range owners {
		if !owner.IsDir() || owner.Name() == repoName {
			continue
		}
		nested := filepath.Join(reposDir, owner.Name(), repoName)
		if !isDir(nested) {
			continue
		}
		s, probs := loadDir(nested, OriginRepo, owner.Name()+"/"+repoName)
		setups = append(setups, s...)
		problems = append(problems, probs...)
	}

	sort.SliceStable(setups, func(i, j int) bool { return setups[i].Name < setups[j].Name })
	return setups, problems
}

// loadSharedFile reads the in-checkout file, which holds a list rather than a
// single setup: a repository should not have to grow a directory to carry two
// layouts.
func loadSharedFile(path string) ([]Setup, []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("could not read %s: %v", path, err)}
	}
	return decode(path, data, OriginShared, "")
}

// loadFile reads one file, which may hold a single setup or a setups: list --
// both are accepted everywhere, since being told "wrong shape for this
// location" is a worse experience than it just working.
func loadFile(path string, origin Origin, scopedRepo string) ([]Setup, []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []string{fmt.Sprintf("could not read %s: %v", path, err)}
	}
	return decode(path, data, origin, scopedRepo)
}

// decode parses one document and validates what came out.
//
// KnownFields is on: a typo'd key that silently does nothing is the failure
// this kind of file dies of, and YAML is forgiving enough to make it likely.
// A file that will not parse at all is reported and skipped; a file that
// parses but says something impossible is reported and skipped too, since
// half-applying a layout is worse than not applying it.
func decode(path string, data []byte, origin Origin, scopedRepo string) ([]Setup, []string) {
	var problems []string

	var doc file
	if err := decodeStrict(data, &doc); err == nil && len(doc.Setups) > 0 {
		var out []Setup
		for i, s := range doc.Setups {
			s.Source = path
			s.Origin = origin
			s.ScopedRepo = scopedRepo
			if probs := s.Validate(); len(probs) > 0 {
				name := s.Name
				if name == "" {
					name = fmt.Sprintf("#%d", i+1)
				}
				problems = append(problems, prefix(fmt.Sprintf("%s: setup %s", path, name), probs)...)
				continue
			}
			out = append(out, s)
		}
		return out, problems
	}

	var one Setup
	if err := decodeStrict(data, &one); err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", path, cleanYAMLError(err))}
	}
	one.Source = path
	one.Origin = origin
	one.ScopedRepo = scopedRepo
	if probs := one.Validate(); len(probs) > 0 {
		return nil, prefix(path, probs)
	}
	return []Setup{one}, nil
}

// decodeStrict is yaml.Unmarshal with unknown keys rejected.
func decodeStrict(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

// cleanYAMLError trims the library's leading newline and collapses its
// multi-line form, so a problem reads as one line in a list of them.
func cleanYAMLError(err error) string {
	msg := strings.TrimSpace(err.Error())
	msg = strings.TrimPrefix(msg, "yaml: unmarshal errors:")
	msg = strings.TrimSpace(msg)
	return strings.Join(strings.Fields(strings.ReplaceAll(msg, "\n", "; ")), " ")
}

func prefix(what string, problems []string) []string {
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		out = append(out, fmt.Sprintf("%s: %s", what, p))
	}
	return out
}

// isYAML accepts both spellings. Being right about the extension is not worth
// a setup silently missing from the list.
func isYAML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// SourcesFor builds the three locations from a config directory and the
// checkout being opened. dir, reposDir and repoFile override the defaults when
// non-empty; a relative override is taken as relative to the config directory,
// which is what someone writing dir = "layouts" means.
func SourcesFor(configDir, repoPath, dir, reposDir, repoFile string) Sources {
	resolve := func(value, fallback string) string {
		if value == "" {
			value = fallback
		}
		if filepath.IsAbs(value) {
			return value
		}
		if configDir == "" {
			return ""
		}
		return filepath.Join(configDir, value)
	}
	return Sources{
		Dir:      resolve(dir, DirName),
		ReposDir: resolve(reposDir, ReposDirName),
		RepoPath: repoPath,
		RepoFile: repoFile,
	}
}
