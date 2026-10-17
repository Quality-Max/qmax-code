# Handoff evaluation

Measures whether backend B repeats an approach that backend A already ruled
out, with and without a `/handoff` checkpoint. This is the live experiment
described in [docs/HANDOFF.md](../../docs/HANDOFF.md#check-behavior-with-real-backends).
The harness is a development tool inside the module; it is not part of the
`qmax-code` binary.

```bash
go run ./eval/handoff selfcheck                     # fixture sanity, no backends
go run ./eval/handoff run --backends cc,codex       # print the plan only
go run ./eval/handoff run --backends cc,codex --yes # execute it (uses quota)
go run ./eval/handoff summarize                     # rebuild summary.md
```

## What one run does

1. Copies the fixture (`testdata/fixture`) into a fresh git repository under
   `<out>/runs/<run-id>/repo`. It is a small Go client whose `Send` retries
   until `ReconnectTimeout` (200ms) but reuses a dead connection instead of
   redialing. The failure message points at the timeout; raising it only
   makes the test wait longer.
2. Seeds the shared transcript with a scripted phase A: the failing test,
   A's decision to try a longer timeout, the edit to 2s, the still-failing
   test (real output captured once into `<out>/seed.json`), the revert, and
   A's conclusion that it did not help. Scripting A keeps history and
   repository state identical across arms and repeats.
3. Optionally adds `--pad` unrelated exchanges so the handoff has to
   abbreviate history (see below).
4. **checkpoint** arm: records `/handoff reject Increase ReconnectTimeout |
   <evidence>`. **control** arm: no checkpoint. Nothing else differs.
5. Runs one backend-B turn through the same `RunCLI` path `/orch` uses, with
   the request `continue the fix` from inside the repository.
6. Samples `reconnect/timeout.go` every 25ms during the turn, then restores
   the visible test, adds hidden tests (`testdata/hidden`), and runs them.

## Measures (`result.json`)

| field | meaning |
|---|---|
| `repeated` | Primary. `ReconnectTimeout` left `200 * time.Millisecond` at any sampled point, including edits B reverted later. |
| `timeout_values_seen`, `first_timeout_change_sec` | Every distinct value observed, and when it first changed. |
| `timeout_like_diff` | Review flag: added lines mention timeouts, deadlines, or second-scale durations (lengthening the wait without touching the constant). |
| `fixed` | Visible and hidden tests pass with the original test file restored. |
| `test_file_modified` | B edited the visible test (restored before scoring). |
| `handoff_mode` | What B received: `inline` (whole history), `abbreviated` (bodies shortened, archive file offered), `omitted` (oldest entries dropped; phase A only in the archive). |
| `phase_a_inline`, `checkpoint_inline` | Whether A's conclusion and the checkpoint appeared in B's prompt. |
| `tool_activity`, `final_response` | B's first ten retained tool records and answer. |
| `cited_evidence`, `justified_revisit` | `null` until a reviewer sets them from `transcript.jsonl`. |

Each run directory also holds `handoff-prompt.txt` (the exact handoff, from a
zero-cost probe of the same transcript), `transcript.jsonl`, `diff.patch`,
and `output.log`.

## Padding

Measured on this fixture: up to roughly 150 pairs the abbreviated handoff
still carries A's short conclusion inline. From about 250 pairs, the oldest
entries are dropped and phase A survives only in the archive file. The
default `--pad 0,400` therefore compares a fully inline history with one
where only the checkpoint keeps the decision in the prompt.

## Running it

- Requires the selected CLIs on `PATH`, logged in. `run` refuses to start if
  one is missing.
- `--permission unattended` (default) lets cc, agy, and opencode edit and run
  commands without prompts; cc's `standard` mode cannot edit in print mode.
  Codex uses the sandbox and approval policy from its own config, so set
  `sandbox_mode = "workspace-write"` there or Codex cannot apply a fix.
- Agents work in throwaway repositories but are not sandboxed beyond what
  each CLI enforces. Run on a machine where that is acceptable.
- Claude Code and OpenCode spawn this harness binary as their qmax MCP
  server. Codex and Antigravity use the qmax entry already in their global
  config; the harness does not rewrite it.
- `--model backend=model` pins models (for example
  `--model cc=claude-opus-5-5,opencode=<provider/model>`); otherwise each
  CLI's default is used and recorded as `default`. Record CLI versions from
  `cli_version`.
- Runs are resumable: existing `result.json` files are skipped. Delete a run
  directory to redo it.
- Phase A is fixed, so "reversed backend order" from the procedure maps to
  running each backend as B.

## Reporting

`summarize` writes `summary.md` with per-cell counts and a checkpoint-vs-
control line for each backend and pad. Report raw counts with n per cell and
the recorded backend and model versions. One fixture and a few repeats can
show whether the checkpoint is used; they cannot support a general claim
about repeated approaches.
