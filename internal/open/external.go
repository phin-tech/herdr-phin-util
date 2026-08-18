package open

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/phin-tech/herdr-phin-util/internal/config"
	"github.com/phin-tech/herdr-phin-util/internal/target"
)

// RunExternal builds a Space for a row an external source produced, and runs
// that source's own command in the Space's root pane.
//
// This is the whole of what a KindExternal row does. There is no agent step,
// no worktree, no prompt: the command *is* what fills the pane -- an attach, a
// tunnel, an ssh -- and the source that printed the row is the only thing that
// knows what "put me there" means. So this creates the Space and hands the
// pane straight to the command, and everything about how the command reaches
// its remote machine stays outside this plugin.
//
// The Space opens wherever the picker was invoked (deps.Cwd). An external row
// carries no path -- that is the point of it -- so there is no checkout to sit
// in, and the local directory the command starts from rarely matters to a
// command whose job is to leave the local machine.
func RunExternal(deps Deps, cfg *config.Settings, label string, argv []string) (Outcome, error) {
	if len(argv) == 0 {
		return Outcome{}, fmt.Errorf("external row %q has no command to run", label)
	}

	done := deps.Progress.step("space", "Creating Space "+label)
	pane, workspaceID, err := deps.Session.CreateWorkspace(deps.Cwd, label, true)
	done(err)
	if err != nil {
		return Outcome{}, fmt.Errorf("create space: %w", err)
	}

	out := Outcome{
		Kind:        target.KindPlain,
		Label:       label,
		RepoPath:    deps.Cwd,
		WorkspaceID: workspaceID,
		PaneID:      pane.PaneID,
	}

	// The Space exists from here on, so a command that fails to run leaves a
	// bare shell behind rather than nothing -- reported the same way every
	// other post-Space failure in this package is, so a half-built Space is
	// never silently called a success.
	if deps.Layout == nil {
		return out, fmt.Errorf("external row %q needs a Herdr session to run its command in", label)
	}
	deps.Progress.mark("command", "Running "+argv[0])
	if err := deps.Layout.RunCommand(pane.PaneID, externalCommandLine(argv)); err != nil {
		return out, fmt.Errorf("run %q: %w", argv[0], err)
	}
	return out, nil
}

// RunExternalCommand runs a source row's command without building a Space for
// it -- the opens:false case. The command is responsible for where the user
// ends up: it focuses an existing Space, opens something itself, or just does
// a thing. This makes no Herdr call at all beyond letting the command reach
// the socket it inherits; nothing here creates, focuses or closes a Space.
//
// It is the answer to a row whose destination already exists -- a mirrored
// Space, say. The command is run as a local subprocess (the same way a source
// or a children command is), so a run that takes tens of seconds for a cold
// cloud machine happens under the progress popup rather than in a throwaway
// pane the row would otherwise have to delete itself out of.
//
// The Outcome names no Space, because none was made: a success is reported as
// "the command ran", and a failure carries the command's own stderr so the
// reason is in the popup rather than in a pane the user has to go read.
func RunExternalCommand(deps Deps, label string, argv []string) (Outcome, error) {
	if len(argv) == 0 {
		return Outcome{}, fmt.Errorf("external row %q has no command to run", label)
	}

	done := deps.Progress.step("run", "Running "+argv[0])
	stderr, err := runLocalCommand(argv)
	done(err)

	out := Outcome{Kind: target.KindPlain, Label: label}
	if err != nil {
		if trimmed := strings.TrimSpace(stderr); trimmed != "" {
			return out, fmt.Errorf("%s exited: %w: %s", argv[0], err, lastLine(trimmed))
		}
		return out, fmt.Errorf("%s exited: %w", argv[0], err)
	}
	return out, nil
}

// runLocalCommand runs argv to completion -- no timeout, since this is a
// deliberate action a person is waiting on, not a background fetch that must
// not hang the picker -- and returns whatever it wrote to stderr for a failure
// to carry. stdout is discarded: a command that navigates has nowhere useful
// to print, and its exit status is the thing that matters.
func runLocalCommand(argv []string) (string, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = envWithoutOriginSpace()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.String(), err
}

// envWithoutOriginSpace is the process environment with Herdr's per-pane
// identity variables stripped out.
//
// This is the rake the issue calls out, removed at the root: HERDR_WORKSPACE_ID
// (and its pane and tab siblings) is set in every pane, so a command that reads
// it to mean "the Space I belong to" would read the popup's launching context
// and act on a Space the user never pointed at -- closing the terminal they
// were sitting in, in the case that motivated this. An opens:false row has no
// origin Space by construction, so the honest environment for its command is
// one where those variables are simply absent rather than present and
// misleading.
func envWithoutOriginSpace() []string {
	const dropPrefix = "HERDR_WORKSPACE_ID=" // plus the two below
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, dropPrefix) ||
			strings.HasPrefix(kv, "HERDR_PANE_ID=") ||
			strings.HasPrefix(kv, "HERDR_TAB_ID=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// lastLine keeps the final non-empty line of a command's stderr, which is
// where a CLI's actual error usually lands -- the lines above it are often a
// stack or a banner that would just push the reason off a narrow popup.
func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return s
}

// externalCommandLine renders a source row's argv as a single shell command
// line for RunCommand to type. A word that needs no quoting is left bare, so
// the common case -- "herdr-anyscale-remote connect --name jim-dev2" -- reads
// in the pane exactly as it would if typed by hand; a word carrying a space or
// a shell metacharacter is single-quoted, the one form sh, bash, zsh and fish
// all agree on (the same shellQuote the setup path uses).
func externalCommandLine(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = quoteArg(a)
	}
	return strings.Join(parts, " ")
}

// shellSafe is the set of characters that carry no meaning to any of the
// shells a pane might be running, so a word made only of them survives being
// typed unquoted. Everything else -- spaces, quotes, globs, redirections,
// $, and so on -- forces the word through shellQuote.
func shellSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("-_./:=@,+", r)
}

func quoteArg(a string) string {
	if a == "" {
		return "''"
	}
	for _, r := range a {
		if !shellSafe(r) {
			return shellQuote(a)
		}
	}
	return a
}
