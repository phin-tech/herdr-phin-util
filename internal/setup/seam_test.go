package setup

import "testing"

// TestSetupIsConstructibleWithoutYAML is a design constraint written as a
// test. Plenty of tests in this package build a Setup literal and resolve it;
// this one exists to say *why* that has to keep working, so nobody reads the
// others as merely convenient.
//
// The invariant: a Setup is the seam a second front end would build against.
// Load is one way to get one, not the only way, and nothing about being
// well-formed may come to depend on having been through it. If a future
// change makes Validate or ResolveData need a field only load.go knows how to
// fill, this test is what fails -- rather than the discovery arriving much
// later, when someone tries to add that second front end and finds the door
// has quietly closed. See the package comment for the whole argument.
func TestSetupIsConstructibleWithoutYAML(t *testing.T) {
	// Every field a file would have set, set by hand instead -- and Source,
	// Origin and ScopedRepo deliberately left at their zero values, since a
	// setup that never came from a file has no path to report and no
	// directory to be scoped by.
	s := Setup{
		Name:        "synthesized",
		Description: "built in Go, not decoded from anything",
		AppliesTo:   []string{"github_pr"},
		Cwd:         "sub",
		Env:         map[string]string{"MODE": "{{.Branch}}"},
		Tabs: []Tab{
			{
				Name: "review",
				Panes: []Pane{
					{Label: "orchestrator", Agent: "claude", Model: "opus", Focus: true, Prompt: "Review {{.Branch}}"},
					{Split: "down", Ratio: 0.3, Command: "roborev review"},
				},
			},
		},
	}

	if probs := s.Validate(); len(probs) > 0 {
		t.Fatalf("a hand-built setup did not validate: %v", probs)
	}

	plan, err := s.ResolveData("/repo", Data{Vars: map[string]string{"Branch": "fix/thing"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(plan.Steps))
	}
	if got := plan.Steps[0].Prompt; got != "Review fix/thing" {
		t.Errorf("prompt = %q, want %q", got, "Review fix/thing")
	}
	if got := plan.Steps[0].Env["MODE"]; got != "fix/thing" {
		t.Errorf("env MODE = %q, want %q", got, "fix/thing")
	}
	if !plan.Steps[0].FirstTab {
		t.Error("the first step should reuse the Space's own tab")
	}

	// Matching is the other thing a front end gets for free, and the one that
	// actually reads a zero-valued field: ScopedRepo == "" has to mean "not
	// scoped to any repository" rather than "scoped to the empty one".
	if !s.Matches(Subject{Kind: "github_pr", Owner: "phin-tech", Repo: "herdr-phin-util"}) {
		t.Error("a setup with no ScopedRepo should match on kind alone")
	}
}
