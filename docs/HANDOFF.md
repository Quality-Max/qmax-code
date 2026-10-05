# Rejected approaches across backends

The shared transcript preserves exposed conversation and tool activity.
`/handoff` adds a small checkpoint of explicit user decisions so a ruled-out
approach stays visible even when that transcript is long or compacted.

```text
/handoff reject Increase the reconnect timeout | TestReconnect fails at both 5s and 60s; the connection is never reopened
/orch
continue the fix
```

Use `/handoff` to inspect the numbered list and `/handoff forget 1` to remove
an outdated rejection. Entry numbers reflect the current list, so inspect it
again after removing an entry. Both the rejection and its removal appear in
the transcript. The current checkpoint supersedes older handoff notes.
`/clear` resets the checkpoint along with the conversation.

The checkpoint is sent before the historical conversation in CLI handoffs
and included in built-in system prompts. Native CLI resumes receive it even
when they have already seen all transcript entries. `/save` and `/resume`
preserve it across restarts; automatic persistence follows the existing
auto-save setting. Notes use retained-transcript redaction, with a total
12 KiB budget that includes the checkpoint instructions. Oversized additions
are rejected without changing existing notes. Recording a note makes no
additional model request. Input recall stores `/handoff` instead of the raw
rejection command, so pasted evidence is inspected through the redacted list.

A failed command does not automatically rule out an approach: a failure can
also mean an unavailable dependency, a flaky test, or incomplete work. The
user supplies the decision and evidence. qmax-code identifies that evidence
as data and instructs the destination to check the list before acting,
explain new evidence before revisiting a rejection, and respect an explicit
user request to revisit it. This is guidance, not a command execution gate.
Hidden reasoning and provider-private state still cannot be transferred.

## Check behavior with real backends

The automated regression tests verify delivery of the full decision and
evidence across failure, compaction, restart, and native resume. They use
test doubles and cannot establish whether a model follows the checkpoint.

To evaluate that separately, use a reproducible bug with a demonstrated
unsuccessful approach, such as increasing a timeout when the connection is
never reopened:

1. Let backend A try the approach and run the failing test. Keep the test
   output and resulting repository state as the starting point for both runs.
2. In the checkpoint run, record the approach and its evidence with
   `/handoff reject`. In the control run, retain the ordinary transcript
   without adding a checkpoint.
3. Switch to backend B and use the same request: `continue the fix`. Avoid
   repeating the rejected approach in the request itself.
4. Inspect B's first ten tool calls and final outcome. Record whether it
   repeated the rejected change, cited the evidence, justified a revisit
   with new evidence, and passed the regression test.
5. Repeat from the same repository state with reversed backend order and
   multiple fresh sessions. Record backend/model versions and effort levels.

Running the existing regression test to establish a baseline is not itself
a repeated approach. Count a repeated change or attempted remedy that the
checkpoint ruled out. Distinguish justified revisits from unexplained retries,
and report results separately from whether the bug was eventually fixed.

Do not claim a measured reduction in repeated approaches until those live
runs have been completed. Automatic decision extraction would need its own
evaluation for incorrectly ruling out viable approaches.
