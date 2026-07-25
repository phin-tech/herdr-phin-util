// Package open decides how to turn a pasted link or string into a Herdr
// Space, and optionally gets an agent started in it with a prompt ready to
// send.
//
// The three target kinds branch into genuinely different plumbing -- a pull
// request needs GitHub and a fetch, a Linear issue needs neither, a plain
// string needs no worktree at all -- so this package is the seam where that
// gets decided, kept independent of both the Herdr socket and the network so
// it can be tested against fakes.
package open

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/gh"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// Session is the slice of the Herdr API this operation needs.
type Session interface {
	CreateWorktree(req herdr.WorktreeRequest) (herdr.Pane, string, error)
	OpenWorktree(req herdr.WorktreeRequest) (herdr.Pane, string, error)
	CreateWorkspace(cwd, label string, focus bool) (herdr.Pane, string, error)
	StartAgent(paneID, name, kind string) error
	WaitAgentIdle(paneID string) error
	WaitPaneOutput(paneID, value string, timeoutMs int) error
	SendText(paneID, text string) error
}

// readyMarkers is on-screen text that means "this agent kind is actually
// showing its input prompt", verified by starting the kind in a scratch pane
// and reading it back -- not guessed. agent_status can report idle during a
// startup lull before anything has been drawn, so a kind with a marker here
// gets one extra, concrete check before a prompt is typed into it. A kind
// missing from this map falls back to agent_status alone: waiting for text
// that turns out to be wrong would hang the action rather than just mistime
// it, and hanging is worse.
var readyMarkers = map[string]string{
	"claude": "❯",
}

// readyMarkerTimeoutMs bounds the extra on-screen check. It runs after
// WaitAgentIdle already succeeded, so this is just confirming the render
// caught up -- generous, but far short of a whole startup budget.
const readyMarkerTimeoutMs = 30000

// sleep is the retry backoff, swapped out in tests so they do not spend real
// seconds waiting.
var sleep = time.Sleep

// agent.start rejects a pane Herdr has only just built with agent_pane_busy:
// the shell exists but is not yet registered as an available target. Creating
// the Space and starting the agent in one breath is exactly the case that
// races it, so the busy answer is retried rather than surfaced. Only that one
// code is retried -- repeating a genuine rejection several times would just
// slow the failure down.
const (
	startAgentBusyCode = "agent_pane_busy"
	startAgentAttempts = 5
	startAgentBackoff  = 300 * time.Millisecond
)

// agent.start validates its name: it must start with a lowercase letter and
// hold only lowercase letters, digits, '-' or '_', up to 32 characters. A
// Space label satisfies almost none of that -- "roux#42", "ENG-123",
// "roux/feature" and "scratch space" are all rejected -- so the label is a
// display name and has to be converted before it can also be an agent name.
const (
	agentNameMaxLen = 32
	// agentNameFallback is used when a label sanitises away to nothing, which
	// a label made entirely of punctuation does.
	agentNameFallback = "agent"
)

var agentNameInvalid = regexp.MustCompile(`[^a-z0-9_-]+`)

// agentName turns a Space label into something agent.start will accept,
// keeping it recognisable: "roux#42" becomes "roux-42" rather than a hash.
func agentName(label string) string {
	name := agentNameInvalid.ReplaceAllString(strings.ToLower(strings.TrimSpace(label)), "-")

	// Collapse the runs the replacement above can produce, so "a / b" does not
	// become "a---b".
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	name = strings.Trim(name, "-_")

	// The first character has to be a letter. A leading digit is stripped only
	// when something is left over that still identifies the Space; otherwise a
	// prefix keeps the name whole, since "fa-service" would be a worse answer
	// than "agent-2fa-service" for a repo actually called 2fa-service.
	if name == "" {
		name = agentNameFallback
	} else if name[0] < 'a' || name[0] > 'z' {
		name = agentNameFallback + "-" + name
	}

	if len(name) > agentNameMaxLen {
		name = strings.Trim(name[:agentNameMaxLen], "-_")
	}
	if name == "" {
		return agentNameFallback
	}
	return name
}

// startAgentWithRetry backs off linearly: the pane usually settles within the
// first interval, and the total budget stays comfortably under the time a
// person would wait before deciding the keypress did nothing.
func startAgentWithRetry(s Session, paneID, name, kind string) error {
	var err error
	for attempt := 1; attempt <= startAgentAttempts; attempt++ {
		if err = s.StartAgent(paneID, name, kind); err == nil {
			return nil
		}
		var apiErr *herdr.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != startAgentBusyCode {
			return err
		}
		if attempt < startAgentAttempts {
			sleep(time.Duration(attempt) * startAgentBackoff)
		}
	}
	return err
}

// PRLookup resolves a pull request's branch and title, and an issue's title.
// gh.Client implements this against the real gh CLI.
type PRLookup interface {
	LookupPR(owner, repo string, number int) (gh.PRInfo, error)
	LookupIssue(owner, repo string, number int) (gh.IssueInfo, error)
}

// Fetcher makes a remote branch available locally. gitcmd.Runner implements
// this against the real git binary.
type Fetcher interface {
	FetchBranch(repoPath, branch string) error
}

// Deps bundles everything Run needs from the outside world.
type Deps struct {
	Session Session
	PRs     PRLookup
	Git     Fetcher
	// Clone fetches a repository that is not on this machine yet. Only the
	// clone path uses it, so the rest of the package works with it nil.
	Clone Cloner
	// Cwd is where a Linear or plain target's Space is made. A Linear URL
	// carries no repository the way a GitHub PR URL does, so that target is
	// built wherever the caller already is -- the repo you're sitting in
	// when you paste the link.
	Cwd string
}

// Options carries the popup's (or CLI flags') overrides of the config
// defaults.
type Options struct {
	// Agent overrides cfg.Agent.Enabled when non-nil.
	Agent *bool
	// Prompt overrides the rendered template text outright when non-empty:
	// it is what the user edited the box to say, so the template does not
	// get a vote.
	Prompt string
}

// Outcome reports what Run did.
type Outcome struct {
	Kind        target.Kind
	Label       string
	Branch      string
	RepoPath    string
	WorkspaceID string
	PaneID      string

	AgentStarted bool
	// PromptSent is the exact text typed into the pane, empty when the agent
	// step did not run.
	PromptSent string
}

// Run parses input and builds the Space it describes.
func Run(deps Deps, cfg *config.Settings, input string, opts Options) (Outcome, error) {
	tgt := target.Parse(input)

	switch tgt.Kind {
	case target.KindGitHubPR:
		return runGitHubPR(deps, cfg, tgt, opts)
	case target.KindGitHubIssue:
		return runGitHubIssue(deps, cfg, tgt, opts)
	case target.KindLinear:
		return runLinear(deps, cfg, tgt, opts)
	case target.KindGitHubRepo:
		return RunClone(deps, cfg, tgt, opts)
	default:
		return runPlain(deps, cfg, tgt, opts)
	}
}

func runGitHubPR(deps Deps, cfg *config.Settings, tgt target.Target, opts Options) (Outcome, error) {
	// Resolved before any network call: there is no point asking GitHub
	// about a PR when the checkout is not even findable on this machine.
	// ResolveRepo's error already lists what it tried, so it is returned as
	// it stands rather than wrapped with the same paths a second time.
	repoPath, _, err := cfg.ResolveRepo(tgt)
	if err != nil {
		return Outcome{}, err
	}

	info, err := deps.PRs.LookupPR(tgt.Owner, tgt.Repo, tgt.Number)
	if err != nil {
		return Outcome{}, err
	}

	if err := deps.Git.FetchBranch(repoPath, info.Branch); err != nil {
		return Outcome{}, err
	}

	path, _ := cfg.ResolveWorktreePath(tgt, repoPath, info.Branch)
	req := herdr.WorktreeRequest{
		Cwd:    repoPath,
		Branch: info.Branch,
		// Fetch just brought origin/<branch> up to date; basing the new
		// local branch there is what makes it match the PR exactly.
		Base:  "origin/" + info.Branch,
		Path:  path,
		Label: tgt.Label(),
		Focus: true,
	}

	pane, workspaceID, err := createOrOpenWorktree(deps.Session, req)
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{
		Kind: tgt.Kind, Label: tgt.Label(), Branch: info.Branch, RepoPath: repoPath,
		WorkspaceID: workspaceID, PaneID: pane.PaneID,
	}
	return runAgentStep(deps.Session, cfg, tgt, opts, pane.PaneID, promptData(tgt, info.Branch, info.Title), out)
}

// runGitHubIssue sits between the other two: an issue URL names its own
// repository the way a pull request does, but names no branch, so one is
// derived the way a Linear issue's is.
//
// The title is a nicety, not a requirement -- a gh failure downgrades the
// branch from "99-fix-the-thing" to "issue-99" rather than failing the whole
// action, since the Space is still exactly the one that was asked for.
func runGitHubIssue(deps Deps, cfg *config.Settings, tgt target.Target, opts Options) (Outcome, error) {
	repoPath, _, err := cfg.ResolveRepo(tgt)
	if err != nil {
		return Outcome{}, err
	}

	var title string
	if info, err := deps.PRs.LookupIssue(tgt.Owner, tgt.Repo, tgt.Number); err == nil {
		title = info.Title
		tgt.Slug = info.Title
	}

	branch := tgt.Branch()
	path, _ := cfg.ResolveWorktreePath(tgt, repoPath, branch)
	req := herdr.WorktreeRequest{
		Cwd:    repoPath,
		Branch: branch,
		// No base: the work has not started, so it begins wherever the source
		// checkout already is.
		Path:  path,
		Label: tgt.Label(),
		Focus: true,
	}

	pane, workspaceID, err := createOrOpenWorktree(deps.Session, req)
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{
		Kind: tgt.Kind, Label: tgt.Label(), Branch: branch, RepoPath: repoPath,
		WorkspaceID: workspaceID, PaneID: pane.PaneID,
	}
	return runAgentStep(deps.Session, cfg, tgt, opts, pane.PaneID, promptData(tgt, branch, title), out)
}

func runLinear(deps Deps, cfg *config.Settings, tgt target.Target, opts Options) (Outcome, error) {
	if deps.Cwd == "" {
		return Outcome{}, fmt.Errorf("no working directory to build a worktree in -- run this from inside the repo the issue belongs to")
	}

	branch := tgt.Branch()
	path, _ := cfg.ResolveWorktreePath(tgt, deps.Cwd, branch)
	req := herdr.WorktreeRequest{
		Cwd:    deps.Cwd,
		Branch: branch,
		// No base: this branch does not exist anywhere yet, so it starts
		// from whatever HEAD already is.
		Path:  path,
		Label: tgt.Label(),
		Focus: true,
	}

	pane, workspaceID, err := createOrOpenWorktree(deps.Session, req)
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{
		Kind: tgt.Kind, Label: tgt.Label(), Branch: branch, RepoPath: deps.Cwd,
		WorkspaceID: workspaceID, PaneID: pane.PaneID,
	}
	return runAgentStep(deps.Session, cfg, tgt, opts, pane.PaneID, promptData(tgt, branch, ""), out)
}

func runPlain(deps Deps, cfg *config.Settings, tgt target.Target, opts Options) (Outcome, error) {
	pane, workspaceID, err := deps.Session.CreateWorkspace(deps.Cwd, tgt.Label(), true)
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{Kind: tgt.Kind, Label: tgt.Label(), RepoPath: deps.Cwd, WorkspaceID: workspaceID, PaneID: pane.PaneID}
	return runAgentStep(deps.Session, cfg, tgt, opts, pane.PaneID, promptData(tgt, "", ""), out)
}

// createOrOpenWorktree tries to make a new worktree, and falls back to
// opening an existing one. worktree.create fails when the branch is already
// checked out somewhere; worktree.open is exactly the escape hatch for that,
// so a create failure is the trigger to try it rather than giving up.
func createOrOpenWorktree(s Session, req herdr.WorktreeRequest) (herdr.Pane, string, error) {
	pane, workspaceID, err := s.CreateWorktree(req)
	if err == nil {
		return pane, workspaceID, nil
	}
	createErr := err

	pane, workspaceID, err = s.OpenWorktree(req)
	if err != nil {
		return herdr.Pane{}, "", fmt.Errorf("create worktree: %w; open worktree: %v", createErr, err)
	}
	return pane, workspaceID, nil
}

// runAgentStep starts the configured agent and types (but does not submit)
// the rendered prompt, when enabled. It is shared by all three target kinds
// so the toggle and template logic exist in exactly one place.
func runAgentStep(s Session, cfg *config.Settings, tgt target.Target, opts Options, paneID string, data map[string]string, out Outcome) (Outcome, error) {
	enabled := cfg.Agent.Enabled
	if opts.Agent != nil {
		enabled = *opts.Agent
	}
	if !enabled {
		return out, nil
	}

	if err := startAgentWithRetry(s, paneID, agentName(tgt.Label()), cfg.Agent.Kind); err != nil {
		return out, fmt.Errorf("start agent: %w", err)
	}
	// Typing into a pane that is still on a startup banner would land in the
	// wrong place, so this waits for the agent to actually be ready.
	if err := s.WaitAgentIdle(paneID); err != nil {
		return out, fmt.Errorf("wait for agent: %w", err)
	}
	if marker, ok := readyMarkers[cfg.Agent.Kind]; ok {
		if err := s.WaitPaneOutput(paneID, marker, readyMarkerTimeoutMs); err != nil {
			return out, fmt.Errorf("wait for agent to render its prompt: %w", err)
		}
	}

	prompt := opts.Prompt
	if prompt == "" {
		rendered, err := renderPrompt(cfg.Prompts.For(tgt.Kind), data)
		if err != nil {
			return out, fmt.Errorf("render prompt: %w", err)
		}
		prompt = rendered
	}

	if strings.TrimSpace(prompt) == "" {
		// Nothing to type. A project has no configured prompt by default, and
		// leaving the agent sitting on a clean input is the desired end state
		// -- sending it whitespace would be a worse version of doing nothing.
		out.AgentStarted = true
		return out, nil
	}

	if err := s.SendText(paneID, prompt); err != nil {
		return out, fmt.Errorf("send prompt: %w", err)
	}

	out.AgentStarted = true
	out.PromptSent = prompt
	return out, nil
}

// PreviewPrompt renders the prompt a target would get, without running
// anything. It exists for the popup: a PR's real branch and title are not
// knowable without gh, so this renders those fields empty rather than
// blocking the UI on a network call for every keystroke.
func PreviewPrompt(cfg *config.Settings, tgt target.Target) (string, error) {
	// tgt.Branch() is only non-empty for Linear -- a PR's is unknown until
	// gh is asked, which is exactly the case this preview must not block on.
	return renderPrompt(cfg.Prompts.For(tgt.Kind), promptData(tgt, tgt.Branch(), ""))
}

// promptData is what a prompt template renders against: the target's own
// fields plus whatever this run resolved (a PR's branch and title come from
// gh, not from the URL). Every value is a string, including Number, so that a
// map lookup's zero value -- what a missing field falls back to -- is ""
// rather than an untyped nil that would print as "<no value>".
func promptData(t target.Target, branch, title string) map[string]string {
	return map[string]string{
		"URL":    t.URL,
		"Host":   t.Host,
		"Owner":  t.Owner,
		"Repo":   t.Repo,
		"Number": strconv.Itoa(t.Number),
		"Title":  title,
		"Issue":  t.Issue,
		"Slug":   t.Slug,
		"Branch": branch,
		"Text":   t.Text,
	}
}

// renderPrompt executes a prompt template against a plain map rather than a
// struct, with missingkey=zero: a typo'd placeholder in someone's config then
// renders as empty text instead of aborting the whole action.
func renderPrompt(tmplText string, data map[string]string) (string, error) {
	tmpl, err := template.New("prompt").Option("missingkey=zero").Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("parse prompt template: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute prompt template: %w", err)
	}
	return buf.String(), nil
}
