# QA Quality Gate

Enforce a release quality gate before code merges or ships. Generates tests for
changed code, runs them, reviews the results, and produces a pass/fail verdict
with quality findings.

## Workflow

1. **Scope the diff** — determine the changed files and the surface area they
   affect (API, UI, data, infra).
2. **Generate coverage** — for each changed unit, generate or extend tests via
   the qmax generate tool. Cover happy path, boundaries, and the regression the
   change is most likely to introduce.
3. **Run** — execute the generated and existing relevant suites through qmax.
4. **Review** — evaluate failures, flake, and coverage gaps. Classify each
   finding by severity.
5. **Gate** — emit a verdict:
   - **PASS** — all required checks green, coverage threshold met.
   - **FAIL** — list blocking findings with the exact file:line and the failing
     assertion, plus the smallest fix that would unblock.

Always cite the concrete failing test and the changed line that caused it. Never
pass a gate on unverified assumptions — run the suite.
