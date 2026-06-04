# Complexity Hotspots

Rank the most complex code in a repository — long functions, deep nesting, high
cyclomatic complexity, and god-files — worst-first, with a refactor for each.

## Workflow

1. **Use a metrics tool** if installed and parse it: `radon cc -s` (Python),
   `lizard` (multi-language), `gocyclo` (Go), or the ESLint complexity rule.
2. **Otherwise estimate** per function/method:
   - **Length** (body lines), **nesting depth** (max control-flow indentation),
   - **Cyclomatic complexity** (1 + count of `if/for/while/case/catch/&&/||/?:`),
   - **Parameter count**. Also flag **god-files** doing many unrelated jobs.
   Rough thresholds: length >50 warn / >100 bad · nesting >3 / >5 · cyclomatic
   >10 / >20 · params >4 / >7 · file >400 / >800 lines.
3. **Rank worst-first.** For each hotspot, name the dominant smell and a concrete
   refactor (extract function, replace nested if-ladder with a dispatch table,
   pass a params object, split the file).
4. **Report** the top offenders with `file:line` and why each matters — these are
   where defects cluster and change is hardest.

Tie the recommendation to the metric. "Cyclomatic 31 → extract each branch"
lands; "this looks complex" does not.
