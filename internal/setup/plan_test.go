package setup

import (
	"reflect"
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

func TestResolveTurnsModelAndArgsIntoOneCommandLine(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{
		Agent: "claude",
		Model: "opus",
		Args:  []string{"--permission-mode", "plan", "--add-dir", "{{.Path}}"},
	}}}}}

	plan, err := s.Resolve("/repo", map[string]string{"Path": "/repo"})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"--model", "opus", "--permission-mode", "plan", "--add-dir", "/repo"}
	got := plan.Steps[0].Args
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

// A pane that names neither gets no args at all, rather than an empty list
// agent.start would have to be taught to ignore.
func TestResolveLeavesArgsNilWhenNoneAreAsked(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{Agent: "claude"}}}}}
	plan, err := s.Resolve("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Steps[0].Args != nil {
		t.Errorf("args = %v, want nil", plan.Steps[0].Args)
	}
}

// stackData is a Data with a 3-element "layers" list, standing in for what a
// github_stack target would eventually resolve: three layers of a stack,
// each with its own pr number, head sha and worktree path.
func stackData() Data {
	return Data{
		Vars: map[string]string{"Repo": "roux"},
		Lists: map[string][]map[string]string{
			"layers": {
				{"layer": "1", "pr": "101", "head_sha": "aaa", "worktree": "/w/1"},
				{"layer": "2", "pr": "102", "head_sha": "bbb", "worktree": "/w/2"},
				{"layer": "3", "pr": "103", "head_sha": "ccc", "worktree": "/w/3"},
			},
		},
	}
}

func forEachTab() Tab {
	return Tab{
		ForEach: "layers",
		As:      "layer",
		Name:    "L{{.layer_layer}} #{{.layer_pr}}",
		Cwd:     "{{.layer_worktree}}",
		Panes: []Pane{{
			Label:  "l{{.layer_layer}}-claude",
			Agent:  "claude",
			Submit: true,
			Prompt: "Review PR #{{.layer_pr}} at {{.layer_head_sha}}",
		}},
	}
}

func TestResolveDataExpandsForEachIntoOneTabPerElement(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{forEachTab()}}

	plan, err := s.ResolveData("/repo", stackData())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("len(Steps) = %d, want 3, one per layer", len(plan.Steps))
	}

	wantNames := []string{"L1 #101", "L2 #102", "L3 #103"}
	wantCwds := []string{"/w/1", "/w/2", "/w/3"}
	wantLabels := []string{"l1-claude", "l2-claude", "l3-claude"}
	wantPrompts := []string{
		"Review PR #101 at aaa",
		"Review PR #102 at bbb",
		"Review PR #103 at ccc",
	}
	for i, step := range plan.Steps {
		if step.TabName != wantNames[i] {
			t.Errorf("step %d TabName = %q, want %q", i, step.TabName, wantNames[i])
		}
		if step.Cwd != wantCwds[i] {
			t.Errorf("step %d Cwd = %q, want %q", i, step.Cwd, wantCwds[i])
		}
		if step.Label != wantLabels[i] {
			t.Errorf("step %d Label = %q, want %q", i, step.Label, wantLabels[i])
		}
		if step.Prompt != wantPrompts[i] {
			t.Errorf("step %d Prompt = %q, want %q", i, step.Prompt, wantPrompts[i])
		}
	}
}

// Step.Tab, NewTab and FirstTab all describe the emitted tab, not the source
// line of YAML -- a for_each tab following a plain one has to number its
// three repetitions 1, 2, 3, none of which reuse the Space's own tab, since
// the plain tab already claimed that.
func TestResolveDataNumbersEmittedTabsNotSourceTabs(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{
		{Name: "stack", Panes: []Pane{{}}},
		forEachTab(),
	}}

	plan, err := s.ResolveData("/repo", stackData())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 4 {
		t.Fatalf("len(Steps) = %d, want 4 (1 plain + 3 layers)", len(plan.Steps))
	}

	if !plan.Steps[0].FirstTab || plan.Steps[0].NewTab {
		t.Error("the plain tab's pane should reuse the Space's own tab")
	}
	wantTab := []int{0, 1, 2, 3}
	for i, step := range plan.Steps {
		if step.Tab != wantTab[i] {
			t.Errorf("step %d Tab = %d, want %d", i, step.Tab, wantTab[i])
		}
		if i > 0 && (!step.NewTab || step.FirstTab) {
			t.Errorf("step %d NewTab=%v FirstTab=%v, want a new, non-first tab", i, step.NewTab, step.FirstTab)
		}
	}
}

// An empty list is not an error -- a stack of one layer legitimately has no
// "the rest of the stack" -- and the tab after it correctly becomes the one
// that reuses the Space's own tab, since the empty for_each built nothing.
func TestResolveDataEmptyListEmitsZeroTabsAndTheNextTabBecomesFirst(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{
		{ForEach: "layers", Name: "L{{.layer_layer}}", Panes: []Pane{{}}},
		{Name: "after", Panes: []Pane{{}}},
	}}
	data := Data{Lists: map[string][]map[string]string{"layers": {}}}

	plan, err := s.ResolveData("/repo", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1 (the empty for_each built nothing)", len(plan.Steps))
	}
	if !plan.Steps[0].FirstTab || plan.Steps[0].NewTab {
		t.Error("the tab after an empty for_each should reuse the Space's own tab")
	}
	if plan.Steps[0].TabName != "after" {
		t.Errorf("TabName = %q, want %q", plan.Steps[0].TabName, "after")
	}
}

// A missing list has to fail before anything is built, and the error has to
// say which name was missing -- a typo'd for_each that silently built zero
// tabs would be far harder to track down than a load error.
func TestResolveDataMissingListIsAnError(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{
		{Name: "stack", Panes: []Pane{{}}},
		{ForEach: "layers", Name: "L{{.layer_layer}}", Panes: []Pane{{}}},
	}}

	_, err := s.ResolveData("/repo", Data{})
	if err == nil {
		t.Fatal("want an error, got none")
	}
	if !strings.Contains(err.Error(), `tab 2: for_each names "layers", but this target provides no lists`) {
		t.Errorf("error = %q, want it to name the missing list", err.Error())
	}
}

// A missing list among several available ones names what was on offer,
// sorted, so the message reads the same on every run.
func TestResolveDataMissingListNamesWhatIsAvailable(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{{ForEach: "widgets", Panes: []Pane{{}}}}}
	data := Data{Lists: map[string][]map[string]string{
		"layers": {{"a": "1"}},
		"deps":   {{"a": "1"}},
	}}

	_, err := s.ResolveData("/repo", data)
	if err == nil {
		t.Fatal("want an error, got none")
	}
	if !strings.Contains(err.Error(), "deps, layers") {
		t.Errorf("error = %q, want the available lists named, sorted", err.Error())
	}
}

// as: defaults to the for_each name when left blank, so {{.layers_pr}} is
// available without also having to write "as: layers".
func TestResolveDataAsDefaultsToForEachName(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{{
		ForEach: "layers",
		Panes:   []Pane{{Command: "echo {{.layers_pr}}"}},
	}}}
	data := Data{Lists: map[string][]map[string]string{
		"layers": {{"pr": "101"}},
	}}

	plan, err := s.ResolveData("/repo", data)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Steps[0].Command; got != "echo 101" {
		t.Errorf("command = %q, want the default as-prefix to resolve", got)
	}
}

// <as>_index is 1-based: the first element is 1, matching how a person reads
// "layer 1" rather than the zero a programmer would default to.
func TestResolveDataIndexIsOneBased(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{{
		ForEach: "layers",
		As:      "layer",
		Panes:   []Pane{{Command: "echo {{.layer_index}}"}},
	}}}
	data := Data{Lists: map[string][]map[string]string{
		"layers": {{"pr": "101"}, {"pr": "102"}},
	}}

	plan, err := s.ResolveData("/repo", data)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Steps[0].Command != "echo 1" {
		t.Errorf("first element index = %q, want \"1\"", plan.Steps[0].Command)
	}
	if plan.Steps[1].Command != "echo 2" {
		t.Errorf("second element index = %q, want \"2\"", plan.Steps[1].Command)
	}
}

// A setup with no for_each anywhere is Resolve's whole existing surface, and
// has to keep resolving byte-identical to what it produced before ResolveData
// existed -- the regression guard for everyone who never writes a for_each.
func TestResolveDataWithNoForEachIsIdenticalToResolve(t *testing.T) {
	s := Setup{
		Name: "x",
		Cwd:  "sub",
		Tabs: []Tab{
			{Name: "a", Cwd: "web", Panes: []Pane{
				{Label: "one", Agent: "claude", Prompt: "review #{{.Number}}"},
				{Split: "down", Command: "roborev {{.Branch}}"},
			}},
			{Name: "b", Command: "lazygit"},
		},
	}
	vars := map[string]string{"Number": "42", "Branch": "fix/thing"}

	viaResolve, err := s.Resolve("/repo", vars)
	if err != nil {
		t.Fatal(err)
	}
	viaResolveData, err := s.ResolveData("/repo", Data{Vars: vars})
	if err != nil {
		t.Fatal(err)
	}

	if len(viaResolve.Steps) != len(viaResolveData.Steps) {
		t.Fatalf("Resolve produced %d steps, ResolveData %d", len(viaResolve.Steps), len(viaResolveData.Steps))
	}
	for i := range viaResolve.Steps {
		if !reflect.DeepEqual(viaResolve.Steps[i], viaResolveData.Steps[i]) {
			t.Errorf("step %d differs:\n  Resolve:     %+v\n  ResolveData: %+v", i, viaResolve.Steps[i], viaResolveData.Steps[i])
		}
	}
	if viaResolve.FocusStep != viaResolveData.FocusStep {
		t.Errorf("FocusStep = %d, want %d", viaResolveData.FocusStep, viaResolve.FocusStep)
	}
}

// Describe's output for an expanded plan reads like three tabs, each named
// for its own layer, not like one tab described three times over.
func TestDescribeCoversAnExpandedForEachPlan(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{forEachTab()}}
	plan, err := s.ResolveData("/repo", stackData())
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Join(plan.Describe(), "\n")

	for _, want := range []string{"L1 #101", "L2 #102", "L3 #103", "l1-claude", "l2-claude", "l3-claude"} {
		if !strings.Contains(out, want) {
			t.Errorf("Describe() does not mention %q:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "tab (reuses the Space's own)"); got != 1 {
		t.Errorf("Describe() reused the Space's own tab %d times, want exactly 1", got)
	}
}

func TestDescribeShowsTheAgentCommandLine(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{{Name: "a", Panes: []Pane{{
		Agent: "claude", Model: "opus", Args: []string{"--permission-mode", "plan"},
	}}}}}
	plan, err := s.Resolve("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}

	got := strings.Join(plan.Describe(), "\n")
	if !strings.Contains(got, `args    "--model" "opus" "--permission-mode" "plan"`) {
		t.Errorf("--dry-run does not show the command line:\n%s", got)
	}
}

func TestFoldLabel(t *testing.T) {
	cases := []struct {
		label string
		want  string
		ok    bool
	}{
		{"meta-orchestrator", "META_ORCHESTRATOR", true},
		{"Worker #2!", "WORKER_2", true},
		{"  spaced out  ", "SPACED_OUT", true},
		{"!!!", "", false},
		{"2fast", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := FoldLabel(c.label)
		if got != c.want || ok != c.ok {
			t.Errorf("FoldLabel(%q) = (%q, %v), want (%q, %v)", c.label, got, ok, c.want, c.ok)
		}
	}
}

// --dry-run cannot know a pane's real id -- nothing has been built yet -- but
// it can and must be honest about which HERDR_ variable *names* a command
// pane will receive, since those depend only on which labels the plan has.
func TestDescribeListsTheHerdrVariablesACommandPaneWillReceive(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{{Name: "review", Panes: []Pane{
		{Label: "meta-orchestrator", Agent: "claude"},
		{Split: "down", Command: "./discover.py"},
	}}}}
	plan, err := s.Resolve("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}

	out := strings.Join(plan.Describe(), "\n")
	for _, want := range []string{
		"HERDR_WORKSPACE_ID", "HERDR_TAB_ID", "HERDR_PANE_ID", "HERDR_PANE_META_ORCHESTRATOR",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Describe() does not list %q for the command pane:\n%s", want, out)
		}
	}
	// The ids themselves are never knowable before anything is built, so the
	// preview must not fabricate one.
	if strings.Contains(out, "HERDR_PANE_ID=") || strings.Contains(out, "HERDR_TAB_ID=") {
		t.Errorf("Describe() fabricated a real id instead of just naming the variable:\n%s", out)
	}
}

// A plan with no command pane at all has nothing to name -- an agent pane
// gets no HERDR_ variables (see herdrPaneEnv in internal/open), so its own
// Describe entry should not claim otherwise.
func TestDescribeNamesNoHerdrVariablesForAnAgentOnlyPlan(t *testing.T) {
	s := Setup{Name: "x", Tabs: []Tab{{Name: "review", Panes: []Pane{
		{Label: "orchestrator", Agent: "claude", Prompt: "go"},
	}}}}
	plan, err := s.Resolve("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}

	out := strings.Join(plan.Describe(), "\n")
	if strings.Contains(out, "HERDR_") {
		t.Errorf("Describe() named Herdr variables for an agent-only plan:\n%s", out)
	}
}
