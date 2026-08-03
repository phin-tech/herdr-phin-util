package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/setup"
)

// loadSetups reads every setup that could apply to a checkout, reporting any
// problems on stderr. repoPath may be empty, in which case only the generic
// directory is consulted -- the other two sources are both about a specific
// repository.
func loadSetups(cfg *config.Settings, repoPath string) []setup.Setup {
	dir, err := config.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "setups:", err)
		return nil
	}

	setups, problems := setup.Load(setup.SourcesFor(
		dir, repoPath,
		cfg.Setups.Dir, cfg.Setups.ReposDir, cfg.Setups.RepoFile,
	))
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "setup:", p)
	}
	return setups
}

// resolveSetup finds a setup by name for a checkout. A name that matches
// nothing is an error rather than a silent fallback to the ordinary path: it
// is a typo, and quietly opening a plain Space would hide it until you
// noticed the layout was missing.
func resolveSetup(cfg *config.Settings, repoPath, name string) (*setup.Setup, error) {
	if name == "" {
		return nil, nil
	}
	setups := loadSetups(cfg, repoPath)
	found, ok := setup.Find(setups, name)
	if !ok {
		return nil, fmt.Errorf("no setup named %q%s", name, availableSetups(setups))
	}
	return &found, nil
}

func availableSetups(setups []setup.Setup) string {
	if len(setups) == 0 {
		return " (none are defined -- see herdr-phin-util setups)"
	}
	names := make([]string, 0, len(setups))
	for _, s := range setups {
		names = append(names, s.Name)
	}
	return " (have: " + strings.Join(names, ", ") + ")"
}

const setupsUsage = "usage: herdr-phin-util setups [--repo DIR]"

// runSetups lists what is defined and where it came from.
//
// Like "projects", it needs no Herdr session: a setup that is not being
// offered is either not loading or not matching, and this answers which
// without a Space having to be created to find out.
func runSetups(args []string) int {
	repoPath := invocationCwd()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			fmt.Println(setupsUsage)
			return 0
		case "--repo":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "--repo needs a path")
				return 2
			}
			abs, err := filepath.Abs(args[i])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			repoPath = abs
		default:
			fmt.Fprintf(os.Stderr, "unknown flag %q\n", args[i])
			return 2
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, p := range cfg.Problems {
		fmt.Fprintln(os.Stderr, "config:", p)
	}

	dir, err := config.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	src := setup.SourcesFor(dir, repoPath, cfg.Setups.Dir, cfg.Setups.ReposDir, cfg.Setups.RepoFile)

	setups := loadSetups(cfg, repoPath)
	if len(setups) == 0 {
		fmt.Fprintf(os.Stderr, "no setups found\n  generic  %s\n  per-repo %s\n  shared   %s\n",
			src.Dir, src.ReposDir, filepath.Join(repoPath, setupRepoFile(cfg)))
		return 1
	}

	fmt.Printf("resolved for %s\n\n", repoPath)
	for _, s := range setups {
		fmt.Printf("%-20s %s\n", s.Name, s.Description)
		fmt.Printf("%-20s %s  %s\n", "", s.Origin, s.Source)

		var scope []string
		if len(s.AppliesTo) > 0 {
			scope = append(scope, "kinds "+strings.Join(s.AppliesTo, ", "))
		}
		if len(s.Repos) > 0 {
			scope = append(scope, "repos "+strings.Join(s.Repos, ", "))
		}
		if len(s.Branches) > 0 {
			scope = append(scope, "branches "+strings.Join(s.Branches, ", "))
		}
		if s.ScopedRepo != "" {
			scope = append(scope, "scoped to "+s.ScopedRepo)
		}
		if len(scope) == 0 {
			scope = append(scope, "anything")
		}
		fmt.Printf("%-20s %s\n", "", strings.Join(scope, " · "))

		fmt.Printf("%-20s %s\n\n", "", strings.Join(shape(s), ", "))
	}
	return 0
}

// shape summarises a setup in a line: what it will actually put on screen.
func shape(s setup.Setup) []string {
	var out []string
	for _, tab := range s.Tabs {
		panes := tab.EffectivePanes()
		name := tab.Name
		if name == "" {
			name = "tab"
		}
		out = append(out, fmt.Sprintf("%s (%d pane%s)", name, len(panes), plural(len(panes))))
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func setupRepoFile(cfg *config.Settings) string {
	if cfg.Setups.RepoFile != "" {
		return cfg.Setups.RepoFile
	}
	return setup.RepoFileName
}

// printPlan writes what a setup would build, for --dry-run.
func printPlan(plan setup.Plan) {
	fmt.Printf("setup %s\n", plan.Name)
	for _, line := range plan.Describe() {
		fmt.Println(line)
	}
	fmt.Println("\nnothing was created -- drop --dry-run to build it")
}
