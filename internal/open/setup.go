package open

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

	// Lists is resolved only for the names def actually asks for
	// (def.ForEachNames()), and only a github_pr target produces any --
	// "layers", the chain of open pull requests tgt belongs to (see
	// internal/open/stack.go). Deliberately not a new target kind: #7
	// sketched applies_to: [github_stack], but there is no pasted-input
	// shape that parses as one, so #13 built this instead as a github_pr
	// resolving a list (github_stack itself is #14, unbuilt, tracked
	// separately). A setup naming for_each: layers against any other target
	// kind, or a setup naming a list nobody produces at all, still fails
	// with tabIterations' "provides no lists" error below -- exactly what a
	// preview of an unsupported for_each should show.
	lists, err := resolveLists(deps.PRs, tgt, def.ForEachNames())
	if err != nil {
		return setup.Plan{}, tgt, err
	}
	// A tab's own worktree: needs somewhere to compute a path against too,
	// and the promise this function makes -- nothing here creates, writes or
	// focuses -- has to keep holding for it specifically: worktreePathFn
	// below is pure string arithmetic over cfg and repoRoot, the same
	// deterministic computation applySetup's pre-pass will later use to
	// decide whether to create anything. It never touches disk on its own.
	plan, err := def.ResolveData(cwd, setup.Data{Vars: data, Lists: lists, WorktreePath: worktreePathFn(cfg, tgt, repoRoot)})
	return plan, tgt, err
}

// worktreePathFn closes over what internal/setup deliberately does not
// import -- a *config.Settings and the repo root a worktree is cut from --
// so ResolveData can compute a tab's worktree path without this package's
// config dependency leaking into setup (see the doc comment on
// setup.Data.WorktreePath).
//
// repoRoot empty means there is no repository to build a worktree in at all
// (a plain or Linear target invoked outside a checkout) -- nil is returned
// rather than a function that would always fail, so a setup with no
// worktree: tab still costs nothing and a worktree: tab gets a clear "no
// repository" error out of ResolveData rather than a path built from an
// empty root.
//
// The real target is threaded through rather than a synthesised stand-in,
// because [worktrees].path expands {host}, {owner} and {repo} from it. Those
// are exactly the placeholders someone already uses in [repos].templates, so
// a path like "~/wt/{host}/{owner}/{repo}/{ref}" is the obvious thing to
// write here -- and a stand-in target would silently expand two of its three
// segments to nothing.
func worktreePathFn(cfg *config.Settings, tgt target.Target, repoRoot string) func(ref string) string {
	if repoRoot == "" {
		return nil
	}
	// A target that names no repository still has to fill {repo} with
	// something, and the checkout's own directory name is what every other
	// path in this plugin falls back to.
	if tgt.Repo == "" {
		tgt.Repo = filepath.Base(repoRoot)
	}
	return func(ref string) string {
		return cfg.ResolveTabWorktreePath(tgt, repoRoot, ref)
	}
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

// WorktreeGit is what a tab's own `worktree:` needs beyond Fetcher's
// whole-Space FetchBranch: bringing an arbitrary ref down (not just a
// branch), laying a worktree out, and answering what is already checked out
// somewhere for the collision rule. gitcmd.Runner implements this the same
// way it already implements Fetcher -- adding methods to *Runner is all
// wiring this up needed, since Deps.Git's declared type is the only thing
// that changed.
//
// This -- not Herdr's own worktree.* API -- is the only way #12 could be
// built at all. herdr worktree create takes no --detach and always checks
// out a named branch it makes itself (WorktreeRequest.Branch is documented as
// "the name of the branch that gets made"), and every one of its calls is
// Space-scoped: CreateWorktree returns a new Workspace, and worktree.remove
// takes a workspace id, never a bare path. A tab's worktree is a directory a
// tab points its cwd at, not a Space, so there was never a Herdr call to
// route this through.
type WorktreeGit interface {
	Fetcher
	FetchRef(repoPath, ref string) error
	WorktreeAdd(repoPath, path, ref string) error
	WorktreeAddBranch(repoPath, path, ref string) error
	HeadCommit(path string) (string, error)
	ResolveRef(repoPath, ref string) (string, error)
}

// applySetup builds a whole Space from a resolved plan.
//
// It runs in three passes now, not two: a tab's own worktree (see
// applyWorktrees) has to exist before buildPanes calls CreateTab for it,
// since Herdr's tab.create takes cwd at creation and there is no "cd into
// this afterward" call. The other two passes are the shape herdr-plus arrived
// at and worth keeping: every pane is created, then every pane is given its
// command or agent. Splitting a tab after something has started in it
// resizes a running program, and a layout that visibly assembles itself
// before anything starts is also the one that looks deliberate rather than
// sequential.
//
// Panes are independent, so a step that fails is reported and skipped rather
// than ending the run: one agent that would not start is no reason for the
// three panes after it to be left as bare shells. The problems come back as a
// list for the caller to surface -- an error return would say the whole thing
// failed, which is exactly the thing that was hardest to diagnose about a
// half-built Space. Only a plan that cannot be resolved at all is an error,
// since then nothing has been built to report on. A tab whose own worktree
// failed to build follows the same contract: reported, that tab's panes
// skipped, the rest of the layout still built.
func applySetup(deps Deps, cfg *config.Settings, tgt target.Target, def setup.Setup, root herdr.Pane, workspaceID, cwd, repoRoot string, data map[string]string) (setup.Plan, []string, []string, error) {
	l := deps.Layout

	// Same source PreviewSetup uses (see its own comment): resolved only for
	// the list names def actually names in a for_each. If gh fails here,
	// that is an error and nothing gets built -- but by the time
	// applySetup runs, the Space and its worktree already exist (runAgentStep
	// only calls this after both), so a failure here does not leave nothing
	// behind the way a failure earlier in the run would. That is the honest
	// answer to "what state does a failed resolve leave", not something this
	// change reorders to fix -- every other resolve failure in this function
	// already has the same property.
	lists, err := resolveLists(deps.PRs, tgt, def.ForEachNames())
	if err != nil {
		return setup.Plan{}, nil, nil, fmt.Errorf("setup %q: %w", def.Name, err)
	}
	plan, err := def.ResolveData(cwd, setup.Data{Vars: data, Lists: lists, WorktreePath: worktreePathFn(cfg, tgt, repoRoot)})
	if err != nil {
		return setup.Plan{}, nil, nil, fmt.Errorf("setup %q: %w", def.Name, err)
	}
	if len(plan.Steps) == 0 {
		return plan, nil, nil, nil
	}

	// The new middle pass: every tab's own worktree exists (or is reported
	// and skipped) before a single tab is created. See applyWorktrees for the
	// collision rule this enforces. Not run at all when no step needs one --
	// most setups -- so this costs nothing beyond a slice scan for the common
	// case.
	var abandonedWorktrees map[int]bool
	var worktreeProblems []string
	if anyStepNeedsAWorktree(plan) {
		done := deps.Progress.step("worktrees", "Building worktrees")
		abandonedWorktrees, worktreeProblems = applyWorktrees(deps.Git, plan, repoRoot)
		done(nil)
	}

	// The two passes are the two things worth watching separately: the layout
	// appearing, then each pane being given what it is for. The first is fast
	// and the second is where the minutes go.
	done := deps.Progress.step("panes", fmt.Sprintf("Building %s", countOf(len(plan.Steps), "pane")))
	built, problems := buildPanes(l, plan, root, workspaceID, abandonedWorktrees)
	problems = append(worktreeProblems, problems...)
	done(nil)

	// The public shape (a plain []string, one id per step) is kept for
	// applySetup's own callers -- Outcome.SetupPanes only ever wanted a count
	// and a focus target -- while fillPanes gets the richer built slice, since
	// that is what constructing a command's HERDR_TAB_ID needs.
	panes := make([]string, len(built))
	for i, b := range built {
		panes[i] = b.PaneID
	}

	problems = append(problems, fillPanes(deps, cfg, plan, built, workspaceID)...)

	// Focus last, once there is nothing left to build that could steal it.
	// Losing it is cosmetic next to a Space that is otherwise standing.
	if focus := plan.FocusStep; focus < len(panes) && panes[focus] != "" {
		_ = l.FocusPane(panes[focus])
	}
	return plan, panes, problems, nil
}

// anyStepNeedsAWorktree reports whether applyWorktrees has anything to do,
// so a plan with no worktree: tab -- most of them -- skips the pass (and its
// own progress line) entirely rather than doing a no-op scan disguised as
// work.
func anyStepNeedsAWorktree(plan setup.Plan) bool {
	for _, step := range plan.Steps {
		if step.Worktree != nil {
			return true
		}
	}
	return false
}

// applyWorktrees is the pass applySetup runs between resolving the plan and
// building any pane: every step whose Worktree is set (only a tab-opening
// step ever carries one, see Step.Worktree's doc comment) gets its worktree
// created, reused, or -- on a collision -- reported and abandoned, before
// buildPanes gets anywhere near calling CreateTab for it.
//
// abandoned is keyed by Step.Tab, not by step index: buildPanes' own abandon
// tracking works the same way, and a tab is what gets abandoned, not a single
// step.
func applyWorktrees(git WorktreeGit, plan setup.Plan, repoRoot string) (abandoned map[int]bool, problems []string) {
	for _, step := range plan.Steps {
		wt := step.Worktree
		if wt == nil {
			continue
		}
		if err := ensureWorktree(git, repoRoot, step.Cwd, wt.Ref, wt.Detach); err != nil {
			if abandoned == nil {
				abandoned = map[int]bool{}
			}
			abandoned[step.Tab] = true
			problems = append(problems, fmt.Sprintf("worktree for tab %q: %v", stepTab(step), err))
		}
	}
	return abandoned, problems
}

// ensureWorktree is the collision rule #12's design settled on, confirmed by
// the repo owner during review rather than guessed at:
//
//   - path missing -> create it.
//   - path exists and its HEAD matches ref -> reuse it, no-op.
//   - path exists and its HEAD differs from ref -> report that tab as failed
//     and skip it. This function never removes anything.
//
// That last point is the one worth defending against a future edit that
// "fixes" it: `git worktree remove --force` then re-add would make a re-run
// always succeed, and it is exactly the behaviour the issue ruled out --
// "I would rather it accumulate predictably than get cleverly cleaned up and
// occasionally delete something someone was using." A directory at this path
// that is not what was asked for might be exactly that: something someone
// else built or is still using. So a mismatch is a reported, actionable
// failure -- the error names the precise command to run by hand -- never an
// automatic one.
//
// ref is fetched before anything is compared, tolerating "already have it"
// (see gitcmd.FetchRef): the common case is a worktree whose commit is
// already present locally, and that must not need the network to confirm.
func ensureWorktree(git WorktreeGit, repoRoot, path, ref string, detach bool) error {
	if repoRoot == "" {
		return fmt.Errorf("no repository to build a worktree in")
	}
	if err := git.FetchRef(repoRoot, ref); err != nil {
		return fmt.Errorf("fetch %s: %w", ref, err)
	}
	wantSHA, err := git.ResolveRef(repoRoot, ref)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", ref, err)
	}

	if _, statErr := os.Stat(path); statErr == nil {
		haveSHA, err := git.HeadCommit(path)
		if err != nil {
			return fmt.Errorf("read what is checked out at %s: %w", path, err)
		}
		if haveSHA == wantSHA {
			// Reuse, no-op: this is the re-run case the deterministic naming
			// scheme exists for.
			return nil
		}
		return fmt.Errorf(
			"%s is already a worktree checked out at %s, not %s (%s) -- if you mean to replace it, run: git worktree remove --force %s",
			path, haveSHA, ref, wantSHA, path)
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("check %s: %w", path, statErr)
	}

	if detach {
		return git.WorktreeAdd(repoRoot, path, ref)
	}
	return git.WorktreeAddBranch(repoRoot, path, ref)
}

// builtPane is what a step turned into once buildPanes has run: the pane id
// (empty if it could not be made) and the id of the tab it lives in.
//
// TabID is never asked of Layout.SplitPane -- unlike CreateTab, splitting a
// tab that already exists has no reason to report that tab's id back -- so it
// is carried here instead, filled in from whichever FirstTab/NewTab step
// opened the tab a split belongs to. That is enough on its own: every split
// step's Step.Tab names the tab it is in, and that tab's id was learned the
// moment it was created.
type builtPane struct {
	PaneID string
	TabID  string
}

// buildPanes creates every tab and pane, returning one builtPane per step --
// an empty PaneID for a step whose pane could not be made -- and what went
// wrong.
//
// Labels are applied here, in this same pass, before fillPanes runs a single
// command or starts a single agent. That ordering is not incidental: it is
// what #9's HERDR_PANE_<LABEL> variables lean on, and what makes the "labels
// land before any command runs" guarantee true rather than aspirational. See
// fillPane and herdrPaneEnv for the other half of that story -- pane ids
// exist by the time this function returns, but an agent later in the plan
// still starts later in fillPanes, so a labelled pane's *agent* is not
// guaranteed to have attached yet, only its id and label.
// abandonedWorktrees names tabs whose own worktree failed to build, keyed by
// Step.Tab -- the output of applyWorktrees, or nil when nothing in the plan
// asked for one. buildPanes treats it as one more reason a tab gets abandoned,
// alongside a CreateTab or SplitPane call that failed on its own.
func buildPanes(l Layout, plan setup.Plan, root herdr.Pane, workspaceID string, abandonedWorktrees map[int]bool) ([]builtPane, []string) {
	built := make([]builtPane, len(plan.Steps))
	var problems []string

	// prev is the pane the next split targets: a split is relative to the pane
	// before it, which is what makes a list of panes read like the layout it
	// produces. A new tab resets it to that tab's own root pane. A failed
	// split leaves it alone, so the next pane in the tab chains off the last
	// one that does exist rather than off nothing.
	prev := root.PaneID

	// tabID is the id of whichever tab is currently being filled in -- set the
	// moment that tab's own FirstTab/NewTab step runs, and reused by every
	// split step in it, since a split never changes what tab it is in.
	var tabID string

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
			built[i].PaneID = root.PaneID
			built[i].TabID = root.TabID
			tabID = root.TabID
			if abandonedWorktrees[step.Tab] {
				// Different from every other abandon reason here: there is no
				// "reuse the Space's own tab" fallback to lose, because the
				// Space's own tab and root pane exist no matter what -- only
				// the *pinned* directory never got built (already reported by
				// applyWorktrees). The root pane is used exactly as it would
				// be otherwise, sitting in the Space's own directory rather
				// than the one that was asked for. What is abandoned is the
				// rest of *this* tab: a split whose cwd is the worktree path
				// that never got created would only fail again, less clearly,
				// once Herdr tried to use it.
				abandoned = step.Tab
			}

		case step.NewTab:
			if abandonedWorktrees[step.Tab] {
				// No cwd to create this tab in -- its own worktree failed
				// before CreateTab was ever attempted, already reported by
				// applyWorktrees. Unlike FirstTab there is nothing here to
				// fall back to: the tab itself was never going to exist.
				abandoned = step.Tab
				continue
			}
			pane, newTabID, err := l.CreateTab(workspaceID, step.Cwd, step.TabName, step.Env, false)
			if err != nil {
				problems = append(problems, fmt.Sprintf("create tab %q: %v", stepTab(step), err))
				abandoned = step.Tab
				continue
			}
			built[i].PaneID = pane.PaneID
			built[i].TabID = newTabID
			tabID = newTabID
			prev = pane.PaneID

		default:
			pane, err := l.SplitPane(prev, step.Split, step.Ratio, step.Cwd, step.Env, false)
			if err != nil {
				problems = append(problems, fmt.Sprintf("split pane %d of tab %q: %v", step.PaneIdx+1, stepTab(step), err))
				continue
			}
			built[i].PaneID = pane.PaneID
			built[i].TabID = tabID
			prev = pane.PaneID
		}

		if step.Label != "" && built[i].PaneID != "" {
			// A label is decoration; losing it is not worth losing the Space.
			_ = l.RenamePane(built[i].PaneID, step.Label)
		}
	}
	return built, problems
}

// fillPanes starts what each pane is for, in the order the file listed them,
// and reports what did not start without holding up what is next.
func fillPanes(deps Deps, cfg *config.Settings, plan setup.Plan, built []builtPane, workspaceID string) []string {
	s, l := deps.Session, deps.Layout
	var problems []string

	for i, step := range plan.Steps {
		paneID := built[i].PaneID
		if paneID == "" {
			continue
		}

		// One line per pane, named the way the file names it, so a run that is
		// sitting on a slow agent says which agent rather than just "working".
		done := deps.Progress.step(fmt.Sprintf("pane-%d", i), fillLabel(step))
		err := fillPane(s, l, cfg, plan, built, i, workspaceID)
		done(err)

		if err != nil {
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
			done := deps.Progress.step(fmt.Sprintf("wait-%d", i), fmt.Sprintf("Waiting for %q in %s", step.WaitFor.Match, stepTab(step)))
			_ = s.WaitPaneOutput(paneID, step.WaitFor.Match, step.WaitFor.TimeoutMs)
			// Reported as finished either way: a wait that timed out is not a
			// failure here, and marking it as one would say the run went wrong
			// when it deliberately carried on.
			done(nil)
		}
	}
	return problems
}

// fillPane gives one pane the thing it exists for: a command, or an agent and
// whatever prompt goes with it.
//
// Only a command pane gets the HERDR_ environment (see herdrPaneEnv): the
// issue this answers has no use for it on an agent pane, since an agent can
// just run `herdr pane current` itself and has the patience to poll, and
// prefixing an agent's prompt would make no sense -- a prompt is not a shell
// command, and there is nothing to prefix it onto.
func fillPane(s Session, l Layout, cfg *config.Settings, plan setup.Plan, built []builtPane, index int, workspaceID string) error {
	step := plan.Steps[index]
	paneID := built[index].PaneID

	switch {
	case step.Command != "":
		command := step.Command
		if env := herdrPaneEnv(plan, built, workspaceID, index); len(env) > 0 {
			command = herdrEnvPrefix(env) + command
		}
		if err := l.RunCommand(paneID, command); err != nil {
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

// herdrPaneEnv builds the Herdr identity environment for one command step:
// its own workspace, tab and pane id, plus one HERDR_PANE_<NAME> variable per
// labelled pane anywhere in the whole plan that was actually built --
// including this step's own pane, which is harmless (issue #9 called it out
// explicitly) and keeps the resulting set predictable rather than
// special-casing self-reference away.
//
// This can only run from fillPanes, never earlier: a step's own pane id does
// not exist until buildPanes has created it, and a sibling's id does not
// exist until the whole layout has been built, both of which are true by the
// time fillPanes runs and neither of which is true any sooner. See
// herdrEnvPrefix for the mechanism this environment travels over and its one
// real limitation.
//
// A step whose own pane failed to build gets nil: there is no pane to type an
// env prefix into. A labelled pane that failed to build is left out of the
// sibling set for the same reason -- built[i].PaneID == "" -- so a script
// never sees an HERDR_PANE_<NAME> pointing at a pane that does not exist.
//
// Label folding collisions resolve first-in-plan-order: the loop below walks
// plan.Steps in file order and env's key-existence check makes the first
// pane whose label folds to a given name the one that wins, silently
// discarding a later label that folds the same way. That includes the
// reserved keys seeded before the loop -- a label that happens to fold to
// "ID" cannot displace this step's own HERDR_PANE_ID, since that key already
// exists in env when the loop reaches it.
func herdrPaneEnv(plan setup.Plan, built []builtPane, workspaceID string, index int) map[string]string {
	pane := built[index]
	if pane.PaneID == "" {
		return nil
	}

	env := map[string]string{
		"HERDR_WORKSPACE_ID": workspaceID,
		"HERDR_TAB_ID":       pane.TabID,
		"HERDR_PANE_ID":      pane.PaneID,
	}

	for i, step := range plan.Steps {
		if step.Label == "" || built[i].PaneID == "" {
			continue
		}
		name, ok := setup.FoldLabel(step.Label)
		if !ok {
			continue
		}
		key := "HERDR_PANE_" + name
		if _, taken := env[key]; taken {
			continue
		}
		env[key] = built[i].PaneID
	}
	return env
}

// herdrEnvPrefix renders a step's Herdr identity variables as a shell
// assignment prefix -- "KEY='value' KEY2='value2' " -- typed ahead of the
// command itself in RunCommand.
//
// This is the *only* channel available for it, which is worth being explicit
// about since the obvious-looking alternative does not exist. Herdr's
// env-at-creation (the env parameter CreateTab and SplitPane already take)
// cannot carry these values: a pane's own id is not assigned until Herdr
// creates it, and a sibling's id is not assigned until the whole layout is
// built -- both after creation-time env would have had to be supplied to
// have any effect. There is also no pane.setenv (or similar) call in Herdr's
// API to fill that gap afterwards. A typed prefix in front of the command
// the setup already sends via RunCommand is what is left, and it works
// because sh, bash, zsh and fish (3.1+) all honour "KEY=val cmd" as scoping
// KEY to that one command's environment.
//
// That scoping is also the caveat that has to be spelled out here rather
// than discovered the hard way: a var=val prefix applies to exactly one
// *simple* command. "HERDR_PANE_ID=w2H:p2 cd x && python y" does NOT put
// HERDR_PANE_ID in python's environment -- only cd sees it, because && opens
// a new simple command. The same is true of ||, ;, | and a literal newline:
// only the first stage of a chain or pipeline gets these vars for free. There
// is no portable fix folded in here on purpose -- fish spells "export" as
// "set -x", POSIX shells spell it "export", and the pane's shell is not
// knowable from this function, so guessing wrong would be worse than saying
// nothing. A setup command that needs the vars past the first stage of a
// chain has to re-export them itself; one that pipes is usually fine, since
// it is normally the first stage that does the reading.
//
// Keys are emitted in sorted order for two reasons: a deterministic typed
// command is easier to reason about than one whose order depends on map
// iteration, and RunCommand's echoProbe (internal/herdr/api.go) matches
// against a short leading fragment of whatever was actually typed. A prefix
// does not break that match -- the probe is looking for whatever the pane
// echoes, and the prefix is exactly what gets echoed first -- but an
// unstable prefix would make that fragment differ run to run for no reason.
// One real consequence of echoProbeLen (12 bytes) is worth knowing: once a
// step carries any of these variables, the probe is dominated by
// "HERDR_WORKS..." rather than by the command itself, so it stops
// distinguishing one command from another. That costs nothing here --
// RunCommand only ever checks its own pane's own screen against its own
// probe, never one pane's probe against another's -- but it would matter to
// anything that started reusing echoProbe to tell commands apart.
func herdrEnvPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+shellQuote(env[k]))
	}
	return strings.Join(parts, " ") + " "
}

// shellQuote wraps a value in single quotes, closing and reopening the quote
// around any single quote already in it (' -> '\”). That is the one quoting
// form sh, bash, zsh and fish all agree on -- fish's own quoting rules differ
// from POSIX's in other ways, but not in this one.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
//
// index is the pane's position in the whole plan, not within its tab -- the
// caller passes plan.Steps' own index (see fillPanes) -- which is what keeps
// a for_each tab safe without this function knowing for_each exists: three
// tabs repeated from one template still render the same Label or TabName
// three times over, but each occurrence lands at a different plan index, so
// the suffix is never the same twice. Do not "simplify" this back to a
// per-tab counter; that is exactly the collision #5's agent_name_taken guards
// against, and a two-element test list would not catch it going missing.
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

// fillLabel says what a pane is about to be given, in the terms the setup file
// used: the agent or command, and the tab it is in. "Starting codex in
// reviewers" is the line someone watching a slow run wants -- it names the
// thing being waited on, which is the whole point of showing it.
func fillLabel(step setup.Step) string {
	switch {
	case step.Command != "":
		return fmt.Sprintf("Running %s in %s", firstWord(step.Command), stepTab(step))
	case step.Agent != "":
		if step.Label != "" {
			return fmt.Sprintf("Starting %s in %s", step.Label, stepTab(step))
		}
		return fmt.Sprintf("Starting %s in %s", step.Agent, stepTab(step))
	default:
		return "Opening " + stepTab(step)
	}
}

// firstWord keeps a command line to the program being run. A setup's commands
// carry flags and paths that would push everything else off a narrow popup.
func firstWord(command string) string {
	if i := strings.IndexAny(command, " \t"); i > 0 {
		return command[:i]
	}
	return command
}

// countOf pluralises a small count for a status line: "1 pane", "4 panes".
func countOf(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// stepTab names a tab for an error message, falling back to its position.
func stepTab(step setup.Step) string {
	if step.TabName != "" {
		return step.TabName
	}
	return fmt.Sprintf("#%d", step.Tab+1)
}
