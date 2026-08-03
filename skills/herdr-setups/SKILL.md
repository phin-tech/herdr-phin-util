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
        model: gpt-5.1-codex-max          # agent panes only
        args: ["--sandbox", "read-only"]  # extra argv, verbatim, after --model
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

### A command pane's Herdr identity

Every `command:` pane is typed with a leading environment assignment naming
where it is, so it never has to poll `herdr pane list` to find itself or a
labelled sibling:

```
HERDR_WORKSPACE_ID=w2H HERDR_TAB_ID=w2H:t1 HERDR_PANE_ID=w2H:p2 HERDR_PANE_META_ORCHESTRATOR=w2H:p1 ./my-script.py
```

- `HERDR_WORKSPACE_ID`, `HERDR_TAB_ID`, `HERDR_PANE_ID` — the pane's own three
  ids.
- `HERDR_PANE_<NAME>` — one per **labelled pane anywhere in the setup**, not
  just the same tab, including the command pane's own label if it has one.
  `<NAME>` is the label upper-cased, with every run of non-alphanumeric
  characters folded to a single `_` and leading/trailing `_` trimmed:
  `meta-orchestrator` → `META_ORCHESTRATOR`. A label that folds to nothing
  (pure punctuation) or that would start with a digit gets no variable — an
  illegal name is worse than a missing one. If two labels fold to the same
  name, **the first one in the file wins**; the later one is silently
  dropped rather than left to chance.
- Agent panes get none of this. They have no use for it (`herdr pane
  current` answers the same question from inside the agent), and there is
  nothing to prefix a prompt onto.
- `--dry-run` cannot show real ids — nothing has been built yet when a plan
  is only previewed — but it does list which variable *names* a `command:`
  pane will receive, e.g. `herdr    HERDR_WORKSPACE_ID, HERDR_TAB_ID,
  HERDR_PANE_ID, HERDR_PANE_META_ORCHESTRATOR`.

**The one real limitation, and it will bite silently if ignored:** a
`KEY=value` prefix scopes to exactly one *simple* command. It works for
`./my-script.py` or `python discover.py --flag`. It does **not** propagate
past a shell control operator:

```yaml
command: cd layers && ./discover.py     # HERDR_* vars reach `cd`, not discover.py
```

`&&`, `||`, `;`, `|` and a literal newline all start a new simple command, and
only the first one sees the prefix. There is no portable fix folded in here
on purpose — `export` is spelled differently in fish (`set -x`) than in
POSIX shells, and the pane's shell is not knowable from the setup file — so a
`command:` that chains stages and needs the vars past the first one has to
re-export them itself. A pipeline is usually fine as written, since the first
stage is normally the one doing the reading.

Labels (and any agent already declared) are guaranteed to be attached before
any `command:` pane in the whole setup runs: every pane is built and every
label applied in one pass, before any command or agent prompt goes out in a
second pass (see "Inheritance and ordering" below). An agent that comes
*later* in the file is not guaranteed to have **launched** by the time an
earlier command pane runs — only its pane id is, via `HERDR_PANE_<NAME>`,
which is enough to address it without polling for it to exist.

### model and args

`model:` and `args:` are the agent's command line: `--model <model>` first,
then `args` verbatim. Both are agent-pane only — a `command:` pane spells its
own flags out, and setting either there is a load-time problem, not silence.

Neither is validated. Model names change faster than a file like this can keep
up with, so a bad one is the agent's error to give.

`args` is a **list**, so nothing has to be shell-quoted:

```yaml
- label: reviewer
  agent: claude
  model: opus
  args: ["--permission-mode", "plan", "--add-dir", "{{.Path}}"]
```

Values render as templates, same as prompts.

**Use it for a read-only reviewer.** A prompt that says "do not edit anything"
is a request, and the diff under review can argue with it. `--permission-mode
plan` (claude) or `--sandbox read-only` (codex) cannot be argued with. If a
pane must not mutate the repo, put it here, not in the prompt.

### submit

`submit: true` sends the prompt with Enter. **Omitted means type it and leave
it unsent** — deliberately, and it is the right default for the pane a person
will read. The usual shape of a good fan-out setup is workers with
`submit: true` and one orchestrator without, so the workers are already going
by the time you have read the brief.

### for_each: repeating a tab

A tab can build itself once per element of a **named list** instead of once.
Today `layers` is the only list a target resolves, and only a `github_pr`
target resolves it — the chain of open pull requests it belongs to, bottom of
the stack first:

```yaml
applies_to: [github_pr]
tabs:
  - for_each: layers          # a name the target resolved, not a template
    as: layer                 # defaults to for_each's own name if omitted
    name: "L{{.layer_layer}} #{{.layer_pr}}"
    panes:
      - label: "l{{.layer_layer}}-claude"
        agent: claude
        submit: true
        prompt: "Review PR #{{.layer_pr}} at {{.layer_head_sha}} (base #{{.layer_base_pr}})"
```

`for_each` names a list, it is never itself a template expression. A setup
naming a list no target has ever produced still fails at run time with an
error naming the lists that were available (or that none were), which is
`--dry-run`'s way of saying "this setup needs a source this plugin cannot yet
resolve."

**Where `layers` comes from, and why not a new target kind.** A target kind is
chosen by parsing pasted input, and there is no pasted shape that means "a
stack" — you paste a pull request URL, and that parses as `github_pr` no
matter how many other pull requests are stacked on it. So rather than add an
`applies_to: [github_stack]` kind nobody can name, any `github_pr` target
resolves its own `layers`: `gh pr list` fetched once, then walked by
`baseRefName`/`headRefName` to reconstruct the chain (bottom-first — the layer
based on the trunk is `layer: "1"`). A standalone pull request, based directly
on the trunk with nothing built on top, resolves to a **one-element** list,
not an error, so a setup does not need to special-case it. Only the list
names a setup's own `for_each` tabs actually mention are ever resolved, so a
setup with no `for_each` pays nothing extra, and even several tabs repeating
over `layers` cost exactly one `gh pr list` call. (A `github_stack` target
kind, mainly useful so the picker could show a stack as one row, is separate
and unbuilt, tracked as issue #14.)

Each layer's fields, all strings: `layer` (1-based), `pr`, `title`, `url`,
`head_branch`, `head_sha`, `base_branch`, `base_pr` (the PR number immediately
below this layer, empty for the bottom layer — it bases on the trunk, not on
another open PR).

A stack GitHub's own stacking tool created is read directly from GitHub's
stack API in a single query; any other stack (plain git, rebase-based
tooling, another editor) is reconstructed from `baseRefName` by the walk
above. Both resolve to the identical per-layer fields, so which one answered
is only visible if it misbehaves.

**A `for_each` tab can give every layer its own checkout.** Put `worktree:`
on the repeated tab and let its `ref` vary per element:

```yaml
tabs:
  - for_each: layers
    as: layer
    name: "L{{.layer_index}} #{{.layer_pr}}"
    worktree: {ref: "{{.layer_head_sha}}"}
    panes:
      - agent: claude
        submit: true
        prompt: "Review #{{.layer_pr}} — {{.layer_title}}"
```

That is what replaces a bootstrap script that built one worktree per layer.
Two rules are enforced at load, because both are always mistakes and both
would otherwise cost real disk before anyone noticed:

- **A `ref` that does not name the element.** A constant ref builds the same
  worktree once per element, which means the element was never used. Checked
  against the unrendered template by asking whether it mentions `{{.<as>_…}}`
  — deliberately loose, so it errs toward accepting.
- **`detach: false` inside a `for_each` tab.** A branch cannot be checked out
  in two worktrees at once, so every element after the first fails in git.
  Outside a `for_each` it stays perfectly legitimate.

Each element's own fields render **flat**, prefixed with `as`: `{{.layer_pr}}`,
never `{{.layer.pr}}`. That is deliberate, not an oversight — the same plain
`map[string]string` backs every template in this file, and nesting would need
a second, richer value type just for this one feature. `<as>_index` is also
set, 1-based (`{{.layer_index}}`), alongside the explicit `layer` field
above — both exist on purpose, rather than making a setup reach for one and
hope it means the other. An element field actually named `index` wins over
the bookkeeping one, since the element's own data should never be shadowed by
bookkeeping this feature added.

An empty list is not an error — it builds **zero** tabs for that entry and
moves on, which matters if a `for_each` is the first tab in the file: the next
tab correctly becomes the one that reuses the Space's own tab. A `for_each`
naming a list the target never provided **is** an error, and it is checked
before any pane in the whole setup is built, not partway through.

`focus: true` inside a `for_each` tab is rejected at load time: every
repetition would set it, and only the last one built would silently win.

This is the one loop this file format has, and deliberately the only one — no
`when:`, no conditionals, no nesting one `for_each` inside another. Anything
that needs real logic belongs in a `command:` pane instead of in YAML.

### worktree: pinning a tab to its own ref

A tab can be checked out at a ref of its own, rather than sharing the Space's
one worktree:

```yaml
tabs:
  - name: baseline
    worktree: { ref: "main" }        # detached by default
    panes: [{ command: "npm test" }]

  - name: work                       # no worktree: -- uses the Space's own
    panes: [{ agent: claude }]       # cwd, exactly as before
```

This is for comparing two versions side by side, pinning a test runner to a
known-good commit while another tab keeps moving, or reviewing a tag while
`HEAD` moves under it — none of which need `for_each` at all. (A `for_each`
tab whose `ref` varies per element — one worktree per stacked PR layer — is a
separate, not-yet-built interaction; see the note at the end of this
section.)

`ref` is a template, rendered against the same data as `cwd`: `{{.Branch}}`,
`{{.Number}}`, and inside a `for_each` tab, `{{.layer_head_sha}}` and the rest
of that element's fields.

**Detached by default.** A branch cannot be checked out in two worktrees at
once, and it moves under you the moment someone pushes to it mid-review --
neither is a problem for a detached `HEAD`, which just is whatever commit
`ref` named. `detach: false` opts into a branch checkout instead, for the
single-tab case where the point is to commit on it.

**`cwd:` and `worktree:` together is rejected.** They are two answers to
"where does this tab live," not a precedence rule to resolve quietly.
`worktree:` with a blank `ref` is rejected too -- there is nothing to check
out.

**Where it lives on disk.** The same `[worktrees].path` template that already
places a whole Space's worktree now also takes a `{ref}` placeholder,
sanitized the same way `{branch}` is. Left unconfigured, a tab's own worktree
defaults to `{repo_root}/.herdr-worktrees/{ref}` -- keyed on the repo root and
the ref **alone**, deliberately: no setup name, no run id, no timestamp, so
re-running the same setup against the same ref reuses the worktree that is
already there rather than growing a new one next to it.

**The collision rule**, if something is already at that path:

- missing → it is created.
- present, and already checked out at the commit `ref` names → reused, silently.
- present, but checked out somewhere **else** → that tab is reported as failed
  and skipped. **Nothing here ever force-removes it.** The error names the
  exact command to run by hand if you mean to replace it:

  ```
  git worktree remove --force <path>
  ```

  This is deliberate, not a missing feature: automatically forcing it would
  be "occasionally delete something someone was using," which is worse than a
  setup that accumulates predictably and asks before clearing anything.

**`--dry-run` never creates anything.** It prints the deterministic path (the
same one a real run would use, since it needs no worktree to exist to be
computed) and the rendered `ref`, and says plainly that nothing has been
built yet.

**Not built yet: per-layer worktrees for a `for_each` tab.** A constant `ref`
on a `for_each` tab (every element building an identical worktree) and
`detach: false` on one (every element fighting over the same branch checkout)
are both validation rules this version does not enforce, since neither one
means anything without `for_each` actually varying per element -- everything
else here (the schema, the naming, the collision rule) is written so that
piece can land without reshaping this one.

### Templates

Prompts, commands, env values, a tab's `name`, a pane's `label` and every
`cwd` are Go `text/template`, rendered against the target:

`{{.URL}} {{.Host}} {{.Owner}} {{.Repo}} {{.Number}} {{.Title}} {{.Issue}}
{{.Slug}} {{.Branch}} {{.Text}} {{.Path}}`

— plus, inside a `for_each` tab, every `<as>_<key>` field for that element and
`<as>_index`.

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
  Labels are applied in this same first pass, so every `HERDR_PANE_<NAME>`
  variable a `command:` pane receives names a pane that already has its
  label — see "A command pane's Herdr identity" above.
- `wait_for` holds the rest of the layout until that pane's output matches. Use
  it when a later pane's prompt depends on an earlier pane having started. A
  timeout is not fatal — it warns and carries on. A pane whose work never
  started is not waited on at all.
- A pane that fails is reported and **skipped**, not fatal: the panes after it
  still get built and filled. Only a tab that could not be created takes its
  own panes with it, since their splits would land in the tab before it.

## Using one

```sh
herdr-phin-util setups                                 # what loads, and from where
herdr-phin-util setups --repo ~/src/github.com/o/r     # as if that were the row
herdr-phin-util open <url> --setup pr-review --dry-run # resolve, print, touch nothing
herdr-phin-util open <url> --setup pr-review
herdr-phin-util project ~/src/o/r --setup dev
```

In the picker (`herdr-phin-util pick`), `tab` on a worktree, branch or link row
opens the setups that apply to it, and `ctrl+t` does so from any row; `esc` or
`shift+tab` comes back. Enter without either is unchanged.

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
agent; `model` and `args` need an agent; an agent and a command cannot share a
pane; only one pane may be `focus`; `focus: true` is rejected inside a
`for_each` tab outright; `as:` requires a `for_each:` on the same tab; a
`for_each:` with nothing after it (once trimmed) is rejected rather than
silently treated as "no for_each".

**The agent rejected a flag.** `model` and `args` are passed through
untouched, so an unknown model name or a flag that kind does not have is that
agent's own error, visible in the pane. `--dry-run` prints the exact argv under
`args`.

**The layout builds but a prompt is blank.** A placeholder is misspelled, or
the field is empty for that target kind. `--dry-run` shows it.

**A pane came up bare.** Read the `warning:` lines the run printed — one per
failed step, naming the tab and the cause — and look for a pane renamed
`failed: <label>` in the Space. `agent_not_ready` from `agent.prompt` after the
agent visibly started usually means that agent is sitting on something modal
(codex's update prompt does this); the prompt is settled and retried once
before it is reported.

**The panes are on the wrong branch.** A warning says so: `worktree.create`
failed and the Space fell back to something else. If it fell back to the source
checkout, the whole layout is looking at that checkout's branch — fix the
worktree (usually one already exists for that branch) rather than the file.

**A `worktree:` tab is missing and a problem names a path.** Something is
already at that tab's deterministic path, checked out at a different commit
than `ref` asked for — the collision rule refuses to touch it rather than
force-remove and recreate. The problem line names the exact command to run by
hand if you mean to replace it: `git worktree remove --force <path>`.

**A command sits at the prompt unrun.** That is what the plugin's own pacing
exists to prevent; if it recurs, the shell is unusually slow to draw its
prompt. Nothing in the file fixes it.
