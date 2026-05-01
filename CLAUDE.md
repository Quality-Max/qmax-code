# CLAUDE.md

## Git workflow

- Never commit or push directly to `main`. Always create a branch and open a PR.

## Merging your own PRs

Branch protection requires 1 approval + CI. Since self-approval isn't allowed,
use this pattern for your own branches:

```bash
gh api repos/Quality-Max/qmax-code/branches/main/protection/enforce_admins --method DELETE
gh pr merge <number> --squash --delete-branch --admin
gh api repos/Quality-Max/qmax-code/branches/main/protection/enforce_admins --method POST
```

External contributor PRs still need your explicit approval before merging normally.
