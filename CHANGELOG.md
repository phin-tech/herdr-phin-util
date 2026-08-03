# Changelog

What changed in each released version. The manifest's `version` is what
`herdr plugin list` reports, and a `v*` tag is what cuts a permanent release,
so the two always name the same thing.

Dates are the day the version was cut.

## 0.7.0 — 2026-08-03

A setup can repeat a tab, a `command:` pane is told where it is, and `--help`
stops building a Space called `--help`.

- `open --help` and `project --help` built a real Space from the literal flag
  text. Both functions took `args[0]` as their input before looking at it, and
  their flag loops started at the second argument, so the flag was never seen.
  Both now print that subcommand's usage and exit 0, as do `setups --help` and
  `handoff --help`. The bare word `help` is deliberately still an input:
  `open`'s argument is arbitrary text, and a checkout can be named anything.
- `for_each:` repeats a tab once per element of a **named list**, which is the
  first thing a setup could not express at all -- `applies_to` renders against
  exactly one target, so a layout whose tab count is unknown until discovery
  runs had to be a script that gave up `--dry-run`, validation and step
  reporting to get it.
- The list is a name resolved outside the template layer, never a template
  expression: values stay strings all the way down, so `Render` and
  `missingkey=zero` are untouched. Elements render flat, as `{{.layer_pr}}`
  rather than `{{.layer.pr}}` -- uglier, and a far smaller blast radius than
  the `map[string]any` nesting would need. `{{.<as>_index}}` is 1-based.
- A missing list fails before a single pane exists; an empty one builds zero
  tabs and moves on. `focus: true` inside a repeated tab is rejected, since
  every repetition would set it and only the last built would quietly win.
- No target kind produces a list yet, so every `for_each` currently fails
  naming what was available. The shape, the validation and the errors are what
  landed; the discovery that fills it is its own piece of work.
- This is the one loop, deliberately. A second control-flow feature would make
  this a programming language written in YAML, and that is the signal to add a
  real language as a second front end instead. `internal/setup`'s package
  comment records what that would need, and a test pins it.
- Tab names, pane labels and every `cwd` now render as templates too -- they
  had to, for a repeated tab to differ from itself.
- A `command:` pane is typed with `HERDR_WORKSPACE_ID`, `HERDR_TAB_ID`,
  `HERDR_PANE_ID` and one `HERDR_PANE_<LABEL>` per labelled pane in the setup,
  so a script no longer polls `pane list` to find itself or the agent pane the
  file declared beside it. The label stops being an undeclared contract that
  fails silently when renamed.
- These ride in as a shell assignment prefix rather than through `env:`,
  because they have to: Herdr sets a pane's environment at creation, and a
  pane's own id does not exist until it has been created. The prefix scopes to
  one simple command -- `cd x && ./script` does not carry them past `cd` --
  which is documented rather than papered over, since `export` is spelled
  differently in fish than in POSIX shells.
- `--dry-run` names which of those variables a command pane will get, and
  never invents the ids, which do not exist until something is built.

## 0.6.0 — 2026-07-29

A Linear ticket finds its own repository, and the checkouts are offered to the
jump launcher.

- A Linear issue names no repository, and the plugin used to cut its worktree
  in whatever directory the popup was fired from. A ticket for one project
  pasted while standing in another built the branch in the wrong one.
- Pasting a ticket now offers to take it rather than open it. Enter puts it in
  hand, the project list answers "which repository", and the level below --
  already a list of that repository's refs -- answers "from what". The branch
  itself is never asked for: it comes from the URL slug, so the rows below are
  bases rather than branches.
- Taking the ticket is an explicit enter or tab. A URL parses as a valid
  ticket several characters before it is finished, so acting on sight
  swallowed the tail of a paste and read it back as a different reference.
- While a ticket is held, enter on a project row means "this is the
  repository" rather than "open this", rows with no directory behind them are
  not offered, and esc gives the ticket up before the popup.
- The prompt and the setup see the ticket, not the checkout: `{{.Issue}}` and
  `{{.URL}}` render, and a setup with `applies_to: [linear]` can now also be
  scoped by `repos:` -- impossible before, when nothing knew the repository.
- `jump-rows` prints the checkouts as rows for the herdr-phin-jump launcher,
  registered through `herdr-jump.toml`. Neither plugin depends on the other:
  Herdr ignores the file, and jump finds it through the plugin root that
  `plugin.list` already reports.
- The CLI's own `open <linear-url>` is unchanged -- inside a repository, the
  working directory is a fair reading of where the work goes.

## 0.5.1 — 2026-07-28

The picker's filter now ranks what it matched.

- Typing a project's name left it eighth of thirteen rows, under things it had
  nothing to do with. The match ran over the label and the path as one string,
  and every row's path contains `~/src/github.com/...` -- so a four-letter
  subsequence like `orca` was nearly free and barely narrowed anything.
- Matching is label-first. The path is a fallback that only survives when
  nothing matched by label, so filtering by where something lives still works
  without every loose path match riding along on a query that named a label.
- Matches sort exact, then label prefix, then substring, then subsequence.
  Within a tier rows keep the order they had, and an empty query is left
  exactly as it was -- open Spaces before checkouts.

## 0.5.0 — 2026-07-27

The popup says what it is doing while it does it.

- Opening a Space is a clone, a worktree and several agents drawing their
  inputs. The popup said `working...` for all of it, which looks exactly like a
  popup that has hung.
- Both popups now draw a checklist for the duration, replacing the form they
  were sitting behind: `[ ]` for the step in flight, `[✓]` once it lands,
  elapsed against each and a running total. Steps appear as they start, since
  the list is not knowable up front -- a repo already on disk is not cloned,
  and a setup's panes come from the file.
- A step that fails keeps its line, marked `[x]` with the reason under it, and
  the checklist stays on screen beside the error rather than disappearing with
  it. How far a failed run got is most of the diagnosis.
- Reported steps: cloning, the PR or issue lookup, the branch fetch, the
  worktree (and its retry on a different base), building the panes, each pane's
  agent or command by the name the setup file gave it, each `wait_for`, and the
  prompt.
- Nothing about a run without a popup changed: reporting is a nil-able
  callback, so every CLI path takes exactly the code it did before.

## 0.4.3 — 2026-07-27

codex is no longer prompted into its first-run screen ([#6]).

- On a first run in a fresh worktree, codex draws an update nag and a "do you
  trust this directory" prompt before it draws an input. The prompt went out
  into those screens, `agent.prompt` answered ok because delivery is not
  verified, and the pane sat there empty with no warning.
- Measured against a live codex: `launch_pending` clears about three seconds
  after start, on process detection, with the nag menu still up -- and Herdr's
  `interactive_ready` reports **true** on both gate screens, so gating on it
  instead would not have helped. Both screens are also full of `›`, which is
  codex's menu cursor as much as its input caret.
- What does distinguish them is the footer codex draws under its input
  (`<model> · <cwd>`), absent from both gate screens and drawn within a second
  of the input in every run. codex now waits for that before it is prompted,
  the way claude already waits for `❯`.
- A codex that never reaches its input now costs its step loudly -- reported,
  and the pane labelled `failed:` -- instead of swallowing the prompt.

## 0.4.2 — 2026-07-27

A name another Space already took no longer leaves a dead pane ([#5]).

- Agent names are global to Herdr, not scoped to a Space, and they are derived
  from the pane's label and its position -- so a second concurrent run of a
  setup asks for the names the first one is still holding. `agent.start`
  refused with `agent_name_taken` and that pane stayed a bare shell. The rest
  of the run was fine, which made it easy to miss.
- `agent.start` now retries a taken name qualified by the Space it is in:
  `codex-reviewer-3` becomes `codex-reviewer-3-w14`, the same way a pane that
  is not ready yet is retried through `agent_pane_busy`. The Space id is used
  rather than a hash because it stays short and greppable -- the name is still
  something you can read off a pane and match to a window.
- Only the internal agent name takes the suffix, and only on a real collision.
  The pane label is what a person reads, and it was never the thing that
  collided.
- The single-agent path gets the same recovery: the same pull request opened
  twice derives the same name from the same label.

## 0.4.1 — 2026-07-27

`submit: true` waits for the agent to be promptable ([#4]).

- A submitted prompt was being sent the moment the agent drew its input, which
  is not the moment Herdr will accept one: `agent.start` leaves the agent
  `launch_pending`, and `agent.prompt` rejects a pending agent with
  `agent_not_ready` no matter what the pane shows. Neither check the setup ran
  saw it -- `agent.wait` answers "idle" for an agent that has not really
  started, since an agent doing nothing yet looks exactly like one that is
  done. On a four-pane run this dropped both reviewer prompts.
- Setups now poll `agent.list` for the flag Herdr actually gates on, and send
  the prompt once it clears. A retry that fails on readiness re-waits rather
  than backing off and guessing again.
- An agent that never finishes launching -- typically one stuck on a first-run
  prompt of its own, a trust dialog or an upgrade nag -- is reported as
  "never finished launching" instead of as a bare rejection from the prompt.
  Only a `submit: true` pane fails over it; a typed prompt goes through
  `pane.send_text`, which needs no such thing, and still lands.

## 0.4.0 — 2026-07-27

Agent panes take a command line ([#1], [#2]).

- `model:` on an agent pane launches that agent with `--model <value>`, so a
  setup can put an Opus orchestrator beside cheaper workers instead of
  documenting "set your default to X" out of band.
- `args:` is the general form — a list, so nothing has to be shell-quoted,
  appended after the model flag and passed through verbatim. This is what
  makes a reviewer genuinely read-only: `--permission-mode plan` for claude,
  `--sandbox read-only` for codex. A prompt asking the agent not to edit
  anything is a request the diff under review can argue with.
- Both render as templates (`--add-dir {{.Path}}`), both are agent-pane only
  and rejected at load time on a `command:` pane, and neither is validated —
  a bad model name is the agent's own error to give, and an allowlist here
  would go stale.
- `--dry-run` prints the resolved argv.

## 0.3.2 — 2026-07-27

Setups no longer half-build a Space in silence ([#3]).

- A failed step is reported and skipped instead of ending the run. One agent
  that will not start is no longer a reason for the panes after it to be left
  as bare shells; only a tab that could not be created takes its own panes with
  it, since their splits would land in the tab before it.
- Every failure prints a `warning:` line naming the tab and the cause, the pane
  is renamed `failed: <label>` so the Space itself says which one it was, and
  the command exits non-zero with the Space still standing.
- A setup's tabs and splits are built in the Space's own directory rather than
  the checkout it was derived from. For a pull request those are different, and
  using the checkout put a whole review layout on the source branch while only
  the first pane sat in the worktree.
- `worktree.create` falling back to `worktree.open` is no longer silent. Reusing
  an existing worktree says so; landing on the source checkout — a different
  branch, and identical from the API — says so loudly.
- A pane's `wait_for` is skipped when its work never started, rather than
  spending the whole timeout waiting for output that cannot come.
- A prompt is settled briefly and retried once before it is reported as failed:
  `agent.prompt` can answer `agent_not_ready` after every readiness check has
  passed.

## 0.3.1 — 2026-07-27

- Setups: YAML recipes that build a whole Space — tabs, panes, agents, prompts
  and commands — from one resolved target, with `--dry-run` to print the plan
  without touching Herdr.
- The picker reaches a row's setups with `tab`, including from rows that have
  nothing below them.

## 0.3.0 — 2026-07-25

- `handoff`: resume a Claude session started outside Herdr in a Space of its
  own.

## 0.2.0 — 2026-07-24

- The project picker, and the worktree and branch level under it.
- Pasted links, clones and worktrees all reached from the one picker.

## 0.1.0 — 2026-07-22

- First cut: the plugin, and `promote` for moving a pane into its own Space.

[#1]: https://github.com/phin-tech/herdr-phin-util/issues/1
[#2]: https://github.com/phin-tech/herdr-phin-util/issues/2
[#3]: https://github.com/phin-tech/herdr-phin-util/issues/3
[#4]: https://github.com/phin-tech/herdr-phin-util/issues/4
[#5]: https://github.com/phin-tech/herdr-phin-util/issues/5
[#6]: https://github.com/phin-tech/herdr-phin-util/issues/6
