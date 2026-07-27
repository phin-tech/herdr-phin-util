# Changelog

What changed in each released version. The manifest's `version` is what
`herdr plugin list` reports, and a `v*` tag is what cuts a permanent release,
so the two always name the same thing.

Dates are the day the version was cut.

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
