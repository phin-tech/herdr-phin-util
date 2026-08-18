package config

import (
	"strings"
	"testing"
)

func TestNoSourcesByDefault(t *testing.T) {
	got, err := LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(got.Sources) != 0 {
		t.Errorf("Sources = %v, want none when no [[sources]] is written", got.Sources)
	}
}

func TestSourcesAreReadWithDefaultTimeout(t *testing.T) {
	dir := writeConfig(t, `
[[sources]]
name = "anyscale"
command = ["herdr-anyscale-remote", "picker-rows"]

[[sources]]
name = "ec2"
command = ["list-boxes"]
timeout_ms = 5000
`)
	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(got.Problems) != 0 {
		t.Fatalf("Problems = %v, want none", got.Problems)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("want 2 sources, got %d: %+v", len(got.Sources), got.Sources)
	}

	first := got.Sources[0]
	if first.Name != "anyscale" || strings.Join(first.Command, " ") != "herdr-anyscale-remote picker-rows" {
		t.Errorf("first source mismatch: %+v", first)
	}
	if first.TimeoutMs != defaultSourceTimeoutMs {
		t.Errorf("omitted timeout should default to %d, got %d", defaultSourceTimeoutMs, first.TimeoutMs)
	}
	if got.Sources[1].TimeoutMs != 5000 {
		t.Errorf("explicit timeout not honoured: %d", got.Sources[1].TimeoutMs)
	}
}

func TestSourceWithoutNameOrCommandIsReportedNotFatal(t *testing.T) {
	dir := writeConfig(t, `
[[sources]]
command = ["nameless"]

[[sources]]
name = "no-command"

[[sources]]
name = "good"
command = ["go"]
`)
	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	// Only the valid one survives; the other two are reported.
	if len(got.Sources) != 1 || got.Sources[0].Name != "good" {
		t.Fatalf("want only the good source, got %+v", got.Sources)
	}
	if len(got.Problems) != 2 {
		t.Fatalf("want 2 problems, got %d: %v", len(got.Problems), got.Problems)
	}
	joined := strings.Join(got.Problems, "\n")
	if !strings.Contains(joined, "no name") || !strings.Contains(joined, "no command") {
		t.Errorf("problems should name both faults: %v", got.Problems)
	}
}

func TestDuplicateSourceNameIsReported(t *testing.T) {
	dir := writeConfig(t, `
[[sources]]
name = "dup"
command = ["first"]

[[sources]]
name = "dup"
command = ["second"]
`)
	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(got.Sources) != 1 || strings.Join(got.Sources[0].Command, " ") != "first" {
		t.Fatalf("want the first of a duplicated name, got %+v", got.Sources)
	}
	if len(got.Problems) != 1 || !strings.Contains(got.Problems[0], "more than once") {
		t.Errorf("want a duplicate-name problem, got %v", got.Problems)
	}
}

func TestSourceTimeoutBoundsAreReported(t *testing.T) {
	dir := writeConfig(t, `
[[sources]]
name = "zero"
command = ["a"]
timeout_ms = 0

[[sources]]
name = "huge"
command = ["b"]
timeout_ms = 999999
`)
	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("both sources should survive a bad timeout, got %+v", got.Sources)
	}
	if got.Sources[0].TimeoutMs != defaultSourceTimeoutMs {
		t.Errorf("zero timeout should fall back to default, got %d", got.Sources[0].TimeoutMs)
	}
	if got.Sources[1].TimeoutMs != maxSourceTimeoutMs {
		t.Errorf("oversized timeout should clamp to max, got %d", got.Sources[1].TimeoutMs)
	}
	if len(got.Problems) != 2 {
		t.Errorf("want both timeouts reported, got %v", got.Problems)
	}
}
