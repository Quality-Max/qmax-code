# Orchestration mode

Orchestration mode lets qmax-code choose its inference engine while preserving
qmax-code's terminal and tool policy. It works in both standalone local-only
mode and QualityMax-connected mode.

The `/orch` command opens one picker for:

1. Backend
2. Model
3. Reasoning or effort level, where the backend supports it

The selection applies immediately and is saved for future qmax-code sessions.

## What orchestration means here

`/orch` does not launch a team of agents. It chooses the single backend that
will handle the next qmax-code turn.

```text
qmax-code
├─ built-in agent loop
│  ├─ Anthropic API
│  ├─ Cerebras
│  └─ Ollama
└─ CLI subprocess
   ├─ Claude Code
   ├─ Codex
   └─ OpenCode
       └─ qmax MCP server
          ├─ connected mode: QualityMax + approved local tools
          └─ standalone mode: workspace tools only
```

The built-in backends call tools directly. The CLI backends connect to an
embedded `qmax-code serve --mcp` stdio server. This gives the coding agent
the tools allowed by the active mode without requiring the separate `qmax`
CLI.

This is also distinct from Conductor's orchestration model. Conductor creates
parallel, isolated git worktree workspaces; qmax-code `/orch` selects the
inference backend within one qmax-code process.

## Backends

| Backend | Requirements | Tool connection | Image input | Session behavior |
| --- | --- | --- | --- | --- |
| Anthropic API | Anthropic API key | Built in | Yes | qmax-code local/cloud sessions |
| Claude Code (`cc`) | `claude` installed and logged in | Embedded MCP | No, text-only subprocess path | Claude Code native state plus qmax history |
| Codex | `codex` installed and logged in | Embedded MCP | No, text-only subprocess path | Codex native state plus qmax history |
| Cerebras | Cerebras API key | Built-in native function calling | Yes for vision-capable models such as Gemma 4 | qmax-code local/cloud sessions |
| OpenCode | `opencode` installed and at least one enabled provider | Embedded MCP through a qmax-managed overlay | No, text-only subprocess path | OpenCode native state plus qmax history |
| Ollama | Reachable HTTP(S) endpoint and configured model | Built in | Model/path dependent; do not assume vision | qmax-code local/cloud sessions |

QualityMax authentication is separate from backend authentication. Use
`--local` to skip QualityMax authentication entirely. QualityMax cloud tools
require connected mode and `qmax-code login`.

## Standalone local-only orchestration

Every backend can be selected in standalone mode:

```bash
qmax-code --local --backend codex
qmax-code --local --backend cc
qmax-code --local --backend cerebras
qmax-code --local --backend opencode
```

The direct API path also works with `--local` when Anthropic is configured.
Ollama can provide an entirely self-hosted inference path:

```bash
qmax-code config set ollama_url http://127.0.0.1:11434
qmax-code config set ollama_model llama3.2:3b
qmax-code --local
```

Persist or disable the startup mode with:

```bash
qmax-code config set local_only true
qmax-code config set local_only false
```

Standalone mode skips QualityMax onboarding and does not load QualityMax
credentials, discover the legacy `qmax` CLI, select a QualityMax project, or
start cloud session/live-feed services. The qmax tool boundary is:

| Surface | Standalone tools |
| --- | --- |
| Built-in agent | `update_plan`, `read_file`, `run_command`, `edit_file`, `write_file` |
| MCP for Claude Code, Codex, OpenCode | `read_file`, `run_command`, `edit_file`, `write_file` |

`run_local_test` is not in that list: it downloads a script from QualityMax and
reports its result, despite executing the test process locally. CLI agents may
still expose their own native file, shell, browser, or network tools; Standard
and Unattended permission modes continue to govern those native capabilities.
Standalone mode is a QualityMax service boundary, not a process sandbox.

When a CLI backend launches `qmax-code serve --mcp`, qmax-code passes
`QMAX_LOCAL_ONLY=1` to the child so it advertises and executes only the local
MCP catalog. Execution is checked again even if an MCP client calls an
undisclosed cloud-tool name directly.

## Start or switch

Start qmax-code and open the picker:

```text
qmax-code
> /orch
```

Direct switches are also available:

```text
/api
/cc
/codex
/opencode
/gemma
/ollama
```

For non-interactive use:

```bash
qmax-code --backend cc -p "review the current diff"
qmax-code --backend codex -p "run the narrowest relevant tests"
qmax-code --backend cerebras -p "inspect this repository for test gaps"
qmax-code --backend opencode -p "review error handling"
qmax-code --local --backend codex -p "review this repository without QualityMax"
```

The saved backend can be changed outside the REPL:

```bash
qmax-code config set backend codex
qmax-code config set backend api
```

Ollama is selected from the REPL rather than the `backend` config field.

## Permission modes

The first activation of Claude Code, Codex, or OpenCode asks how much autonomy
to grant. The answer is persisted in `~/.qmax-code/config.json`.

### Standard

Standard mode is recommended. It auto-approves:

- File reads and repository searches
- Git status and diff inspection
- Common test runners such as `go test`, `pytest`, `npm test`, and
  `cargo test`
- qmax MCP tools

File edits and destructive or broader shell commands remain subject to the
underlying CLI's permission controls.

### Unattended

Unattended mode passes the backend's full-autonomy option. The agent may edit
files, run arbitrary commands, and perform git operations without another
prompt.

Only use unattended mode in a trusted workspace with changes you can recover.
The setting does not create a sandbox, container, or branch boundary.

To change a previously persisted choice, edit or remove
`orch_permission_mode` in `~/.qmax-code/config.json`, then activate a CLI
backend again. Valid values are `standard` and `unattended`.

## MCP installation scope

Claude Code and Codex ask whether qmax should be registered globally.

If accepted, qmax-code adds or updates only the `qmax` MCP entry in:

- Claude Code: `~/.claude/settings.json`
- Codex: `~/.codex/config.toml`

Existing unrelated settings are preserved. The global entry runs:

```text
qmax-code serve --mcp
```

This makes qmax tools available in ordinary `claude` or `codex` sessions as
well as sessions launched by qmax-code.

If global installation is declined, qmax-code uses session-scoped integration
and does not add the user-level MCP entry.

OpenCode is different: qmax-code writes a separate overlay at
`~/.qmax-code/opencode.json` and launches OpenCode with that overlay on top of
the user's existing configuration. It does not overwrite the user's main
OpenCode file.

If a CLI loses its session-scoped transport, use:

```text
/reconnect
```

## QA skill installation

The same catalog is materialized in the native format of each CLI:

- Claude Code: `~/.claude/skills/`
- Codex: `~/.codex/skills/`
- OpenCode: `~/.config/opencode/skills/`

Claude Code and Codex skill installation follows the global-install consent.
OpenCode skills are refreshed whenever that backend is activated. Installation
is idempotent, and upgrading qmax-code refreshes managed skill content.

```text
/skills
/skills install
```

`/skills` shows the install status for all 27 skills. `/skills install`
materializes the catalog for all three CLI backends. Codex receives additional
`agents/openai.yaml` metadata; browser/runtime skills declare their Playwright
MCP dependency there.

Generated skill directories and files are owner-only. qmax-code rejects unsafe
skill names and skill-directory symlinks that resolve outside the user's home.

## OpenCode providers

OpenCode providers are disabled by default and enabled per user:

```text
/providers
/providers enable zai-coding-plan
/providers enable groq
/providers enable openrouter
```

Enabling a provider prompts for its key, saves it in the OS keychain, and adds
the provider's models to `/orch`. Disabling it removes the provider from the
picker but leaves the key in the keychain for later reuse:

```text
/providers disable groq
```

qmax-code currently supports these opt-in providers:

| ID | Provider |
| --- | --- |
| `zai-coding-plan` | Z.AI Coding Plan |
| `groq` | Groq |
| `openrouter` | OpenRouter |

Cerebras remains a first-class native backend and is selected directly in
`/orch`, not through OpenCode.

## Cerebras and Gemma 4

Cerebras uses qmax-code's built-in OpenAI-compatible function-calling loop and
can access the tool set allowed by the active connected or standalone mode.

Use the picker, or activate Gemma 4 directly:

```text
/gemma
/gemma none
/gemma low
/gemma medium
/gemma high
/gemma off
```

`none` disables reasoning for the lowest latency. `off` returns to the direct
Anthropic API backend. Gemma 4 accepts screenshots and pasted images through
qmax-code's multimodal path and reports Cerebras speed metrics when available.

Configuration equivalents:

```bash
qmax-code config set backend cerebras
qmax-code config set cerebras_model gemma
qmax-code config set cerebras_reasoning_effort medium
```

The Cerebras key can come from `CEREBRAS_API_KEY` or the OS keychain prompt.

## Ollama

Configure a reachable HTTP(S) endpoint and model:

```bash
qmax-code config set ollama_url http://127.0.0.1:11434
qmax-code config set ollama_model llama3.2:3b
```

Then use `/ollama` or choose Ollama from `/orch`. qmax-code rejects non-HTTP(S)
Ollama URL schemes.

Ollama capabilities depend on the configured model and qmax-code's local
adapter. Do not assume that an Ollama model supports the same function calling,
context window, or image input as another backend.

## Global configuration effects

Activating orchestration may create or update:

| Path | Purpose |
| --- | --- |
| `~/.qmax-code/config.json` | Selected backend/model/effort and consent choices |
| `~/.qmax-code/opencode.json` | qmax-managed OpenCode overlay |
| `~/.claude/settings.json` | Optional global qmax MCP entry |
| `~/.codex/config.toml` | Optional global qmax MCP entry |
| `~/.claude/skills/` | Managed Claude Code QA skills |
| `~/.codex/skills/` | Managed Codex QA skills |
| `~/.config/opencode/skills/` | Managed OpenCode QA skills |

Provider secrets are not written to those files by qmax-code. They are loaded
from the OS keychain or the provider's supported environment variable and
injected into the launched process.

The same config file stores the optional `local_only` default. A per-run
`--local` selection is also passed to CLI-backend MCP children through
`QMAX_LOCAL_ONLY=1`.

## Troubleshooting

### The backend is missing from `/orch`

- Claude Code, Codex, and OpenCode only become selectable when their executable
  is installed and discoverable.
- OpenCode provider models only appear after the provider is enabled and has a
  usable key.
- Run `/providers` to inspect provider status.

### qmax tools are unavailable

- Run `/status` first. In standalone mode, only the local workspace catalog is
  expected; restart without `--local` (or set `local_only` to `false`) for
  QualityMax tools.
- Run `/reconnect` inside qmax-code.
- Run `/skills` to distinguish missing skills from a missing MCP connection.
- For a global Claude Code or Codex session, confirm the `qmax` MCP entry exists
  in that CLI's user configuration.
- Confirm `qmax-code` is available on `PATH`; the MCP entry launches
  `qmax-code serve --mcp`.

### Images are ignored

The CLI subprocess path is text-only. Switch to a built-in multimodal backend,
such as Gemma 4 on Cerebras, before using `/screenshot` or `/paste` with an
image.

### A provider is enabled but has no models

Re-enable it to repair a missing key:

```text
/providers disable groq
/providers enable groq
```

qmax-code passes the provider's keychain-backed environment to OpenCode model
discovery. If discovery still fails, run the provider's own OpenCode model
listing outside qmax-code to confirm the provider and key are accepted.

### MCP output reports invalid JSON

Use a current qmax-code release. MCP mode reserves stdout for JSON-RPC and sends
diagnostic output to stderr; older builds may not include all stream-isolation
fixes.

## Security guidance

- Prefer Standard mode.
- Use Unattended only in a trusted, recoverable repository.
- Review changes with `git diff` and run the riskiest relevant test before
  committing.
- Treat global MCP and skill installation as user-level configuration changes.
- Keep provider keys in the OS keychain; if an existing OpenCode config contains
  a literal key, rotate it and replace the literal with an environment
  reference.
- Read [the security policy](../SECURITY.md) for the local-agent trust model,
  credential handling, and command limits.
