package open

import (
	"fmt"
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
