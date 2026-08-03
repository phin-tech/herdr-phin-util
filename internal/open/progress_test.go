package open

import (
	"errors"
	"strings"
	"testing"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/setup"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// collect records what a run reported, in order, which is the thing a caller
// draws.
func collect(events *[]Event) Progress {
	return func(e Event) { *events = append(*events, e) }
}

// labels is the started-step labels in order, which is the checklist as a
// person reads it down the popup.
func labels(events []Event) []string {
	var out []string
	for _, e := range events {
		if !e.Done {
			out = append(out, e.Label)
		}
	}
	return out
}

// Every step a setup takes should be named as it happens: the panes going up,
// then each pane being given what it is for, by the name the file gave it.
func TestApplySetupReportsEveryPaneItFills(t *testing.T) {
	var events []Event
	deps := Deps{Session: &fakeSession{}, Layout: &fakeLayout{}, Progress: collect(&events)}

	if _, _, _, err := applySetup(deps, &config.Settings{}, target.Target{}, reviewSetup(), rootPane(), "w1", "/repo", map[string]string{"Number": "42"}); err != nil {
		t.Fatal(err)
	}

	got := strings.Join(labels(events), "\n")
	for _, want := range []string{
		"Building 4 panes",
		"Starting orchestrator in review",
		"Running roborev in review",
		`Waiting for "queued" in review`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("progress missing %q:\n%s", want, got)
		}
	}
}

// Every step that starts has to finish, or the checklist keeps an open box
// next to work that is over and the popup never stops ticking.
func TestApplySetupClosesEveryStepItOpens(t *testing.T) {
	var events []Event
	deps := Deps{Session: &fakeSession{}, Layout: &fakeLayout{}, Progress: collect(&events)}

	if _, _, _, err := applySetup(deps, &config.Settings{}, target.Target{}, reviewSetup(), rootPane(), "w1", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	unclosed := map[string]bool{}
	for _, e := range events {
		if e.Done {
			delete(unclosed, e.Key)
			continue
		}
		unclosed[e.Key] = true
	}
	if len(unclosed) != 0 {
		t.Errorf("steps left open: %v", unclosed)
	}
}

// A pane that fails is reported as a failed step rather than silently closed,
// since that is the line the checklist puts a cross against.
func TestApplySetupReportsAFailedPaneAsAFailedStep(t *testing.T) {
	var events []Event
	deps := Deps{
		Session:  &fakeSession{waitOutputErr: errors.New("timed out")},
		Layout:   &fakeLayout{},
		Progress: collect(&events),
	}
	def := setup.Setup{Name: "x", Tabs: []setup.Tab{{Name: "reviewers", Panes: []setup.Pane{
		{Label: "codex-reviewer", Agent: "codex", Prompt: "review", Submit: true},
	}}}}

	if _, _, _, err := applySetup(deps, &config.Settings{}, target.Target{}, def, rootPane(), "w1", "/repo", nil); err != nil {
		t.Fatal(err)
	}

	var failed bool
	for _, e := range events {
		if e.Done && e.Err != nil {
			failed = true
		}
	}
	if !failed {
		t.Errorf("no step carried the failure:\n%+v", events)
	}
}

// A nil Progress is the normal case -- every CLI path -- and must not be a
// special case at any call site.
func TestApplySetupRunsWithoutAnyoneListening(t *testing.T) {
	deps := Deps{Session: &fakeSession{}, Layout: &fakeLayout{}}
	if _, _, _, err := applySetup(deps, &config.Settings{}, target.Target{}, reviewSetup(), rootPane(), "w1", "/repo", nil); err != nil {
		t.Fatalf("a run with no Progress should behave exactly as before: %v", err)
	}
}
