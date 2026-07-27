---
name: herdr-setups
description: Write, debug and apply herdr-phin-util setups — YAML workspace recipes that build a Herdr Space with several tabs, panes, agents and commands from one picked target (a PR, an issue, a checkout). Use when asked to create a review layout, a multi-agent workspace, a "setup" or "layout" file for Herdr, or when a setup is not being offered or not doing what its file says.
---

# herdr-phin-util setups

A **setup** is a YAML recipe applied to a target that has already been
resolved. The worktree, branch and Space label are decided before it runs; the
setup only says what fills the Space — which tabs, which panes, which agents,
which prompts, which commands.

Every path through the plugin normally ends by starting one agent and typing
one prompt. A setup replaces that step and nothing else.

## Where a file goes

Three sources, in increasing precedence when names collide:

| Path | Applies to | Use it for |
| --- | --- | --- |
| `$HERDR_PLUGIN_CONFIG_DIR/setups/<name>.yaml` | any repo | general-purpose layouts |
| `<checkout>/.herdr-setups.yaml` | that repo, committed | layouts the team shares |
| `$HERDR_PLUGIN_CONFIG_DIR/repos/<repo>/<name>.yaml` | that repo, this machine | personal, repo-specific |

`$HERDR_PLUGIN_CONFIG_DIR` defaults to
`~/.config/herdr/plugins/config/phin-util`. `repos/<owner>/<repo>/` also works
when two checkouts share a name. `.yml` is accepted everywhere `.yaml` is.

Files under `setups/` and `repos/` hold **one setup at the top level**.
`.herdr-setups.yaml` holds a `setups:` **list**, because a repo should not grow
a directory for two layouts.

## The file

```yaml
name: pr-review                 # required, and what --setup takes
description: what it is for     # shown in the picker
applies_to: [github_pr]         # omitted = every kind
repos: ["phin-tech/*"]          # globs; implied for files under repos/
branches: ["fix/*"]             # globs over the resolved branch

cwd: .                          # everything below inherits from here
env:
  REVIEW_MODE: deep

tabs:
  - name: review
    cwd: ./web                  # relative to the setup's cwd
    env: { TAB: value }
    panes:
      - label: orchestrator
        agent: claude
        focus: true             # where you land when the layout is built
        prompt: |
          Review PR #{{.Number}} — {{.Title}}
          {{.URL}}

      - split: right            # "right" or "down"; relative to the pane above
        ratio: 0.3              # 0 < r < 1; omitted = an even split
        agent: codex
        submit: true            # send it; omitted = type it and leave it
        prompt: Review {{.Branch}}.

      - split: down
        label: roborev
        command: roborev review --branch
        wait_for: { match: "queued", timeout_ms: 20000 }

  - name: shell                 # a tab with no panes is one plain shell
  - name: git
    command: lazygit            # single-pane shorthand; never with panes:
```

### Pane shapes

A pane is exactly one of:

- `agent:` plus `prompt:` — starts that agent kind and types the prompt.
- `agent:` plus `skill:` — shorthand for a prompt that is one slash command.
  `skill: /code-review` and `skill: code-review` are the same; templates work
  in it (`skill: /review {{.Branch}}`).
- `command:` — runs in a plain shell pane.
- neither — a shell sitting at its prompt.

`agent` must be a kind Herdr knows: `claude`, `codex`, `gemini`, `cursor`,
`opencode`, `copilot`, `amp`, `droid`, `pi`, and others.

### submit

`submit: true` sends the prompt with Enter. **Omitted means type it and leave
it unsent** — deliberately, and it is the right default for the pane a person
will read. The usual shape of a good fan-out setup is workers with
`submit: true` and one orchestrator without, so the workers are already going
by the time you have read the brief.

### Templates

Prompts, commands and env values are Go `text/template`, rendered against the
target:

`{{.URL}} {{.Host}} {{.Owner}} {{.Repo}} {{.Number}} {{.Title}} {{.Issue}}
{{.Slug}} {{.Branch}} {{.Text}} {{.Path}}`

A misspelled placeholder renders empty rather than failing the action, so
`--dry-run` is the only way to notice one. `{{.Number}}` and `{{.Title}}` are
empty for a plain checkout; `{{.Branch}}` is empty until one is resolved.

### Inheritance and ordering

- `cwd` composes setup → tab → pane. Relative paths resolve against the level
  above; absolute paths and `~` win outright. The base is the Space's own
  directory, which for a PR is the **worktree**, not the source checkout.
- `env` merges the same way, closest level winning per key.
- Panes are built in file order, each `split` relative to the pane before it.
  The first pane of the first tab reuses the Space's own pane; later tabs are
  created unfocused.
- Every pane is created before anything runs in one, and focus is applied last.
- `wait_for` holds the rest of the layout until that pane's output matches. Use
  it when a later pane's prompt depends on an earlier pane having started. A
  timeout is not fatal — it warns and carries on.

## Using one

```sh
herdr-phin-util setups                                 # what loads, and from where
herdr-phin-util setups --repo ~/src/github.com/o/r     # as if that were the row
herdr-phin-util open <url> --setup pr-review --dry-run # resolve, print, touch nothing
herdr-phin-util open <url> --setup pr-review
herdr-phin-util project ~/src/o/r --setup dev
```

In the picker (`herdr-phin-util pick`), `ctrl+t` on a row opens the setups that
apply to it; `esc` comes back. Enter without `ctrl+t` is unchanged.

## Writing one: the loop

1. Write the file into the right source directory for its scope.
2. `herdr-phin-util setups` — confirm it loads, with the origin you intended.
   Problems are printed on stderr, one line per bad file, and one bad file
   never takes the others down.
3. `herdr-phin-util open <a real url> --setup <name> --dry-run` — confirm the
   prompts render with real values and the cwds are what you meant. This needs
   no Herdr session.
4. Only then run it for real.

## When it does not work

**Not in the list.** Run `herdr-phin-util setups --repo <the checkout>`. If it
is absent, it failed to load — the reason is on stderr. If it is present but
the picker does not offer it, the `applies_to` / `repos` / `branches` filters
are excluding that row. A `branches:` filter never matches a row with no branch
resolved yet, which includes plain project rows.

**A key does nothing.** Unknown keys are reported, not ignored — check stderr.
`command` vs `commands`, `prompt` vs `prompts` and `wait_for` vs `waitfor` are
the usual ones.

**Validation rejects it.** The rules: a name is required; a tab has `command`
or `panes`, never both; the first pane of a tab cannot `split`; `split` is only
`right` or `down`; `ratio` is strictly between 0 and 1; a prompt needs an
agent; an agent and a command cannot share a pane; only one pane may be
`focus`.

**The layout builds but a prompt is blank.** A placeholder is misspelled, or
the field is empty for that target kind. `--dry-run` shows it.

**A command sits at the prompt unrun.** That is what the plugin's own pacing
exists to prevent; if it recurs, the shell is unusually slow to draw its
prompt. Nothing in the file fixes it.
