// Command herdr-phin-util is a grab bag of small Herdr conveniences.
//
// Each feature is a subcommand, so one binary and one plugin manifest cover
// everything rather than a repo per idea.
package main

import (
	"fmt"
	"os"

	"github.com/phin-tech/herdr-phin-util/internal/herdr"
	"github.com/phin-tech/herdr-phin-util/internal/promote"
	"github.com/phin-tech/herdr-phin-util/internal/version"
)

const usage = `herdr-phin-util -- small Herdr utilities

usage:
  herdr-phin-util promote [pane_id]   move a pane into a Space of its own
  herdr-phin-util version

promote targets the focused pane when no id is given.
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch args[0] {
	case "promote":
		var target string
		if len(args) > 1 {
			target = args[1]
		}
		os.Exit(runPromote(target))
	case "version":
		fmt.Println(version.Version)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[0], usage)
		os.Exit(2)
	}
}

// runPromote reports through a notification as well as stderr: fired from a
// keybind there is no terminal watching, so a silent failure would look like
// the key simply did nothing.
func runPromote(target string) int {
	client, err := herdr.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	out, err := promote.Run(client, target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = client.Notify("Promote failed", err.Error())
		return 1
	}
	if !out.Moved {
		fmt.Println("already alone in its Space; nothing to promote")
		_ = client.Notify("Nothing to promote", "This pane already has its Space to itself.")
		return 0
	}

	fmt.Printf("promoted %s to Space %q\n", out.PaneID, out.Label)
	_ = client.Notify("Promoted to its own Space", out.Label)
	return 0
}
