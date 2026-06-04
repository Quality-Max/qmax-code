# QA Triage

Triage a failing test run or an incoming bug report into an actionable
diagnosis: real defect vs. flaky test vs. environment issue, with the next
concrete step.

## Workflow

1. **Reproduce** — re-run the failing test(s) through qmax. A failure that does
   not reproduce on a clean run is a flake candidate, not a defect.
2. **Localize** — bisect to the smallest failing assertion. Capture the actual
   vs. expected and the stack/trace.
3. **Classify**:
   - **Defect** — deterministic failure tied to a code path. Point at the
     file:line and the regression that introduced it.
   - **Flake** — passes on retry / depends on timing, order, or shared state.
     Identify the source of nondeterminism.
   - **Environment** — fails due to config, data, or infra, not the code under
     test.
4. **Recommend** — the single highest-value next action (fix, quarantine,
   stabilize, or escalate) with the rationale.

Do not guess the category — gather the evidence (a rerun, a diff, a log) that
distinguishes flake from defect before labeling.
