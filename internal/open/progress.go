package open

// Progress reporting exists because the slow parts of a run are invisible.
// Cloning a repository, cutting a worktree and waiting for four agents to draw
// their inputs is most of a minute, and a popup that says "working..." for all
// of it is indistinguishable from one that has hung. What the steps are is
// already known here -- this is just saying so out loud.
//
// It is decoration, deliberately: a nil Progress is the normal case (every CLI
// path), reporting never returns an error, and nothing downstream of a step
// changes based on whether anyone was listening.

// Event is one thing a run is doing, or has just finished doing. The same Key
// arrives twice -- once when the step starts and once when it ends -- so a
// listener can replace a line rather than accumulate two.
type Event struct {
	// Key identifies the step across its start and end. Steps that repeat per
	// pane carry the pane's index in the key, since they are genuinely
	// different steps rather than one step happening twice.
	Key string
	// Label is the human sentence for the step, already carrying whatever
	// detail makes it worth reading: the branch being fetched, the agent being
	// waited on.
	Label string
	// Done marks the end of the step. Err is set only on a step that failed,
	// and only where failing is interesting enough to show -- a setup reports
	// per-pane failures this way, because the pane stays where it is and the
	// run carries on around it.
	Done bool
	Err  error
}

// Progress receives events as a run makes its way through them. It is called
// from whatever goroutine is doing the work, so an implementation that hands
// events to a UI has to do its own handoff.
type Progress func(Event)

// step opens a step and returns the function that closes it, so a caller says
// what it is about to do and what came of it without having to keep the key
// and label alive in between:
//
//	done := deps.Progress.step("fetch", "Fetching "+branch)
//	err := deps.Git.FetchBranch(repo, branch)
//	done(err)
//
// A nil Progress makes both halves no-ops, which is what keeps this out of the
// way of every path that has no UI attached.
func (p Progress) step(key, label string) func(error) {
	if p == nil {
		return func(error) {}
	}
	p(Event{Key: key, Label: label})
	return func(err error) {
		p(Event{Key: key, Label: label, Done: true, Err: err})
	}
}

// mark reports a step that is instantaneous, or one whose work is not a single
// call to wrap -- it opens and closes in one go.
func (p Progress) mark(key, label string) {
	if p == nil {
		return
	}
	p(Event{Key: key, Label: label})
	p(Event{Key: key, Label: label, Done: true})
}
