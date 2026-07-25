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

## New Space from a pasted link

> The project picker below now takes links in its own box, and adds
> "switch to it if a Space already exists". This popup is still here because
> it puts the prompt front and centre rather than behind `ctrl+e`.

Paste a link, get the Space it describes. What that means depends on the link:

| Pasted | What happens |
| --- | --- |
| GitHub PR URL | Finds the checkout locally, asks `gh` for the PR's branch, fetches it, and opens a worktree Space on it |
| GitHub issue URL | Finds the checkout, asks `gh` for the title, and opens a worktree Space on a branch derived from it |
| Linear issue URL | Derives a branch from the issue key and slug, and opens a worktree Space for it |
| Anything else | A plain Space named after the text |

Optionally it then starts an agent and **types a prompt without sending it** —
the text sits in the input for you to read, edit, and submit yourself. That is
`pane.send_text` rather than `agent.prompt`, which would press Enter for you.

Linear issues are resolved from the URL alone, with no API token: the URL
already carries the issue key and a slug. An `api_key` may be set for later
enrichment, but nothing needs it today.

A GitHub PR carries its own repository, so it is looked up from `[repos]`
regardless of where you run the command. A Linear issue does not, so its
worktree is built wherever you already are — run it from inside the repo the
issue belongs to, same as you would for `git checkout`.

```sh
bin/herdr-phin-util open https://github.com/o/r/pull/42
bin/herdr-phin-util open https://github.com/o/r/issues/99
bin/herdr-phin-util open https://linear.app/team/issue/ENG-123/fix-the-thing
bin/herdr-phin-util open "scratch space"
bin/herdr-phin-util open <link> --no-agent          # skip the agent step
bin/herdr-phin-util open <link> --prompt "custom"   # bypass the template
bin/herdr-phin-util popup                           # the same thing, with a UI
```

In the popup: `tab` moves between fields, `space` flips the agent toggle,
`ctrl+s` submits, `esc` backs out. The prompt box is editable, and once you
touch it your text wins over the template. Everything is mouse-friendly too —
click a field to jump to it, click the toggle to flip it, and use the
Create/Cancel buttons instead of the keys if you'd rather.

### Settings

Lives at `$HERDR_PLUGIN_CONFIG_DIR/config.toml` — Herdr injects that variable
for a plugin action; run by hand it falls back to
`~/.config/herdr/plugins/config/phin-util/config.toml`. Every key is optional.
Because it is per-machine, a work machine and a personal one each get their
own — which is the point, since they rarely agree on where repositories live.

```toml
[repos]
# Tried in order; the first that exists wins. {host} {owner} {repo}
templates = ["~/src/{host}/{owner}/{repo}"]

[agent]
enabled = true      # the popup toggle's starting state
kind = "claude"     # any agent kind Herdr knows

[agent.prompts]     # Go text/template
# Fields: URL Host Owner Repo Number Title Issue Slug Branch Text
github_pr = "Review PR #{{.Number}} — {{.Title}}\n{{.URL}}"
github_issue = "Work issue #{{.Number}} — {{.Title}}\n{{.URL}}"
linear    = "Work {{.Issue}} — {{.Title}}\n{{.URL}}"
plain     = "{{.Text}}"
# Empty by default: opening a checkout is not a task, so the agent starts on a
# clean input instead of a line you have to delete. Fields: Repo Path
# project = "You are in {{.Repo}} ({{.Path}})"

[projects]
# Where the project picker looks. Unset means "derive it from [repos]", which
# is almost always what you want -- see "Open a project" below.
# roots = ["~/src/*/*", "~/work"]
# git_only = true   # only directories carrying .git
# depth = 1         # how far below each root to look for one

[worktrees]
# Optional. Unset means Herdr decides, which is usually right. Set it to make
# worktrees land where another tool expects them.
# {host} {owner} {repo} {repo_root} {branch}
# path = "{repo_root}/.worktrees/{branch}"

[linear]
# Accepted but unused today -- a Linear issue resolves from its URL alone.
# Reserved for a later step that enriches the title/description over the API.
# api_key = "lin_api_..."
```

A typo does not stop the plugin: a bad value falls back to its default and the
complaint is printed, rather than the setting being silently ignored. A
misspelled `{{.Placeholder}}` renders empty instead of failing the action.

### Bind it

```toml
[[keys.command]]
key = "prefix+shift+o"
type = "plugin_action"
command = "phin-util.open-workspace-maker"
description = "New Space from a link"
```

## Open a project

Point it at a folder full of checkouts and it becomes a switcher: type a few
characters, press enter, land in the repo. Lifted from
[herdr-sessionizer](https://github.com/andrewchng/herdr-sessionizer), which
does the same job in TypeScript over `fzf`.

The list is one list, not two. Spaces that are already open come first, then
every checkout on disk that has no Space yet:

```
▸ open  shift-clock                ~/src/github.com/phin-tech/shift-clock
  open  herdr-phin-util (current)  ~/src/github.com/phin-tech/herdr-phin-util
  new   hearth-mud                 ~/src/github.com/arcane-grimoire/hearth-mud
  new   roux-next-gen              ~/src/github.com/phin-tech/roux-next-gen
```

`open` rows focus. `new` rows create a Space at that directory and optionally
start an agent in it. The point of merging the two is that from the keyboard
they are the same intent — "get me to this repo" should not require knowing in
advance whether it is already running.

Which is also the one rule worth stating outright: **a checkout that already
has a Space never appears twice.** It is offered as the Space, so picking it
switches rather than building a second Space over the same directory. That is
the failure this design exists to prevent, and it is why the picker asks Herdr
what is open before it asks the disk what exists.

Filtering is a subsequence match over the name and the path, so `hpu` finds
`herdr-phin-util` and `acme` finds everything under that owner.

The agent toggle is `ctrl+a`, not `space` — the box owns every printable key.

### Paste a link into it

The same box takes a reference. What you type selects the result set, rather
than a mode being chosen before you know what you are about to type:

```
> https://github.com/phin-tech/roux/pull/42
already open
▸ open    roux#42     already open — switch to it
```
```
> https://github.com/phin-tech/roux/pull/7
one result
▸ link    roux#7      pull request in phin-tech/roux — worktree on its branch
```

A reference is not a filter that matched nothing — it is a query with exactly
one answer, so it replaces the list. And it obeys the same rule as everything
else here: **if a Space already exists for it, you are offered the Space.**

That answer is free. A link's Space label is derived from the URL alone —
`roux#42`, `ENG-123` — so matching it against the open Spaces needs no `gh`,
no branch resolution, no network. It is a heuristic: rename a Space and it
misses, and you fall back to what happened before, which is that `worktree.open`
quietly focuses the existing one anyway.

GitHub issues are recognised alongside pull requests. An issue names no
existing branch, so it behaves like a Linear issue — a branch is derived
(`99-fix-the-flaky-test`, or `issue-99` if `gh` cannot supply the title).

### Clone something you do not have yet

A repository reference — a clone URL, an SSH remote, or `owner/repo` typed by
hand — resolves the same three ways as everything else:

```
> charmbracelet/lipgloss
▸ clone   lipgloss    clone to ~/src/github.com/charmbracelet/lipgloss

> phin-tech/roux
▸ new     roux        already cloned — ~/src/github.com/phin-tech/roux

> phin-tech/roux                      (when a Space is open on it)
▸ open    roux        already open — switch to it
```

The destination is the **first** `[repos].templates` entry, which is the same
list `ResolveRepo` searches. That symmetry is the point: a repo cloned here is
one the paste-a-link flow can find afterwards. Cloning somewhere the templates
do not cover would leave a checkout the rest of the plugin cannot see.

It clones with `gh`, not `git`, so a private repository needs no extra
configuration — `gh` already holds your credentials, the same reason the PR and
issue lookups go through it.

`tab` works on a `clone` row too, and means "fetch it, then show me its
branches" — so going from a repo you have never had to a branch on it is one
pass:

```
> charmbracelet/lipgloss
▸ clone   lipgloss      clone to ~/src/github.com/charmbracelet/lipgloss

  tab  →  cloning lipgloss...

Open a project › lipgloss
> my-new-thing
▸ create  my-new-thing  new branch from main
```

This is the one place `tab` is not instant — everywhere else it is a local
read. The hint says `clone & branch` rather than `worktrees` on that row, so
the difference is visible before you press it.

The `owner/repo` shorthand is only recognised at the project level. One level
down that shape is overwhelmingly a branch name (`codex/iterm-split`), and
nothing but context tells the two apart.

`ctrl+e` opens the prompt box, pre-filled from the template, for any row that
would start an agent. Edit it and your text wins outright. It is hidden by
default because most picks switch to a Space and never start an agent, so a
textarea on screen would be dead weight.

### Where it looks

By default, nowhere new — it derives the roots from the `[repos]` templates
you already have. `~/src/{host}/{owner}/{repo}` says checkouts live two levels
under `~/src`, which is exactly what `~/src/*/*` tells the scanner. Someone who
has said where repos live should not have to say it again in a different shape.

Set `[projects].roots` to override that. Entries are plain paths or globs, and
`**` recurses for checkouts that nest deeper than host/owner/repo. Globs expand
when the picker opens rather than when the config loads, so a repo cloned five
minutes ago is already there.

A directory carrying `.git` is a project and is not descended into — the
submodules and worktrees inside a repository are not separate projects, and
listing them would bury the real ones.

```sh
bin/herdr-phin-util pick                 # the picker
bin/herdr-phin-util projects             # what discovery found, and nothing else
bin/herdr-phin-util project ~/src/x/y    # open one directly
bin/herdr-phin-util project ~/src/x/y --no-agent
```

`projects` is the one to reach for when a repo is missing from the list: it
needs no Herdr session, so it answers "is this a discovery problem or a picker
problem" on its own.

### Worktrees

`→` on a repo descends into its worktrees and branches. `esc` comes back, with
the filter you had typed still in the box.

```
Open a project › roux-next-gen

▸ tree    main                    ~/src/github.com/phin-tech/roux-next-gen
  tree    agent-a8fb383           …/.claude/worktrees/agent-a8fb383e4522b1505
  local   feature/parser          no worktree yet
  remote  origin/fix-the-thing    origin — fetched on open
```

The same rule as the level above, applied twice: a branch that already has a
worktree is not offered as a branch, and a remote branch that also exists
locally is not offered as remote. A worktree that already has a Space becomes
an `open` row, so picking it switches.

Type a name that matches no branch and the list offers to create it:

```
> my-new-thing
  create  my-new-thing            new branch from main
```

That is the whole "new branch" flow — there is no mode to enter, because you
were already typing the name. New branches are based on the repository's
default branch (`origin/HEAD`, falling back to a local `main`/`master`), not on
whatever the source checkout happens to have checked out.

Remote branches are read from the refs already on disk, so the level opens
instantly. The cost is that a branch pushed since your last fetch will not be
there; `ctrl+r` fetches and rebuilds the list when you know it is stale.

Where worktrees land is `[worktrees].path`, shared with the pasted-link flow.
A worktree picked here has no URL behind it, so `{host}` and `{owner}` render
empty in that template — `{repo_root}`, `{repo}` and `{branch}` all work.

```sh
bin/herdr-phin-util pick-worktree    # start at the worktree level for this repo
bin/herdr-phin-util worktrees        # what that level would offer, and nothing else
```

### Bind it

```toml
[[keys.command]]
key = "prefix+shift+f"
type = "plugin_action"
command = "phin-util.pick-project"
description = "Open a project"

# Skips the project list and starts in the repo you are already in.
[[keys.command]]
key = "prefix+shift+b"
type = "plugin_action"
command = "phin-util.pick-worktree"
description = "Open a worktree"
```

## Layout

One binary with a subcommand per feature, so a new utility does not mean a new
repo, plugin id, or keybinding scheme.

```
cmd/herdr-phin-util/   argument dispatch and user-facing reporting
internal/herdr/        socket client -- newline-delimited JSON over $HERDR_SOCKET_PATH
internal/plugin/       parses the context Herdr injects into an action
internal/promote/      the promote decision, behind an interface so it tests without a server
internal/target/       works out what a pasted string refers to; pure parsing
internal/config/       the settings file, and resolving a repo from its templates
internal/gh/           PR lookup, shelling out to the gh CLI
internal/gitcmd/       branches, the default branch, and fetching; shelling out to git
internal/open/         the new-Space decision, behind interfaces for the same reason
internal/discovery/    finding checkouts under the configured roots; filesystem only
internal/session/      the picker's decision: focus what exists, create what does not
internal/ui/           the popups, built on internal/open and internal/session
```

The CLI and the popup are two front ends onto one decision layer, so there is
no second copy of the rules to drift. Each feature is one such layer:
`internal/open` for a pasted link, `internal/session` for the picker — and
`internal/session` builds on `internal/open` rather than beside it, so a Space
made from a picked project goes through the same agent step as one made from a
PR.

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
- `agent.wait`'s `idle` status is Herdr's own activity guess, and it can say
  idle during a brief lull before the agent has actually drawn its prompt.
  For `claude` specifically, the prompt is also confirmed against the pane's
  real on-screen content via `pane.wait_for_output` before anything is typed
  into it — `internal/open`'s `readyMarkers`. Other agent kinds fall back to
  `agent.wait` alone; a wrong guess at their ready-text would hang the action
  rather than merely mistime it.
- `agent.start` can reject a pane with `agent_pane_busy` in the first instant
  after `worktree.create`/`workspace.create` returns it -- the shell exists but
  Herdr has not yet registered it as an available target. That is retried a
  few times with a short linear backoff (`internal/open`'s
  `startAgentWithRetry`); any other error is not, since retrying a genuine
  rejection would just slow the failure down.
- `workspace.list` reports no **cwd**. A Space's directory has to be recovered
  from the panes inside it, which is why the picker calls `pane.list` as well
  and takes the first pane reporting one. Without that join there is no way to
  tell that an open Space and a discovered checkout are the same thing.
