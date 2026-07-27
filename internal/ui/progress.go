package ui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/phin-tech/herdr-phin-util/internal/open"
)

// The run checklist. Opening a Space is a handful of slow steps -- a clone, a
// worktree, four agents drawing their inputs -- and a popup that says
// "working..." through all of it is indistinguishable from one that has hung.
// This is that same wait, itemised:
//
//	[x] Cloning phin-tech/herdr-phin-util      4.1s
//	[x] Building 4 panes                       0.3s
//	[ ] Starting codex in reviewers            2.4s
//
// Steps appear as they start rather than being listed up front, because the
// list genuinely is not known in advance: a repository already on disk is not
// cloned, and a setup's panes depend on the file. What is on screen is
// therefore always either running or finished, which is what the empty box and
// the check mean here.

// progressTick is how often a run in flight redraws. It only moves the elapsed
// figure on the step that is running, so it is paced to look live without
// spending the frame budget of a spinner.
const progressTick = 200 * time.Millisecond

type progressTickMsg time.Time

// progressStep is one line of the checklist.
type progressStep struct {
	key     string
	label   string
	started time.Time
	// elapsed is fixed once the step finishes; until then it is computed
	// against the clock so the line counts up.
	elapsed time.Duration
	done    bool
	err     error
}

// progressList holds the checklist for one run, in the order the steps
// happened.
type progressList struct {
	steps []progressStep
	// started is when the run began, for the total on the last line.
	started time.Time
	// now is the clock, injected so a test can assert on rendered timings
	// without sleeping.
	now func() time.Time
}

func newProgressList() *progressList {
	return &progressList{started: time.Now(), now: time.Now}
}

// apply folds one event into the list: a new key appends a line, a known key
// closes the one already there.
func (p *progressList) apply(e open.Event) {
	if p == nil {
		return
	}
	for i := range p.steps {
		if p.steps[i].key != e.Key {
			continue
		}
		if e.Done {
			p.steps[i].done = true
			p.steps[i].err = e.Err
			p.steps[i].elapsed = p.now().Sub(p.steps[i].started)
		}
		return
	}
	// An end for a step that was never started should still show rather than
	// vanish -- a missing line is harder to explain than a fast one.
	p.steps = append(p.steps, progressStep{
		key:     e.Key,
		label:   e.Label,
		started: p.now(),
		done:    e.Done,
		err:     e.Err,
	})
}

// total is how long the run has been going, which is the figure worth having
// next to a list of per-step times: it is the one a person compares against
// their own patience.
func (p *progressList) total() time.Duration {
	if p == nil {
		return 0
	}
	return p.now().Sub(p.started)
}

// active reports whether anything is still running, which is what decides
// whether the ticker keeps going.
func (p *progressList) active() bool {
	if p == nil {
		return false
	}
	for _, s := range p.steps {
		if !s.done {
			return true
		}
	}
	return false
}

var (
	progressDoneStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	progressFailStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	progressTimeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

// render draws the checklist, widest-first so the timings line up in a column.
// width is the space available; 0 means "do not pad", which is what the tests
// read.
func (p *progressList) render(width int) []string {
	if p == nil || len(p.steps) == 0 {
		return nil
	}

	longest := 0
	for _, s := range p.steps {
		if n := lipgloss.Width(s.label); n > longest {
			longest = n
		}
	}

	lines := make([]string, 0, len(p.steps)+1)
	for _, s := range p.steps {
		box := "[ ]"
		style := lipgloss.NewStyle()
		switch {
		case s.err != nil:
			box, style = "[x]", progressFailStyle
		case s.done:
			box, style = "[✓]", progressDoneStyle
		}

		elapsed := s.elapsed
		if !s.done {
			elapsed = p.now().Sub(s.started)
		}

		pad := longest - lipgloss.Width(s.label)
		if width > 0 && pad < 0 {
			pad = 0
		}
		line := fmt.Sprintf("%s %s%s  %s",
			style.Render(box),
			s.label,
			spaces(pad),
			progressTimeStyle.Render(shortDuration(elapsed)),
		)
		lines = append(lines, line)

		// A step that failed says why underneath it rather than in the label,
		// which keeps the column of boxes readable when one goes wrong.
		if s.err != nil {
			lines = append(lines, progressFailStyle.Render("    "+s.err.Error()))
		}
	}
	return lines
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = ' '
	}
	return string(out)
}

// shortDuration keeps the timing to a width that does not move around: under a
// minute it is seconds to one decimal, past that it is m/s. Milliseconds are
// not shown -- a step that fast is not one anybody is waiting on.
func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// progressChannel is the handoff between the run, which is on a worker
// goroutine, and the UI loop that draws it.
//
// The buffer plus the dropping send is the whole safety argument: a run must
// never block because nobody is reading its progress. A popup that has been
// cancelled stops reading, and a step that cannot report is a cosmetic loss
// against a run that would otherwise wedge on a full channel.
type progressChannel chan open.Event

func newProgressChannel() progressChannel { return make(progressChannel, 64) }

func (c progressChannel) reporter() open.Progress {
	return func(e open.Event) {
		select {
		case c <- e:
		default:
		}
	}
}

// waitForProgress blocks the command goroutine on the next event. Re-armed on
// every event received, which is the standard bubbletea way to turn a channel
// into messages.
func waitForProgress(c progressChannel) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-c
		if !ok {
			return nil
		}
		return progressMsg(e)
	}
}

type progressMsg open.Event

// tickProgress schedules the redraw that moves the running step's clock.
func tickProgress() tea.Cmd {
	return tea.Tick(progressTick, func(t time.Time) tea.Msg { return progressTickMsg(t) })
}
