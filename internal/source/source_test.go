package source

import (
	"strings"
	"testing"
	"time"
)

// sh runs a snippet through /bin/sh, which is how these tests stand in for a
// real source command without shipping a fixture binary. Every assertion is
// about how Run treats what a command prints and how it exits, so a shell
// one-liner that prints and exits is exactly the surface under test.
func sh(script string) Spec {
	return Spec{Name: "test", Command: []string{"/bin/sh", "-c", script}, Timeout: 2 * time.Second}
}

func TestRunParsesRows(t *testing.T) {
	res := Run(sh(`
printf '%s\n' '{"id":"a1","label":"jim-dev2","detail":"RUNNING","command":["attach","--name","jim-dev2"]}'
printf '%s\n' '{"id":"a2","label":"neil","detail":"TERMINATED","command":["attach","--name","neil"]}'
`))

	if len(res.Problems) != 0 {
		t.Fatalf("clean source reported problems: %v", res.Problems)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(res.Rows), res.Rows)
	}
	got := res.Rows[0]
	if got.ID != "a1" || got.Label != "jim-dev2" || got.Detail != "RUNNING" {
		t.Errorf("row 0 mismatch: %+v", got)
	}
	if strings.Join(got.Command, " ") != "attach --name jim-dev2" {
		t.Errorf("row 0 command mismatch: %v", got.Command)
	}
}

func TestRunKeepsGoodRowsAndReportsBadLines(t *testing.T) {
	res := Run(sh(`
printf '%s\n' '{"id":"a1","label":"ok","command":["go"]}'
printf '%s\n' 'not json at all'
printf '%s\n' '{"id":"a2","label":"","command":["go"]}'
printf '%s\n' '{"id":"a3","label":"nocmd"}'
printf '%s\n' ''
printf '%s\n' '{"id":"a4","label":"ok2","command":["go2"]}'
`))

	if len(res.Rows) != 2 {
		t.Fatalf("want 2 valid rows, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0].Label != "ok" || res.Rows[1].Label != "ok2" {
		t.Errorf("kept the wrong rows: %+v", res.Rows)
	}
	// A malformed line, a labelless row and a commandless row: three reported
	// problems, and the blank line silently skipped.
	if len(res.Problems) != 3 {
		t.Fatalf("want 3 problems, got %d: %v", len(res.Problems), res.Problems)
	}
	joined := strings.Join(res.Problems, "\n")
	for _, want := range []string{"not valid JSON", "has no label", "has no command"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems missing %q: %v", want, res.Problems)
		}
	}
}

func TestRunReportsTimeout(t *testing.T) {
	spec := Spec{Name: "slow", Command: []string{"/bin/sh", "-c", "sleep 5"}, Timeout: 100 * time.Millisecond}
	res := Run(spec)

	if len(res.Rows) != 0 {
		t.Fatalf("a source that never printed produced rows: %+v", res.Rows)
	}
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "timed out") {
		t.Fatalf("want a timeout problem, got %v", res.Problems)
	}
	if !strings.Contains(res.Problems[0], "slow") {
		t.Errorf("timeout problem does not name the source: %v", res.Problems)
	}
}

func TestRunReportsFailureWithStderr(t *testing.T) {
	res := Run(sh(`echo "boom: no credentials" >&2; exit 3`))

	if len(res.Problems) != 1 {
		t.Fatalf("want 1 problem, got %v", res.Problems)
	}
	if !strings.Contains(res.Problems[0], "failed") || !strings.Contains(res.Problems[0], "boom: no credentials") {
		t.Errorf("failure problem should carry the command's own stderr: %v", res.Problems[0])
	}
}

func TestRunKeepsRowsPrintedBeforeAFailure(t *testing.T) {
	// A source that paginated one page and then fell over should still offer
	// that page: the rows are good, and the failure is reported alongside them.
	res := Run(sh(`
printf '%s\n' '{"id":"a1","label":"got-this-far","command":["go"]}'
echo "then died" >&2
exit 1
`))

	if len(res.Rows) != 1 || res.Rows[0].Label != "got-this-far" {
		t.Fatalf("lost the row printed before the failure: %+v", res.Rows)
	}
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "then died") {
		t.Errorf("want the failure reported too, got %v", res.Problems)
	}
}

func TestRunNoCommandIsAProblemNotAPanic(t *testing.T) {
	res := Run(Spec{Name: "empty"})
	if len(res.Rows) != 0 || len(res.Problems) != 1 {
		t.Fatalf("want one problem and no rows, got rows=%+v problems=%v", res.Rows, res.Problems)
	}
}
