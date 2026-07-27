package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	Tab      int
	TabName  string
	NewTab   bool
	PaneIdx  int
	Label    string
	Split    string
	Ratio    float64
	Cwd      string
	Env      map[string]string
	Agent    string
	Prompt   string
	Submit   bool
	Command  string
	Focus    bool
	WaitFor  *WaitFor
	FirstTab bool
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

// Resolve renders a setup against the target data and the Space's directory.
//
// data is the same map internal/open builds for an [agent.prompts] template,
// so {{.Number}} and {{.Branch}} mean here exactly what they mean there.
func (s Setup) Resolve(baseCwd string, data map[string]string) (Plan, error) {
	plan := Plan{Name: s.Name}

	setupCwd := joinCwd(baseCwd, s.Cwd)
	setupEnv, err := renderEnv(s.Env, data)
	if err != nil {
		return Plan{}, fmt.Errorf("setup env: %w", err)
	}

	focus := -1
	for ti, tab := range s.Tabs {
		tabCwd := joinCwd(setupCwd, tab.Cwd)
		tabEnv, err := renderEnv(tab.Env, data)
		if err != nil {
			return Plan{}, fmt.Errorf("tab %d env: %w", ti+1, err)
		}
		tabEnv = mergeEnv(setupEnv, tabEnv)

		for pi, pane := range tab.EffectivePanes() {
			paneEnv, err := renderEnv(pane.Env, data)
			if err != nil {
				return Plan{}, fmt.Errorf("tab %d pane %d env: %w", ti+1, pi+1, err)
			}

			prompt, err := renderPanePrompt(pane, data)
			if err != nil {
				return Plan{}, fmt.Errorf("tab %d pane %d prompt: %w", ti+1, pi+1, err)
			}
			command, err := Render(pane.Command, data)
			if err != nil {
				return Plan{}, fmt.Errorf("tab %d pane %d command: %w", ti+1, pi+1, err)
			}

			split := pane.Split
			if pi > 0 && split == "" {
				split = DefaultSplit
			}

			step := Step{
				Tab:      ti,
				TabName:  tab.Name,
				NewTab:   pi == 0 && ti > 0,
				FirstTab: ti == 0,
				PaneIdx:  pi,
				Label:    pane.Label,
				Split:    split,
				Ratio:    pane.Ratio,
				Cwd:      joinCwd(tabCwd, pane.Cwd),
				Env:      mergeEnv(tabEnv, paneEnv),
				Agent:    pane.Agent,
				Prompt:   prompt,
				Submit:   pane.Submit,
				Command:  strings.TrimSpace(command),
				Focus:    pane.Focus,
				WaitFor:  normaliseWait(pane.WaitFor),
			}
			if pane.Focus && focus < 0 {
				focus = len(plan.Steps)
			}
			plan.Steps = append(plan.Steps, step)
		}
	}

	if focus < 0 {
		focus = 0
	}
	plan.FocusStep = focus
	return plan, nil
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

// Describe renders a plan as the lines --dry-run prints: one per pane, in the
// order they will be built, with what each will be given.
func (p Plan) Describe() []string {
	var out []string
	tab := -1
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
		for _, k := range sortedKeys(s.Env) {
			out = append(out, fmt.Sprintf("    env     %s=%s", k, s.Env[k]))
		}
		if s.Command != "" {
			out = append(out, "    run     "+s.Command)
		}
		if s.Agent != "" {
			out = append(out, "    agent   "+s.Agent)
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
