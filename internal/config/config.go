// Package config holds the per-machine settings a user edits by hand.
//
// Layouts differ between a work machine and a personal one, so where a repo
// lives and whether an agent should be started are things the plugin has to
// be told rather than infer. A missing or broken file is never fatal --
// LoadFrom always returns usable Settings, with anything wrong about the file
// reported in Problems instead of stopping the plugin from working.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// FileName is the settings file inside the plugin's config directory.
const FileName = "config.toml"

// PluginID must match herdr-plugin.toml, since Herdr keys the config
// directory by it.
const PluginID = "phin-util"

// DefaultAgentKind is used when the config omits agent.kind, or sets it to
// something agent.start would reject.
const DefaultAgentKind = "claude"

// validAgentKinds mirrors the kinds agent.start accepts. Kept in sync with
// the Herdr socket API by hand -- there is no schema endpoint for it.
var validAgentKinds = map[string]bool{
	"pi": true, "claude": true, "codex": true, "gemini": true, "cursor": true,
	"devin": true, "agy": true, "cline": true, "omp": true, "mastracode": true,
	"opencode": true, "copilot": true, "kimi": true, "kiro": true, "droid": true,
	"amp": true, "grok": true, "hermes": true, "kilo": true, "qodercli": true,
	"maki": true,
}

// KnownAgentKind reports whether agent.start would accept a kind. A setup
// names an agent per pane rather than taking the one configured default, so
// the same list has to be reachable from outside this package.
func KnownAgentKind(kind string) bool {
	return validAgentKinds[kind]
}

// defaultPrompts render into Go text/template against the target plus
// resolved extras. They are deliberately short: this is a nudge typed into an
// agent's input, not a full brief.
//
// Project is empty on purpose. Opening a checkout is not a task the way a PR
// or an issue is, so there is nothing to say yet -- the agent starts with a
// clean input rather than a canned line to delete.
var defaultPrompts = PromptSettings{
	GithubPR:    "Review PR #{{.Number}} — {{.Title}}\n{{.URL}}",
	GithubIssue: "Work issue #{{.Number}} — {{.Title}}\n{{.URL}}",
	Linear:      "Work {{.Issue}} — {{.Title}}\n{{.URL}}",
	Plain:       "{{.Text}}",
	Project:     "",
}

// Project discovery defaults. GitOnly is on because a folder of checkouts
// invariably also holds a few directories that are not one; Depth 1 matches
// the roots derived from a repo template, which already point at the
// directory the checkouts sit in.
const (
	defaultProjectGitOnly = true
	defaultProjectDepth   = 1
	// maxProjectDepth bounds a typo. Each extra level multiplies the walk, and
	// nobody's checkouts are nested eight deep under a root.
	maxProjectDepth = 8
)

// defaultRepoTemplates is the guess for a brand new install: most checkouts
// under github.com end up organised as host/owner/repo somewhere under $HOME.
var defaultRepoTemplates = []string{"~/src/{host}/{owner}/{repo}"}

// AgentSettings controls whether and how an agent is started in a new Space.
type AgentSettings struct {
	// Enabled is the popup toggle's default state.
	Enabled bool
	Kind    string
}

// PromptSettings holds one Go text/template body per target kind.
type PromptSettings struct {
	GithubPR    string
	GithubIssue string
	Linear      string
	Plain       string
	Project     string
}

// For returns the template text for a target kind, falling back to the plain
// prompt for anything this package does not specifically recognise.
func (p PromptSettings) For(k target.Kind) string {
	switch k {
	case target.KindGitHubPR:
		return p.GithubPR
	case target.KindGitHubIssue:
		return p.GithubIssue
	case target.KindLinear:
		return p.Linear
	case target.KindProject:
		return p.Project
	default:
		return p.Plain
	}
}

// ProjectSettings describes where checkouts live on this machine, for the
// picker to enumerate.
type ProjectSettings struct {
	// Roots are plain paths or glob patterns. Expanded at use time, not load
	// time, so a repo cloned since the popup last opened still appears.
	Roots []string
	// GitOnly keeps only directories carrying .git metadata.
	GitOnly bool
	// Depth is how far below each root to look for one, when GitOnly is set.
	Depth int
}

// SetupSettings says where the workspace recipes live, and which one Enter
// uses when nothing is picked explicitly.
//
// The recipes themselves are YAML -- three levels of nesting carrying
// multi-line prompts is the one shape TOML renders badly -- but where to find
// them is per-machine configuration like everything else here, so it stays in
// config.toml.
type SetupSettings struct {
	// Dir holds setups offered for every repository. Relative paths are taken
	// as relative to the config directory.
	Dir string
	// ReposDir holds per-repository directories: repos/<repo>/*.yaml.
	ReposDir string
	// RepoFile is the committed file looked for inside a checkout.
	RepoFile string
	// Default names a setup to use when a row is opened without one being
	// picked. Empty keeps today's behaviour: one agent, one prompt.
	Default string
}

// Settings is the resolved, validated configuration.
type Settings struct {
	// RepoTemplates are tried in order; the first one that exists locally
	// wins. Placeholders: {host}, {owner}, {repo}.
	RepoTemplates []string
	Agent         AgentSettings
	Prompts       PromptSettings
	// Projects is where the picker looks for checkouts. When the file does not
	// say, the roots are derived from RepoTemplates rather than left empty --
	// someone who has already told us where repos live should not have to say
	// it twice in a different shape.
	Projects ProjectSettings
	// Setups is where the workspace recipes are looked for.
	Setups SetupSettings
	// WorktreePath is the raw configured template for where a worktree is
	// created, or empty when unset. Placeholders: {host}, {owner}, {repo},
	// {repo_root}, {branch}. Empty means "let Herdr choose", which is the
	// right default for anyone who has not opted into a specific layout.
	WorktreePath string
	// LinearAPIKey is accepted but unused today: a Linear issue resolves from
	// its URL alone. It is carried through so a later enrichment step (real
	// title, description) has somewhere to read a key from without another
	// config format change.
	LinearAPIKey string
	// Problems are complaints about the file's contents. A bad value falls
	// back to its default rather than stopping the plugin, but it is
	// reported here so a typo does not pass as silently ignored.
	Problems []string
}

// rawConfig is the TOML shape. Enabled is a pointer so that an omitted key
// keeps the default rather than silently reading as false.
type rawConfig struct {
	Repos struct {
		Templates []string `toml:"templates"`
	} `toml:"repos"`
	Agent struct {
		Enabled *bool  `toml:"enabled"`
		Kind    string `toml:"kind"`
		Prompts struct {
			GithubPR    string `toml:"github_pr"`
			GithubIssue string `toml:"github_issue"`
			Linear      string `toml:"linear"`
			Plain       string `toml:"plain"`
			Project     string `toml:"project"`
		} `toml:"prompts"`
	} `toml:"agent"`
	Projects struct {
		Roots []string `toml:"roots"`
		// Pointers so an omitted key keeps its default instead of reading as
		// false/0, which are both meaningful values here.
		GitOnly *bool `toml:"git_only"`
		Depth   *int  `toml:"depth"`
	} `toml:"projects"`
	Setups struct {
		Dir      string `toml:"dir"`
		ReposDir string `toml:"repos_dir"`
		RepoFile string `toml:"repo_file"`
		Default  string `toml:"default"`
	} `toml:"setups"`
	Worktrees struct {
		Path string `toml:"path"`
	} `toml:"worktrees"`
	Linear struct {
		APIKey string `toml:"api_key"`
	} `toml:"linear"`
}

// Dir is the plugin's config directory: the one Herdr injects when it
// launches us, or the same path reconstructed when run by hand.
func Dir() (string, error) {
	if dir := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, ".config", "herdr", "plugins", "config", PluginID), nil
}

// Load reads the settings from the injected (or reconstructed) config
// directory.
func Load() (*Settings, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return LoadFrom(dir)
}

// LoadFrom reads settings from a specific directory. Exposed separately from
// Load so tests can point it at a temp directory instead of the real one.
func LoadFrom(dir string) (*Settings, error) {
	s := &Settings{
		RepoTemplates: append([]string(nil), defaultRepoTemplates...),
		Agent:         AgentSettings{Enabled: true, Kind: DefaultAgentKind},
		Prompts:       defaultPrompts,
		Projects:      ProjectSettings{GitOnly: defaultProjectGitOnly, Depth: defaultProjectDepth},
	}

	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// The normal first run: nobody has written any settings yet.
		s.Projects.Roots = deriveProjectRoots(s.RepoTemplates)
		return s, nil
	}
	if err != nil {
		s.Problems = append(s.Problems, fmt.Sprintf("could not read %s: %v", path, err))
		s.Projects.Roots = deriveProjectRoots(s.RepoTemplates)
		return s, nil
	}

	var raw rawConfig
	if err := toml.Unmarshal(data, &raw); err != nil {
		s.Problems = append(s.Problems, fmt.Sprintf("%s is not valid TOML: %v", path, err))
		s.Projects.Roots = deriveProjectRoots(s.RepoTemplates)
		return s, nil
	}

	if len(raw.Repos.Templates) > 0 {
		s.RepoTemplates = raw.Repos.Templates
	}

	if raw.Agent.Enabled != nil {
		s.Agent.Enabled = *raw.Agent.Enabled
	}
	if raw.Agent.Kind != "" {
		if validAgentKinds[raw.Agent.Kind] {
			s.Agent.Kind = raw.Agent.Kind
		} else {
			s.Problems = append(s.Problems, fmt.Sprintf("agent.kind %q is not a known agent — using %s", raw.Agent.Kind, DefaultAgentKind))
		}
	}
	if raw.Agent.Prompts.GithubPR != "" {
		s.Prompts.GithubPR = raw.Agent.Prompts.GithubPR
	}
	if raw.Agent.Prompts.GithubIssue != "" {
		s.Prompts.GithubIssue = raw.Agent.Prompts.GithubIssue
	}
	if raw.Agent.Prompts.Linear != "" {
		s.Prompts.Linear = raw.Agent.Prompts.Linear
	}
	if raw.Agent.Prompts.Plain != "" {
		s.Prompts.Plain = raw.Agent.Prompts.Plain
	}
	if raw.Agent.Prompts.Project != "" {
		s.Prompts.Project = raw.Agent.Prompts.Project
	}

	if raw.Projects.GitOnly != nil {
		s.Projects.GitOnly = *raw.Projects.GitOnly
	}
	if raw.Projects.Depth != nil {
		switch d := *raw.Projects.Depth; {
		case d < 1:
			s.Problems = append(s.Problems, fmt.Sprintf("projects.depth %d is below 1 — using %d", d, defaultProjectDepth))
		case d > maxProjectDepth:
			s.Problems = append(s.Problems, fmt.Sprintf("projects.depth %d is above the %d maximum — using %d", d, maxProjectDepth, maxProjectDepth))
			s.Projects.Depth = maxProjectDepth
		default:
			s.Projects.Depth = d
		}
	}
	if len(raw.Projects.Roots) > 0 {
		s.Projects.Roots = raw.Projects.Roots
	} else {
		s.Projects.Roots = deriveProjectRoots(s.RepoTemplates)
	}

	s.Setups = SetupSettings{
		Dir:      raw.Setups.Dir,
		ReposDir: raw.Setups.ReposDir,
		RepoFile: raw.Setups.RepoFile,
		Default:  raw.Setups.Default,
	}

	s.WorktreePath = raw.Worktrees.Path
	s.LinearAPIKey = raw.Linear.APIKey

	return s, nil
}

// placeholder matches the {host}/{owner}/{repo} style substitutions a repo
// template is written with.
var placeholder = regexp.MustCompile(`\{[a-z_]+\}`)

// deriveProjectRoots turns repo templates into discovery roots, so the common
// case needs no [projects] section at all: "~/src/{host}/{owner}/{repo}"
// already says checkouts live two levels under ~/src, which is exactly what
// "~/src/*/*" tells the scanner.
//
// The {repo} segment is dropped rather than globbed because it is the checkout
// itself -- keeping it would make the scanner look for repositories one level
// inside each repository.
func deriveProjectRoots(templates []string) []string {
	var out []string
	seen := map[string]bool{}

	for _, tmpl := range templates {
		segments := strings.Split(filepath.ToSlash(strings.TrimSpace(tmpl)), "/")

		cut := -1
		for i, segment := range segments {
			if strings.Contains(segment, "{repo}") {
				cut = i
				break
			}
		}
		if cut <= 0 {
			// No {repo} segment, or it is the whole template: there is no
			// enclosing folder to scan, so this one contributes no root.
			continue
		}

		root := strings.Join(segments[:cut], "/")
		root = placeholder.ReplaceAllString(root, "*")
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, root)
	}
	return out
}

// expandTemplate applies the placeholders every template in this package
// understands, plus leading-~ expansion, and cleans the result.
func expandTemplate(tmpl string, t target.Target, extra map[string]string) string {
	s := tmpl
	if strings.HasPrefix(s, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			s = filepath.Join(home, strings.TrimPrefix(s, "~"))
		}
	}

	host := t.Host
	if host == "" {
		host = "github.com"
	}
	pairs := []string{"{host}", host, "{owner}", t.Owner, "{repo}", t.Repo}
	for k, v := range extra {
		pairs = append(pairs, k, v)
	}
	s = strings.NewReplacer(pairs...).Replace(s)
	return filepath.Clean(s)
}

// ResolveRepo finds the local checkout for a target that names a GitHub
// repository, trying each template in order and returning the first path
// that exists. tried is returned even on failure, since without it a mismatch
// between the templates and this machine's actual layout is unactionable.
func (s *Settings) ResolveRepo(t target.Target) (string, []string, error) {
	if t.Owner == "" || t.Repo == "" {
		// A Space named from a pasted string has no repository behind it;
		// asking where it lives is a programming error, not a lookup miss.
		return "", nil, fmt.Errorf("target %q has no repository to resolve", t.Text)
	}

	var tried []string
	for _, tmpl := range s.RepoTemplates {
		path := expandTemplate(tmpl, t, nil)
		tried = append(tried, path)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path, tried, nil
		}
	}
	return "", tried, fmt.Errorf("no repo template matched %s/%s (tried %v)", t.Owner, t.Repo, tried)
}

// ClonePath is where a repository that is not on this machine yet should be
// cloned to: the first configured template, expanded.
//
// Unlike ResolveRepo this does not care whether the path exists -- that is the
// whole point, since nothing is there yet. The first template wins because a
// list of templates is a list of places to *look*, and the one you would look
// in first is the one you would put a new checkout in.
func (s *Settings) ClonePath(t target.Target) (string, error) {
	if t.Owner == "" || t.Repo == "" {
		return "", fmt.Errorf("target %q has no repository to clone", t.Text)
	}
	if len(s.RepoTemplates) == 0 {
		return "", fmt.Errorf("no repos.templates configured to clone into")
	}
	return expandTemplate(s.RepoTemplates[0], t, nil), nil
}

// ResolveWorktreePath expands the configured worktree path template, if any.
// ok is false when nothing is configured, in which case the caller should
// omit the worktree's path entirely and let Herdr choose.
func (s *Settings) ResolveWorktreePath(t target.Target, repoRoot, branch string) (string, bool) {
	if s.WorktreePath == "" {
		return "", false
	}
	extra := map[string]string{
		"{repo_root}": repoRoot,
		// A branch can contain slashes, which is fine as nested directories,
		// but it still needs the rest of Sanitize's cleanup to be a safe path
		// segment (no leading dots, no doubled separators).
		"{branch}": target.Sanitize(branch),
	}
	return expandTemplate(s.WorktreePath, t, extra), true
}

// AgentKind is the configured agent kind, as a method so a package that only
// needs to name it does not have to import the whole Settings shape.
func (s *Settings) AgentKind() string { return s.Agent.Kind }
