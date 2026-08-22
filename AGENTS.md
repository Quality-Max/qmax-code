# AGENTS.md

Instructions for coding agents working on qmax-code.

## What this is

qmax-code is a terminal QA and coding agent. It can run standalone on a local
repository or connected to QualityMax, and it can host Claude Code, Codex, or
OpenCode through `/orch` while keeping qmax tools and terminal UX.

This repository is **Go 1.24+**, not TypeScript. The thing users install is one
compiled binary.

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
