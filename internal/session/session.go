// Package session is the decision layer behind the project picker: what you
// could switch to, and what happens when you pick one.
//
// It answers with a single list mixing two things -- Spaces that are already
// open, and checkouts on disk that have no Space yet -- because from the
// keyboard they are the same intent. "Get me to this repo" should not require
// knowing in advance whether it is already running.
//
// The rule that makes the list safe to act on: a checkout that already has a
// Space never appears twice. It is offered as the Space, and picking it
// focuses that Space rather than building a second one over the same
// directory.
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/discovery"
	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/open"
	"github.com/phin-tech/herdr-phin-util/internal/setup"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// Kind distinguishes the two things a row can be.
type Kind string

const (
	// KindSpace is a Space that is already open. Picking it focuses it.
	KindSpace Kind = "space"
	// KindProject is a checkout with no Space. Picking it makes one.
	KindProject Kind = "project"
	// KindSetup is a workspace recipe offered for a row that is about to be
	// opened. It is not a thing to open in its own right -- picking one opens
	// the row it was offered for, built that way.
	KindSetup Kind = "setup"
)

// Candidate is one row in the picker.
type Candidate struct {
	Kind Kind
	// Label is the primary text: a Space's own label, or a checkout's
	// directory name.
	Label string
	// Path is the checkout directory. Always set for a project; set for a
	// Space only when its panes report one.
	Path string
	// WorkspaceID is set for KindSpace only.
	WorkspaceID string
	// Branch is set at the worktree level: the branch the row would check out,
	// or already has.
	Branch string
	// Base is set on a KindLinearBase row: the ref Branch would be cut from.
	// Every other creating kind derives its base from its own Kind, which is
	// why this is the only row that has to carry one.
	Base string
	// Target is set on a KindLink row: the parsed reference the row came from,
	// carried through so acting on it needs no second parse.
	Target target.Target
	// Setup is set on a KindSetup row: the recipe that row would apply. Nil on
	// the "default" row, which is the single-agent behaviour the picker has
	// always had.
	Setup *setup.Setup
	// Focused marks the Space the user is looking at right now.
	Focused bool
	// Detail is the dimmed secondary text -- the path, or a shape summary for
	// a Space that has no directory to show.
	Detail string
}

// Lister is the slice of the Herdr API the listing needs.
type Lister interface {
	Workspaces() ([]herdr.Workspace, error)
	Panes() ([]herdr.Pane, error)
}

// Focuser brings an existing Space to the front.
type Focuser interface {
	FocusWorkspace(workspaceID string) error
}

// Deps bundles what the picker needs: Herdr for the Space side, git for the
// branch side, and the open package's dependencies for the create side.
type Deps struct {
	Herdr Lister
	Open  open.Deps
	// Setups reads the workspace recipes that apply to a checkout. Nil means
	// the setup level offers only "default", which is exactly what a machine
	// with no setups written should see.
	Setups SetupLoader
	// Worktrees and Git are only used once the picker descends into a
	// repository, so the project level works with both left nil.
	Worktrees WorktreeLister
	Git       Brancher
}

// List builds the picker's rows: every open Space first, then every
// discovered checkout that does not already have one.
//
// Spaces come first because switching to something running is the more common
// intent, and because it is the answer that costs nothing -- offering to
// create what already exists is the one outcome worth designing against.
func List(l Lister, cfg *config.Settings) ([]Candidate, error) {
	workspaces, err := l.Workspaces()
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}

	// workspace.list carries no cwd, so a Space's directory has to come from
	// the panes inside it.
	panes, err := l.Panes()
	if err != nil {
		return nil, fmt.Errorf("list panes: %w", err)
	}
	cwds := workspaceCwds(panes)

	occupied := map[string]bool{}
	spaces := make([]Candidate, 0, len(workspaces))
	for _, ws := range workspaces {
		path := cwds[ws.WorkspaceID]
		if path != "" {
			occupied[path] = true
		}
		spaces = append(spaces, Candidate{
			Kind:        KindSpace,
			Label:       spaceLabel(ws, path),
			Path:        path,
			WorkspaceID: ws.WorkspaceID,
			Focused:     ws.Focused,
			Detail:      spaceDetail(ws, path),
		})
	}

	// The focused Space is the one you are already in, so it is the least
	// useful thing to switch to; it sorts last among Spaces rather than being
	// hidden, since it is still a legitimate target from a popup.
	sort.SliceStable(spaces, func(i, j int) bool {
		if spaces[i].Focused != spaces[j].Focused {
			return !spaces[i].Focused
		}
		return false
	})

	out := spaces
	for _, path := range discovery.List(cfg.Projects.Roots, discovery.Options{
		GitOnly: cfg.Projects.GitOnly,
		Depth:   cfg.Projects.Depth,
	}) {
		if occupied[path] {
			continue
		}
		out = append(out, Candidate{
			Kind:   KindProject,
			Label:  filepath.Base(path),
			Path:   path,
			Detail: shortenHome(path),
		})
	}
	return out, nil
}

// workspaceCwds maps each Space to a directory taken from its panes. The
// first pane reporting one wins: a Space's panes can technically sit in
// different directories, but the root pane's is what the Space is "about".
func workspaceCwds(panes []herdr.Pane) map[string]string {
	out := map[string]string{}
	for _, p := range panes {
		if p.Cwd == "" || p.WorkspaceID == "" {
			continue
		}
		if _, ok := out[p.WorkspaceID]; !ok {
			out[p.WorkspaceID] = filepath.Clean(p.Cwd)
		}
	}
	return out
}

func spaceLabel(ws herdr.Workspace, path string) string {
	if ws.Label != "" {
		return ws.Label
	}
	if path != "" {
		return filepath.Base(path)
	}
	return ws.WorkspaceID
}

func spaceDetail(ws herdr.Workspace, path string) string {
	if path != "" {
		return shortenHome(path)
	}
	return fmt.Sprintf("%d tabs · %d panes", ws.TabCount, ws.PaneCount)
}

// homeDir is a variable so a test can pin it instead of depending on whatever
// home the machine running the suite happens to have.
var homeDir = os.UserHomeDir

// shortenHome is display-only: the full path is what gets acted on, but
// ~/src/... reads far better than /Users/someone/src/... in a narrow popup.
func shortenHome(path string) string {
	home, err := homeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

// Open acts on a picked row: focus what exists, create what does not.
func Open(deps Deps, focuser Focuser, cfg *config.Settings, c Candidate, opts open.Options) (open.Outcome, error) {
	switch c.Kind {
	case KindSpace:
		if err := focuser.FocusWorkspace(c.WorkspaceID); err != nil {
			return open.Outcome{}, fmt.Errorf("focus %s: %w", c.Label, err)
		}
		// No agent step: the Space is already whatever it already is, and
		// starting a second agent in it is not what "switch to this" means.
		return open.Outcome{
			Label:       c.Label,
			RepoPath:    c.Path,
			WorkspaceID: c.WorkspaceID,
		}, nil

	case KindProject:
		return open.RunProject(deps.Open, cfg, c.Path, opts)

	case KindLink:
		return OpenLink(deps, cfg, c, opts)

	case KindClone:
		return OpenClone(deps, cfg, c, opts)

	default:
		return open.Outcome{}, fmt.Errorf("unknown candidate kind %q", c.Kind)
	}
}
