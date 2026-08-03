package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// Step is one pane, fully resolved: no inheritance left to apply, no template
// left to render, nothing else to look up. Everything a step needs to be
// carried out is in the struct.
//
// Resolving ahead of execution is what makes --dry-run honest. A preview that
// re-derived any of this could disagree with what the real run does, and a
// preview you cannot trust is worse than none.
type Step struct {
	// Tab is the index of the tab this pane belongs to, and NewTab marks the
	// pane that opens it. The first pane of the first tab opens no tab at all:
	// the Space arrived with one.
	Tab     int
	TabName string
	NewTab  bool
	PaneIdx int
	Label   string
	Split   string
	Ratio   float64
	Cwd     string
	Env     map[string]string
	Agent   string
	// Args is the agent's whole command line as it will be passed to
	// agent.start: the model flag, if a model was named, followed by whatever
	// args the pane spelled out. Resolving them into one list here is what
	// keeps execution from having to know that model is sugar.
	Args    []string
	Prompt  string
	Submit  bool
	Command string
	Focus   bool
	WaitFor *WaitFor
	// OnLaunch is the pane's on_launch:, timeouts filled in from
	// DefaultOnLaunchTimeoutMs where the file left one out -- see
	// normaliseOnLaunch, which mirrors normaliseWait below for WaitFor.
	OnLaunch []OnLaunchStep
	FirstTab bool
	// Worktree is set only on the step that opens this tab (FirstTab or
	// NewTab), never on a split -- a tab's worktree is created once, before
	// the tab exists, not once per pane in it. nil is the ordinary case: no
	// worktree: on the tab.
	//
	// Cwd, above, is deliberately NOT left unresolved for this step the way
	// "create it, then fill this in" might suggest. It already holds the
	// worktree's real, deterministic path (see ResolveWorktreePath in
	// internal/config and applyWorktrees in internal/open/setup.go) --
	// computable from the repo root and the rendered ref alone, without
	// creating anything or touching disk. That is what keeps this struct's
	// own invariant intact: "no template left to render... resolving ahead of
	// execution is what makes --dry-run honest" (see the doc comment above).
	// If Cwd here meant "ask Herdr where this ends up" instead, Describe would
	// have to fabricate a placeholder or shell out to git during a preview,
	// and every reader of Cwd -- buildPanes, fillPanes, Describe itself --
	// would have to know to treat this one step's Cwd differently from every
	// other step's. Nothing does; Cwd means what it always means, and
	// Worktree carries only what buildPanes cannot get from Cwd: the ref and
	// detach flag the pre-pass needs to actually create the thing at that
	// path.
	Worktree *StepWorktree
}

// StepWorktree is a tab-opening step's rendered `worktree:` -- the ref as a
// concrete string, no template left in it, and whether to leave HEAD detached
// or check ref out as a branch. See the long comment on Step.Worktree for why
// this does not also carry the path: Step.Cwd already does, and carrying it
// twice would leave two fields that could disagree.
type StepWorktree struct {
	Ref    string
	Detach bool
}

// Plan is a setup resolved against one target.
type Plan struct {
	Name  string
	Steps []Step
	// FocusStep is the index into Steps of the pane to end on. It is always
	// valid for a non-empty plan: unmarked, it is the first pane, since
	// landing wherever the last split happened is arbitrary.
	FocusStep int
}

// Data is what a setup renders against. Vars is exactly today's promptData --
// one flat map[string]string, unchanged, so every existing call site and
// every existing template keeps meaning what it always meant. Lists is the
// second channel a for_each tab reads from: a name such as "layers" mapped to
// one map[string]string per element, each of which becomes one repetition of
// the tab that named it.
//
// Lists is a separate field rather than folded into Vars because nothing
// iterable can pass through a text/template placeholder without Render
// itself changing -- see the comment on Tab.ForEach for why that would cost
// more than it is worth. A second field costs nothing: most setups never set
// for_each and never look at it.
type Data struct {
	Vars  map[string]string
	Lists map[string][]map[string]string
	// WorktreePath resolves where a tab's `worktree:` ref should be checked
	// out, given the rendered ref. It is a function rather than a repo root
	// plus a config template because this package -- deliberately, see its
	// own doc comment -- talks to neither git nor a config file: the caller
	// (internal/open) already has both a *config.Settings and the repo root a
	// worktree is cut from, and closes over them once before calling
	// ResolveData. That keeps this package's only new dependency for #12 a
	// single function value, not a new import.
	//
	// nil here is the ordinary case -- PreviewSetup and applySetup both set
	// it, but Resolve (the pre-for_each signature nothing here-facing calls)
	// does not, and any Setup with no worktree: tab never calls it either.
	// A worktree: tab resolved with this nil is an error (see ResolveData),
	// since there would be nowhere to build one.
	WorktreePath func(ref string) string
}

// Resolve renders a setup against the target data and the Space's directory.
//
// data is the same map internal/open builds for an [agent.prompts] template,
// so {{.Number}} and {{.Branch}} mean here exactly what they mean there. This
// signature is kept exactly as it has always been -- rather than folded into
// ResolveData -- so that for_each's arrival does not ripple into every file
// that calls Resolve and will never write a for_each tab of its own.
func (s Setup) Resolve(baseCwd string, data map[string]string) (Plan, error) {
	return s.ResolveData(baseCwd, Data{Vars: data})
}

// ResolveData is Resolve with the second data channel a for_each tab needs.
//
// The list a for_each tab names resolves before a single pane of that tab is
// built -- tabIterations runs at the top of each pass through s.Tabs, ahead of
// any Step being appended -- which is the invariant the issue asked for: a
// missing or misspelled list fails with nothing half-built. That in turn only
// matters because of where this is called from: applySetup calls Resolve
// before it has touched Herdr at all, so a plan that never resolves never
// reaches buildPanes either. Nothing extra had to be added to get that; it
// falls out of Resolve already running first.
func (s Setup) ResolveData(baseCwd string, data Data) (Plan, error) {
	plan := Plan{Name: s.Name}

	setupCwd, err := renderedCwd(baseCwd, s.Cwd, data.Vars)
	if err != nil {
		return Plan{}, fmt.Errorf("setup cwd: %w", err)
	}
	setupEnv, err := renderEnv(s.Env, data.Vars)
	if err != nil {
		return Plan{}, fmt.Errorf("setup env: %w", err)
	}

	focus := -1
	// emitted counts tabs actually built, not tabs the file listed -- the two
	// disagree the moment a for_each tab expands to something other than
	// exactly one. Step.Tab, NewTab and FirstTab all have to be about the
	// emitted tab: buildPanes decides "reuse the Space's own tab" from
	// FirstTab and "create a new one" from NewTab, and neither of those
	// questions has anything to do with which line of YAML asked for it. One
	// consequence worth calling out: a for_each over an empty list in the
	// first slot of a setup means the *next* tab is the one that becomes
	// FirstTab and reuses the Space's own tab -- correctly, since the empty
	// for_each built nothing for the Space to have already become.
	emitted := 0
	for ti, tab := range s.Tabs {
		iterations, err := tabIterations(ti, tab, data)
		if err != nil {
			return Plan{}, err
		}

		for _, vars := range iterations {
			tabName, err := Render(tab.Name, vars)
			if err != nil {
				return Plan{}, fmt.Errorf("tab %d name: %w", ti+1, err)
			}
			tabCwd, err := renderedCwd(setupCwd, tab.Cwd, vars)
			if err != nil {
				return Plan{}, fmt.Errorf("tab %d cwd: %w", ti+1, err)
			}

			// A worktree: tab does not live under the Space's own directory
			// tree -- it is its own checkout, somewhere the naming scheme in
			// internal/config puts it -- so its cwd is the worktree's path
			// outright, not tabCwd joined against anything. Validate already
			// refuses cwd: and worktree: together, so there is no ordering
			// question about which one wins here.
			//
			// Ref renders in this same per-iteration pass as tabCwd, against
			// the same vars a for_each element would supply -- deliberately,
			// so stage two's for_each interaction costs nothing extra to
			// wire up here when it lands.
			var stepWorktree *StepWorktree
			if tab.Worktree != nil {
				ref, err := Render(tab.Worktree.Ref, vars)
				if err != nil {
					return Plan{}, fmt.Errorf("tab %d worktree ref: %w", ti+1, err)
				}
				ref = strings.TrimSpace(ref)
				if ref == "" {
					return Plan{}, fmt.Errorf("tab %d worktree: ref rendered empty", ti+1)
				}
				if data.WorktreePath == nil {
					return Plan{}, fmt.Errorf("tab %d worktree: no repository to build it in", ti+1)
				}
				detach := true
				if tab.Worktree.Detach != nil {
					detach = *tab.Worktree.Detach
				}
				tabCwd = data.WorktreePath(ref)
				stepWorktree = &StepWorktree{Ref: ref, Detach: detach}
			}

			tabEnv, err := renderEnv(tab.Env, vars)
			if err != nil {
				return Plan{}, fmt.Errorf("tab %d env: %w", ti+1, err)
			}
			tabEnv = mergeEnv(setupEnv, tabEnv)

			for pi, pane := range tab.EffectivePanes() {
				label, err := Render(pane.Label, vars)
				if err != nil {
					return Plan{}, fmt.Errorf("tab %d pane %d label: %w", ti+1, pi+1, err)
				}
				paneEnv, err := renderEnv(pane.Env, vars)
				if err != nil {
					return Plan{}, fmt.Errorf("tab %d pane %d env: %w", ti+1, pi+1, err)
				}

				prompt, err := renderPanePrompt(pane, vars)
				if err != nil {
					return Plan{}, fmt.Errorf("tab %d pane %d prompt: %w", ti+1, pi+1, err)
				}
				command, err := Render(pane.Command, vars)
				if err != nil {
					return Plan{}, fmt.Errorf("tab %d pane %d command: %w", ti+1, pi+1, err)
				}
				args, err := renderAgentArgs(pane, vars)
				if err != nil {
					return Plan{}, fmt.Errorf("tab %d pane %d args: %w", ti+1, pi+1, err)
				}
				paneCwd, err := renderedCwd(tabCwd, pane.Cwd, vars)
				if err != nil {
					return Plan{}, fmt.Errorf("tab %d pane %d cwd: %w", ti+1, pi+1, err)
				}

				split := pane.Split
				if pi > 0 && split == "" {
					split = DefaultSplit
				}

				// Worktree rides only on the pane that opens the tab: a
				// worktree is created once, before the tab exists, never once
				// per split in it.
				var paneWorktree *StepWorktree
				if pi == 0 {
					paneWorktree = stepWorktree
				}

				step := Step{
					Tab:      emitted,
					TabName:  tabName,
					NewTab:   pi == 0 && emitted > 0,
					FirstTab: emitted == 0,
					PaneIdx:  pi,
					Label:    label,
					Split:    split,
					Ratio:    pane.Ratio,
					Cwd:      paneCwd,
					Env:      mergeEnv(tabEnv, paneEnv),
					Agent:    pane.Agent,
					Args:     args,
					Prompt:   prompt,
					Submit:   pane.Submit,
					Command:  strings.TrimSpace(command),
					Focus:    pane.Focus,
					WaitFor:  normaliseWait(pane.WaitFor),
					OnLaunch: normaliseOnLaunch(pane.OnLaunch),
					Worktree: paneWorktree,
				}
				if pane.Focus && focus < 0 {
					focus = len(plan.Steps)
				}
				plan.Steps = append(plan.Steps, step)
			}
			emitted++
		}
	}

	if focus < 0 {
		focus = 0
	}
	plan.FocusStep = focus
	return plan, nil
}

// tabIterations resolves how many times a tab is built and what each build
// renders against. A tab with no for_each is the one iteration every setup
// has always had -- data.Vars, untouched, so a setup that never uses for_each
// resolves exactly as it did before this existed.
//
// A for_each tab looks data.Lists[name] up. Absent is an error: this is the
// one place cardinality can go wrong before anything is built, and a typo'd
// list name failing silently (zero tabs, no complaint) would be far worse to
// track down than failing loudly here. Present but empty is not an error --
// zero elements is a legitimate answer from a live target (a stack of one
// layer has no "the rest of the stack") and yields zero tabs for this source
// tab, which is exactly what the emitted-tab counter in ResolveData is built
// to cope with.
func tabIterations(ti int, tab Tab, data Data) ([]map[string]string, error) {
	name := strings.TrimSpace(tab.ForEach)
	if name == "" {
		return []map[string]string{data.Vars}, nil
	}

	list, ok := data.Lists[name]
	if !ok {
		return nil, missingListErr(ti, name, data.Lists)
	}

	as := strings.TrimSpace(tab.As)
	if as == "" {
		as = name
	}

	out := make([]map[string]string, 0, len(list))
	for i, elem := range list {
		vars := make(map[string]string, len(data.Vars)+len(elem)+1)
		for k, v := range data.Vars {
			vars[k] = v
		}
		// _index goes in before the element's own keys, not after: an
		// element field named "index" is vanishingly unlikely, but if a list
		// source ever has one, the data it actually carries should win over
		// the bookkeeping this function bolted on, not the other way round.
		vars[as+"_index"] = strconv.Itoa(i + 1)
		for k, v := range elem {
			vars[as+"_"+k] = v
		}
		out = append(out, vars)
	}
	return out, nil
}

// missingListErr names, sorted, what lists the target did provide -- sorted
// so the message a user sees is the same every run rather than whichever
// order a map iteration happened to produce. An empty roster gets its own
// wording rather than "provides: " trailing off into nothing.
func missingListErr(ti int, name string, lists map[string][]map[string]string) error {
	if len(lists) == 0 {
		return fmt.Errorf("tab %d: for_each names %q, but this target provides no lists", ti+1, name)
	}
	names := make([]string, 0, len(lists))
	for k := range lists {
		names = append(names, k)
	}
	sort.Strings(names)
	return fmt.Errorf("tab %d: for_each names %q, but this target only provides %s", ti+1, name, strings.Join(names, ", "))
}

// renderedCwd renders a cwd against this iteration's vars before resolving it
// against the level above it. Templating a cwd at all is new: nothing needed
// it before a for_each tab's cwd could contain {{.layer_worktree}}, so a
// setup with no for_each and no "{{" in a cwd anywhere renders byte-identical
// to what joinCwd alone produced.
func renderedCwd(base, rel string, vars map[string]string) (string, error) {
	rendered, err := Render(rel, vars)
	if err != nil {
		return "", err
	}
	return joinCwd(base, rendered), nil
}

// renderPanePrompt turns whichever of prompt/skill was given into the text to
// type. A skill is just a prompt whose whole body is a slash command, so it
// renders the same way -- which means /review {{.Branch}} works, and someone
// who wants that does not have to know they have crossed into template land.
func renderPanePrompt(p Pane, data map[string]string) (string, error) {
	text := p.Prompt
	if text == "" && p.Skill != "" {
		text = p.Skill
		if !strings.HasPrefix(text, "/") {
			text = "/" + text
		}
	}
	if text == "" {
		return "", nil
	}
	return Render(text, data)
}

// renderAgentArgs builds the agent's command line: the model flag first, then
// the pane's own args. Model is deliberately sugar rather than a separate
// field carried through execution -- "--model opus" is what it always meant,
// and writing it out here means there is one thing to pass to agent.start
// instead of two that could disagree about ordering.
//
// Args going last is what makes the escape hatch an escape hatch: a pane that
// needs to say something the sugar cannot can still say it, and says it after.
// Each value renders as a template, so --add-dir {{.Path}} works.
func renderAgentArgs(p Pane, data map[string]string) ([]string, error) {
	if p.Agent == "" || (strings.TrimSpace(p.Model) == "" && len(p.Args) == 0) {
		return nil, nil
	}

	var out []string
	if model := strings.TrimSpace(p.Model); model != "" {
		rendered, err := Render(model, data)
		if err != nil {
			return nil, fmt.Errorf("model: %w", err)
		}
		if rendered = strings.TrimSpace(rendered); rendered != "" {
			out = append(out, "--model", rendered)
		}
	}
	for i, arg := range p.Args {
		rendered, err := Render(arg, data)
		if err != nil {
			return nil, fmt.Errorf("arg %d: %w", i+1, err)
		}
		out = append(out, rendered)
	}
	return out, nil
}

// normaliseWait fills in the default timeout, so execution never has to.
func normaliseWait(w *WaitFor) *WaitFor {
	if w == nil || strings.TrimSpace(w.Match) == "" {
		return nil
	}
	out := *w
	if out.TimeoutMs <= 0 {
		out.TimeoutMs = DefaultWaitTimeoutMs
	}
	return &out
}

// normaliseOnLaunch fills in the default per-entry timeout, the same way
// normaliseWait does for WaitFor -- so runOnLaunch (internal/open/setup.go)
// never has to know DefaultOnLaunchTimeoutMs exists. nil in, nil out: most
// panes have no on_launch: at all, and a nil slice is what lets that stay
// free of any of this.
func normaliseOnLaunch(steps []OnLaunchStep) []OnLaunchStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]OnLaunchStep, len(steps))
	for i, s := range steps {
		out[i] = s
		if out[i].TimeoutMs <= 0 {
			out[i].TimeoutMs = DefaultOnLaunchTimeoutMs
		}
	}
	return out
}

// joinCwd resolves a possibly-relative directory against the level above it.
// An absolute path wins outright, and "~" expands, so a setup can point a pane
// somewhere else entirely when it means to.
func joinCwd(base, rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return base
	}
	if strings.HasPrefix(rel, "~") {
		if home, err := homeDir(); err == nil && home != "" {
			return filepath.Clean(filepath.Join(home, strings.TrimPrefix(rel, "~")))
		}
	}
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	if base == "" {
		return filepath.Clean(rel)
	}
	return filepath.Clean(filepath.Join(base, rel))
}

// mergeEnv layers child over parent without mutating either.
func mergeEnv(parent, child map[string]string) map[string]string {
	if len(parent) == 0 && len(child) == 0 {
		return nil
	}
	out := make(map[string]string, len(parent)+len(child))
	for k, v := range parent {
		out[k] = v
	}
	for k, v := range child {
		out[k] = v
	}
	return out
}

func renderEnv(env map[string]string, data map[string]string) (map[string]string, error) {
	if len(env) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(env))
	// Sorted so an error names the same key every time rather than whichever
	// the map happened to yield first.
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v, err := Render(env[k], data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

// Render executes a Go text/template against the target data, with
// missingkey=zero so a typo'd placeholder renders empty instead of failing the
// whole action. This is deliberately identical to internal/open's prompt
// rendering: one template dialect across the plugin, one set of surprises.
func Render(text string, data map[string]string) (string, error) {
	if text == "" || !strings.Contains(text, "{{") {
		return text, nil
	}
	tmpl, err := template.New("setup").Option("missingkey=zero").Parse(text)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// FoldLabel turns a pane label into the suffix of the HERDR_PANE_<NAME>
// environment variable execution injects for it (see the long comment on
// herdrEnvPrefix in internal/open/setup.go for the mechanism): upper-cased,
// with each run of characters that are not ASCII letters or digits folded to
// one "_", and any leading or trailing "_" trimmed. ok is false when that
// leaves nothing (a label that is pure punctuation) or leaves a name starting
// with a digit -- neither survives as a shell variable name, so a label like
// that gets no HERDR_PANE_ variable rather than a broken one.
//
// This lives here, not in internal/open, so Describe (which only has the
// plan, never a built pane) and the real run (which has both) fold a label
// identically. If they used separate logic, a --dry-run preview of which
// HERDR_PANE_ names will exist could disagree with what actually gets set --
// exactly the kind of preview-vs-reality gap this package exists to prevent.
func FoldLabel(label string) (string, bool) {
	var b strings.Builder
	sep := false
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
			sep = false
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			sep = false
		default:
			if !sep {
				b.WriteByte('_')
				sep = true
			}
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		return "", false
	}
	return name, true
}

// herdrEnvNames lists the names (never the values -- those don't exist until
// the plan is really built) of the Herdr identity variables a command step's
// environment will carry: its own three ids, then one HERDR_PANE_<NAME> per
// labelled pane anywhere in the plan whose label folds to something legal,
// first occurrence in plan order winning a collision. That order matches
// execution's own first-wins rule exactly (see herdrPaneEnv in
// internal/open/setup.go) so a name --dry-run lists is never one the real run
// would have skipped or overwritten differently.
//
// "ID" is pre-seeded into seen because a label that folds to it would collide
// with HERDR_PANE_ID -- the step's own pane id, always present -- which must
// win rather than being quietly replaced by whichever labelled pane happened
// to fold onto the same name.
func (p Plan) herdrEnvNames() []string {
	names := []string{"HERDR_WORKSPACE_ID", "HERDR_TAB_ID", "HERDR_PANE_ID"}
	seen := map[string]bool{"ID": true}
	for _, step := range p.Steps {
		if step.Label == "" {
			continue
		}
		name, ok := FoldLabel(step.Label)
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, "HERDR_PANE_"+name)
	}
	return names
}

// Describe renders a plan as the lines --dry-run prints: one per pane, in the
// order they will be built, with what each will be given.
func (p Plan) Describe() []string {
	var out []string
	tab := -1
	// Computed once: which HERDR_PANE_* names exist depends only on which
	// labels the plan has, the same set for every command step in it.
	envNames := p.herdrEnvNames()
	for _, s := range p.Steps {
		if s.Tab != tab {
			tab = s.Tab
			name := s.TabName
			if name == "" {
				name = fmt.Sprintf("tab %d", s.Tab+1)
			}
			verb := "tab"
			if s.FirstTab {
				verb = "tab (reuses the Space's own)"
			}
			out = append(out, fmt.Sprintf("%s  %s", verb, name))
		}

		where := "  pane"
		if s.Split != "" {
			where = fmt.Sprintf("  split %s", s.Split)
			if s.Ratio > 0 {
				where += fmt.Sprintf(" %.2f", s.Ratio)
			}
		}
		if s.Label != "" {
			where += fmt.Sprintf(" [%s]", s.Label)
		}
		out = append(out, where)

		if s.Cwd != "" {
			out = append(out, "    cwd     "+s.Cwd)
		}
		if s.Worktree != nil {
			// The path above is real -- deterministic, computed from the
			// repo root and the rendered ref alone -- so it is printed
			// unqualified, the same as any other step's cwd. What a preview
			// cannot claim is that it exists: nothing here has touched disk,
			// the same promise WorktreePlaceholder makes for a whole-Space
			// worktree whose path is not even knowable yet. This one differs
			// only in that the path *is* knowable; it still is not there.
			mode := "detached"
			if !s.Worktree.Detach {
				mode = "branch checkout"
			}
			out = append(out, fmt.Sprintf("    worktree ref %s, %s -- not created yet", s.Worktree.Ref, mode))
		}
		for _, k := range sortedKeys(s.Env) {
			out = append(out, fmt.Sprintf("    env     %s=%s", k, s.Env[k]))
		}
		if s.Command != "" {
			out = append(out, "    run     "+s.Command)
			// The ids themselves are not knowable here -- a pane's own id does
			// not exist until Herdr creates it, and this is a preview of a
			// plan that has not touched Herdr at all -- so this states which
			// variables land, not what they will hold. See herdrEnvNames.
			if len(envNames) > 0 {
				out = append(out, "    herdr    "+strings.Join(envNames, ", "))
			}
		}
		if s.Agent != "" {
			out = append(out, "    agent   "+s.Agent)
		}
		if len(s.Args) > 0 {
			// Quoted per value: an arg with a space in it is one arg, and a
			// preview that ran them together would read as several.
			quoted := make([]string, 0, len(s.Args))
			for _, a := range s.Args {
				quoted = append(quoted, fmt.Sprintf("%q", a))
			}
			out = append(out, "    args    "+strings.Join(quoted, " "))
		}
		// Printed here, before the prompt, because that is the order
		// execution runs them in: on_launch answers a startup modal before
		// anything is typed. A preview that hid these would misrepresent
		// what the run actually does with a pane that has them.
		for _, ol := range s.OnLaunch {
			out = append(out, fmt.Sprintf("    on_launch if %q -> keys %s (%dms)", ol.Match, strings.Join(ol.Keys, " "), ol.TimeoutMs))
		}
		if s.Prompt != "" {
			verb := "type"
			if s.Submit {
				verb = "send"
			}
			for i, line := range strings.Split(strings.TrimRight(s.Prompt, "\n"), "\n") {
				label := "    " + verb + "    "
				if i > 0 {
					label = "            "
				}
				out = append(out, label+line)
			}
		}
		if s.WaitFor != nil {
			out = append(out, fmt.Sprintf("    wait    %q (%dms)", s.WaitFor.Match, s.WaitFor.TimeoutMs))
		}
		if s.Focus {
			out = append(out, "    focus   land here")
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// homeDir is a variable so a test can pin it rather than depending on
// whatever home the machine running the suite has.
var homeDir = os.UserHomeDir
