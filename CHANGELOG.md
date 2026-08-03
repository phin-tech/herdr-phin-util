# Changelog

What changed in each released version. The manifest's `version` is what
`herdr plugin list` reports, and a `v*` tag is what cuts a permanent release,
so the two always name the same thing.

Dates are the day the version was cut.

## 0.10.0 — 2026-08-03

A stack GitHub knows about is read from GitHub; everything else is still
reconstructed by hand.

- `internal/gh` gained `Stacks`, enumerating every path from the bottom of a
  chain to a tip rather than refusing on a fork the way `Stack` always has —
  a prerequisite for #14's picker, which needs one row per path rather than
  one answer. `Stack` is now a thin wrapper: exactly one path is returned
  as-is, more than one still produces the same fork error as before, wording
  unchanged.
- Both now try GitHub's own `PullRequestStack` API first — one query,
  authoritative, forkless by construction — before falling back to the
  `baseRefName` walk that has done this since #13. That fast path is
  narrower than it sounds: `pr.stack` came back **null** against the live
  API for a real stack built with plain git (verified against this repo's
  own #16), so it only ever answers for a stack GitHub's own stacking tool
  created. Any failure — the field is null, the command errors, the JSON is
  not what was expected — falls through to the walk silently; nothing about
  this is allowed to be fatal, since a user on an older `gh` must see no
  behaviour change at all.
- The cost of that, stated plainly rather than buried: a stack built with
  plain git now pays one extra `gh api graphql` round trip before the walk
  it was always going to do, every time a `for_each: layers` setup resolves.
  That is the whole price of the fast path for anyone not using GitHub's own
  stacking, and it buys them nothing today. It is worth it only on the bet
  that native stacks become the common case; if they do not, the honest
  thing later is to delete the fast path rather than keep paying for it.

## 0.9.0 — 2026-08-03

A tab can pin itself to a git ref of its own ([#12]).

0.8.0 gave `for_each` a source but explicitly no per-layer checkout, because
that needed a tab that could pin itself to a ref, which was a separate,
larger piece of work. This is that piece, for the plain case: `worktree:` on
an ordinary tab, no `for_each` in sight. It is more general than the stack
that motivated it — comparing two versions side by side, pinning a test
runner to a known-good commit while another tab keeps moving, reviewing a
tag while `HEAD` moves under it are all the same feature, and none of them
need a loop.

- Every hard decision here is architectural rather than YAML surface, which
  is why it shipped on its own before the `for_each` interaction: **Herdr's
  own worktree API cannot do this, at all.** `herdr worktree create` takes no
  `--detach` and always checks out a *named branch* it makes itself
  (`WorktreeRequest.Branch` is documented as "the name of the branch that
  gets made"), and every one of its calls is Space-scoped — `CreateWorktree`
  returns a new Workspace, `worktree.remove` takes a workspace id, never a
  bare path. A tab's worktree is a directory a tab points its `cwd` at, not a
  Space, so there was never a Herdr call to route this through. It goes
  through git directly instead.
- Which means `internal/gitcmd` writes to disk for the first time.
  `WorktreeAdd`/`WorktreeAddBranch` run `git worktree add`; the package's own
  doc comment used to say nothing here touches the working tree, and that
  line is gone now rather than left to quietly go stale. `FetchRef` is
  `FetchBranch`'s counterpart for an arbitrary ref (GitHub allows fetching a
  bare SHA, and `git fetch origin <branch>` is not that call), and tolerates
  a commit already present locally without erroring — the common case, and
  git itself refuses that fetch rather than treating it as a no-op.
  `WorktreeRemove` is wired for tests and a future cleanup story, but nothing
  calls it automatically; see the collision rule below for why.
- Detached by default: a branch can't be checked out in two worktrees at
  once and moves under you the moment someone pushes to it mid-review,
  neither of which is a problem for a detached `HEAD`. `detach: false` opts
  into a branch checkout instead, safe here because without `for_each` there
  is only one tab to collide with itself.
- The naming scheme extends `[worktrees].path` rather than inventing a
  second notion of a plugin-managed directory: a `{ref}` placeholder,
  sanitized the same way `{branch}` already is. Left unconfigured, a tab's
  own worktree defaults to `{repo_root}/.herdr-worktrees/{ref}` — keyed on
  repo root and ref **alone**, no setup name, no run id, no timestamp, which
  is what makes a re-run reuse rather than accumulate. That preference was
  explicit in the issue: "I would rather it accumulate predictably than get
  cleverly cleaned up and occasionally delete something someone was using."
- The collision rule that sentence produced, confirmed during review rather
  than guessed at: path missing → create it; path present and already
  checked out at the ref's commit → reuse it, no-op; present and checked out
  at a *different* commit → report that tab as failed and skip it. **Nothing
  here ever runs `git worktree remove --force`.** The error names the exact
  command to run by hand instead. Auto-forcing was precisely the thing the
  issue ruled out, and the code says so at the point a future edit would be
  tempted to add it back.
- `ref` is a template, rendered in the same per-iteration pass `cwd` already
  is — not because this version needs it to vary per element, but so a later
  `for_each` tab's differing `ref` costs nothing extra to wire up when it
  lands. A `Step` carries the rendered ref and detach flag in a new field
  rather than folding them into `Cwd`: `Cwd` stays the plain, deterministic,
  computable-before-creation path every other reader (`buildPanes`,
  `fillPanes`, `--dry-run`) already assumes it is, which the naming scheme
  above makes true without any special-casing.
- A worktree has to exist before Herdr's `tab.create` is called for it —
  there is no "cd into this afterward" call — so `applySetup` gained a third
  pass, between resolving the plan and building any pane, that creates every
  tab's own worktree first. A worktree that fails to build marks only its
  own tab abandoned, the same contract a failed pane already had: reported,
  skipped, the rest of the layout still built. The one tab this needed
  careful wording for is the Space's own first tab — there is no "reuse the
  Space's own tab" fallback to lose there, since that tab and its root pane
  exist regardless; it just lands in the Space's own directory rather than
  the pinned one, and says so.
- `--dry-run` never runs this pass. It prints the same deterministic path a
  real run would use, plus the ref, and says plainly that nothing has been
  created yet — consistent with how a whole-Space worktree preview already
  reads when its path isn't knowable at all; this one differs only in that
  the path *is* knowable, and still isn't there.
- `cwd:` and `worktree:` on the same tab is rejected: two answers to "where
  does this tab live," not a precedence rule to resolve quietly. So is
  `worktree:` with a blank `ref`.
- Deliberately **not** included: the two validation rules that only mean
  anything once `ref` can vary per element — a constant `ref` on a
  `for_each` tab (every element would build an identical worktree, always a
  mistake) and `detach: false` on one (every element would fight over the
  same branch checkout). Neither is implemented, on purpose; the schema and
  the resolution pass are written so that piece can land without reshaping
  this one.

## 0.8.0 — 2026-08-03

`for_each` gets a source ([#13]).

0.7.0 shipped `for_each` with nothing that could populate the lists it reads,
which was a known and stated gap -- every setup that used it failed with
"this target provides no lists", by design, until something filled `Lists`
in. This is that something: a `github_pr` target now resolves a `layers`
list, the chain of open pull requests it belongs to, bottom of the stack
first, so a stacked-PR review layout can finally be written and run rather
than only validated.

- `layers` comes from a single `gh pr list --state open`, reconstructed by
  hand rather than asked of `gh` directly -- `gh stack view` answers "not
  part of a stack" for any branch that was not created through its own
  tracking, which is most stacks reviewed here. The walk goes both
  directions from the target PR: down toward the trunk by `baseRefName`, up
  by whichever open PR bases on the current one's head. Walking only one
  direction was the original version of this bug -- a stack's bottom layer
  bases on the trunk, so "base != trunk" alone reads a five-layer stack as
  standalone the moment you ask about its bottom PR.
- A pull request in no stack resolves to a one-element list, not an error --
  most pull requests reviewed day to day are exactly this, and a setup
  should not have to special-case it. A malformed cycle is caught by a
  visited set rather than hung on. Two open pull requests sharing a base
  make the walk ambiguous -- the open PRs are a tree at that point, not a
  chain -- and rather than silently pick a branch, that is refused with an
  error naming every path to a tip, so a person can retry against the
  number they actually meant.
- Resolution is lazy, driven by a new `Setup.ForEachNames()`: only the list
  names a chosen setup's own `for_each` tabs mention are ever resolved, so a
  setup that never uses `for_each` -- most of them -- triggers no `gh` call
  at all, and a setup naming a list nobody produces still gets the existing
  "provides no lists" error. The same check is built to take a second list
  source later behind it, one more clause alongside this one.
- Per layer, all strings: `layer` (1-based, alongside the `layer_index`
  every `for_each` element already gets for free), `pr`, `title`, `url`,
  `head_branch`, `head_sha`, `base_branch`, and `base_pr` -- the PR number
  immediately below this layer, empty for the bottom one, since it bases on
  the trunk rather than on another open PR.
- Deliberately **not** a new `github_stack` target kind, which is what #7
  originally sketched (`applies_to: [github_stack]`). A target kind is
  chosen by parsing pasted input, and there is no shape that means "a
  stack" -- you paste a pull request URL, and that parses as `github_pr`
  regardless of how many other pull requests are stacked on it. So any
  `github_pr` target resolves its own `layers` instead, and a setup asks
  for the chain with `applies_to: [github_pr]` and `for_each: layers` --
  which means #7's own YAML does not run verbatim; `applies_to` has to
  name `github_pr`, not `github_stack`. A `github_stack` kind, mainly
  useful so the picker could show a stack as one row, is tracked
  separately as [#14] and not built here.
- Deliberately **no** per-layer `worktree` field either. Every layer still
  shares the Space's one `cwd`, same as any non-repeated tab reviewing a
  single PR -- giving each layer its own checkout needs a tab that can pin
  itself to a ref, which is a separate and larger feature tracked as [#12].
  This alone makes `for_each` usable; it does not by itself replace a
  bootstrap script that builds one worktree per layer, only shrinks what
  that script would still need to do.

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
[#12]: https://github.com/phin-tech/herdr-phin-util/issues/12
[#13]: https://github.com/phin-tech/herdr-phin-util/issues/13
[#14]: https://github.com/phin-tech/herdr-phin-util/issues/14
