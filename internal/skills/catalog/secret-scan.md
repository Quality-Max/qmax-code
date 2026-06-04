# Secret Scan

Scan the working tree or git diff for hardcoded secrets — API keys, tokens,
private keys, connection strings, cloud credentials — before they reach history.

## Workflow

1. **Scope** — default to `git diff` + `git diff --staged` (what's about to land);
   on request, scan all tracked files (skip `.git`, `node_modules`, lockfiles,
   build output, binaries).
2. **Match** the high-signal signatures: AWS keys (`AKIA…`, 40-char secrets),
   Google `AIza…`, GitHub `ghp_`/`gho_`/`github_pat_`, Slack `xox[baprs]-`,
   Stripe `sk_live_`, OpenAI/Anthropic `sk-`/`sk-ant-`, `-----BEGIN … PRIVATE
   KEY-----`, JWTs, `postgres|mysql|mongodb|redis://user:pass@…` URLs, and generic
   `(password|secret|token|api_key) = "…"` assignments.
3. **Triage** each hit:
   - **Real secret** → BLOCKER. Recommend remove, **rotate** (it's compromised
     once committed), and move to env / secret store.
   - **Placeholder** (`your-key-here`, `xxxx`, `${…}`, obvious fixture) → note as
     ignored.
   - Already committed → flag that rotation is required regardless of removal.
4. **Report** with `file:line` and the value **masked to the first 4 chars** —
   never echo a full live secret.

Precision matters more than recall here: mask aggressively and don't dump
secrets into the report you're trying to protect.
