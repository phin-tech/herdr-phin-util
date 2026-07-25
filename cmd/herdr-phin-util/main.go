// Command herdr-phin-util is a grab bag of small Herdr conveniences.
//
// Each feature is a subcommand, so one binary and one plugin manifest cover
// everything rather than a repo per idea.
package main

import (
	"fmt"
	"os"

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
	"github.com/phin-tech/herdr-phin-util/internal/ui"
	"github.com/phin-tech/herdr-phin-util/internal/version"
)

const usage = `herdr-phin-util -- small Herdr utilities

usage:
  herdr-phin-util open <link-or-text> [--agent|--no-agent] [--prompt TEXT]
                                       make the workspace a pasted link describes
  herdr-phin-util popup                open the "smart workspace maker" popup
  herdr-phin-util pick                 open the project picker popup
  herdr-phin-util pick-worktree        open the picker inside the current repo
  herdr-phin-util projects             list the checkouts the picker would offer
  herdr-phin-util worktrees            list the current repo's worktrees and branches
  herdr-phin-util project <dir> [--agent|--no-agent]
                                       open a Space on a checkout
  herdr-phin-util promote [pane_id]    move a pane into a Space of its own
  herdr-phin-util version

promote targets the focused pane when no id is given.

open recognises a GitHub pull request URL, a Linear issue URL, or anything
else (used as a plain Space name). --agent/--no-agent override the config's
agent.enabled; --prompt overrides the rendered template text outright.

pick lists the Spaces already open followed by every checkout found under
[projects].roots that has no Space yet: switch to the first, create the
second. Right-arrow on a repo descends into its worktrees and branches;
pick-worktree starts there for the repo you are already in.

projects and worktrees print what each level would offer without opening
anything, which is the way to check a roots setting or a branch list.
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
	case "project":
		os.Exit(runProject(args[1:]))
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
		fmt.Fprintln(os.Stderr, "usage: herdr-phin-util open <link-or-text> [--agent|--no-agent] [--prompt TEXT]")
		return 2
	}
	input := args[0]

	var agentOverride *bool
	var promptOverride string
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

	client, err := herdr.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	out, err := open.Run(openDeps(client), cfg, input, open.Options{Agent: agentOverride, Prompt: promptOverride})
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
	if out.AgentStarted {
		fmt.Println("agent started; prompt typed and waiting for you to send it")
	}
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
		PRs:     gh.New(),
		Git:     gitcmd.New(),
		Cwd:     invocationCwd(),
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

	p := tea.NewProgram(
		ui.NewPicker(cfg, pickerDeps(client), client, candidates),
		tea.WithMouseCellMotion(),
	)
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return reportPick(client, final.(*ui.Picker))
}

// pickerDeps wires both levels: Herdr for Spaces and worktrees, git for
// branches.
func pickerDeps(client *herdr.Client) session.Deps {
	return session.Deps{
		Herdr:     client,
		Open:      openDeps(client),
		Worktrees: client,
		Git:       gitcmd.New(),
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
		ui.NewWorktreePicker(cfg, pickerDeps(client), client, repo, candidates),
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
		fmt.Fprintln(os.Stderr, "usage: herdr-phin-util project <dir> [--agent|--no-agent]")
		return 2
	}
	dir := args[0]

	var agentOverride *bool
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			v := true
			agentOverride = &v
		case "--no-agent":
			v := false
			agentOverride = &v
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

	client, err := herdr.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	out, err := open.RunProject(openDeps(client), cfg, dir, open.Options{Agent: agentOverride})
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
	if out.AgentStarted {
		fmt.Println("agent started")
	}
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
