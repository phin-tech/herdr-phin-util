package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/phin-tech/herdr-phin-util/internal/open"
)

// fixedClock is the checklist's clock under test, advanced by hand so a
// rendered timing is something to assert on rather than something to hope
// about.
type fixedClock struct{ t time.Time }

func (c *fixedClock) now() time.Time      { return c.t }
func (c *fixedClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestList() (*progressList, *fixedClock) {
	clock := &fixedClock{t: time.Unix(1700000000, 0)}
	return &progressList{started: clock.now(), now: clock.now}, clock
}

func TestProgressListAppendsAStepWhenItStarts(t *testing.T) {
	p, _ := newTestList()
	p.apply(open.Event{Key: "clone", Label: "Cloning phin-tech/util"})

	lines := p.render(0)
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want one", lines)
	}
	if !strings.Contains(lines[0], "[ ]") || !strings.Contains(lines[0], "Cloning phin-tech/util") {
		t.Errorf("line = %q, want an open box and the label", lines[0])
	}
}

// The second event for a key closes the line the first one opened, rather than
// adding a duplicate underneath it.
func TestProgressListChecksTheStepItAlreadyHas(t *testing.T) {
	p, clock := newTestList()
	p.apply(open.Event{Key: "clone", Label: "Cloning phin-tech/util"})
	clock.add(4100 * time.Millisecond)
	p.apply(open.Event{Key: "clone", Label: "Cloning phin-tech/util", Done: true})

	lines := p.render(0)
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want the one line closed rather than a second added", lines)
	}
	if !strings.Contains(lines[0], "[✓]") {
		t.Errorf("line = %q, want a check", lines[0])
	}
	if !strings.Contains(lines[0], "4.1s") {
		t.Errorf("line = %q, want the elapsed time it took", lines[0])
	}
}

// A step still running counts up: that is the whole difference between this
// and a status line that has frozen.
func TestProgressListCountsUpWhileAStepRuns(t *testing.T) {
	p, clock := newTestList()
	p.apply(open.Event{Key: "agent", Label: "Starting codex in reviewers"})

	clock.add(2 * time.Second)
	first := p.render(0)[0]
	clock.add(3 * time.Second)
	second := p.render(0)[0]

	if !strings.Contains(first, "2.0s") {
		t.Errorf("first = %q, want 2.0s", first)
	}
	if !strings.Contains(second, "5.0s") {
		t.Errorf("second = %q, want the clock to have moved to 5.0s", second)
	}
}

func TestProgressListMarksAFailedStep(t *testing.T) {
	p, _ := newTestList()
	p.apply(open.Event{Key: "pane-1", Label: "Starting codex in reviewers"})
	p.apply(open.Event{Key: "pane-1", Label: "Starting codex in reviewers", Done: true, Err: errors.New("never drew its input")})

	out := strings.Join(p.render(0), "\n")
	if !strings.Contains(out, "[x]") {
		t.Errorf("render = %q, want a cross for the failed step", out)
	}
	if !strings.Contains(out, "never drew its input") {
		t.Errorf("render = %q, want the reason underneath", out)
	}
}

// The ticker stops when the run does, so a finished checklist is not still
// redrawing behind a popup that is about to close.
func TestProgressListActiveOnlyWhileSomethingIsRunning(t *testing.T) {
	p, _ := newTestList()
	p.apply(open.Event{Key: "a", Label: "One"})
	if !p.active() {
		t.Error("a started step should keep the list active")
	}
	p.apply(open.Event{Key: "a", Label: "One", Done: true})
	if p.active() {
		t.Error("everything is finished; the list should be idle")
	}
}

// Timings line up in a column, which is what makes a list of them scannable.
func TestProgressListPadsLabelsToACommonWidth(t *testing.T) {
	p, _ := newTestList()
	p.apply(open.Event{Key: "a", Label: "Short"})
	p.apply(open.Event{Key: "b", Label: "A considerably longer label"})

	lines := p.render(0)
	first := strings.Index(lines[0], "0.0s")
	second := strings.Index(lines[1], "0.0s")
	if first != second {
		t.Errorf("timings start at %d and %d, want one column", first, second)
	}
}

func TestShortDuration(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0.0s"},
		{1500 * time.Millisecond, "1.5s"},
		{59 * time.Second, "59.0s"},
		{90 * time.Second, "1m30s"},
		{-time.Second, "0.0s"},
	} {
		if got := shortDuration(tc.d); got != tc.want {
			t.Errorf("shortDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// A run must never block because nobody is reading its progress: a cancelled
// popup stops reading, and the run still has steps to report.
func TestProgressChannelDropsRatherThanBlocking(t *testing.T) {
	c := newProgressChannel()
	report := c.reporter()

	done := make(chan struct{})
	go func() {
		// Comfortably more than the buffer holds.
		for i := 0; i < cap(c)*4; i++ {
			report(open.Event{Key: "k", Label: "spam"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reporting blocked when nothing was listening")
	}
}
