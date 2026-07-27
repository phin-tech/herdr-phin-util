package open

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/setup"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// WorktreePlaceholder stands in for a directory that does not exist yet, in a
// preview of a target that opens in a worktree. It is only ever printed --
// nothing resolves a path against it -- so it is written to read as what it
// is rather than to look like a path.
const WorktreePlaceholder = "<the worktree Herdr creates>"

// PreviewSetup resolves what a setup would do to a target, without touching
// Herdr. It is what --dry-run prints.
//
// It asks GitHub the same questions the real run does -- a PR's branch and
// title are half of what a prompt says, and a preview that left them blank
// would not be a preview of anything. Nothing here creates, writes or focuses.
func PreviewSetup(deps Deps, cfg *config.Settings, input string, def setup.Setup) (setup.Plan, target.Target, error) {
	tgt := target.Parse(input)

	cwd := deps.Cwd
	var branch, title string
	// repoRoot is the checkout a worktree would be cut from, which is not the
	// directory the panes end up in.
	var repoRoot string

	switch tgt.Kind {
	case target.KindGitHubPR:
		path, _, err := cfg.ResolveRepo(tgt)
		if err != nil {
			return setup.Plan{}, tgt, err
		}
		repoRoot = path
		if info, err := deps.PRs.LookupPR(tgt.Owner, tgt.Repo, tgt.Number); err == nil {
			branch, title = info.Branch, info.Title
		}

	case target.KindGitHubIssue:
		path, _, err := cfg.ResolveRepo(tgt)
		if err != nil {
			return setup.Plan{}, tgt, err
		}
		repoRoot = path
		if info, err := deps.PRs.LookupIssue(tgt.Owner, tgt.Repo, tgt.Number); err == nil {
			title = info.Title
			tgt.Slug = info.Title
		}
		branch = tgt.Branch()

	case target.KindLinear:
		repoRoot = deps.Cwd
		branch = tgt.Branch()
	}

	// A worktree target's panes do not open in the checkout -- they open in the
	// worktree cut from it. Where that lands is knowable only when
	// [worktrees].path says so. Otherwise Herdr picks the directory at create
	// time, and the preview names it as such: a relative pane cwd then reads
	// as "<worktree>/web", which is true, rather than as a path under the
	// source checkout, which would not be.
	if repoRoot != "" {
		if path, ok := cfg.ResolveWorktreePath(tgt, repoRoot, branch); ok {
			cwd = path
		} else {
			cwd = WorktreePlaceholder
		}
	}

	data := promptData(tgt, branch, title)
	if repoRoot != "" {
		data["Repo"] = filepath.Base(repoRoot)
		data["Path"] = repoRoot
	} else if cwd != "" {
		data["Repo"] = filepath.Base(cwd)
		data["Path"] = cwd
	}

	plan, err := def.Resolve(cwd, data)
	return plan, tgt, err
}

// Layout is the slice of the Herdr API a setup needs on top of Session. It is
// separate so the single-agent path -- which is most of this package -- keeps
// working against the smaller interface it always had.
type Layout interface {
	CreateTab(workspaceID, cwd, label string, env map[string]string, focus bool) (herdr.Pane, string, error)
	SplitPane(targetPaneID, direction string, ratio float64, cwd string, env map[string]string, focus bool) (herdr.Pane, error)
	RunCommand(paneID, command string) error
	PromptAgent(paneID, text string) error
	RenamePane(paneID, label string) error
	RenameTab(tabID, label string) error
	FocusPane(paneID string) error
}

// applySetup builds a whole Space from a resolved plan.
//
// It runs in two passes, which is the shape herdr-plus arrived at and worth
// keeping: every pane is created first, then every pane is given its command
// or agent. Splitting a tab after something has started in it resizes a
// running program, and a layout that visibly assembles itself before anything
// starts is also the one that looks deliberate rather than sequential.
//
// Panes are independent, so a step that fails is reported and skipped rather
// than ending the run: one agent that would not start is no reason for the
// three panes after it to be left as bare shells. The problems come back as a
// list for the caller to surface -- an error return would say the whole thing
// failed, which is exactly the thing that was hardest to diagnose about a
// half-built Space. Only a plan that cannot be resolved at all is an error,
// since then nothing has been built to report on.
func applySetup(s Session, l Layout, cfg *config.Settings, def setup.Setup, root herdr.Pane, workspaceID, cwd string, data map[string]string) (setup.Plan, []string, []string, error) {
	plan, err := def.Resolve(cwd, data)
	if err != nil {
		return setup.Plan{}, nil, nil, fmt.Errorf("setup %q: %w", def.Name, err)
	}
	if len(plan.Steps) == 0 {
		return plan, nil, nil, nil
	}

	panes, problems := buildPanes(l, plan, root, workspaceID)
	problems = append(problems, fillPanes(s, l, cfg, plan, panes, workspaceID)...)

	// Focus last, once there is nothing left to build that could steal it.
	// Losing it is cosmetic next to a Space that is otherwise standing.
	if focus := plan.FocusStep; focus < len(panes) && panes[focus] != "" {
		_ = l.FocusPane(panes[focus])
	}
	return plan, panes, problems, nil
}

// buildPanes creates every tab and pane, returning one pane id per step -- an
// empty one for a step whose pane could not be made -- and what went wrong.
func buildPanes(l Layout, plan setup.Plan, root herdr.Pane, workspaceID string) ([]string, []string) {
	panes := make([]string, len(plan.Steps))
	var problems []string

	// prev is the pane the next split targets: a split is relative to the pane
	// before it, which is what makes a list of panes read like the layout it
	// produces. A new tab resets it to that tab's own root pane. A failed
	// split leaves it alone, so the next pane in the tab chains off the last
	// one that does exist rather than off nothing.
	prev := root.PaneID

	// abandoned is a tab whose own pane could not be created. Its splits would
	// target the previous tab's pane instead, quietly putting panes in a tab
	// the file never asked for, so the rest of that tab is skipped.
	abandoned := -1

	for i, step := range plan.Steps {
		if step.Tab == abandoned {
			continue
		}

		switch {
		case step.FirstTab && step.PaneIdx == 0:
			// The Space arrived with a tab and a pane. Using them is not just an
			// optimisation: creating a second tab would leave the first one
			// sitting there empty.
			if step.TabName != "" && root.TabID != "" {
				if err := l.RenameTab(root.TabID, step.TabName); err != nil {
					// A name is decoration; the pane underneath it is fine.
					problems = append(problems, fmt.Sprintf("name the first tab %q: %v", step.TabName, err))
				}
			}
			panes[i] = root.PaneID

		case step.NewTab:
			pane, _, err := l.CreateTab(workspaceID, step.Cwd, step.TabName, step.Env, false)
			if err != nil {
				problems = append(problems, fmt.Sprintf("create tab %q: %v", stepTab(step), err))
				abandoned = step.Tab
				continue
			}
			panes[i] = pane.PaneID
			prev = pane.PaneID

		default:
			pane, err := l.SplitPane(prev, step.Split, step.Ratio, step.Cwd, step.Env, false)
			if err != nil {
				problems = append(problems, fmt.Sprintf("split pane %d of tab %q: %v", step.PaneIdx+1, stepTab(step), err))
				continue
			}
			panes[i] = pane.PaneID
			prev = pane.PaneID
		}

		if step.Label != "" && panes[i] != "" {
			// A label is decoration; losing it is not worth losing the Space.
			_ = l.RenamePane(panes[i], step.Label)
		}
	}
	return panes, problems
}

// fillPanes starts what each pane is for, in the order the file listed them,
// and reports what did not start without holding up what is next.
func fillPanes(s Session, l Layout, cfg *config.Settings, plan setup.Plan, panes []string, workspaceID string) []string {
	var problems []string

	for i, step := range plan.Steps {
		paneID := panes[i]
		if paneID == "" {
			continue
		}

		if err := fillPane(s, l, cfg, step, paneID, i, workspaceID); err != nil {
			problems = append(problems, err.Error())
			// The Space itself says which pane did not get what it was for,
			// so a bare shell is self-explaining rather than a mystery.
			markPaneFailed(l, paneID, step)
			// And nothing is waited for: what the wait was listening for is
			// the work that just failed to start, so this would be the full
			// timeout spent to learn what is already known.
			continue
		}

		// The wait runs after the pane has been given its work, since what it
		// is waiting for is that work saying something. This is the ordering
		// primitive: it is what makes "prompt the orchestrator only once
		// roborev has queued" a statement rather than a race.
		//
		// A timeout is not fatal. The pane exists and the command ran; the
		// match may simply have been guessed wrong, and stopping the layout
		// over a wrong guess would be a worse answer than continuing.
		if step.WaitFor != nil {
			_ = s.WaitPaneOutput(paneID, step.WaitFor.Match, step.WaitFor.TimeoutMs)
		}
	}
	return problems
}

// fillPane gives one pane the thing it exists for: a command, or an agent and
// whatever prompt goes with it.
func fillPane(s Session, l Layout, cfg *config.Settings, step setup.Step, paneID string, index int, workspaceID string) error {
	switch {
	case step.Command != "":
		if err := l.RunCommand(paneID, step.Command); err != nil {
			return fmt.Errorf("run %q in tab %q: %w", step.Command, stepTab(step), err)
		}

	case step.Agent != "":
		if err := startSetupAgent(s, cfg, step, paneID, index, workspaceID, step.Submit); err != nil {
			return err
		}
		if strings.TrimSpace(step.Prompt) == "" {
			return nil
		}
		return sendSetupPrompt(s, l, step, paneID)
	}
	return nil
}

// Prompt pacing. The readiness checks in startSetupAgent are a good guess at
// "ready to be typed into", not a guarantee: they answer the moment a marker
// renders, and an agent that has just drawn its input can still be a beat
// away from accepting one. A settle and a single retry cover that beat, which
// is cheaper than either racing it or waiting a fixed long time for every
// pane.
const (
	promptSettle       = 400 * time.Millisecond
	promptRetryBackoff = 1500 * time.Millisecond
)

// sendSetupPrompt types a pane's prompt, or sends it for a submit:true pane.
func sendSetupPrompt(s Session, l Layout, step setup.Step, paneID string) error {
	send := func() error {
		if step.Submit {
			return l.PromptAgent(paneID, step.Prompt)
		}
		return s.SendText(paneID, step.Prompt)
	}

	sleep(promptSettle)
	err := send()
	if err == nil {
		return nil
	}
	// A submitted prompt that was rejected was rejected on readiness, so the
	// retry waits for the state agent.prompt wants rather than for a fixed
	// interval and another guess. The launch wait already ran before this, so
	// reaching here means it went stale between then and now -- rare, and worth
	// one more pass rather than a dropped prompt.
	if step.Submit {
		_ = waitAgentLaunched(s, paneID)
	}
	sleep(promptRetryBackoff)
	if retryErr := send(); retryErr == nil {
		return nil
	}

	// The first error is the one reported: a retry that fails the same way
	// adds nothing, and one that fails differently is usually failing on the
	// state the first attempt left behind.
	verb := "type the prompt into"
	if step.Submit {
		verb = "send the prompt to"
	}
	return fmt.Errorf("%s %s in tab %q: %w", verb, step.Agent, stepTab(step), err)
}

// markPaneFailed renames a pane that did not get what it was for. It is the
// per-pane status the Space can show without anywhere to put a message: a
// pane labelled "failed: codex-reviewer" beside three working ones says in
// the terminal what a log line only says in a file.
func markPaneFailed(l Layout, paneID string, step setup.Step) {
	label := step.Label
	if label == "" {
		label = step.TabName
	}
	if label == "" {
		label = step.Agent
	}
	if label == "" {
		label = "pane"
	}
	_ = l.RenamePane(paneID, "failed: "+label)
}

// startSetupAgent starts one pane's agent and waits for it to be ready to be
// typed into, reusing the retry and readiness rules the single-agent path
// already established -- including that agent.start rejects a pane Herdr has
// only just built, and that a name another Space already took is retried
// qualified by this Space's id.
//
// needsPrompting says whether the pane's prompt goes through agent.prompt,
// which has a stricter idea of ready than anything on screen does. Only then is
// a launch that never completes fatal to the step: a pane that is only being
// typed into works fine from the moment its input renders.
func startSetupAgent(s Session, cfg *config.Settings, step setup.Step, paneID string, index int, workspaceID string, needsPrompting bool) error {
	kind := step.Agent
	if !config.KnownAgentKind(kind) {
		return fmt.Errorf("tab %q: %q is not an agent Herdr knows", stepTab(step), kind)
	}

	if err := startAgentWithRetry(s, paneID, workspaceID, setupAgentName(step, index), kind, step.Args); err != nil {
		return fmt.Errorf("start %s in tab %q: %w", kind, stepTab(step), err)
	}
	// Nothing is typed into a pane that has not drawn its input yet.
	if err := s.WaitAgentIdle(paneID); err != nil {
		return fmt.Errorf("wait for %s in tab %q: %w", kind, stepTab(step), err)
	}
	if marker, ok := readyMarkers[kind]; ok {
		if err := s.WaitPaneOutput(paneID, marker, readyMarkerTimeoutMs); err != nil {
			return fmt.Errorf("wait for %s in tab %q to render its prompt: %w", kind, stepTab(step), err)
		}
	}
	if err := waitAgentLaunched(s, paneID); err != nil && needsPrompting {
		return fmt.Errorf("wait for %s in tab %q to finish launching: %w", kind, stepTab(step), err)
	}
	return nil
}

// Launch settling. agent.wait answers "idle" for an agent that has not really
// started -- an agent doing nothing yet looks exactly like an agent that is
// done -- so launch_pending is the floor underneath it: agent.prompt rejects a
// pending agent outright, whatever the pane shows.
//
// It is a floor and not a guarantee. Measured on a live codex, launch_pending
// clears on process detection, seconds before the input is drawn, and Herdr's
// interactive_ready reports true on codex's own first-run screens as well. The
// on-screen marker in readyMarkers is what actually distinguishes an input from
// a startup screen, which is why a kind that has one waits for it too.
const (
	agentLaunchBudget = 45 * time.Second
	agentLaunchPoll   = 300 * time.Millisecond
)

// waitAgentLaunched blocks until Herdr says the agent has finished launching,
// which is when it will accept a prompt.
//
// Each pass re-waits for idle as well as polling, because agent.wait is what
// makes Herdr reconcile a launch it has already completed -- polling alone can
// watch a launched agent stay pending. An agent that never gets there is
// usually one stuck on its own first-run UI (a trust prompt, an upgrade nag),
// so the timeout says that rather than repeating a bare code.
func waitAgentLaunched(s Session, paneID string) error {
	deadline := now().Add(agentLaunchBudget)
	for {
		launched, err := s.AgentLaunched(paneID)
		if err != nil {
			return err
		}
		if launched {
			return nil
		}
		if !now().Before(deadline) {
			return fmt.Errorf("agent never finished launching -- it may be waiting on a prompt of its own")
		}
		_ = s.WaitAgentIdle(paneID)
		sleep(agentLaunchPoll)
	}
}

// setupAgentName keeps agent names recognisable and unique within a Space:
// the pane's label if it has one, the tab's name if it does not, and an index
// suffix either way, since a fan-out setup runs three agents that would
// otherwise all want the same name.
func setupAgentName(step setup.Step, index int) string {
	base := step.Label
	if base == "" {
		base = step.TabName
	}
	if base == "" {
		base = step.Agent
	}
	return agentName(fmt.Sprintf("%s-%d", base, index+1))
}

// setupStartsAnAgent reports whether a plan puts an agent anywhere, which is
// what the caller's "started an agent" line is about. A setup of nothing but
// shell commands is a perfectly good setup and should not claim otherwise.
func setupStartsAnAgent(plan setup.Plan) bool {
	for _, step := range plan.Steps {
		if step.Agent != "" {
			return true
		}
	}
	return false
}

// stepTab names a tab for an error message, falling back to its position.
func stepTab(step setup.Step) string {
	if step.TabName != "" {
		return step.TabName
	}
	return fmt.Sprintf("#%d", step.Tab+1)
}
