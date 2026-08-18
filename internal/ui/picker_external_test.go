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
