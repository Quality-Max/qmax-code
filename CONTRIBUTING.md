# Contributing to qmax-code

Thanks for your interest in improving `qmax-code`. This document covers everything you need to go from zero to a merged pull request.

---

## Table of contents

- [Prerequisites](#prerequisites)
- [Building from source](#building-from-source)
- [Running tests](#running-tests)
- [Architecture overview](#architecture-overview)
- [Common contribution patterns](#common-contribution-patterns)
  - [Adding a slash command](#adding-a-slash-command)
  - [Adding an agent tool](#adding-an-agent-tool)
  - [Adding a theme](#adding-a-theme)
- [Code style](#code-style)
- [Security-sensitive changes](#security-sensitive-changes)
- [Public source boundary](#public-source-boundary)
- [Pull request process](#pull-request-process)
- [License](#license)

---

## Prerequisites

| Requirement | Notes |
|---|---|
| **Go 1.24+** | See `go.mod` for the exact version. `go version` to check. |
| **Inference backend** | The direct API path needs an Anthropic key. You can instead develop against a logged-in Claude Code/Codex CLI, an enabled OpenCode provider, Cerebras, or Ollama. |
| **QualityMax account** | Required only for connected cloud tools (test generation, crawl, repo review). Standalone `--local` development and most unit tests do not need one. |

Optional but useful:

- [golangci-lint](https://golangci-lint.run/usage/install/) — the CI linter; run it locally to catch issues before pushing
- [Claude Code](https://claude.ai/download), [Codex](https://github.com/openai/codex), or [OpenCode](https://opencode.ai) — only needed when working on that CLI orchestration backend
- [Ollama](https://ollama.com) or a Cerebras key — only needed when working on that inference adapter

---

## Building from source

```bash
git clone https://github.com/Quality-Max/qmax-code.git
cd qmax-code
go build -o qmax-code .
./qmax-code --version
```

To match a release build (version injected from a tag):

```bash
go build -ldflags="-s -w -X main.Version=dev" -o qmax-code .
```

---

## Running tests

```bash
go test ./...
```

If your environment can't write to the default Go build cache:

```bash
GOCACHE=/tmp/qmax-code-gocache go test ./...
```

For verbose output on a specific file:

```bash
go test -v -run TestTheme ./...
```

The CI pipeline runs `go vet`, `golangci-lint`, and `go test` on every PR. Run all three locally before pushing:

```bash
go vet ./...
golangci-lint run
go test ./...
```

Tests that hit the QualityMax API or a live Anthropic model are skipped when the relevant env vars are absent — the test suite is designed to be runnable offline.

---

## Architecture overview

```text
main.go                                Process entry, flags, subcommands, backend startup
internal/repl/repl.go                  REPL, slash commands, queue, backend switching
internal/agent/agent.go                Built-in streaming loop and history compression
internal/agent/tools.go                Tool definitions, dispatch, safety, local execution
internal/agent/cc_agent.go             Claude Code subprocess backend
internal/agent/codex_agent.go          Codex subprocess backend
internal/agent/opencode_agent.go       OpenCode subprocess backend
internal/agent/cerebras_agent.go       Cerebras native function-calling loop
internal/agent/ollama_agent.go         Ollama full-agent mode
internal/api/client.go                 QualityMax REST client
internal/api/auth.go                   QualityMax auth and keychain helpers
internal/api/config.go                 Persistent user configuration
internal/api/providers.go              Opt-in OpenCode provider registry
internal/mcp/server.go                 Embedded stdio MCP server
internal/setup/orch.go                 MCP registration and skill installation
internal/skills/                       Backend-neutral 27-skill catalog/materializer
internal/session/                      Local/cloud sessions and prompt queue
internal/tui/                          Bubble Tea input, output, themes, media, pickers
internal/security/                     Command validation and credential redaction
internal/httpx/ and receipt.go          Guarded egress and Exposure Receipts
```

The interactive loop lives in `internal/repl/repl.go`. Built-in backend prompts
flow through `Agent.RunStreaming`, while CLI backends implement
`agent.CLIAgent` and receive qmax tools from `internal/mcp/server.go`. Tools are
declared in `BuildToolDefs` and dispatched by `ExecuteTool` in
`internal/agent/tools.go`.

Standalone mode is an explicit capability boundary. New tools must not be added
to `localOnlyToolNames` unless they work without QualityMax credentials, API
calls, the legacy `qmax` CLI, or cloud result reporting. Keep tool discovery
and execution-time rejection tests in sync; MCP clients can call tool names
that were never advertised.

---

## Common contribution patterns

### Adding a slash command

Slash commands are handled in two places — miss either one and the command either won't work or won't appear in the autocomplete menu.

**1. Register the handler** in `runREPL` (`internal/repl/repl.go`):

```go
case input == "/mycommand":
    handleMyCommand(agent, term)
    continue
```

**2. Add a menu entry** in `internal/tui/input.go` so it appears in the `/`
autocomplete:

```go
var slashMenuItems = []SlashMenuItem{
    // ... existing entries ...
    {Command: "/mycommand", Description: "Short description shown in menu"},
}
```

Both steps are required. The menu entry is what users see when they type `/`; the handler is what runs when they submit it.

---

### Adding an agent tool

Tools are declared in `BuildToolDefs` (`internal/agent/tools.go`) and dispatched
in `ExecuteTool`. To add one:

**1. Declare the tool** in `buildAllToolDefs`:

```go
{
    Name:        "my_tool",
    Description: "What the agent should understand about when to use this.",
    InputSchema: obj(props(
        prop("param_name", "string", "What this param does", true),
    )),
},
```

**2. Add a case** in `ExecuteTool` (or `executeToolViaAPI` for cloud-backed tools):

```go
case "my_tool":
    return api.MyTool(ctx, strVal(input, "param_name"))
```

**3. Add the API method** in `internal/api/client.go` if it calls the QualityMax
backend:

```go
func (c *APIClient) MyTool(ctx context.Context, param string) string {
    return c.get(ctx, "/api/my-endpoint/"+param)
}
```

**4. Assign a cost tier** in `ToolCost` (`tools.go`) — `"low"`, `"medium"`, or `"high"`. This is shown to the user before expensive operations.

Write a focused test in `internal/api/` covering the request shape if the tool
hits the network. Outbound HTTP must go through `internal/httpx`; the static
egress guard fails CI if a package constructs an unreceipted raw HTTP client or
request.

---

### Adding a theme

Themes are defined in `internal/tui/theme.go`. Add an entry to the `themes` map:

```go
"mytheme": {
    Name:       "My Theme",
    Primary:    lipgloss.Color("#hexcode"),
    Secondary:  lipgloss.Color("#hexcode"),
    Accent:     lipgloss.Color("#hexcode"),
    Muted:      lipgloss.Color("#hexcode"),
    Background: lipgloss.Color("#hexcode"),
    Border:     lipgloss.Color("#hexcode"),
},
```

Run `go test ./...` — `internal/tui/theme_test.go` validates that all registered
themes have complete field sets.

---

## Code style

- Standard `gofmt` formatting — the linter enforces it.
- Exported symbols get a one-line doc comment; unexported ones generally don't need one unless the behavior is non-obvious.
- Prefer explicit error handling. Don't swallow errors silently.
- No comments that restate what the code does; a comment should explain *why* something works the way it does — a hidden constraint, a workaround, a subtle invariant.
- Keep functions focused. If a function is doing two distinct things, consider splitting.
- Write table-driven tests where there are multiple cases to cover.

---

## Security-sensitive changes

Read [SECURITY.md](SECURITY.md) before touching:

- auth or credential storage (`internal/api/auth.go`, `internal/api/keychain.go`)
- provider configuration or subprocess credential injection
- telemetry or error reporting (`internal/sysutil/error_reporting.go`)
- Exposure Receipts or outbound HTTP (`receipt.go`, `internal/httpx/`)
- `read_file`, `edit_file`, `write_file`, `run_command`, or `run_local_test` tool implementations
- command validation logic (`internal/security/`)
- orchestration consent, global MCP configuration, or skill materialization
- script healing, backup, or rollback behavior
- API error handling or any code path that might log user data

For any of the above, add or update focused tests in the owning package and
note the security impact in your PR description.

---

## Public source boundary

`qmax-code` is the public client/agent layer. The QualityMax backend stays closed source. When contributing, do not add:

- Private service names, internal hostnames, or unpublished API routes
- Proprietary scoring, ranking, or review heuristics
- Unreleased roadmap behavior or experimental endpoints not yet intended for public support
- Backend implementation details that belong in the closed monorepo

See [OPEN_SOURCE_SCOPE.md](OPEN_SOURCE_SCOPE.md) for the full surface classification.

---

## Pull request process

1. **Fork** the repo and create a branch from `main`.
2. **Write tests** for any behavior you add or change.
3. **Run the full suite** locally: `go vet ./... && golangci-lint run && go test ./...`
4. **Keep the PR focused** — one logical change per PR makes review faster.
5. **Update docs** — if your change affects user-facing behavior, update README or SECURITY.md as appropriate.
6. **Open the PR** against `main`. Fill out the description with what changed and why.
7. A maintainer will review. Small, well-tested PRs get reviewed fastest.

Do not commit generated binaries, release archives, customer reports, local credentials, or test artifacts.

---

## License

`qmax-code` is released under the [Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2)](LICENSE). By contributing, you agree your contributions are licensed under the same terms.

FSL permits all non-competing use and automatically converts to Apache 2.0 two years after each release. See [fsl.software](https://fsl.software) for a plain-language explanation.
