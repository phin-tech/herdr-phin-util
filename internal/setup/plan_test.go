package setup

import (
	"strings"
	"testing"
)

func TestResolveComposesCwdDownTheLevels(t *testing.T) {
	s := Setup{
		Name: "x",
		Cwd:  "sub",
		Tabs: []Tab{
			{Name: "a", Cwd: "web", Panes: []Pane{
				{},
				{Split: "down", Cwd: "src"},
				{Split: "down", Cwd: "/elsewhere"},
			}},
			{Name: "b"},
		},
	}

	plan, err := s.Resolve("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"/repo/sub/web", "/repo/sub/web/src", "/elsewhere", "/repo/sub"}
	for i, w := range want {
		if plan.Steps[i].Cwd != w {
			t.Errorf("step %d cwd = %q, want %q", i, plan.Steps[i].Cwd, w)
		}
	}
}

func TestResolveMergesEnvDownTheLevels(t *testing.T) {
	s := Setup{
		Name: "x",
		Env:  map[string]string{"A": "setup", "B": "setup"},
		Tabs: []Tab{{
			Name: "a",
			Env:  map[string]string{"B": "tab", "C": "tab"},
			Panes: []Pane{
				{Env: map[string]string{"C": "pane"}},
				{Split: "down"},
			},
		}},
	}

	plan, err := s.Resolve("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}

	first := plan.Steps[0].Env
	if first["A"] != "setup" || first["B"] != "tab" || first["C"] != "pane" {
		t.Errorf("first pane env = %v, want the closest level to win each key", first)
	}
	// The pane-level override must not have leaked sideways into its sibling.
	if second := plan.Steps[1].Env; second["C"] != "tab" {
		t.Errorf("second pane env = %v, want C from the tab", second)
	}
}

func TestResolveRendersPromptsAndCommands(t *testing.T) {
	s := Setup{
		Name: "x",
		Tabs: []Tab{{Name: "a", Panes: []Pane{
			{Agent: "claude", Prompt: "review #{{.Number}} on {{.Branch}}"},
			{Split: "down", Command: "roborev review {{.Branch}}"},
			{Split: "down", Agent: "claude", Skill: "code-review"},
		}}},
	}

	plan, err := s.Resolve("/repo", map[string]string{"Number": "42", "Branch": "fix/thing"})
	if err != nil {
		t.Fatal(err)
	}

	if got := plan.Steps[0].Prompt; got != "review #42 on fix/thing" {
		t.Errorf("prompt = %q", got)
	}
	if got := plan.Steps[1].Command; got != "roborev review fix/thing" {
		t.Errorf("command = %q", got)
	}
	// A skill without its slash is still a slash command.
	if got := plan.Steps[2].Prompt; got != "/code-review" {
		t.Errorf("skill = %q, want a leading slash", got)
	}
}

// A misspelled placeholder must not fail the whole action, the same way it
// does not in an [agent.prompts] template.
func TestResolveTypoInPlaceholderRendersEmpty(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{Agent: "claude", Prompt: "n={{.Numbr}}"}}}}}

	plan, err := s.Resolve("/repo", map[string]string{"Number": "42"})
	if err != nil {
		t.Fatalf("Resolve() failed on a typo: %v", err)
	}
	if got := plan.Steps[0].Prompt; got != "n=" {
		t.Errorf("prompt = %q, want the placeholder to render empty", got)
	}
}

func TestResolveDefaultsAndFlags(t *testing.T) {
	s := Setup{
		Name: "x",
		Tabs: []Tab{
			{Name: "a", Panes: []Pane{{}, {}}},
			{Name: "b", Command: "lazygit"},
		},
	}

	plan, err := s.Resolve("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}

	if plan.Steps[0].Split != "" {
		t.Errorf("first pane split = %q, want none -- it is the tab itself", plan.Steps[0].Split)
	}
	if plan.Steps[1].Split != DefaultSplit {
		t.Errorf("second pane split = %q, want the %q default", plan.Steps[1].Split, DefaultSplit)
	}
	if !plan.Steps[0].FirstTab || plan.Steps[0].NewTab {
		t.Error("the first pane of the first tab must reuse the Space's own tab")
	}
	if !plan.Steps[2].NewTab {
		t.Error("the second tab has to be created")
	}
	if plan.Steps[2].Command != "lazygit" {
		t.Errorf("tab command shorthand = %q", plan.Steps[2].Command)
	}
}

func TestResolveFocusStep(t *testing.T) {
	unmarked := Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{}, {Split: "down"}}}}}
	plan, err := unmarked.Resolve("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.FocusStep != 0 {
		t.Errorf("FocusStep = %d, want the first pane when none is marked", plan.FocusStep)
	}

	marked := Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{}, {Split: "down", Focus: true}}}}}
	plan, err = marked.Resolve("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.FocusStep != 1 {
		t.Errorf("FocusStep = %d, want the marked pane", plan.FocusStep)
	}
}

func TestResolveFillsInWaitTimeout(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{
		{Command: "roborev", WaitFor: &WaitFor{Match: "queued"}},
		{Split: "down", Command: "x", WaitFor: &WaitFor{Match: "  "}},
	}}}}

	plan, err := s.Resolve("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Steps[0].WaitFor.TimeoutMs != DefaultWaitTimeoutMs {
		t.Errorf("timeout = %d, want the default", plan.Steps[0].WaitFor.TimeoutMs)
	}
	if plan.Steps[1].WaitFor != nil {
		t.Error("a wait_for with a blank match should be dropped, not waited on")
	}
}

func TestDescribeCoversWhatAPlanWillDo(t *testing.T) {
	s := Setup{
		Name: "pr-review",
		Tabs: []Tab{{Name: "review", Panes: []Pane{
			{Label: "orchestrator", Agent: "claude", Prompt: "line one\nline two", Focus: true},
			{Split: "right", Ratio: 0.25, Command: "roborev review-branch", WaitFor: &WaitFor{Match: "queued"}},
		}}},
	}

	plan, err := s.Resolve("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Join(plan.Describe(), "\n")

	for _, want := range []string{
		"review",         // the tab name
		"orchestrator",   // the pane label
		"split right",    // how it is built
		"0.25",           // the ratio
		"agent   claude", // what runs
		"line one",       // the prompt, in full
		"line two",
		"queued",    // the wait
		"land here", // the focus
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Describe() does not mention %q:\n%s", want, out)
		}
	}

	// A typed prompt and a submitted one have to be distinguishable in a
	// preview: it is the difference between reading it and it being gone.
	if !strings.Contains(out, "type ") {
		t.Errorf("Describe() does not say the prompt is only typed:\n%s", out)
	}
}
