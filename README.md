# Phin Util

Small everyday [Herdr](https://herdr.dev) utilities in one plugin: get to a
repo, a branch, a PR or a whole pre-laid-out room in one keystroke.

**This is built for me.** Actions may be renamed or dropped and behavior may
change without a version bump. If you depend on it, pin a commit or fork it.

Requires Herdr 0.7.4+. macOS and Linux.

## Install

```sh
herdr plugin install phin-tech/herdr-phin-util
herdr plugin link /path/to/herdr-phin-util   # or a local checkout
```

Builds from source if Go is present, otherwise fetches the CI binary and
verifies its checksum.

Needs `git`, and `gh` **authenticated** (`gh auth login`) — PR and issue
lookups and cloning all go through it.

The binary lands at `$HERDR_PLUGIN_ROOT/bin/herdr-phin-util` — enough for
everything driven from inside Herdr, since the actions are invoked through that
variable. CLI examples below say `herdr-phin-util`; from a checkout that's
`bin/herdr-phin-util`, and from an installed copy you'll want it on your `PATH`:

```sh
# installs land under ~/.config/herdr/plugins/github/<name>-<hash>/
ln -s ~/.config/herdr/plugins/github/*herdr-phin-util*/bin/herdr-phin-util ~/.local/bin/
```

Only [handoff](#hand-a-claude-session-in) really needs that, since it runs from
a terminal outside Herdr where `$HERDR_PLUGIN_ROOT` doesn't exist.

## Set up keybindings

Add to your Herdr config. These are what I use; pick whatever is free for you.

```toml
# Open a project — the one you'll use most.
[[keys.command]]
key = "prefix+shift+f"
type = "plugin_action"
command = "phin-util.pick-project"
description = "Open a project"

# Same picker, but starts at the branch level for the repo you're already in.
[[keys.command]]
key = "prefix+shift+b"
type = "plugin_action"
command = "phin-util.pick-worktree"
description = "Open a worktree"

# Move the current pane into a Space of its own.
[[keys.command]]
key = "prefix+shift+s"
type = "plugin_action"
command = "phin-util.promote"
description = "Promote pane to its own Space"

# Paste-a-link popup. Superseded by the project picker — bind only if you want it.
[[keys.command]]
key = "prefix+shift+o"
type = "plugin_action"
command = "phin-util.open-workspace-maker"
description = "New Space from a link"
```

All actions are also in the command palette and the pane action menu.

## Tell it where your repos live

`$HERDR_PLUGIN_CONFIG_DIR/config.toml`, or by hand at
`~/.config/herdr/plugins/config/phin-util/config.toml`. Everything is optional,
but this line makes the picker useful:

```toml
[repos]
templates = ["~/src/{host}/{owner}/{repo}"]
```

Full reference in [Configuration](#configuration).

## What's in it

| Feature | How you reach it | Use it when |
| --- | --- | --- |
| [Project picker](#project-picker) | `prefix+shift+f` | you want to be in a repo, open or not |
| [Worktrees & branches](#worktrees--branches) | `tab` on a repo, or `prefix+shift+b` | you want a branch, existing or new |
| [Paste a link](#paste-a-link) | type/paste into the picker | a PR, issue or Linear ticket needs a Space |
| [Clone](#clone-something-you-dont-have) | type `owner/repo` into the picker | you don't have the repo yet |
| [Setups](#setups) | `ctrl+t` in the picker | you want a whole layout, not one pane |
| [Promote](#promote-a-pane-to-its-own-space) | `prefix+shift+s` | a pane in a crowded Space deserves its own |
| [Handoff](#hand-a-claude-session-in) | `herdr-phin-util handoff` | a Claude session is running outside Herdr |

---

## Project picker

Type a few characters, press enter, land in the repo. Open Spaces first, then
checkouts on disk that have no Space yet.

```
▸ open  shift-clock                ~/src/github.com/phin-tech/shift-clock
  open  herdr-phin-util (current)  ~/src/github.com/phin-tech/herdr-phin-util
  new   hearth-mud                 ~/src/github.com/arcane-grimoire/hearth-mud
  new   roux-next-gen              ~/src/github.com/phin-tech/roux-next-gen
```

`open` rows focus an existing Space. `new` rows create one and optionally start
an agent. A checkout that already has a Space **never appears twice** — you get
the Space, so you can't build a second one over the same directory.

| Key | Does |
| --- | --- |
| type | subsequence filter over name and path (`hpu` → `herdr-phin-util`) |
| `enter` | open it |
| `tab` | go deeper — **worktrees** on a repo row, **setups** on anything else (`ctrl+w` too) |
| `ctrl+t` | setups directly, from any row |
| `shift+tab` / `esc` | back up a level, filter intact |
| `ctrl+a` | toggle "start an agent" |
| `ctrl+e` | edit the agent prompt for this launch |
| `ctrl+r` | fetch and rebuild — worktree level only |

Arrow keys move the cursor in the filter box, so they don't navigate levels —
the box holds pasted URLs, and text you can't move a cursor through is text you
can't correct. The footer names what `tab` would do on the highlighted row.

### Worktrees & branches

`tab` on a repo (or `prefix+shift+b` from inside one):

```
Open a project › roux-next-gen

▸ tree    main                    ~/src/github.com/phin-tech/roux-next-gen
  tree    agent-a8fb383           …/.claude/worktrees/agent-a8fb383e4522b1505
  local   feature/parser          no worktree yet
  remote  fix-the-thing           origin — fetched on open
```

A `remote` row shows the local name the branch would get, not `origin/…`.

Same rule twice over: a branch with a worktree isn't offered as a branch, a
remote branch that exists locally isn't offered as remote, and a worktree with
a Space becomes an `open` row.

Type a name that matches nothing and you get a `create` row — that's the whole
new-branch flow. New branches are based on the repo's default branch, resolved
from `origin/HEAD` and falling back to a local `main`/`master`. If neither
exists, git decides, which means the source checkout's current `HEAD`.

Remote branches come from refs already on disk, so the level opens instantly.
`ctrl+r` fetches when you know it's stale.

### Paste a link

The same box takes a reference and replaces the list with the one answer.

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

| Pasted | What you get |
| --- | --- |
| GitHub PR URL | worktree Space on the PR's branch (looked up with `gh`) |
| GitHub issue URL | worktree Space on a branch derived from the title (`99-fix-the-flaky-test`, or `issue-99` if `gh` can't answer) |
| Linear issue URL | worktree Space on a branch from the issue key and slug — no API token needed |
| `owner/repo` | clone it, or open it if you have it |
| anything else | a plain Space named after the text |

If a Space already exists for it, you're offered the Space instead of a second
one. That match is a heuristic — the Space's label is derived from the URL
alone (`roux#42`, `ENG-123`), so it costs no `gh` call and no network, but
renaming the Space defeats it. You then fall back to the old behavior, where
`worktree.open` quietly focuses the existing one anyway.

A PR carries its own repo, so it's found via `[repos]` from anywhere. A Linear
issue doesn't — run it from inside the repo it belongs to, same as you would
for `git checkout`. Issue and Linear branches are also created from the current
checkout's `HEAD` rather than the default branch, since neither names a base;
check what you have checked out before you make one.

### Clone something you don't have

```
> charmbracelet/lipgloss
▸ clone   lipgloss    clone to ~/src/github.com/charmbracelet/lipgloss

> phin-tech/roux
▸ new     roux        already cloned — ~/src/github.com/phin-tech/roux
```

Clones with `gh`, so private repos need no extra setup. Destination is the
**first** `[repos].templates` entry — the same list everything else searches, so
what you clone is findable afterwards.

`tab` on a `clone` row means "fetch it, then show me its branches". This is the
one place `tab` isn't instant; the row says `clone & branch` so you know.

A GitHub HTTPS URL or an `git@github.com:owner/repo.git` remote works here too.
The bare `owner/repo` shorthand is only recognised at the project level — one
level down that shape is almost always a branch name.

### Where it looks

By default it derives roots from `[repos].templates` —
`~/src/{host}/{owner}/{repo}` means `~/src/*/*`. Override with
`[projects].roots`. Globs expand when the picker opens, so a repo cloned five
minutes ago is there. A directory with `.git` is a project and isn't descended
into.

When a repo is missing from the list, `herdr-phin-util projects` prints exactly
what discovery found and needs no Herdr session — so it answers "is this a
discovery problem or a picker problem" on its own.

---

## Promote a pane to its own Space

`prefix+shift+s`, or `herdr-phin-util promote [pane-id]`.

Takes the pane you're looking at and gives it a Space of its own, named after
its working directory.

The pane is **moved, not restarted** — same PID, same scrollback, and for an
agent, the whole session. A Claude session promoted mid-task comes out still
holding its context.

A pane already alone in its Space is a no-op. A pane id that no longer exists
reports and exits non-zero.

```sh
herdr-phin-util promote          # the focused pane
herdr-phin-util promote w4:p2    # a named pane
```

## Hand a Claude session in

For the sessions promote can't reach: you started Claude in a plain terminal,
it turned into real work, and you want it in Herdr.

```sh
herdr-phin-util handoff                        # resume this session in a new Space
herdr-phin-util handoff --dry-run              # say which session, open nothing
herdr-phin-util handoff --label "auth rework"  # name the Space yourself
herdr-phin-util handoff --session <uuid>       # some other conversation
herdr-phin-util handoff --cwd ~/src/other      # look for the session elsewhere
```

**It's not a move, and can't be.** Herdr can only relocate panes it already
owns, so this opens a Space on the same directory and runs a fresh
`claude --resume` against the same session file. The conversation arrives
intact; the process doesn't. **Quit the original with `/exit`** — two Claudes
appending to one session file will diverge.

| | promote | handoff |
| --- | --- | --- |
| Run from | inside Herdr | outside it |
| What moves | the live pane — same PID, same scrollback | the transcript only |
| Afterwards | nothing to do | quit the original |

Run inside Herdr it refuses and points at promote; `--force` overrides.

No keybinding — being outside Herdr is the whole premise. It does need to be on
your `PATH`, since an external terminal has no `$HERDR_PLUGIN_ROOT`.

**Which session it picks**, stopping at the first answer: `--session` →
`$CLAUDE_CODE_SESSION_ID` (the usual one, exported into every Claude session) →
newest transcript for the current directory → newest transcript anywhere. That
last one announces itself, and opens the Space in the *session's* directory:

```
$ herdr-phin-util handoff
no session here -- using the most recent one:
  a3460e51  ~/src/github.com/ogulcancelik/herdr  (12m ago)
resumed in Space w1D (pane w1D:p1)
```

---

## Setups

Every pick above ends with one agent, one prompt, one pane. A setup replaces
that last step with a whole room: an orchestrating agent, a second opinion from
another model, and `roborev` already running — laid out and prompted in one
keystroke.

Reach them with `ctrl+t` from any row, or `tab` on a row that has nothing below
it — a worktree, a branch, a pasted link. (On a repo row `tab` still means its
worktrees.) **Enter is unchanged** — setups are opt-in per launch.

```
Open a project › roux-next-gen › setup

▸ setup   default      one claude, prompt typed not sent
  setup   pr-review    orchestrator + second opinion + roborev — generic
  setup   dev          agent, a test loop, and the plugin log — repo
```

The list is filtered to what applies to that row, and each row says which
source it came from.

### Writing one

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
        model: gpt-5.1-codex-max
        args: ["--sandbox", "read-only"]
        submit: true
        prompt: Review the diff on {{.Branch}} for correctness bugs.

      - split: down
        ratio: 0.3
        label: roborev
        command: roborev review --branch

  - name: tests
```

A setup is a **recipe applied to a target**, not a workspace of its own — the
worktree, branch and label are decided before it runs. So `{{.Number}}` and
`{{.Branch}}` mean the same thing they do in `[agent.prompts]`, and one file
works for a PR, an issue, a Linear ticket or a plain checkout.

**Pane keys:**

| | |
| --- | --- |
| `agent:` + `prompt:` | start an agent, type the rendered template |
| `agent:` + `skill:` | shorthand for a prompt that's one slash command |
| `command:` | run it in a plain shell |
| neither | a shell at its prompt |

Also per pane: `split` (`right`/`down`), `ratio`, `label`, `cwd`, `env`,
`focus`, `wait_for`, and on an agent pane `model:` and `args:`. `cwd` and `env`
inherit setup → tab → pane. A tab takes `command:` directly instead of
`panes:` when it's one command and nothing else, and a tab with neither is one
plain shell.

- `submit: true` presses Enter. **Omitted means type it and leave it** — read
  the orchestrator's brief before firing it, while the workers are already going.
  A submitted prompt waits for Herdr to finish launching the agent, which is a
  couple of seconds after its input appears; a pane whose agent never gets
  there — stuck on a trust dialog or an upgrade nag — says so rather than
  sitting idle with nothing typed.
- `wait_for: { match: "queued", timeout_ms: 20000 }` holds the rest of the
  layout until that pane's output matches. A timeout isn't fatal.
- `model: opus` and `args: ["--permission-mode", "plan"]` are the agent's
  command line: `model` first, then `args` verbatim. This is how a reviewer is
  made **read-only** — a prompt that says "don't edit anything" is a request,
  and the diff being reviewed can argue with it; `--permission-mode plan`
  can't be argued with. Values render as templates, so `--add-dir {{.Path}}`
  works. Neither is validated: the agent's own rejection of a bad model name
  beats an allowlist that goes stale.
- Unknown keys are reported, not ignored.

Narrow which targets a setup offers itself for with `applies_to`, `repos`
(globs over `owner/repo`), and `branches` (globs). Valid `applies_to` kinds:
`github_pr`, `github_issue`, `github_repo`, `linear`, `plain`, `project`.

### Where they live

```
~/.config/herdr/plugins/config/phin-util/
  setups/pr-review.yaml                  generic — offered for any repo
  repos/roux-next-gen/dev.yaml           this machine's, for one repo
  repos/phin-tech/roux/pr-review.yaml    same, disambiguated by owner
<checkout>/.herdr-setups.yaml            committed, travels to every worktree
```

`repos/<repo>/` is the everyday case; files there need no `repos:` key and
aren't offered elsewhere. `.herdr-setups.yaml` holds a `setups:` list rather
than one per file — there's one in this repo as an example.

Same name twice resolves `setups/` < checkout file < `repos/`, so a team setup
can be overridden on your machine without touching a tracked file. Those three
locations are `[setups].dir`, `repos_dir` and `repo_file` if you want to move
them.

### Check one before you run it

```sh
herdr-phin-util setups                  # what's defined, and where from
herdr-phin-util setups --repo ~/src/x   # as if that checkout were the row
herdr-phin-util open <PR> --setup pr-review --dry-run
herdr-phin-util open <PR> --setup pr-review
herdr-phin-util project ~/src/x --setup dev
```

`--dry-run` prints every tab, split, cwd and rendered prompt with the PR's real
number, title and branch, and touches nothing. It needs no Herdr session, which
is the point — it works while you're writing the file.

### When a pane doesn't come up

Panes are independent, so a step that fails is reported and skipped rather than
ending the run: an agent that won't start is no reason for the three panes
after it to be left as bare shells. Each failure gets a `warning:` line naming
the tab and the cause, the pane itself is renamed `failed: <label>` so the
Space says which one it was, and the command exits non-zero.

The same goes for the worktree. If `worktree.create` fails and the Space falls
back to an existing worktree, you get a line saying so; if it falls back to the
*source checkout*, you get a louder one, because that Space is sitting on
whatever branch the checkout has rather than the one you asked for — a PR
review setup that quietly reviews `main` is worse than one that failed.

Shape borrowed from [herdr-plus](https://github.com/cloudmanic/herdr-plus)'s
projects; model from [herdr-spreader](https://github.com/yuk1ty/herdr-spreader).

---

## Command reference

```sh
herdr-phin-util pick                  # project picker
herdr-phin-util pick-worktree         # picker, starting at this repo's branches
herdr-phin-util popup                 # the older paste-a-link popup
herdr-phin-util projects              # what discovery found
herdr-phin-util worktrees             # what the branch level would offer
herdr-phin-util setups [--repo DIR]   # what setups are defined, and from where
herdr-phin-util version

herdr-phin-util open <link-or-text> [--agent|--no-agent] [--prompt TEXT]
                                     [--setup NAME] [--dry-run]
herdr-phin-util project <dir>        [--agent|--no-agent] [--setup NAME] [--dry-run]
herdr-phin-util promote [pane-id]
herdr-phin-util handoff [--session ID] [--label TEXT] [--cwd PATH]
                        [--dry-run] [--force]
```

`--agent`/`--no-agent` override `[agent].enabled`; `--prompt` replaces the
rendered template outright.

<details>
<summary><b>The older paste-a-link popup</b> (<code>popup</code>, bound above as
<code>prefix+shift+o</code>)</summary>

Superseded by the project picker, which takes links in its own box and adds
"switch to it if a Space already exists". Still shipped because it puts the
prompt front and centre instead of behind `ctrl+e`.

It resolves links the same way, then optionally starts an agent and **types a
prompt without sending it**. Keys: `tab` between fields, `space` flips the agent
toggle, `ctrl+s` submits, `esc` backs out. Touch the prompt box and your text
wins over the template. Mouse works throughout.

</details>

---

## Configuration

`$HERDR_PLUGIN_CONFIG_DIR/config.toml`, falling back to
`~/.config/herdr/plugins/config/phin-util/config.toml` when run by hand. Every
key is optional. It's per-machine on purpose — work and personal machines
rarely agree on where repositories live.

```toml
[repos]
# Tried in order; the first that exists wins. {host} {owner} {repo}
templates = ["~/src/{host}/{owner}/{repo}"]

[agent]
enabled = true      # the agent toggle's starting state
kind = "claude"     # any agent kind Herdr knows

[agent.prompts]     # Go text/template
# Fields: URL Host Owner Repo Number Title Issue Slug Branch Text
github_pr    = "Review PR #{{.Number}} — {{.Title}}\n{{.URL}}"
github_issue = "Work issue #{{.Number}} — {{.Title}}\n{{.URL}}"
linear       = "Work {{.Issue}} — {{.Title}}\n{{.URL}}"
plain        = "{{.Text}}"
# Empty by default: opening a checkout is not a task, so the agent starts on a
# clean input instead of a line you have to delete. Fields: Repo Path
# project = "You are in {{.Repo}} ({{.Path}})"

[projects]
# Where the picker looks. Unset means "derive it from [repos]", which is
# almost always what you want.
# roots = ["~/src/*/*", "~/work"]   # plain paths or globs; ** recurses
# git_only = true                   # only directories carrying .git
# depth = 1                         # how far below each root to look

[setups]
# Where setup files are read from, relative to this directory.
# dir       = "setups"
# repos_dir = "repos"
# repo_file = ".herdr-setups.yaml"   # looked for inside a checkout

[worktrees]
# Unset means Herdr decides, which is usually right. Set it to make worktrees
# land where another tool expects them.
# {host} {owner} {repo} {repo_root} {branch}
# path = "{repo_root}/.worktrees/{branch}"

[linear]
# Accepted but unused today -- a Linear issue resolves from its URL alone.
# api_key = "lin_api_..."
```

A bad *value* — an unknown agent kind, a nonsense depth — falls back to its
default and prints the complaint rather than failing the action. A misspelled
`{{.Placeholder}}` renders empty. A misspelled **key** is silently ignored,
unlike the YAML setup files, which are strict — so check a config change took
effect with `herdr-phin-util projects`.

A worktree picked from the branch level has no URL behind it, so `{owner}`
renders empty there and `{host}` defaults to `github.com`; `{repo_root}`,
`{repo}` and `{branch}` all work.

## Skills

Two Claude Code skills, installable from this repo:

```sh
npx skills add phin-tech/herdr-phin-util
```

- **herdr-setups** — writing and debugging a setup file: the schema, the three
  sources, the dry-run loop.
- **herdr-phin-util** — driving the CLI.

---

## Hacking on it

One binary, a subcommand per feature, so a new utility doesn't mean a new repo,
plugin id or keybinding scheme.

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

The CLI and the popup are two front ends onto one decision layer, so there's no
second copy of the rules to drift. `internal/session` builds *on*
`internal/open`, so a Space made from a picked project goes through the same
agent step as one made from a PR. `internal/setup` is pure — files in, ordered
steps out — which is what makes `--dry-run` honest: it prints the same resolved
plan the real run walks.

Herdr API gotchas that shaped the code:

- `pane.current` resolves from `$HERDR_PANE_ID`, so it reports the **calling**
  pane — wrong for anything fired by a keybind. Use the injected context, or
  the `focused` flag in `pane.list`.
- A pane is **renumbered** when it changes workspace; the id that comes back
  from a move replaces the one that went in.
- Plugin actions run **asynchronously**. `plugin action invoke` returns
  `status: "running"`; the outcome shows up in `herdr plugin log list`.
- `agent.wait`'s `idle` status is Herdr's activity guess and can fire during a
  lull before the agent has drawn its prompt. For `claude` the prompt is also
  confirmed against real screen content via `pane.wait_for_output`
  (`internal/open`'s `readyMarkers`); other kinds fall back to `agent.wait`.
- `agent.start` can reject a pane with `agent_pane_busy` in the instant after
  `worktree.create`/`workspace.create` returns it. Retried with a short linear
  backoff (`startAgentWithRetry`); other errors aren't.
- `worktree.create` failing and `worktree.open` succeeding does **not** mean
  you got the worktree: open can land on the source checkout, which looks
  identical from the API and is a different branch. Compare the returned pane's
  cwd against the request's (`landedOnSourceCheckout`).
- The pane a worktree Space comes back with is in the **worktree**; the repo
  path the plugin resolved is the **checkout it was cut from**. A setup's tabs
  and splits take their cwd from the former (`spaceCwd`) — using the latter
  builds a PR review that reviews `main`.
- `agent.prompt` can answer `agent_not_ready` right after every readiness check
  has passed — codex sitting on an update prompt does it. Settled and retried
  once (`sendSetupPrompt`), then reported.
- `workspace.list` reports no **cwd** — it's recovered from the panes inside,
  which is why the picker calls `pane.list` too. Without that join there's no
  way to tell an open Space and a discovered checkout are the same thing.
- Panes are all created before anything runs in one, and focus is decided last:
  splitting a tab after a TUI has started resizes a running program.
- The handoff directory comes from inside the transcript, not the folder Claude
  files it under — that name turns both `/` and `.` into `-`, so `foo.bar` and
  `foo/bar` are indistinguishable once written.
