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

## Hand a Claude session in from outside Herdr

The companion to promote, for the sessions promote cannot reach. You started
Claude in a plain terminal window, the conversation turned into real work, and
now you want it in Herdr with everything else.

```sh
herdr-phin-util handoff                        # resume this session in a new Space
herdr-phin-util handoff --dry-run              # say which session, open nothing
herdr-phin-util handoff --label "auth rework"  # name the Space yourself
herdr-phin-util handoff --session <uuid>       # resume some other conversation
herdr-phin-util handoff --cwd ~/src/other      # look for the session elsewhere
```

**It is not a move, and it cannot be.** Herdr can only relocate panes it
already owns, so there is no live process to carry across — this opens a Space
on the same directory and starts a fresh `claude --resume` against the same
session file. The conversation arrives intact; the process does not. **Quit the
original with `/exit`** once the new one is up, because two Claudes appending
to one session file will diverge.

That is the whole difference between the two commands, and it is worth keeping
straight:

| | promote | handoff |
| --- | --- | --- |
| Run from | inside Herdr | outside it |
| What moves | the live pane — same PID, same scrollback | the transcript only |
| Afterwards | nothing to do | quit the original |

Run inside Herdr it refuses and points at promote, since promote is strictly
better whenever it applies. `--force` overrides that if you actually want a
second, resumed copy.

### Which session it picks

Outwards, stopping at the first thing that answers:

1. `--session`, if you passed one.
2. `$CLAUDE_CODE_SESSION_ID`, which Claude exports into every session it runs.
   This is the exact answer, and the usual one — you are normally running the
   command from inside the session being handed off.
3. The newest transcript for the current directory. This covers running from a
   shell *beside* the session rather than inside it, and is the same guess
   `claude --continue` makes.
4. The newest transcript **anywhere**, for when you are somewhere Claude has
   never been. The Space then opens in the *session's* directory rather than
   the one you are standing in — resuming a conversation about one repo in the
   directory of another would be worse than not finding it.

Step 4 is a guess, so it announces itself:

```
$ herdr-phin-util handoff
no session here -- using the most recent one:
  a3460e51  ~/src/github.com/ogulcancelik/herdr  (12m ago)
resumed in Space w1D (pane w1D:p1)
```

`--dry-run` asks the same question without acting on the answer, which is the
cheap way to check a guess before it becomes a Space:

```
$ herdr-phin-util handoff --dry-run
would resume a3460e51 (12m ago)
        into ~/src/github.com/ogulcancelik/herdr, Space "herdr"
        found outside this directory -- the Space follows the session
```

The directory comes from inside the transcript, not from the folder Claude
files it under: that folder's name turns both `/` and `.` into `-`, so
`foo.bar` and `foo/bar` are indistinguishable once written.

There is no keybinding and no action menu entry: a plugin action fires from
inside Herdr, and being outside it is this command's entire premise. It does
need to be on your `PATH`, since an external terminal has no
`$HERDR_PLUGIN_ROOT` to find the binary through.

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

## Setups

Everything above ends the same way: one agent, one prompt, one pane. A setup
replaces that last step with a whole room.

The case it exists for: paste a PR and get an orchestrating agent, a second
opinion from a different model, and `roborev` already running on the branch —
laid out, prompted, and in one keystroke.

```yaml
name: pr-review
description: orchestrator + second opinion + roborev
applies_to: [github_pr]

tabs:
  - name: review
    panes:
      - label: orchestrator
        agent: claude
        focus: true
        prompt: |
          Review PR #{{.Number}} — {{.Title}}
          {{.URL}}
          Two other panes are already working on this. Reconcile them.

      - split: right
        agent: codex
        submit: true
        prompt: Review the diff on {{.Branch}} for correctness bugs.

      - split: down
        ratio: 0.3
        label: roborev
        command: roborev review --branch

  - name: tests
```

A setup is a **recipe applied to a target**, not a workspace of its own. The
worktree, the branch and the label are already decided by the time one runs —
which is why `{{.Number}}` and `{{.Branch}}` mean exactly what they mean in
`[agent.prompts]`, and why the same file works for a PR, an issue, a Linear
ticket or a plain checkout.

It is the only YAML here. Three levels of nesting carrying multi-line prompts
is the one shape TOML renders badly: `[[tabs.panes]]` five times describes a
layout without ever looking like one, and indentation does. `config.toml` is
unchanged.

### Pick one

`ctrl+t` in the project picker, on any row that would actually build
something:

```
Open a project › roux-next-gen › setup

▸ setup   default      one claude, prompt typed not sent
  setup   pr-review    orchestrator + second opinion + roborev — generic
  setup   dev          agent, a test loop, and the plugin log — repo
```

**Enter is unchanged.** Setups are opt-in per launch, so the muscle memory that
already works keeps working, and `esc` comes back with your filter intact. The
list is filtered to what applies to that row, and each row says which of the
three sources it came from — because two setups with the same name resolve by
precedence, which is otherwise invisible at exactly the moment you are choosing
between them.

### Where they live

```
~/.config/herdr/plugins/config/phin-util/
  setups/pr-review.yaml                  generic — offered for any repo
  repos/roux-next-gen/dev.yaml           this machine's, for one repo
  repos/phin-tech/roux/pr-review.yaml    same, disambiguated by owner
<checkout>/.herdr-setups.yaml            committed, travels to every worktree
```

`repos/<repo>/` is the everyday case: layouts that only make sense for one
checkout, without putting a file in the repository. Files there need no `repos:`
key — the path already says it — and are not offered anywhere else.

`.herdr-setups.yaml` is for a layout the team shares. It holds a `setups:` list
rather than one per file, since a repo should not grow a directory for this.
There is one in this repo, as an example.

**When the same name appears twice:** `setups/` < the checkout file < `repos/`.
Generic is weakest and your own machine's repo directory is strongest, so a
team setup can be overridden locally without editing a tracked file — the same
reason `config.toml` is per-machine.

A setup narrows itself with `applies_to` (target kinds), `repos` (globs over
`owner/repo`), and `branches` (globs). That last one is how "worktree-specific"
is said: a worktree is a branch.

### What a pane can be

| | |
| --- | --- |
| `agent:` + `prompt:` | starts an agent and types the rendered template |
| `agent:` + `skill:` | shorthand for a prompt that is one slash command |
| `command:` | runs it in a plain shell |
| neither | a shell, sitting at its prompt |

`submit: true` sends the prompt with Enter. **Omitted means type it and leave
it**, which is what the rest of the plugin does — the point of an orchestrator
pane is that you read its brief before firing it, while the workers it
coordinates are already going.

Also per pane: `split` (`right`/`down`), `ratio`, `label`, `cwd`, `env`,
`focus`, and `wait_for`. `cwd` and `env` inherit down setup → tab → pane, so
one pane in `./web` needs no repeated path.

`wait_for: { match: "queued", timeout_ms: 20000 }` holds the rest of the layout
until that pane's output matches. It is the ordering primitive — "prompt the
orchestrator only once roborev has actually queued" is a statement rather than
a race. A timeout is not fatal: the pane exists and the command ran, and a
wrong guess at the match should not strand a Space you can already see.

Unknown keys are reported rather than ignored. A typo'd `prompt_` silently
doing nothing is the failure this kind of file dies of, and YAML is forgiving
enough to make it likely.

### Check one before you run it

```sh
bin/herdr-phin-util setups                  # what is defined, and where from
bin/herdr-phin-util setups --repo ~/src/x   # as if that checkout were the row
bin/herdr-phin-util open <PR> --setup pr-review --dry-run
bin/herdr-phin-util open <PR> --setup pr-review
bin/herdr-phin-util project ~/src/x --setup dev
```

`--dry-run` prints every tab, split, cwd and rendered prompt — with the PR's
real number, title and branch, since it asks `gh` the same questions the real
run does — and touches nothing. It needs no Herdr session, which is the point:
it works in the place you most want it, which is while writing the file.

`setups` is the counterpart to `projects`: when a setup is not being offered,
it says whether it failed to load or simply did not match.

Panes are all created before anything runs in one, and focus is decided last.
Splitting a tab after a TUI has started in it resizes a running program.

Setups take their shape from
[herdr-plus](https://github.com/cloudmanic/herdr-plus)'s projects — tabs,
panes, splits, one file per definition, and its rule that the first tab reuses
the Space's own — and their model from
[herdr-spreader](https://github.com/yuk1ty/herdr-spreader): ratios, `wait_for`,
inherited `cwd` and `env`, explicit focus, strict key checking, and `--dry-run`.

### Bind it

Nothing to bind: `ctrl+t` lives inside the picker you already opened.

## Skills

`skills/` holds two Claude Code skills, installable straight from this repo:

```sh
npx skills add phin-tech/herdr-phin-util
```

- **herdr-setups** — writing and debugging a setup file. The schema, the three
  sources, and the dry-run loop, so "make me a review layout for this repo"
  produces something that actually loads.
- **herdr-phin-util** — driving the CLI: opening links, picking projects,
  promoting panes, handing a session in.

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
internal/setup/        the YAML recipes: load, match, and resolve into ordered steps
internal/session/      the picker's decision: focus what exists, create what does not
internal/ui/           the popups, built on internal/open and internal/session
skills/                Claude Code skills, installable with npx skills add
```

The CLI and the popup are two front ends onto one decision layer, so there is
no second copy of the rules to drift. Each feature is one such layer:
`internal/open` for a pasted link, `internal/session` for the picker — and
`internal/session` builds on `internal/open` rather than beside it, so a Space
made from a picked project goes through the same agent step as one made from a
PR.

Setups follow the same split, one layer further in. `internal/setup` is pure --
files in, an ordered list of resolved steps out, with no socket and no network
-- so precedence, inheritance and matching are testable without a session.
Carrying those steps out lives in `internal/open`, next to the agent-start
retry and readiness rules it has to reuse. That is also what makes `--dry-run`
honest: the preview prints the same resolved plan the real run walks, rather
than a second derivation of it that could disagree.

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
