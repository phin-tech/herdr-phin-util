---
name: herdr-phin-util
description: Drive the herdr-phin-util plugin CLI — open a Herdr Space from a pasted PR/issue/Linear link, pick a project or worktree, promote a pane into its own Space, hand an outside Claude session into Herdr, and apply setup layouts. Use when working with Herdr Spaces, worktrees or panes through this plugin, or when configuring its config.toml.
---

# herdr-phin-util

A personal Herdr plugin: one binary, a subcommand per feature. It runs as a
Herdr plugin action and as a plain CLI — the two are front ends onto the same
decision layer, so anything a popup does can be done from a shell and tested
there.

Config lives at `$HERDR_PLUGIN_CONFIG_DIR/config.toml`, defaulting to
`~/.config/herdr/plugins/config/phin-util/config.toml`.

## Commands

```sh
herdr-phin-util open <link-or-text> [--agent|--no-agent] [--prompt TEXT]
                                    [--setup NAME] [--dry-run]
herdr-phin-util popup                # the same thing, with a UI
herdr-phin-util pick                 # project picker
herdr-phin-util pick-worktree        # picker, starting inside this repo
herdr-phin-util projects             # what discovery found, and nothing else
herdr-phin-util worktrees            # what the worktree level would offer
herdr-phin-util setups [--repo DIR]  # what setups are defined, and from where
herdr-phin-util project <dir> [--agent|--no-agent] [--setup NAME] [--dry-run]
herdr-phin-util promote [pane_id]    # move a pane into a Space of its own
herdr-phin-util handoff [--session ID] [--label TEXT] [--cwd PATH]
                        [--dry-run] [--force]
herdr-phin-util version
```

## What `open` does with what you paste

| Pasted | Result |
| --- | --- |
| GitHub PR URL | finds the checkout, asks `gh` for the branch, fetches it, opens a worktree Space |
| GitHub issue URL | finds the checkout, derives a branch from the title, opens a worktree Space |
| Linear issue URL | derives a branch from the key and slug; built in the repo you are already in |
| `owner/repo` | clones it if it is missing, opens it if it is not |
| anything else | a plain Space named after the text |

It then optionally starts an agent and **types a prompt without sending it** —
`pane.send_text`, not `agent.prompt`, so the text sits in the input to be read
and edited. That is the plugin's consistent stance on prompts.

A GitHub link carries its own repository, so it is resolved from
`[repos].templates` wherever you run it. A Linear link does not, so run it from
inside the repo it belongs to.

## The picker

`pick` lists open Spaces first, then every checkout with no Space. The rule
that makes it safe: **a checkout that already has a Space never appears twice**
— it is offered as the Space, so picking it switches rather than building a
second one over the same directory.

Keys: `tab` descends into a repo's worktrees and branches, `esc` comes back
with the filter intact, `ctrl+a` toggles the agent, `ctrl+e` opens the prompt
box, `ctrl+t` opens the setups for that row, `ctrl+r` fetches at the worktree
level. Typing a name that matches no branch offers to create it.

The box also takes a pasted link: a reference is a query with one answer, so it
replaces the list rather than filtering it.

## promote vs handoff

Both get a session into a Space of its own; which one is right depends on where
you are.

| | promote | handoff |
| --- | --- | --- |
| Run from | inside Herdr | outside it |
| What moves | the live pane — same PID, same scrollback, same session | the transcript only |
| Afterwards | nothing to do | quit the original with `/exit` |

`promote` uses `pane.move --new-workspace`, so a Claude session promoted
mid-task keeps its whole context. `handoff` cannot move anything — it opens a
Space on the same directory and runs `claude --resume` against the same session
file. Two Claudes appending to one session file will diverge, so the original
has to go.

Inside Herdr, `handoff` refuses and points at `promote`.

## Setups

`--setup NAME` replaces the single-agent step with a YAML layout: several tabs,
panes, agents and commands built from the target being opened. `--dry-run`
prints what it would build without touching Herdr. `setups` lists what is
defined and where each came from.

For writing or debugging those files, use the **herdr-setups** skill, which
covers the schema, the three source directories and their precedence.

## config.toml

```toml
[repos]
templates = ["~/src/{host}/{owner}/{repo}"]   # first that exists wins

[agent]
enabled = true
kind = "claude"

[agent.prompts]                                # Go text/template
github_pr = "Review PR #{{.Number}} — {{.Title}}\n{{.URL}}"

[projects]
# roots = ["~/src/*/*"]    # unset = derived from [repos].templates
# git_only = true
# depth = 1

[setups]
# dir = "setups"
# repos_dir = "repos"
# repo_file = ".herdr-setups.yaml"
# default = "pair"          # a setup Enter uses instead of one agent

[worktrees]
# path = "{repo_root}/.worktrees/{branch}"     # unset = Herdr decides
```

A bad value falls back to its default and the complaint is printed rather than
the setting being silently ignored.

## Debugging

- `projects`, `worktrees` and `setups` need no Herdr session. Each answers "is
  this a discovery problem or a picker problem" on its own.
- Plugin actions run asynchronously: `plugin action invoke` returns
  `running`, and the outcome lands in `herdr plugin log list`.
- `HERDR_SOCKET_PATH` must be set for anything that talks to Herdr; run it
  inside a Herdr session.
