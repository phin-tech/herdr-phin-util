package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/session"
	"github.com/phin-tech/herdr-phin-util/internal/source"
)

// externalPicker builds a project picker wired to a fake source runner, so the
// rows and problems a source "prints" are literals rather than a spawned
// process.
func externalPicker(t *testing.T, run source.Runner, sources []config.SourceSettings, candidates ...session.Candidate) *Picker {
	t.Helper()
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}, Sources: sources}
	return NewPicker(cfg, session.Deps{}, nil, candidates).WithSources(run)
}

// drainSources runs every source command Init would fire and feeds the
// resulting messages back through Update, which is what the bubbletea runtime
// does for real -- just synchronously, so a test can assert on the outcome.
func drainSources(p *Picker) {
	for _, cmd := range p.fetchSources() {
		if msg := cmd(); msg != nil {
			p.Update(msg)
		}
	}
}

func fakeRunner(byName map[string]source.Result) source.Runner {
	return func(spec source.Spec) source.Result {
		res := byName[spec.Name]
		res.Name = spec.Name
		return res
	}
}

func externalRow(id, label string) source.Row {
	return source.Row{ID: id, Label: label, Detail: "RUNNING · anyscale", Command: []string{"connect", "--name", label}}
}

// press sends one key and drains the command it returned, defined alongside
// run in picker_worktree_test.go.

var (
	keyTab      = tea.KeyMsg{Type: tea.KeyTab}
	keyShiftTab = tea.KeyMsg{Type: tea.KeyShiftTab}
	keyEsc      = tea.KeyMsg{Type: tea.KeyEsc}
	keyEnter    = tea.KeyMsg{Type: tea.KeyEnter}
	keyDown     = tea.KeyMsg{Type: tea.KeyDown}
)

// nestedRunner answers each level of a machine → repos → branches tree by the
// name descendExternal fetches with (the label of the row descended into).
func nestedRunner() source.Runner {
	return fakeRunner(map[string]source.Result{
		"anyscale": {Rows: []source.Row{{
			ID: "m1", Label: "machine", Detail: "RUNNING", Command: []string{"connect"},
			Children: []string{"list", "--repos"}, ChildrenLabel: "repos",
		}}},
		"machine": {Rows: []source.Row{{
			ID: "r1", Label: "sous-chef", Detail: "/mnt/repos/sous-chef",
			Children: []string{"list", "--branches"}, ChildrenLabel: "branches",
		}}},
		"sous-chef": {Rows: []source.Row{
			{ID: "b1", Label: "main", Command: []string{"worktree", "main"}},
			{ID: "b2", Label: "feature", Command: []string{"worktree", "feature"}},
		}},
	})
}

func TestTabDescendsIntoExternalChildren(t *testing.T) {
	p := externalPicker(t, nestedRunner(), []config.SourceSettings{{Name: "anyscale", Command: []string{"x"}}})
	p.width, p.height = 80, 24
	drainSources(p)

	if got := labels(p.filtered); strings.Join(got, ",") != "machine" {
		t.Fatalf("top level: got %v, want machine", got)
	}

	press(p, keyTab) // descend into the machine's repos
	if p.level != levelExternal {
		t.Fatalf("after tab: level = %v, want levelExternal", p.level)
	}
	if got := labels(p.filtered); strings.Join(got, ",") != "sous-chef" {
		t.Fatalf("repos level: got %v, want sous-chef", got)
	}
	if !strings.Contains(p.View(), "machine") {
		t.Errorf("breadcrumb should name the descended machine, view:\n%s", p.View())
	}

	press(p, keyTab) // descend again into the repo's branches
	if got := labels(p.filtered); strings.Join(got, ",") != "main,feature" {
		t.Fatalf("branches level: got %v, want main,feature", got)
	}
	// Two levels deep: the breadcrumb carries both crumbs.
	view := p.View()
	if !strings.Contains(view, "machine") || !strings.Contains(view, "sous-chef") {
		t.Errorf("breadcrumb should show the whole trail, view:\n%s", view)
	}
}

func TestPopExternalRestoresParent(t *testing.T) {
	p := externalPicker(t, nestedRunner(), []config.SourceSettings{{Name: "anyscale", Command: []string{"x"}}})
	p.width, p.height = 80, 24
	drainSources(p)

	press(p, keyTab)  // into repos
	press(p, keyTab)  // into branches
	press(p, keyDown) // select "feature"
	if sel, _ := p.selected(); sel.Label != "feature" {
		t.Fatalf("precondition: selected %q, want feature", sel.Label)
	}

	press(p, keyEsc) // back to repos
	if p.level != levelExternal || strings.Join(labels(p.filtered), ",") != "sous-chef" {
		t.Fatalf("after one pop: level=%v rows=%v, want repos", p.level, labels(p.filtered))
	}

	press(p, keyShiftTab) // back to the top
	if p.level != levelProjects || strings.Join(labels(p.filtered), ",") != "machine" {
		t.Fatalf("after full pop: level=%v rows=%v, want the top", p.level, labels(p.filtered))
	}
}

// A pure branch node -- children, no command -- descends on Enter, since there
// is nothing to open.
func TestEnterDescendsCommandlessNode(t *testing.T) {
	p := externalPicker(t, nestedRunner(), []config.SourceSettings{{Name: "anyscale", Command: []string{"x"}}})
	p.width, p.height = 80, 24
	drainSources(p)

	press(p, keyTab) // now on "sous-chef", which has children and no command
	if sel, _ := p.selected(); len(sel.Command) != 0 || !hasChildren(sel) {
		t.Fatalf("precondition: sous-chef should be a command-less node: %+v", sel)
	}

	press(p, keyEnter)
	if p.level != levelExternal || strings.Join(labels(p.filtered), ",") != "main,feature" {
		t.Errorf("Enter on a branch node should descend, got level=%v rows=%v", p.level, labels(p.filtered))
	}
	// Descending is not opening: the picker is still running, nothing was picked.
	if _, _, _, done := p.Result(); done {
		t.Error("Enter on a branch node should not finish the picker")
	}
}

// A leaf row -- a command and no children -- offers no descent, so tab does
// nothing rather than opening anything.
func TestTabOnLeafExternalRowDoesNothing(t *testing.T) {
	p := externalPicker(t, nestedRunner(), []config.SourceSettings{{Name: "anyscale", Command: []string{"x"}}})
	p.width, p.height = 80, 24
	drainSources(p)
	press(p, keyTab) // repos
	press(p, keyTab) // branches -- "main" is a leaf

	before := p.level
	press(p, keyTab)
	if p.level != before {
		t.Errorf("tab on a leaf should not change level, went %v -> %v", before, p.level)
	}
}

func TestFailingChildrenShowsWarning(t *testing.T) {
	run := fakeRunner(map[string]source.Result{
		"anyscale": {Rows: []source.Row{{ID: "m1", Label: "machine", Command: []string{"connect"}, Children: []string{"list"}, ChildrenLabel: "repos"}}},
		"machine":  {Problems: []string{`source "machine" timed out after 3s`}},
	})
	p := externalPicker(t, run, []config.SourceSettings{{Name: "anyscale", Command: []string{"x"}}})
	p.width, p.height = 80, 24
	drainSources(p)

	press(p, keyTab)
	if p.level != levelExternal {
		t.Fatalf("a failing descent should still push the level so the reason is visible, level=%v", p.level)
	}
	if !strings.Contains(p.View(), "timed out") {
		t.Errorf("the descent's failure should show as a ⚠ line, view:\n%s", p.View())
	}

	// Popping back clears that level's problem, restoring the parent's (none).
	press(p, keyEsc)
	if len(p.sourceProblems) != 0 {
		t.Errorf("parent level should have no problems after pop, got %v", p.sourceProblems)
	}
}

func TestExternalRowsLandBesideLocalRows(t *testing.T) {
	run := fakeRunner(map[string]source.Result{
		"anyscale": {Rows: []source.Row{externalRow("a1", "jim-dev2"), externalRow("a2", "neil")}},
	})
	p := externalPicker(t, run,
		[]config.SourceSettings{{Name: "anyscale", Command: []string{"x"}}},
		project("local-repo", "/src/local-repo"),
	)

	// Before any source answers, only the local row is present -- local rows
	// draw first, which is the whole point of fetching off the critical path.
	if got := labels(p.filtered); strings.Join(got, ",") != "local-repo" {
		t.Fatalf("before sources: got %v, want just the local row", got)
	}

	drainSources(p)

	got := labels(p.filtered)
	want := "local-repo,jim-dev2,neil"
	if strings.Join(got, ",") != want {
		t.Errorf("after sources: got %v, want %v", got, want)
	}
	if p.filtered[1].Kind != session.KindExternal {
		t.Errorf("external row has kind %q, want %q", p.filtered[1].Kind, session.KindExternal)
	}
}

func TestExternalRowsAreDedupedByID(t *testing.T) {
	run := fakeRunner(map[string]source.Result{
		"a": {Rows: []source.Row{externalRow("shared", "box"), externalRow("only-a", "a-box")}},
		"b": {Rows: []source.Row{externalRow("shared", "box"), externalRow("only-b", "b-box")}},
	})
	p := externalPicker(t, run, []config.SourceSettings{
		{Name: "a", Command: []string{"x"}},
		{Name: "b", Command: []string{"y"}},
	})

	drainSources(p)

	// "shared" appears once; the two unique rows survive.
	got := labels(p.filtered)
	if len(got) != 3 {
		t.Fatalf("want 3 rows after dedup, got %v", got)
	}
	shared := 0
	for _, l := range got {
		if l == "box" {
			shared++
		}
	}
	if shared != 1 {
		t.Errorf("shared row appears %d times, want exactly 1: %v", shared, got)
	}
}

// A source answering while the user has navigated down should not yank the
// selection back to the top: the row they were on stays highlighted.
func TestExternalRowsPreserveSelection(t *testing.T) {
	run := fakeRunner(map[string]source.Result{
		"anyscale": {Rows: []source.Row{externalRow("a1", "remote")}},
	})
	p := externalPicker(t, run,
		[]config.SourceSettings{{Name: "anyscale", Command: []string{"x"}}},
		project("one", "/src/one"),
		project("two", "/src/two"),
	)

	p.Update(tea.KeyMsg{Type: tea.KeyDown}) // now on "two"
	if sel, _ := p.selected(); sel.Label != "two" {
		t.Fatalf("precondition: selection is %q, want two", sel.Label)
	}

	drainSources(p)

	if sel, _ := p.selected(); sel.Label != "two" {
		t.Errorf("selection jumped to %q when a source answered; want two", sel.Label)
	}
}

func TestFailingSourceIsReportedNotSwallowed(t *testing.T) {
	run := fakeRunner(map[string]source.Result{
		"anyscale": {Problems: []string{`source "anyscale" timed out after 3s`}},
	})
	p := externalPicker(t, run, []config.SourceSettings{{Name: "anyscale", Command: []string{"x"}}})
	p.width, p.height = 80, 24

	drainSources(p)

	if len(p.sourceProblems) != 1 {
		t.Fatalf("want the problem kept, got %v", p.sourceProblems)
	}
	view := p.View()
	if !strings.Contains(view, "timed out") {
		t.Errorf("the missing rows' reason should be on screen, view was:\n%s", view)
	}
}

// A run-only row (opens:false) says "running", not "opening", in the popup --
// no Space is being made, so "opening" would name a thing that is not
// happening.
func TestRunOnlyRowStatusSaysRunning(t *testing.T) {
	opening := session.Candidate{Kind: session.KindExternal, Label: "goes-somewhere", Command: []string{"go"}, Opens: true}
	runOnly := session.Candidate{Kind: session.KindExternal, Label: "just-does-it", Command: []string{"go"}, Opens: false}

	if got := statusFor(opening); !strings.HasPrefix(got, "opening") {
		t.Errorf("opens:true status = %q, want it to open", got)
	}
	if got := statusFor(runOnly); !strings.HasPrefix(got, "running") {
		t.Errorf("opens:false status = %q, want it to run", got)
	}
}

// A run-only row is fed through the picker like any external row: it lands in
// the list carrying Opens=false, so submit will route it to the run-only path.
func TestRunOnlyRowLandsWithOpensFalse(t *testing.T) {
	run := fakeRunner(map[string]source.Result{
		"anyscale": {Rows: []source.Row{{ID: "a", Label: "mirror", Command: []string{"connect", "--handoff"}, Opens: boolp(false)}}},
	})
	p := externalPicker(t, run, []config.SourceSettings{{Name: "anyscale", Command: []string{"x"}}})
	drainSources(p)

	if len(p.filtered) != 1 {
		t.Fatalf("want the run-only row, got %v", labels(p.filtered))
	}
	if p.filtered[0].Opens {
		t.Error("the run-only row should carry Opens=false into the picker")
	}
}

func boolp(b bool) *bool { return &b }

// An external row runs its source's own command, so it offers neither the
// agent step nor a setup -- picking it is a single act.
func TestExternalRowStartsNoAgent(t *testing.T) {
	c := session.Candidate{Kind: session.KindExternal, ID: "a1", Label: "remote", Command: []string{"go"}}
	if startsAnAgent(c) {
		t.Error("an external row should not start an agent")
	}
	if offersSetups(c) {
		t.Error("an external row should not offer setups")
	}
}

// The worktree picker is inside one repository, where a remote machine is not
// one of the branches, so it never fetches sources.
func TestWorktreePickerDoesNotFetchSources(t *testing.T) {
	run := fakeRunner(map[string]source.Result{"anyscale": {Rows: []source.Row{externalRow("a1", "remote")}}})
	cfg := &config.Settings{Agent: config.AgentSettings{Enabled: true, Kind: "claude"}, Sources: []config.SourceSettings{{Name: "anyscale", Command: []string{"x"}}}}
	p := NewWorktreePicker(cfg, session.Deps{}, nil, session.RepoContext{Name: "repo"}, nil).WithSources(run)

	if cmds := p.fetchSources(); cmds != nil {
		t.Errorf("worktree picker should not fetch sources, got %d commands", len(cmds))
	}
}
