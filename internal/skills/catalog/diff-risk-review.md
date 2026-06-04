# Diff Risk Review

Review the current git diff for correctness, security, and performance risk
before it is committed — a focused second pass over only what changed.

## Workflow

1. **Get the change** — `git diff` and `git diff --staged` for uncommitted work,
   or `git diff <base>...HEAD` for a branch. Review only the changed hunks.
2. **Inspect each hunk** against three lenses:
   - **Correctness** — off-by-one, inverted conditions, wrong operator, null
     deref on newly accessed fields, a changed signature with un-updated callers,
     missing `await` / unhandled rejection / race on shared state.
   - **Security** — user input reaching SQL, shell, `eval`, file paths, or HTML
     without sanitization; a new route/handler missing an ownership or permission
     check (BOLA); logging that now emits tokens or PII.
   - **Performance** — a new N+1 query, unbounded allocation on user-controlled
     size, or work moved onto a hot path.
3. **Rank** — BLOCKER (breaks or is exploitable) · WARNING (likely bug/risk) ·
   NIT (clarity). Cite `file:line` for every finding.
4. **Report** — findings worst-first, each with the concrete fix. If a hunk is
   clean, say so rather than padding.

Only report findings you can justify from the diff. A review that flags
everything trains the reader to ignore it.
