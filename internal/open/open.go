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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/gh"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/setup"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// Session is the slice of the Herdr API this operation needs.
type Session interface {
	CreateWorktree(req herdr.WorktreeRequest) (herdr.Pane, string, error)
	OpenWorktree(req herdr.WorktreeRequest) (herdr.Pane, string, error)
	CreateWorkspace(cwd, label string, focus bool) (herdr.Pane, string, error)
	StartAgent(paneID, name, kind string, args []string) error
	WaitAgentIdle(paneID string) error
	WaitPaneOutput(paneID, value string, timeoutMs int) error
	AgentLaunched(paneID string) (bool, error)
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
//
// A marker has to be text the input draws and the startup screens do not, which
// is a stricter test than it sounds. Measured against a live codex on its first
// run in a fresh worktree:
//
//	t+0.3s  launch_pending=true    update-nag menu drawn
//	t+3.4s  launch_pending absent, interactive_ready=TRUE, still on the menu
//	...     trust prompt, still interactive_ready=true, input never drawn
//	        -- and only once past both does the footer appear
//
// So neither flag Herdr exposes is the answer for codex: launch_pending clears
// on process detection, and interactive_ready -- the field the obvious fix
// would reach for -- reports true while codex sits on its update nag and its
// "do you trust this directory" prompt. Both screens are also full of "›",
// which is codex's menu cursor as much as its input caret, so the caret is not
// a marker either.
//
// The footer codex draws under its input ("<model> · <cwd>", U+00B7) is absent
// from both gate screens and appeared within a second of the input in every
// run. That is what is waited for.
var readyMarkers = map[string]string{
	"claude": "❯",
	"codex":  " · ",
}

// readyMarkerTimeoutMs bounds the extra on-screen check. It runs after
// WaitAgentIdle already succeeded, so this is just confirming the render
// caught up -- generous, but far short of a whole startup budget.
const readyMarkerTimeoutMs = 30000

// sleep is the retry backoff, swapped out in tests so they do not spend real
// seconds waiting.
var sleep = time.Sleep

// now is the clock a bounded wait measures its budget against. It travels with
// sleep: a test that skips the waiting also has to skip the time, or a wait
// that is never going to succeed would still spend its whole budget.
var now = time.Now

// agent.start rejects a pane Herdr has only just built with agent_pane_busy:
// the shell exists but is not yet registered as an available target. Creating
// the Space and starting the agent in one breath is exactly the case that
// races it, so the busy answer is retried rather than surfaced. Only that one
// code is retried -- repeating a genuine rejection several times would just
// slow the failure down.
//
// agent_name_taken is the other rejection worth handling rather than
// surfacing. Agent names are global to Herdr, not scoped to a Space, so two
// concurrent runs of the same setup -- or the same target opened twice --
// derive the same name and the second one fails, leaving a bare shell where an
// agent should be. That is retried too, under a name disambiguated by the
// Space it is in.
const (
	startAgentBusyCode  = "agent_pane_busy"
	startAgentTakenCode = "agent_name_taken"
	startAgentAttempts  = 5
	startAgentBackoff   = 300 * time.Millisecond
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

// agentNameSuffix turns a Space id into something that can be glued onto an
// agent name. Herdr's ids ("w13") already pass, but nothing promises that, and
// a suffix that made the name invalid would trade one failure for another.
func agentNameSuffix(workspaceID string) string {
	suffix := agentNameInvalid.ReplaceAllString(strings.ToLower(strings.TrimSpace(workspaceID)), "-")
	return strings.Trim(suffix, "-_")
}

// agentNameIn disambiguates a name by the Space it belongs to:
// "codex-reviewer-3" becomes "codex-reviewer-3-w14". The Space id is used
// rather than a random hash because it is short, unique and still greppable --
// the name stays something you can read off a pane and match to a window.
//
// The length cap is spent on the base, not the suffix: a truncated suffix
// would not be the thing that makes the name unique. An empty Space id has
// nothing to disambiguate with, so the name is returned unchanged and the
// caller's retry is a no-op.
func agentNameIn(name, workspaceID string) string {
	suffix := agentNameSuffix(workspaceID)
	if suffix == "" {
		return name
	}
	if room := agentNameMaxLen - len(suffix) - 1; len(name) > room {
		if room <= 0 {
			return name
		}
		name = strings.Trim(name[:room], "-_")
	}
	if name == "" {
		name = agentNameFallback
	}
	return name + "-" + suffix
}

// startAgentWithRetry backs off linearly: the pane usually settles within the
// first interval, and the total budget stays comfortably under the time a
// person would wait before deciding the keypress did nothing.
//
// A name already in use is answered once, with the Space-qualified name, and
// then given the same busy retries -- the two rejections are independent, and
// a pane that is both freshly built and colliding should still end up with an
// agent in it.
func startAgentWithRetry(s Session, paneID, workspaceID, name, kind string, args []string) error {
	err := startAgentBusyRetry(s, paneID, name, kind, args)
	if !isAgentStartCode(err, startAgentTakenCode) {
		return err
	}
	qualified := agentNameIn(name, workspaceID)
	if qualified == name {
		return err
	}
	return startAgentBusyRetry(s, paneID, qualified, kind, args)
}

// startAgentBusyRetry is one name's worth of attempts.
func startAgentBusyRetry(s Session, paneID, name, kind string, args []string) error {
	var err error
	for attempt := 1; attempt <= startAgentAttempts; attempt++ {
		if err = s.StartAgent(paneID, name, kind, args); err == nil {
			return nil
		}
		if !isAgentStartCode(err, startAgentBusyCode) {
			return err
		}
		if attempt < startAgentAttempts {
			sleep(time.Duration(attempt) * startAgentBackoff)
		}
	}
	return err
}

// isAgentStartCode reports whether err is the named rejection from Herdr,
// rather than a transport failure or a different refusal.
func isAgentStartCode(err error, code string) bool {
	var apiErr *herdr.APIError
	return errors.As(err, &apiErr) && apiErr.Code == code
}

// PRLookup resolves a pull request's branch and title, an issue's title, and
// (see internal/open/stack.go) the stack of open pull requests a pull
// request belongs to. gh.Client implements this against the real gh CLI.
type PRLookup interface {
	LookupPR(owner, repo string, number int) (gh.PRInfo, error)
	LookupIssue(owner, repo string, number int) (gh.IssueInfo, error)
	// Stack resolves the chain of open pull requests number belongs to,
	// bottom first -- see gh.Client.Stack for the walk. Only a github_pr
	// setup naming for_each: layers ever calls this (resolveLists), so a
	// fake need not implement anything fancier than the chain a given test
	// wants back.
	Stack(owner, repo string, number int) ([]gh.StackPR, error)
	// Stacks resolves every path from number's chain to a tip -- see
	// gh.Client.Stacks. Unlike Stack it never refuses on a fork, which is
	// exactly why it, not Stack, is what answers "is this stacked" for
	// applies_to: [github_stack] matching (session.SetupRows -- see
	// setup.Subject.Stacked): matching is a yes/no question ("does any path
	// have 2+ layers"), and a fork must not fail it just because the
	// unrelated question of *which* chain to build for_each's layers list
	// from is ambiguous.
	Stacks(owner, repo string, number int) ([][]gh.StackPR, error)
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
	// Git's declared type is WorktreeGit (setup.go), a superset of Fetcher --
	// the whole-Space paths above only ever call FetchBranch, but a tab's own
	// worktree: (see applyWorktrees) needs the rest of it too, and gitcmd.New
	// already implements all of it. Widening the field, not adding a second
	// one, is what makes "one *gitcmd.Runner wired in main.go" still true.
	Git WorktreeGit
	// Layout is the extra slice of the API a setup needs: tabs, splits,
	// commands and focus. Only the setup path uses it, so everything else in
	// this package works with it nil.
	Layout Layout
	// Clone fetches a repository that is not on this machine yet. Only the
	// clone path uses it, so the rest of the package works with it nil.
	Clone Cloner
	// Cwd is where a Linear or plain target's Space is made. A Linear URL
	// carries no repository the way a GitHub PR URL does, so that target is
	// built wherever the caller already is -- the repo you're sitting in
	// when you paste the link.
	Cwd string
	// Progress is told what the run is doing as it does it, for a caller with
	// somewhere to show it. Nil everywhere else, which is most callers.
	Progress Progress
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
	// Setup replaces the single-agent step with a whole layout. It is set
	// when a setup was picked, and nil for the ordinary path -- which is why
	// nothing about this package's behaviour changes without one.
	Setup *setup.Setup
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

	// SetupName is the setup that built the Space, empty for the ordinary
	// single-agent path.
	SetupName string
	// SetupPanes is every pane a setup created, in the order the file listed
	// them, including the Space's own root pane.
	SetupPanes []string

	// Warnings are the things that went wrong without stopping the run: a
	// worktree that fell back to something other than what was asked for, a
	// pane whose agent never started, a prompt that was never sent. The Space
	// exists either way, which is exactly why these have to be said out loud
	// -- a half-built Space that reports success is the failure that costs
	// the most to work out afterwards.
	Warnings []string

	// SessionID is the agent session a handoff resumed. Empty for every other
	// path through this package, which all start something new.
	SessionID string
	// SessionWidened records that the session was found outside the directory
	// the command was run from, so the caller can report a pick the user did
	// not obviously ask for.
	SessionWidened bool
	// SessionModTime is when that session was last written, which is the only
	// useful thing to say about a session found by searching.
	SessionModTime time.Time
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

	done := deps.Progress.step("lookup", fmt.Sprintf("Looking up %s", tgt.Label()))
	info, err := deps.PRs.LookupPR(tgt.Owner, tgt.Repo, tgt.Number)
	done(err)
	if err != nil {
		return Outcome{}, err
	}

	done = deps.Progress.step("fetch", "Fetching "+info.Branch)
	err = deps.Git.FetchBranch(repoPath, info.Branch)
	done(err)
	if err != nil {
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

	pane, workspaceID, warnings, err := createOrOpenWorktreeReporting(deps.Session, deps.Progress, req)
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{
		Kind: tgt.Kind, Label: tgt.Label(), Branch: info.Branch, RepoPath: repoPath,
		WorkspaceID: workspaceID, PaneID: pane.PaneID, Warnings: warnings,
	}
	return runAgentStep(deps, cfg, tgt, opts, pane, promptData(tgt, info.Branch, info.Title), out)
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

	pane, workspaceID, warnings, err := createOrOpenWorktreeReporting(deps.Session, deps.Progress, req)
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{
		Kind: tgt.Kind, Label: tgt.Label(), Branch: branch, RepoPath: repoPath,
		WorkspaceID: workspaceID, PaneID: pane.PaneID, Warnings: warnings,
	}
	return runAgentStep(deps, cfg, tgt, opts, pane, promptData(tgt, branch, title), out)
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

	pane, workspaceID, warnings, err := createOrOpenWorktreeReporting(deps.Session, deps.Progress, req)
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{
		Kind: tgt.Kind, Label: tgt.Label(), Branch: branch, RepoPath: deps.Cwd,
		WorkspaceID: workspaceID, PaneID: pane.PaneID, Warnings: warnings,
	}
	return runAgentStep(deps, cfg, tgt, opts, pane, promptData(tgt, branch, ""), out)
}

func runPlain(deps Deps, cfg *config.Settings, tgt target.Target, opts Options) (Outcome, error) {
	done := deps.Progress.step("space", "Creating Space "+tgt.Label())
	pane, workspaceID, err := deps.Session.CreateWorkspace(deps.Cwd, tgt.Label(), true)
	done(err)
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{Kind: tgt.Kind, Label: tgt.Label(), RepoPath: deps.Cwd, WorkspaceID: workspaceID, PaneID: pane.PaneID}
	return runAgentStep(deps, cfg, tgt, opts, pane, promptData(tgt, "", ""), out)
}

// waitAgentDrawn blocks until the agent in paneID has actually drawn its
// input: idle first, then the kind's on-screen marker where one is known. It
// is the single-agent path's half of what startSetupAgent does per pane, and
// the two say the same thing about readiness for the same reasons.
func waitAgentDrawn(s Session, paneID, kind string) error {
	if err := s.WaitAgentIdle(paneID); err != nil {
		return fmt.Errorf("wait for agent: %w", err)
	}
	marker, ok := readyMarkers[kind]
	if !ok {
		return nil
	}
	if err := s.WaitPaneOutput(paneID, marker, readyMarkerTimeoutMs); err != nil {
		return fmt.Errorf("wait for agent to render its prompt: %w", err)
	}
	return nil
}

// createOrOpenWorktree tries to make a new worktree, and falls back to
// opening an existing one. worktree.create fails when the branch is already
// checked out somewhere; worktree.open is exactly the escape hatch for that,
// so a create failure is the trigger to try it rather than giving up.
//
// The fallback is reported rather than swallowed. Reusing a worktree that
// already exists is the good case and still worth a line -- but worktree.open
// can also land on the source checkout, which is a different branch than the
// one that was asked for, and a Space that quietly reviews main instead of
// the pull request is worse than one that failed outright.
func createOrOpenWorktree(s Session, req herdr.WorktreeRequest) (herdr.Pane, string, []string, error) {
	return createOrOpenWorktreeReporting(s, nil, req)
}

// createOrOpenWorktreeReporting is the same thing with somewhere to say so.
// Cutting a worktree is one of the two steps that can take real time on a big
// repository, so it is worth a line of its own even though it is a single call.
func createOrOpenWorktreeReporting(s Session, prog Progress, req herdr.WorktreeRequest) (herdr.Pane, string, []string, error) {
	done := prog.step("worktree", "Creating worktree "+req.Branch)
	pane, workspaceID, warnings, err := createOrOpenWorktreeInner(s, req)
	done(err)
	return pane, workspaceID, warnings, err
}

func createOrOpenWorktreeInner(s Session, req herdr.WorktreeRequest) (herdr.Pane, string, []string, error) {
	pane, workspaceID, err := s.CreateWorktree(req)
	if err == nil {
		return pane, workspaceID, nil, nil
	}
	createErr := err

	pane, workspaceID, err = s.OpenWorktree(req)
	if err != nil {
		return herdr.Pane{}, "", nil, fmt.Errorf("create worktree: %w; open worktree: %v", createErr, err)
	}
	return pane, workspaceID, worktreeFallbackWarnings(req, pane, createErr), nil
}

// worktreeFallbackWarnings says what falling back to worktree.open actually
// produced, in the terms someone reading it cares about: which branch the
// panes are on, not which call failed.
func worktreeFallbackWarnings(req herdr.WorktreeRequest, pane herdr.Pane, createErr error) []string {
	if landedOnSourceCheckout(req, pane) {
		return []string{fmt.Sprintf(
			"worktree.create failed (%v), so this Space opened on the source checkout at %s rather than a worktree for %s -- whatever runs here is not looking at that branch",
			createErr, pane.Cwd, req.Branch)}
	}
	return []string{fmt.Sprintf(
		"worktree.create failed (%v); reused the worktree already on disk instead", createErr)}
}

// landedOnSourceCheckout reports the degradation worth shouting about: the
// Space's own directory is the checkout the worktree was to be cut from, so
// it is on whatever branch that checkout happens to have.
//
// A Herdr that reports no cwd is taken at its word rather than guessed at:
// claiming the wrong branch would be its own kind of wrong.
func landedOnSourceCheckout(req herdr.WorktreeRequest, pane herdr.Pane) bool {
	if pane.Cwd == "" || req.Cwd == "" {
		return false
	}
	return filepath.Clean(pane.Cwd) == filepath.Clean(req.Cwd)
}

// spaceCwd is the directory a setup's tabs and splits are built in.
//
// It is the Space's own root pane's directory, not the checkout the Space was
// derived from: for a worktree target those are different, and using the
// checkout would put every tab and split of a pull-request layout on the
// source branch while the root pane sat in the worktree. RepoPath is only the
// fallback for a pane Herdr reported no cwd for.
func spaceCwd(root herdr.Pane, repoPath string) string {
	if strings.TrimSpace(root.Cwd) != "" {
		return root.Cwd
	}
	return repoPath
}

// runAgentStep is the last step of every path through this package: what
// actually fills the Space that was just made.
//
// It is one function rather than four so the toggle, the template and now the
// setup all behave identically whether the Space came from a pull request, an
// issue, a Linear ticket or a plain checkout. A setup replaces the
// single-agent body wholesale -- that substitution living here is why every
// target kind gained setups at once.
func runAgentStep(deps Deps, cfg *config.Settings, tgt target.Target, opts Options, root herdr.Pane, data map[string]string, out Outcome) (Outcome, error) {
	s := deps.Session
	paneID := root.PaneID

	if opts.Setup != nil {
		if deps.Layout == nil {
			return out, fmt.Errorf("setup %q needs a Herdr session to build panes in", opts.Setup.Name)
		}
		plan, panes, problems, err := applySetup(deps, cfg, tgt, *opts.Setup, root, out.WorkspaceID, spaceCwd(root, out.RepoPath), out.RepoPath, data)
		out.SetupName = plan.Name
		out.SetupPanes = panes
		out.Warnings = append(out.Warnings, problems...)
		if err != nil {
			return out, err
		}
		out.AgentStarted = setupStartsAnAgent(plan)
		return out, nil
	}

	enabled := cfg.Agent.Enabled
	if opts.Agent != nil {
		enabled = *opts.Agent
	}
	if !enabled {
		return out, nil
	}

	// No args: every path through here starts a fresh agent, and only the
	// handoff has a session to resume.
	done := deps.Progress.step("agent", "Starting "+cfg.Agent.Kind)
	err := startAgentWithRetry(s, paneID, out.WorkspaceID, agentName(tgt.Label()), cfg.Agent.Kind, nil)
	done(err)
	if err != nil {
		return out, fmt.Errorf("start agent: %w", err)
	}

	// Typing into a pane that is still on a startup banner would land in the
	// wrong place, so this waits for the agent to actually be ready. It is its
	// own step because it is the slow one -- an agent drawing its input is
	// seconds, and on a first run it can be much longer.
	done = deps.Progress.step("agent-ready", "Waiting for "+cfg.Agent.Kind)
	err = waitAgentDrawn(s, paneID, cfg.Agent.Kind)
	done(err)
	if err != nil {
		return out, err
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

	deps.Progress.mark("prompt", "Typing the prompt")
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
