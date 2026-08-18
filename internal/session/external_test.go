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

func TestExternalCandidatesStampChildren(t *testing.T) {
	run := func(spec source.Spec) source.Result {
		return source.Result{Name: spec.Name, Rows: []source.Row{
			{ID: "m1", Label: "machine", Command: []string{"attach"}, Children: []string{"list", "--repos"}, ChildrenLabel: "repos"},
			{ID: "leaf", Label: "flat", Command: []string{"go"}},
		}}
	}
	cands, _ := ExternalCandidates(run, config.SourceSettings{Name: "anyscale", Command: []string{"x"}, TimeoutMs: 4000})

	if len(cands) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(cands))
	}
	withKids := cands[0]
	if len(withKids.Children) != 2 || withKids.ChildrenLabel != "repos" {
		t.Errorf("children not carried: %+v", withKids)
	}
	// A row with children inherits the source's timeout, so the level below is
	// bounded the same way without a config entry.
	if withKids.ChildrenTimeout != 4*time.Second {
		t.Errorf("ChildrenTimeout = %v, want the source's 4s", withKids.ChildrenTimeout)
	}
	// A leaf keeps none of it.
	if cands[1].Children != nil || cands[1].ChildrenTimeout != 0 {
		t.Errorf("leaf row should carry no children state: %+v", cands[1])
	}
}

func TestExternalChildrenInheritTimeoutAndName(t *testing.T) {
	var gotSpec source.Spec
	run := func(spec source.Spec) source.Result {
		gotSpec = spec
		return source.Result{Name: spec.Name, Rows: []source.Row{{ID: "r1", Label: "sous-chef", Command: []string{"branches"}}}}
	}

	cands, _ := ExternalChildren(run, "sam-post-vpn-fix", []string{"list", "--repos-on", "sam-post-vpn-fix"}, 3*time.Second)

	if gotSpec.Name != "sam-post-vpn-fix" || gotSpec.Timeout != 3*time.Second {
		t.Errorf("children spec built wrong: %+v", gotSpec)
	}
	if len(cands) != 1 || cands[0].Label != "sous-chef" || cands[0].Kind != KindExternal {
		t.Errorf("children not mapped: %+v", cands)
	}
}
