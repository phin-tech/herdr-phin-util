package open

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// RunProject opens a Space on a checkout that already exists on disk.
//
// No worktree and no branch: the directory is the thing, so this is the
// shortest of the four paths through this package. It still shares the agent
// step with the rest, which is the only reason the toggle and the template
// behave the same here as everywhere else.
func RunProject(deps Deps, cfg *config.Settings, path string, opts Options) (Outcome, error) {
	if path == "" {
		return Outcome{}, fmt.Errorf("no project directory given")
	}

	// A relative path would be resolved against this process's working
	// directory, which for a plugin action is not the one the user was
	// looking at. Resolving here means the Space lands where the picker said.
	abs, err := filepath.Abs(path)
	if err != nil {
		return Outcome{}, fmt.Errorf("resolve %s: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Outcome{}, fmt.Errorf("open project %s: %w", abs, err)
	}
	if !info.IsDir() {
		return Outcome{}, fmt.Errorf("open project %s: not a directory", abs)
	}

	name := filepath.Base(abs)
	tgt := target.Target{Kind: target.KindProject, Text: name}

	done := deps.Progress.step("space", "Creating Space "+tgt.Label())
	pane, workspaceID, err := deps.Session.CreateWorkspace(abs, tgt.Label(), true)
	done(err)
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{
		Kind:        tgt.Kind,
		Label:       tgt.Label(),
		RepoPath:    abs,
		WorkspaceID: workspaceID,
		PaneID:      pane.PaneID,
	}

	// Repo and Path are the fields a project prompt actually has to work
	// with; the URL-shaped ones a PR template uses are empty here.
	data := promptData(tgt, "", "")
	data["Repo"] = name
	data["Path"] = abs

	return runAgentStep(deps, cfg, tgt, opts, pane, data, out)
}
