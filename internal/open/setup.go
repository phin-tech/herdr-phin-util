package open

import (
	"fmt"
	"path/filepath"
	"strings"

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
// A failure leaves what was already built standing. A half-built Space that
// names the pane it stopped at is debuggable; tearing down something the user
// can already see would be worse than either.
func applySetup(s Session, l Layout, cfg *config.Settings, def setup.Setup, root herdr.Pane, workspaceID, cwd string, data map[string]string) (setup.Plan, []string, error) {
	plan, err := def.Resolve(cwd, data)
	if err != nil {
		return setup.Plan{}, nil, fmt.Errorf("setup %q: %w", def.Name, err)
	}
	if len(plan.Steps) == 0 {
		return plan, nil, nil
	}

	panes, err := buildPanes(l, plan, root, workspaceID)
	if err != nil {
		return plan, panes, err
	}
	if err := fillPanes(s, l, cfg, plan, panes); err != nil {
		return plan, panes, err
	}

	// Focus last, once there is nothing left to build that could steal it.
	if focus := plan.FocusStep; focus < len(panes) && panes[focus] != "" {
		if err := l.FocusPane(panes[focus]); err != nil {
			// Cosmetic next to a Space that is otherwise fully built.
			return plan, panes, nil
		}
	}
	return plan, panes, nil
}

// buildPanes creates every tab and pane, returning one pane id per step.
func buildPanes(l Layout, plan setup.Plan, root herdr.Pane, workspaceID string) ([]string, error) {
	panes := make([]string, len(plan.Steps))

	// prev is the pane the next split targets: a split is relative to the pane
	// before it, which is what makes a list of panes read like the layout it
	// produces. A new tab resets it to that tab's own root pane.
	prev := root.PaneID

	for i, step := range plan.Steps {
		switch {
		case step.FirstTab && step.PaneIdx == 0:
			// The Space arrived with a tab and a pane. Using them is not just an
			// optimisation: creating a second tab would leave the first one
			// sitting there empty.
			if step.TabName != "" && root.TabID != "" {
				if err := l.RenameTab(root.TabID, step.TabName); err != nil {
					return panes, fmt.Errorf("name the first tab %q: %w", step.TabName, err)
				}
			}
			panes[i] = root.PaneID

		case step.NewTab:
			pane, _, err := l.CreateTab(workspaceID, step.Cwd, step.TabName, step.Env, false)
			if err != nil {
				return panes, fmt.Errorf("create tab %q: %w", stepTab(step), err)
			}
			panes[i] = pane.PaneID
			prev = pane.PaneID

		default:
			pane, err := l.SplitPane(prev, step.Split, step.Ratio, step.Cwd, step.Env, false)
			if err != nil {
				return panes, fmt.Errorf("split pane %d of tab %q: %w", step.PaneIdx+1, stepTab(step), err)
			}
			panes[i] = pane.PaneID
			prev = pane.PaneID
		}

		if step.Label != "" && panes[i] != "" {
			// A label is decoration; losing it is not worth losing the Space.
			_ = l.RenamePane(panes[i], step.Label)
		}
	}
	return panes, nil
}

// fillPanes starts what each pane is for, in the order the file listed them.
func fillPanes(s Session, l Layout, cfg *config.Settings, plan setup.Plan, panes []string) error {
	for i, step := range plan.Steps {
		paneID := panes[i]
		if paneID == "" {
			continue
		}

		switch {
		case step.Command != "":
			if err := l.RunCommand(paneID, step.Command); err != nil {
				return fmt.Errorf("run %q in tab %q: %w", step.Command, stepTab(step), err)
			}

		case step.Agent != "":
			if err := startSetupAgent(s, cfg, step, paneID, i); err != nil {
				return err
			}
			if strings.TrimSpace(step.Prompt) == "" {
				break
			}
			if step.Submit {
				if err := l.PromptAgent(paneID, step.Prompt); err != nil {
					return fmt.Errorf("send the prompt to %s in tab %q: %w", step.Agent, stepTab(step), err)
				}
			} else if err := s.SendText(paneID, step.Prompt); err != nil {
				return fmt.Errorf("type the prompt into %s in tab %q: %w", step.Agent, stepTab(step), err)
			}
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
	return nil
}

// startSetupAgent starts one pane's agent and waits for it to be ready to be
// typed into, reusing the retry and readiness rules the single-agent path
// already established -- including that agent.start rejects a pane Herdr has
// only just built.
func startSetupAgent(s Session, cfg *config.Settings, step setup.Step, paneID string, index int) error {
	kind := step.Agent
	if !config.KnownAgentKind(kind) {
		return fmt.Errorf("tab %q: %q is not an agent Herdr knows", stepTab(step), kind)
	}

	if err := startAgentWithRetry(s, paneID, setupAgentName(step, index), kind, nil); err != nil {
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
	return nil
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
