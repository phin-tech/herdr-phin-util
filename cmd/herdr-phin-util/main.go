// Command herdr-phin-util is a grab bag of small Herdr conveniences.
//
// Each feature is a subcommand, so one binary and one plugin manifest cover
// everything rather than a repo per idea.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/discovery"
	"github.com/phin-tech/herdr-phin-util/internal/gh"
	"github.com/phin-tech/herdr-phin-util/internal/gitcmd"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/open"
	"github.com/phin-tech/herdr-phin-util/internal/plugin"
	"github.com/phin-tech/herdr-phin-util/internal/promote"
	"github.com/phin-tech/herdr-phin-util/internal/session"
	"github.com/phin-tech/herdr-phin-util/internal/setup"
	"github.com/phin-tech/herdr-phin-util/internal/ui"
	"github.com/phin-tech/herdr-phin-util/internal/version"
)

const usage = `herdr-phin-util -- small Herdr utilities

usage:
  herdr-phin-util open <link-or-text> [--agent|--no-agent] [--prompt TEXT]
                         [--setup NAME] [--dry-run]
                                       make the workspace a pasted link describes
  herdr-phin-util popup                open the "smart workspace maker" popup
  herdr-phin-util pick                 open the project picker popup
  herdr-phin-util pick-worktree        open the picker inside the current repo
  herdr-phin-util projects             list the checkouts the picker would offer
  herdr-phin-util worktrees            list the current repo's worktrees and branches
  herdr-phin-util setups [--repo DIR]  list the setups defined for a checkout
  herdr-phin-util project <dir> [--agent|--no-agent] [--setup NAME] [--dry-run]
                                       open a Space on a checkout
  herdr-phin-util promote [pane_id]    move a pane into a Space of its own
  herdr-phin-util handoff [--session ID] [--label TEXT] [--cwd PATH]
                         [--dry-run] [--force]
                                       resume a Claude session in a new Space
  herdr-phin-util version

promote targets the focused pane when no id is given.

open recognises a GitHub pull request or issue URL, a repository reference, a
Linear issue URL, or anything else (used as a plain Space name).
--agent/--no-agent override the config's agent.enabled; --prompt overrides the
rendered template text outright.

pick lists the Spaces already open followed by every checkout found under
[projects].roots that has no Space yet: switch to the first, create the
second. tab on a repo descends into its worktrees and branches, and shift+tab
comes back; pick-worktree starts there for the repo you are already in.

projects, worktrees and setups print what each level would offer without
opening anything, which is the way to check a roots setting, a branch list, or
why a setup is not being offered.

A setup is a YAML recipe -- tabs, panes, agents, prompts and commands -- read
from setups/ and repos/<repo>/ in the plugin config directory, and from
.herdr-setups.yaml inside a checkout. --setup applies one to whatever is being
opened; --dry-run prints what it would build and touches nothing. In the
picker, tab on a worktree or link row opens the setup list, and ctrl+t does so
from any row.

handoff is run from a Claude session that started outside Herdr. It opens a
Space on the same directory and resumes the same conversation there. It is not
a move -- the original process stays where it is and has to be quit -- so
inside Herdr use promote instead, which moves the live pane.

It finds the session from $CLAUDE_CODE_SESSION_ID, then the newest transcript
for this directory, then the newest anywhere -- and when it has to reach that
far it says so and opens the Space in the session's own directory. --dry-run
prints which session it would take without opening anything.
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch args[0] {
	case "open":
		os.Exit(runOpen(args[1:]))
	case "popup":
		os.Exit(runPopup())
	case "pick":
		os.Exit(runPick())
	case "projects":
		os.Exit(runProjects())
	case "pick-worktree":
		os.Exit(runPickWorktree())
	case "worktrees":
		os.Exit(runWorktrees())
	case "setups":
		os.Exit(runSetups(args[1:]))
	case "project":
		os.Exit(runProject(args[1:]))
	case "handoff":
		os.Exit(runHandoff(args[1:]))
	case "promote":
		var target string
		if len(args) > 1 {
			target = args[1]
		}
		os.Exit(runPromote(target))
	case "version":
		fmt.Println(version.Version)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[0], usage)
		os.Exit(2)
	}
}

// runOpen is the plain, testable-from-a-shell entry point for the "smart
// workspace maker": everything the popup does, without the popup.
func runOpen(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: herdr-phin-util open <link-or-text> [--agent|--no-agent] [--prompt TEXT] [--setup NAME] [--dry-run]")
		return 2
	}
	input := args[0]

	var agentOverride *bool
	var promptOverride string
	var setupName string
	var dryRun bool
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			v := true
			agentOverride = &v
		case "--no-agent":
			v := false
			agentOverride = &v
		case "--prompt":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "--prompt needs a value")
				return 2
			}
			promptOverride = args[i]
		case "--setup":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "--setup needs a name")
				return 2
			}
			setupName = args[i]
		case "--dry-run":
			dryRun = true
		default:
			fmt.Fprintf(os.Stderr, "unknown flag %q\n", args[i])
			return 2
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, p := range cfg.Problems {
		fmt.Fprintln(os.Stderr, "config:", p)
	}

	// Setups are resolved before Herdr is reached: a typo'd name should fail
	// before anything is built, not after.
	chosen, err := resolveSetup(cfg, invocationCwd(), setupName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if dryRun {
		if chosen == nil {
			fmt.Fprintln(os.Stderr, "--dry-run only has something to show with --setup")
			return 2
		}
		// No Herdr client: a preview that needed a session would be useless in
		// the place you most want one, which is while writing the file.
		plan, _, err := open.PreviewSetup(previewDeps(), cfg, input, *chosen)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		printPlan(plan)
		return 0
	}

	client, err := herdr.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	out, err := open.Run(openDeps(client), cfg, input, open.Options{Agent: agentOverride, Prompt: promptOverride, Setup: chosen})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		// The agent step runs after the Space exists, so a failure there
		// leaves a perfectly good Space behind. Saying it was not created
		// would send you looking for something that is actually sitting
		// there, so the two cases are reported apart.
		if out.WorkspaceID != "" {
			fmt.Printf("Space %s (%s) was created; the agent step failed\n", out.Label, out.WorkspaceID)
			_ = client.Notify("Space ready, agent failed", err.Error())
		} else {
			_ = client.Notify("Workspace not created", err.Error())
		}
		return 1
	}

	fmt.Printf("opened %q in Space %s (pane %s)\n", out.Label, out.WorkspaceID, out.PaneID)
	if out.Branch != "" {
		fmt.Printf("branch: %s\n", out.Branch)
	}
	reportSetupOrAgent(out)
	_ = client.Notify("Space ready", out.Label)
	return 0
}

// runPopup drives the same open.Run this package's "open" subcommand uses,
// through a small bubbletea UI instead of flags -- the two are just different
// front ends onto one decision layer.
func runPopup() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	client, err := herdr.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	m := ui.New(cfg, openDeps(client))
	p := tea.NewProgram(m, tea.WithMouseCellMotion())
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	out, runErr, done := final.(*ui.Model).Result()
	if !done {
		// Cancelled with esc/ctrl+c before anything ran: quitting quietly is
		// the whole point of a popup you can back out of.
		return 0
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		_ = client.Notify("Workspace not created", runErr.Error())
		return 1
	}
	_ = client.Notify("Space ready", out.Label)
	return 0
}

// openDeps is the same wiring every subcommand that builds a Space needs.
func openDeps(client *herdr.Client) open.Deps {
	return open.Deps{
		Session: client,
		Layout:  client,
		PRs:     gh.New(),
		Git:     gitcmd.New(),
		Clone:   gh.New(),
		Cwd:     invocationCwd(),
	}
}

// previewDeps is openDeps without the Herdr half, for --dry-run. Everything a
// preview reads -- a PR's branch and title -- comes from gh, not from a
// session, so it works from a plain terminal.
func previewDeps() open.Deps {
	return open.Deps{
		PRs: gh.New(),
		Git: gitcmd.New(),
		Cwd: invocationCwd(),
	}
}

// reportSetupOrAgent says what ended up in the Space. A setup and the
// single-agent path deserve different lines: "prompt typed and waiting" is
// wrong for a layout where some prompts were sent and some were not.
func reportSetupOrAgent(out open.Outcome) {
	if out.SetupName != "" {
		fmt.Printf("setup %s built %d pane%s\n", out.SetupName, len(out.SetupPanes), plural(len(out.SetupPanes)))
		return
	}
	if out.AgentStarted {
		fmt.Println("agent started; prompt typed and waiting for you to send it")
	}
}

// runPick opens the project picker: Spaces already open, then checkouts that
// have none yet.
//
// The candidate list is built before the Program starts. A popup that opened
// and then reported it could not reach Herdr would be a worse way to say the
// same thing, and there is nothing to interact with in that state anyway.
func runPick() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	client, err := herdr.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	candidates, err := session.List(client, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = client.Notify("Picker failed", err.Error())
		return 1
	}
	if len(candidates) == 0 {
		const msg = "no Spaces open and no checkouts found -- check [projects].roots"
		fmt.Fprintln(os.Stderr, msg)
		_ = client.Notify("Nothing to pick", msg)
		return 1
	}

	// The raw Space list is handed over as well, so a pasted link can be
	// matched against it by label without another round trip.
	workspaces, err := client.Workspaces()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	p := tea.NewProgram(
		ui.NewPicker(cfg, pickerDeps(client, cfg), client, candidates).WithWorkspaces(workspaces),
		tea.WithMouseCellMotion(),
	)
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return reportPick(client, final.(*ui.Picker))
}

// pickerDeps wires every level: Herdr for Spaces and worktrees, git for
// branches, and the config directory for setups.
func pickerDeps(client *herdr.Client, cfg *config.Settings) session.Deps {
	return session.Deps{
		Herdr:     client,
		Open:      openDeps(client),
		Worktrees: client,
		Git:       gitcmd.New(),
		Setups: func(repoPath string) []setup.Setup {
			return loadSetups(cfg, repoPath)
		},
	}
}

// runPickWorktree opens the picker already inside the repo the action was
// fired from -- the "branches of what I am looking at" key, as opposed to
// descending from the project list.
func runPickWorktree() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	client, err := herdr.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	cwd := invocationCwd()
	if cwd == "" {
		const msg = "no working directory to resolve a repository from"
		fmt.Fprintln(os.Stderr, msg)
		_ = client.Notify("No repository", msg)
		return 1
	}

	candidates, repo, err := session.ListWorktrees(client, gitcmd.New(), cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = client.Notify("Not a repository", err.Error())
		return 1
	}

	p := tea.NewProgram(
		ui.NewWorktreePicker(cfg, pickerDeps(client, cfg), client, repo, candidates),
		tea.WithMouseCellMotion(),
	)
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return reportPick(client, final.(*ui.Picker))
}

// reportPick turns a finished picker into an exit code and a notification. It
// is shared by both entry points so they cannot disagree about what a
// cancelled popup means.
func reportPick(client *herdr.Client, p *ui.Picker) int {
	out, picked, runErr, done := p.Result()
	if !done {
		// Backed out with esc before picking anything.
		return 0
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		_ = client.Notify("Could not open", runErr.Error())
		return 1
	}
	if picked.Kind == session.KindSpace {
		// Switching is its own confirmation -- you are looking at the result.
		return 0
	}
	_ = client.Notify("Space ready", out.Label)
	return 0
}

// runWorktrees prints the worktree level's rows without opening anything, the
// same debugging role "projects" plays for the level above.
func runWorktrees() int {
	client, err := herdr.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	cwd := invocationCwd()
	candidates, repo, err := session.ListWorktrees(client, gitcmd.New(), cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Printf("%s (default branch: %s)\n", repo.Name, orNone(repo.DefaultBranch))
	for _, c := range candidates {
		fmt.Printf("  %-14s %-40s %s\n", c.Kind, c.Label, c.Detail)
	}
	return 0
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// runProjects prints what discovery found and nothing else. This is the way to
// check a [projects].roots setting: it needs no Herdr session, so it answers
// the "why is my repo not in the list" question directly.
func runProjects() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, p := range cfg.Problems {
		fmt.Fprintln(os.Stderr, "config:", p)
	}

	paths := discovery.List(cfg.Projects.Roots, discovery.Options{
		GitOnly: cfg.Projects.GitOnly,
		Depth:   cfg.Projects.Depth,
	})
	if len(paths) == 0 {
		fmt.Fprintf(os.Stderr, "no checkouts found under %v (git_only=%t, depth=%d)\n",
			cfg.Projects.Roots, cfg.Projects.GitOnly, cfg.Projects.Depth)
		return 1
	}
	for _, path := range paths {
		fmt.Println(path)
	}
	return 0
}

// runProject is the picker's outcome without the picker -- the shell-testable
// half, same as "open" is for the workspace maker.
func runProject(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: herdr-phin-util project <dir> [--agent|--no-agent] [--setup NAME] [--dry-run]")
		return 2
	}
	dir := args[0]

	var agentOverride *bool
	var setupName string
	var dryRun bool
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			v := true
			agentOverride = &v
		case "--no-agent":
			v := false
			agentOverride = &v
		case "--setup":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "--setup needs a name")
				return 2
			}
			setupName = args[i]
		case "--dry-run":
			dryRun = true
		default:
			fmt.Fprintf(os.Stderr, "unknown flag %q\n", args[i])
			return 2
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, p := range cfg.Problems {
		fmt.Fprintln(os.Stderr, "config:", p)
	}

	// The checkout being opened is what a setup is looked up against here,
	// rather than wherever the command was run from.
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	chosen, err := resolveSetup(cfg, abs, setupName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if dryRun {
		if chosen == nil {
			fmt.Fprintln(os.Stderr, "--dry-run only has something to show with --setup")
			return 2
		}
		deps := previewDeps()
		deps.Cwd = abs
		plan, _, err := open.PreviewSetup(deps, cfg, filepath.Base(abs), *chosen)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		printPlan(plan)
		return 0
	}

	client, err := herdr.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	out, err := open.RunProject(openDeps(client), cfg, dir, open.Options{Agent: agentOverride, Setup: chosen})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		// Same split as runOpen: a Space that exists but whose agent step
		// failed is not the same as no Space at all.
		if out.WorkspaceID != "" {
			fmt.Printf("Space %s (%s) was created; the agent step failed\n", out.Label, out.WorkspaceID)
			_ = client.Notify("Space ready, agent failed", err.Error())
		} else {
			_ = client.Notify("Workspace not created", err.Error())
		}
		return 1
	}

	fmt.Printf("opened %q in Space %s (pane %s)\n", out.Label, out.WorkspaceID, out.PaneID)
	reportSetupOrAgent(out)
	_ = client.Notify("Space ready", out.Label)
	return 0
}

// invocationCwd is where a Linear or plain target's Space gets built: the
// directory the action was fired from, or -- run by hand -- wherever the
// shell already is. The injected context is preferred over $PWD because it
// is resolved against the pane the user was looking at, not the pane this
// process happens to inherit its cwd from.
func invocationCwd() string {
	if ctx := plugin.FromEnv(); ctx != nil && ctx.FocusedPaneCwd != "" {
		return ctx.FocusedPaneCwd
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// runHandoff resumes the Claude session the caller is sitting in inside a new
// Space.
//
// Unlike everything else here it is meant to be run from *outside* Herdr, so
// there is no plugin action for it and no notification-only path: there is
// always a terminal watching, and that terminal is the one being left behind.
func runHandoff(args []string) int {
	var opts open.HandoffOptions
	var force, dryRun bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force":
			force = true
		case "--dry-run":
			dryRun = true
		case "--session", "--label", "--cwd":
			flag := args[i]
			i++
			if i >= len(args) {
				fmt.Fprintf(os.Stderr, "%s needs a value\n", flag)
				return 2
			}
			switch flag {
			case "--session":
				opts.SessionID = args[i]
			case "--label":
				opts.Label = args[i]
			case "--cwd":
				opts.Cwd = args[i]
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown flag %q\n", args[i])
			return 2
		}
	}

	// A dry run answers "which conversation would you take?" and stops. It
	// deliberately runs before the Herdr connection and before the guard
	// below: neither has anything to do with the question being asked, and
	// the answer is just as useful from inside Herdr as outside it.
	if dryRun {
		plan, err := open.PlanHandoff(open.Deps{Cwd: invocationCwd()}, opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		// The environment names a session without saying anything about when
		// it was last touched, so the age is genuinely unknown there rather
		// than zero.
		if plan.ModTime.IsZero() {
			fmt.Printf("would resume %s\n", shortSessionID(plan.SessionID))
		} else {
			fmt.Printf("would resume %s (%s)\n", shortSessionID(plan.SessionID), ago(plan.ModTime))
		}
		fmt.Printf("        into %s, Space %q\n", tildePath(plan.Cwd), plan.Label)
		if plan.Widened {
			fmt.Println("        found outside this directory -- the Space follows the session")
		}
		return 0
	}

	// Inside Herdr, handing off *this* session is the wrong tool. promote
	// relocates the live pane -- same PID, same scrollback, same session --
	// where this can only resume a transcript into a new process. Silently
	// doing the worse thing when the better one is available would be a bad
	// trade to make on someone's behalf.
	//
	// An explicit --session is a different request: it names some other
	// conversation, which promote cannot reach at all.
	if os.Getenv("HERDR_ENV") == "1" && opts.SessionID == "" && !force {
		fmt.Fprintln(os.Stderr, "this session is already inside Herdr; use `promote` instead --")
		fmt.Fprintln(os.Stderr, "it moves the live pane rather than resuming a copy of it.")
		fmt.Fprintln(os.Stderr, "pass --force if you really want a second, resumed session.")
		return 2
	}

	client, err := herdr.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	out, err := open.RunHandoff(openDeps(client), opts)
	reportWidenedSession(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		// Same split as runOpen and runProject: a Space that exists but whose
		// agent step failed is not the same as no Space at all.
		if out.WorkspaceID != "" {
			fmt.Printf("Space %s (%s) was created; resuming the session failed\n", out.Label, out.WorkspaceID)
			_ = client.Notify("Space ready, handoff failed", err.Error())
		} else {
			_ = client.Notify("Handoff failed", err.Error())
		}
		return 1
	}

	fmt.Printf("resumed in Space %s (pane %s)\n", out.WorkspaceID, out.PaneID)
	// The whole point of saying this: the transcript moved, the process did
	// not. Two Claudes appending to one session file will diverge, and the
	// one still running here is the one that has to go.
	fmt.Println("this session is now the old copy -- quit it with /exit")
	if os.Getenv("CLAUDECODE") == "1" {
		fmt.Println("(you are inside that session's own shell, so it is very much still running)")
	}
	_ = client.Notify("Session handed off", out.Label)
	return 0
}

// reportWidenedSession announces a session found outside the directory the
// command was run from.
//
// Widening is a guess, and an invisible guess is the bad kind: the whole
// point of printing it is that "that is not the conversation I meant" should
// be obvious immediately rather than after reading the transcript.
func reportWidenedSession(out open.Outcome) {
	if !out.SessionWidened {
		return
	}
	fmt.Println("no session here -- using the most recent one:")
	fmt.Printf("  %s  %s  (%s)\n", shortSessionID(out.SessionID), tildePath(out.RepoPath), ago(out.SessionModTime))
}

// shortSessionID trims a session uuid to its first group, which is enough to
// recognise one and short enough to sit in a line of prose.
func shortSessionID(id string) string {
	if group, _, ok := strings.Cut(id, "-"); ok {
		return group
	}
	return id
}

// tildePath shortens a path under the user's home, since that prefix is the
// same on every line and carries no information.
func tildePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return path
	}
	return "~" + path[len(home):]
}

// ago renders an age the way a person would say it. Precision past the unit
// is noise here -- the question being answered is only ever "is this the
// session I was just in, or some forgotten one?".
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// runPromote reports through a notification as well as stderr: fired from a
// keybind there is no terminal watching, so a silent failure would look like
// the key simply did nothing.
func runPromote(target string) int {
	client, err := herdr.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	out, err := promote.Run(client, target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = client.Notify("Promote failed", err.Error())
		return 1
	}
	if !out.Moved {
		fmt.Println("already alone in its Space; nothing to promote")
		_ = client.Notify("Nothing to promote", "This pane already has its Space to itself.")
		return 0
	}

	fmt.Printf("promoted %s to Space %q\n", out.PaneID, out.Label)
	_ = client.Notify("Promoted to its own Space", out.Label)
	return 0
}
