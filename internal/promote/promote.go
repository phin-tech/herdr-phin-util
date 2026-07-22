// Package promote moves a pane into a Space of its own.
package promote

import (
	"fmt"
	"path/filepath"

	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/plugin"
)

// Session is the slice of the Herdr API this operation needs. It is an
// interface so the decision-making below can be tested without a live server.
type Session interface {
	Panes() ([]herdr.Pane, error)
	MoveToNewWorkspace(paneID, label string, focus bool) (herdr.Pane, error)
}

// Outcome reports what Run decided to do.
type Outcome struct {
	// Moved is false when the pane was already alone in its Space.
	Moved bool
	// Label is the new Space's name, set only when Moved.
	Label string
	// PaneID is the pane's id after the move, which differs from the one
	// passed in: Herdr renumbers a pane when it changes workspace.
	PaneID string
}

// Run promotes target into a new Space. An empty target means "the pane the
// user is looking at".
func Run(s Session, target string) (Outcome, error) {
	panes, err := s.Panes()
	if err != nil {
		return Outcome{}, err
	}

	// Prefer the id Herdr injected over re-deriving focus: it was resolved at
	// the moment the action fired, before any focus change this process races.
	if target == "" {
		if ctx := plugin.FromEnv(); ctx != nil {
			target = ctx.FocusedPaneID
		}
	}
	if target == "" {
		for _, p := range panes {
			if p.Focused {
				target = p.PaneID
				break
			}
		}
	}
	if target == "" {
		return Outcome{}, fmt.Errorf("no focused pane to promote")
	}

	var pane *herdr.Pane
	siblings := 0
	for i := range panes {
		if panes[i].PaneID == target {
			pane = &panes[i]
		}
	}
	if pane == nil {
		return Outcome{}, fmt.Errorf("no such pane: %s", target)
	}
	for i := range panes {
		if panes[i].WorkspaceID == pane.WorkspaceID {
			siblings++
		}
	}

	// Promoting the only pane in a Space would just trade one empty Space for
	// another, so that is a no-op rather than sidebar churn.
	if siblings <= 1 {
		return Outcome{Moved: false, PaneID: pane.PaneID}, nil
	}

	label := SpaceLabel(pane.Cwd)
	moved, err := s.MoveToNewWorkspace(pane.PaneID, label, true)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Moved: true, Label: label, PaneID: moved.PaneID}, nil
}

// SpaceLabel names a Space after the directory its pane sits in, which is what
// makes it findable once it is one row among many.
func SpaceLabel(cwd string) string {
	switch base := filepath.Base(cwd); base {
	case "", ".", "/":
		return "promoted"
	default:
		return base
	}
}
