# Phin Util

Small everyday [Herdr](https://herdr.dev) utilities — one plugin to hang my
one-off conveniences off, rather than a repo per idea.

**This is built for me.** It is a personal toolbox that changes whenever my
workflow does. You are welcome to use it, but nothing here is promised:
actions may be renamed or dropped, behavior may change without a version bump,
and the keybindings I suggest are the ones that suit my layout. If you depend
on it, pin a commit or fork it — that will hurt less than the alternative.

Requires Herdr 0.7.4+. Tested on macOS.

## Install

```sh
herdr plugin link /path/to/herdr-phin-util   # local checkout
herdr plugin install phin-tech/herdr-phin-util
```

The build compiles from source when Go is present and otherwise fetches the
binary CI publishes, verifying its checksum first.

## Promote a pane to its own Space

Takes the pane you are looking at and gives it a Space of its own.

The pane is **moved, not restarted**. Herdr's `pane move --new-workspace`
relocates the live terminal, so the process keeps its PID, its scrollback, and
— for an agent — its whole session. A Claude session promoted mid-task comes
out the other side still holding its context. Re-creating the Space and
relaunching the agent would be the obvious implementation and would throw all
of that away.

The new Space is named after the pane's working directory, which is what makes
it findable once it is one row among many.

Two cases are deliberately quiet:

- A pane that is already alone in its Space is a no-op — promoting it would
  only trade one empty Space for another.
- A pane id that no longer exists reports and exits non-zero.

### Bind it

```toml
[[keys.command]]
key = "prefix+shift+s"
type = "plugin_action"
command = "phin-util.promote"
description = "Promote pane to its own Space"
```

`prefix+p` and `prefix+shift+p` are both taken by Herdr's own defaults, so this
uses `prefix+shift+s` — mnemonically "own **S**pace". Pick whatever is free in
your own config.

It is also reachable from the command palette, and from a pane's action menu.

### Run it directly

```sh
bin/herdr-phin-util promote          # promotes the focused pane
bin/herdr-phin-util promote w4:p2    # promotes a named pane
```

The explicit form is what makes the action testable: focus follows the real UI,
so a test cannot hold focus on a background Space to exercise itself.

## Layout

One binary with a subcommand per feature, so a new utility does not mean a new
repo, plugin id, or keybinding scheme.

```
cmd/herdr-phin-util/   argument dispatch and user-facing reporting
internal/herdr/        socket client -- newline-delimited JSON over $HERDR_SOCKET_PATH
internal/plugin/       parses the context Herdr injects into an action
internal/promote/      the promote decision, behind an interface so it tests without a server
```

A few things about the API that are easy to get wrong, and are why the code
looks the way it does:

- `pane.current` resolves from `$HERDR_PANE_ID`, so it reports the **calling**
  pane. For anything fired by a keybind that is the wrong pane. The focused
  pane comes from the injected context, or from the `focused` flag in
  `pane.list`.
- A pane is **renumbered** when it changes workspace, so the id that comes back
  from a move replaces the one that went in.
- Plugin actions run **asynchronously**. `plugin action invoke` returns
  `status: "running"`; the outcome shows up in `herdr plugin log list`.
