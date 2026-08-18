package session

import (
	"time"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/source"
)

// ExternalCandidates runs one configured source and turns the rows it prints
// into picker candidates, returning them alongside the problems worth
// surfacing rather than swallowing.
//
// This is the seam between "a command that prints rows" (internal/source) and
// "a row in the picker" (Candidate). It is deliberately thin: a source's Row
// already carries exactly the fields a KindExternal candidate needs, so the
// mapping is a rename, not a translation. The problems come straight through
// -- a failing source is reported the same way an unknown setup key is, and
// this function is the only place that decides how a source's failure reaches
// the list.
func ExternalCandidates(run source.Runner, src config.SourceSettings) ([]Candidate, []string) {
	res := run(sourceSpec(src))
	out := make([]Candidate, 0, len(res.Rows))
	for _, r := range res.Rows {
		out = append(out, Candidate{
			Kind:    KindExternal,
			ID:      r.ID,
			Label:   r.Label,
			Detail:  r.Detail,
			Command: r.Command,
		})
	}
	return out, res.Problems
}

// sourceSpec turns the per-machine config for a source into the spec the
// runner executes, converting the milliseconds config speaks into the duration
// the runner wants in one place.
func sourceSpec(src config.SourceSettings) source.Spec {
	return source.Spec{
		Name:    src.Name,
		Command: src.Command,
		Timeout: time.Duration(src.TimeoutMs) * time.Millisecond,
	}
}
