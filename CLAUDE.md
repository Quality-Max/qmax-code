# CLAUDE.md

qmax-code is evolving into a **standalone IDE with optional, first-class
QualityMax connectivity through MCP**. Its current foundation is a **Go**
terminal agent (Go 1.27.3+). It ships as one compiled binary:
`curl | bash` installs a file, not a Node or Python runtime.
Claude Code, Codex, Antigravity, and OpenCode are separate subprocesses that `/orch` can
host; they are not this repository's runtime.

**Positioning:** an independent environment for coding, testing, review, and
agent workflows. QualityMax is optional, with first-class MCP connectivity;
using qmax-code does not require a QualityMax account. QA (tests, crawls,
review, receipts) remains a core strength. Describe the standalone IDE as the
product direction and the terminal app as what ships today; describe planned
IDE features as future work.
qmax-code switches inference in `/orch`. **Cerebras** is the fast path
(~1000–2000+ tok/s: GPT-OSS 120B, GLM 4.7, Gemma 4; **Qwen 3.8 coming soon**).
Do not add Qwen to the picker until Cerebras publishes the live model ID.

**Why Go (not a smarter or faster model):** one-file install, six OS/arch
binaries from the same source, instant TUI startup, goroutines for the agent
turn / live feed / MCP / signals. Do not add npm, pip, or another language
runtime as a dependency of the CLI itself.

Build and test: `go build -o qmax-code .` then `go test ./...`. Project-wide
agent instructions: [AGENTS.md](AGENTS.md). Contribution map:
[CONTRIBUTING.md](CONTRIBUTING.md).

## Git workflow

- Never commit or push directly to `main`. Always create a branch and open a PR.

## Merging your own PRs

Branch protection requires 1 approval + CI. Since self-approval isn't allowed,
Codex/QMax/Claude agents may use this one-time admin-enforcement lift for their
own branches after verifying the PR is non-draft and all required checks pass:

```bash
gh api repos/Quality-Max/qmax-code/branches/main/protection/enforce_admins --method DELETE
gh pr merge <number> --squash --delete-branch --admin
gh api repos/Quality-Max/qmax-code/branches/main/protection/enforce_admins --method POST
```

External contributor PRs still need your explicit approval before merging normally.
