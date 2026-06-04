# Error Handling Audit

Audit a repository or diff for weak error handling — places where failures
disappear silently instead of being surfaced or recovered.

## Workflow

1. **Scope** — whole repo, a directory, or the pending `git diff` (default to the
   diff when changes are staged).
2. **Flag the anti-patterns**:
   - **Swallowed** — `except: pass`, `except Exception: pass`, `catch (e) {}`,
     empty/comment-only handlers with no log, rethrow, or recovery.
   - **Over-broad** — `except Exception` / `catch (Throwable)` that hides bugs
     which should crash, or returns a default that masks the failure.
   - **Async** — a floating promise (not `await`ed, no `.catch`), `Promise.all`
     dropping siblings on one rejection, no top-level rejection handler.
   - **Network/IO** — HTTP call with no timeout, no retry/backoff on a flaky
     dependency, a resource opened without `finally`/`with`/`defer` cleanup.
   - **Observability** — error caught and logged but the caller still gets a
     success; `logger.error(e)` with no stack/context.
3. **Report** each with `file:line`, the risk (what failure becomes invisible),
   and the fix.

The question for every handler: if this fails in production, does anyone find
out? If not, it's a finding.
