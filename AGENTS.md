# AGENTS.md

Instructions for coding agents working on qmax-code.

## What this is

qmax-code is a **developer-first coding environment**: a Go terminal agent
that works on the repository in front of the user, with no account, no
project, and no QualityMax request in standalone mode. It is evolving into a
**standalone IDE with optional, first-class QualityMax connectivity through
MCP**. Through `/orch` it hosts Claude Code, Codex, Antigravity, or OpenCode
while keeping qmax tools and terminal UX, or runs the built-in loop on
Anthropic, Cerebras, or Ollama.

This repository is **Go 1.27.3+**, not TypeScript. The thing users install is one
compiled binary.

## Positioning

Lead with the developer. The product is the environment a developer works
in: their repo, their terminal, their choice of model and agent, their keys
in their keychain. QualityMax is an optional integration with first-class
MCP support, never a prerequisite. QA remains a core strength, framed as part
of the developer's own loop (write, test, review, ship) rather than as a
separate service.

Be exact about the standalone boundary. `--local` exposes five agent tools
(`update_plan`, `read_file`, `write_file`, `edit_file`, `run_command`), the
`/orch` backends, and `/skills`. Test generation from managed test cases,
hosted test runs, crawls, repository review, project and test-case
management, PR creation, CI setup, and script healing require a QualityMax
connection. Say so when describing a feature; do not imply a connected tool
works offline.

Distinguish the IDE direction from capabilities shipped today. The current
release is a terminal application; describe planned IDE features as future work.

- **Go** = one-file install and instant TUI. It does not make tokens faster.
- **Cerebras** = fast inference (~1000–2000+ tok/s). Models today: GPT-OSS
  120B, GLM 4.7, Gemma 4. **Qwen 3.8 is coming soon** on Cerebras; do not
  wire it into `/orch` until the model ID is live on the Cerebras API.
- **Claude Code / Codex / Antigravity** = judgment for hard design and tricky refactors.
  Antigravity uses Google OAuth (run interactive `agy` to sign in), not a Gemini API key.
- **OpenCode** = opt-in providers (Z.AI GLM, Groq, OpenRouter) via
  `/providers`.

When writing product-direction copy, say "a developer-first coding
environment, evolving into a standalone IDE with optional, first-class
QualityMax connectivity through MCP." Describe current terminal capabilities
accurately and attribute inference speed to the backend.

## Why Go

qmax-code is written in Go so it behaves like a Unix tool, not like a Node app.

- **One file.** `curl | bash` drops a single binary (`~/.qmax-code/qmax-code`).
  There is no Node, npm, Python, or version manager for qmax-code itself.
- **Every OS from one source tree.** A tagged release builds macOS, Linux, and
  Windows (Intel and ARM) from the same commit.
- **Starts immediately.** A TUI people open all day cannot wait on an
  interpreter.
- **Concurrent without extra machinery.** The agent turn, live browser feed,
  signals, and embedded MCP server run as goroutines in one process.

Go does not make the model smarter. Claude Code, Codex, and OpenCode stay
separate CLIs that this binary can launch. Do not add npm, pip, or another
language runtime as a dependency of qmax-code.

## Git

Never commit or push directly to `main`. Create a branch and open a PR.
Claude-specific merge notes live in [CLAUDE.md](CLAUDE.md).

## Build and test

```bash
go build -o qmax-code .
go test ./...
go vet ./...
```

Match a release build with:

```bash
go build -ldflags="-s -w -X main.Version=dev" -o qmax-code .
```

Architecture, slash-command and tool patterns, and PR expectations:
[CONTRIBUTING.md](CONTRIBUTING.md).
