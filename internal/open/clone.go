package open

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// Cloner fetches a repository that is not on this machine yet. gh.Client
// implements this against the gh CLI, which already holds the credentials a
// private repository needs.
type Cloner interface {
	Clone(owner, repo, dest string) error
}

// RunClone gets a repository onto this machine and opens a Space on it.
//
// The destination comes from the first [repos] template, which is the same
// list ResolveRepo searches -- so a repository cloned here is one the paste-a-
// link flow can find afterwards without anything else being configured. That
// symmetry is the point: cloning somewhere ResolveRepo would not look would
// leave a checkout the rest of the plugin cannot see.
func RunClone(deps Deps, cfg *config.Settings, tgt target.Target, opts Options) (Outcome, error) {
	dest, err := EnsureCloned(deps, cfg, tgt)
	if err != nil {
		return Outcome{}, err
	}
	return RunProject(deps, cfg, dest, opts)
}

// EnsureCloned makes sure a repository is on this machine and returns where it
// is, cloning it only if it is not there already.
//
// It is separate from RunClone so the picker can reuse it: descending into a
// repository you do not have yet means fetching it first, and that is the same
// step, not a second implementation of it.
func EnsureCloned(deps Deps, cfg *config.Settings, tgt target.Target) (string, error) {
	if tgt.Owner == "" || tgt.Repo == "" {
		return "", fmt.Errorf("nothing to clone from %q", tgt.Text)
	}

	dest, err := cfg.ClonePath(tgt)
	if err != nil {
		return "", err
	}

	// An existing directory is not an error worth failing on -- it means the
	// repository arrived by some other route, and using it is what was wanted
	// anyway. Cloning over it would fail with a worse message.
	if info, err := os.Stat(dest); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("clone destination %s exists and is not a directory", dest)
		}
		return dest, nil
	}

	if deps.Clone == nil {
		return "", fmt.Errorf("no cloner configured")
	}

	// gh clones into the destination, but the parent has to exist first: a
	// brand new machine has no ~/src/github.com/<owner> yet.
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("prepare %s: %w", filepath.Dir(dest), err)
	}

	// The slowest step in the whole action, and the one most worth naming: a
	// cold clone of a large repository is the case where a popup that says
	// nothing looks most like a popup that has hung.
	done := deps.Progress.step("clone", fmt.Sprintf("Cloning %s/%s", tgt.Owner, tgt.Repo))
	err = deps.Clone.Clone(tgt.Owner, tgt.Repo, dest)
	done(err)
	if err != nil {
		return "", err
	}
	return dest, nil
}
