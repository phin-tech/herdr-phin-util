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
	return externalRows(run, sourceSpec(src))
}

// ExternalChildren runs a row's own children command -- the "what is inside
// this row?" descent tab makes -- and maps the rows the same way
// ExternalCandidates maps a top-level source's.
//
// The timeout is the one the row was fetched with, passed back in here, so a
// machine's repos and a repo's branches are all bounded by the same deadline
// as the machine itself was, without any of them needing a config entry.
// The name is the descended row's label, so a child fetch that fails is
// attributed to the thing that was descended into ("sam-post-vpn-fix timed
// out") rather than to a source name the user never sees at this level.
func ExternalChildren(run source.Runner, name string, command []string, timeout time.Duration) ([]Candidate, []string) {
	return externalRows(run, source.Spec{Name: name, Command: command, Timeout: timeout})
}

// externalRows is the shared body: run the spec, map each row to a candidate,
// and inherit the spec's timeout onto any row that itself has children so the
// next level down is bounded the same way this one was.
func externalRows(run source.Runner, spec source.Spec) ([]Candidate, []string) {
	res := run(spec)
	out := make([]Candidate, 0, len(res.Rows))
	for _, r := range res.Rows {
		c := Candidate{
			Kind:    KindExternal,
			ID:      r.ID,
			Label:   r.Label,
			Detail:  r.Detail,
			Command: r.Command,
		}
		if len(r.Children) > 0 {
			c.Children = r.Children
			c.ChildrenLabel = r.ChildrenLabel
			c.ChildrenTimeout = spec.Timeout
		}
		out = append(out, c)
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
