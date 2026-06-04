# Test Quality Review

Review an existing test suite for quality rather than coverage — tests that look
green but assert little or nothing.

## Workflow

1. **Find the tests** (`*.test.*`, `*.spec.*`, `*_test.go`, `test_*.py`) and read
   them.
2. **Flag the quality smells**:
   - **No real assertion** — exercises code but never asserts, `expect(true)`,
     snapshot-only with no meaningful check, a test that can't fail.
   - **Weak assertion** — `toBeTruthy()`/`assert result` where an exact value is
     knowable; asserts status code but not body; asserts length but not contents.
   - **Disabled/hidden** — `.skip`, `.only`, `xit`, `xdescribe`, `test.todo`,
     `describe.skip`, commented-out tests (`.only` is the worst — it silently
     drops the rest of the file from CI).
   - **Over-mocking** — mocks the unit under test, or asserts a mock was called
     but never that the result is correct.
   - **Missing edge cases** — happy path only for logic with empty/null/error/
     boundary cases; hardcoded `sleep`, real network/time/random without control.
3. **Report** per finding with `file:line`, why it's weak, and the stronger
   assertion to write.

A passing test that asserts nothing is worse than no test — it buys false
confidence. Call those out first.
