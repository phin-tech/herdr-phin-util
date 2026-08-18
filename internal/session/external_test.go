package session

import (
	"testing"
	"time"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/source"
)

func TestExternalCandidatesMapRows(t *testing.T) {
	var gotSpec source.Spec
	run := func(spec source.Spec) source.Result {
		gotSpec = spec
		return source.Result{
			Name: spec.Name,
			Rows: []source.Row{
				{ID: "anyscale:expwrk_2d74", Label: "jim-dev2", Detail: "RUNNING · anyscale", Command: []string{"connect", "--name", "jim-dev2"}},
			},
			Problems: []string{"one row was malformed"},
		}
	}

	cands, problems := ExternalCandidates(run, config.SourceSettings{
		Name: "anyscale", Command: []string{"herdr-anyscale-remote", "picker-rows"}, TimeoutMs: 3000,
	})

	// The config's milliseconds become the runner's duration, in one place.
	if gotSpec.Timeout != 3*time.Second || gotSpec.Name != "anyscale" {
		t.Errorf("spec built wrong: %+v", gotSpec)
	}
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(cands))
	}
	c := cands[0]
	if c.Kind != KindExternal {
		t.Errorf("kind = %q, want %q", c.Kind, KindExternal)
	}
	if c.ID != "anyscale:expwrk_2d74" || c.Label != "jim-dev2" || c.Detail != "RUNNING · anyscale" {
		t.Errorf("candidate mismatch: %+v", c)
	}
	if len(c.Command) != 3 || c.Command[2] != "jim-dev2" {
		t.Errorf("command not carried through: %v", c.Command)
	}
	// Problems pass straight through, so a failing source is reported rather
	// than swallowed.
	if len(problems) != 1 {
		t.Errorf("problems = %v, want the one reported", problems)
	}
}
