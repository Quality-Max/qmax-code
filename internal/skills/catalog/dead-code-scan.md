# Dead Code Scan

Find dead code in a repository — unused exports, unreferenced files, unreachable
branches, and unused imports — with a safe-to-remove confidence on each.

## Workflow

1. **Reach for a static analyzer** if present and parse it: `knip`/`ts-prune`
   (JS/TS), `vulture` (Python), `deadcode`/`staticcheck` (Go).
2. **Otherwise grep-and-reason**:
   - Exported symbol with zero references outside its own file (and not a public
     entry point) → candidate dead export.
   - Imported name never used in the file → unused import.
   - Module never imported anywhere and not a route/entry point → unreferenced
     file.
   - Statements after `return`/`throw`/`break`, or `if (false)` → unreachable.
3. **Assign confidence, conservatively.** Anything reachable via dynamic import,
   reflection, string-keyed dispatch, a public package export, or a framework
   convention (routes, hooks, migrations, tasks) is **low confidence** — mark it
   "verify before deleting", never "safe to remove".
4. **Report** in two buckets: high-confidence removals (with `file:line`) and
   verify-first items (with the reason each might still be live).

When unsure, downgrade confidence. A false "safe to delete" costs far more than
a missed dead function.
