# AGENTS.md

Instructions for coding agents working on qmax-code.

## What this is

qmax-code is a terminal **QA** agent that can also host coding CLIs. It runs
standalone on a local repository or connected to QualityMax. Through `/orch`
it can launch Claude Code, Codex, or OpenCode while keeping qmax tools and
terminal UX — or run the built-in loop on Anthropic, Cerebras, or Ollama.

This repository is **Go 1.24+**, not TypeScript. The thing users install is one
compiled binary.

## Positioning (do not oversell)

qmax-code is **not** a faster Claude and **not** an IDE. Cursor owns the
editor. Claude Code and Codex own general coding in a terminal. qmax-code owns
QA on the repo (crawl, generate/run tests, review, heal, receipts) and borrows
those other agents when the job is coding.

- **Go** = one-file install and instant TUI. It does not make tokens faster.
- **Cerebras** = fast inference (~1000–2000+ tok/s). Models today: GPT-OSS
  120B, GLM 4.7, Gemma 4. **Qwen 3.8 is coming soon** on Cerebras; do not
  wire it into `/orch` until the model ID is live on the Cerebras API.
- **Claude Code / Codex** = judgment for hard design and tricky refactors.
- **OpenCode** = opt-in providers (Z.AI GLM, Groq, OpenRouter) via
  `/providers`.

When writing copy or comments, say “the QA terminal that can switch
inference,” not “faster than Claude.”

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
