# SAST Pre-Surgery

Run a focused static-analysis security pass on the code about to be changed
("pre-surgery") so vulnerabilities are found before the edit lands, not after.

## Workflow

1. **Define the surgical site** — the files and functions the upcoming change
   will touch, plus their immediate callers and callees.
2. **Scan** — look for the high-signal SAST categories on that surface:
   injection (SQL/command/template), SSRF, path traversal, deserialization,
   auth/authorization gaps, secret handling, and unsafe crypto.
3. **Triage** — for each finding record severity, exploitability, and whether
   the planned change makes it better or worse.
4. **Pre-empt** — propose the secure pattern to use during the edit so the new
   code does not reintroduce the class of bug.
5. **Report** — a short list of must-fix items with file:line and a one-line
   remediation each. No false-positive padding.

Prefer precision over recall: a security report that cries wolf gets ignored.
Flag only findings you can justify with a concrete data-flow path.
