# Command reference

This reference describes the qmax-code v1.21 command-line and interactive
surfaces. Run `qmax-code --help` for the flags compiled into your installed
version and `/help` for its REPL commands.

## Command-line usage

```text
qmax-code [flags]
qmax-code [flags] "prompt"
qmax-code login [--api-key KEY]
qmax-code config [show|set|unset|reset]
qmax-code receipt [list|show|verify] [id|latest]
qmax-code cc connect
qmax-code codex connect
qmax-code serve --mcp
```

### Main flags

| Flag | Meaning |
| --- | --- |
| `--local` | Run standalone: skip QualityMax login and expose only local workspace tools. |
| `--project-id ID` | Set the active QualityMax project for this run. |
| `--model MODEL` | Select a direct Anthropic model or known model shorthand. |
| `--anthropic-api-key KEY` | Supply an Anthropic key for this process. Prefer an environment variable or interactive keychain storage over shell history. |
| `--cloud-url URL` | Override the QualityMax cloud URL. Normal users should use `qmax-code login`. |
| `-p "PROMPT"` | Run one prompt and exit. |
| `--resume ID` | Resume a saved qmax-code session; use `last` for the newest. |
| `--list-sessions` | List recent saved sessions and exit. |
| `--save-session` | Save this run even when automatic saving is disabled. Applies to interactive and one-shot built-in-backend sessions; CLI backends manage native resume state. |
| `--verbose` | Show tool calls and raw responses. |
| `--professional` | Disable the cat personality for this run. |
| `-q` | Reserved for a future quiet/CI output mode; currently has no effect. |
| `--backend NAME` | Override the saved backend with `api`, `cc`, `codex`, `cerebras`, or `opencode`. |
| `--version` | Print the version and exit. |

Non-interactive stdin must include either `-p` or a positional prompt. This
prevents setup or credential prompts from blocking on a pipe:

```bash
qmax-code -p "run the project test suite"
qmax-code "review the current diff"
qmax-code --local --backend codex -p "review the current diff"
```

In standalone mode, `--project-id`, `--cloud-url`, cloud sessions, and
QualityMax-backed tools do not apply. The selected model or CLI backend may
still require its own provider authentication.

## Authentication commands

### QualityMax login

```bash
qmax-code login
qmax-code login --api-key qm-YOUR-API-KEY
```

The first command opens the one-time browser flow. The second uses a
QualityMax API key.

### Attach coding-agent accounts

```bash
qmax-code cc connect
qmax-code codex connect
```

These attach the active local Claude Code or a fresh Codex login to the
currently authenticated QualityMax user. They are separate from selecting a
backend with `/cc` or `/codex`.

## Configuration subcommand

```bash
qmax-code config
qmax-code config show
qmax-code config set KEY VALUE
qmax-code config unset KEY
qmax-code config reset
```

Supported keys:

| Key | Values or purpose |
| --- | --- |
| `default_framework` | `playwright`, `pytest`, `go_test`, `rust_cargo`, or empty |
| `default_project` | Numeric project ID |
| `default_model` | `auto`, `sonnet`, `opus`, `haiku`, or a recognized full Claude model ID |
| `professional` | Boolean |
| `local_only` | Boolean; make standalone local-only startup persistent |
| `auto_save` | Boolean |
| `output_verbose` | Boolean; compact vs. detailed CLI-backend answers |
| `max_token_budget` | Integer token budget |
| `ollama_url` | HTTP(S) Ollama endpoint |
| `ollama_model` | Ollama chat/full-agent model |
| `ollama_agent_model` | Optional heavier Ollama agent model |
| `backend` | `api`, `cc`, `codex`, `cerebras`, `opencode`, or empty |
| `cerebras_key` | Stored in the OS keychain; never in config JSON |
| `cerebras_model` | Cerebras model ID or supported alias such as `gemma` |
| `cerebras_base_url` | Cerebras-compatible API base URL |
| `cerebras_reasoning_effort` | `none`, `low`, `medium`, or `high` |
| `theme` | `historic`, `ocean`, `neon`, `ember`, `aurora`, `paper`, `sky`, `sparkling`, `radiance`, or `goldenhour` |
| `cloud_sync` | Boolean or unset |
| `live_feed` | Boolean |

Configuration is stored with owner-only permissions in
`~/.qmax-code/config.json`. Secret keys handled by qmax-code are excluded from
that JSON and stored in the OS keychain.

## Exposure Receipt commands

```bash
qmax-code receipt list
qmax-code receipt show latest
qmax-code receipt show RUN_ID
qmax-code receipt verify latest
qmax-code receipt verify RUN_ID
```

`list` shows receipt IDs, run kinds, request counts, and destinations. `show`
prints the selected local manifest. `verify` checks its signature offline.

## REPL commands

### Backend and model selection

| Command | Action |
| --- | --- |
| `/orch` | Open the unified backend/model/effort picker. |
| `/api` | Switch to the direct Anthropic API. |
| `/cc` | Switch to the Claude Code CLI backend. |
| `/codex` | Switch to the Codex CLI backend. |
| `/opencode` | Switch to the OpenCode CLI backend. |
| `/gemma [none\|low\|medium\|high]` | Activate Gemma 4 on Cerebras with the chosen reasoning level. |
| `/gemma off` | Return to the direct Anthropic API. |
| `/ollama` | Toggle the configured Ollama backend. |
| `/providers` | Show opt-in OpenCode providers. |
| `/providers enable ID` | Prompt for a key if needed and enable a provider. |
| `/providers disable ID` | Hide a provider; retain its key in the keychain. |
| `/reconnect` | Restore the active Claude Code or Codex MCP transport. |

See [Orchestration mode](ORCHESTRATION.md) for detailed backend and permission
behavior.

### QualityMax context

| Command | Action |
| --- | --- |
| `/connect` | Log in to QualityMax through the browser. |
| `/disconnect` | Log out and remove saved QualityMax credentials. |
| `/project ID` | Change the active project. |
| `/context` | Show current session context. |
| `/status` | Show connection, session, usage, and model information. |
| `/cost` | Show token usage and estimated model cost. |

In standalone mode, `/connect` and `/project` explain how to return to connected
mode. `/status`, `/context`, and `/config` identify the active standalone
boundary.

### Sessions and queue

| Command | Action |
| --- | --- |
| `/sessions` | Open a picker with recent saved sessions. |
| `/resume [ID]` | Resume a session; no ID means the latest. |
| `/save` | Save the current session. |
| `/clear` | Clear conversation history and the OpenCode session when active. |
| `/queue` | Show pending prompts. |
| `/queue PROMPT` | Add a prompt to the queue. |
| `/queue clear` | Remove all queued prompts. |

Typing while an agent turn is running also queues that input and processes it
after the current turn.

### Skills and configuration

| Command | Action |
| --- | --- |
| `/skills` | List all qmax QA skills and their install status by CLI backend. |
| `/skills install` | Refresh skills for Claude Code, Codex, and OpenCode. |
| `/config` | Show selected session configuration. |
| `/set KEY VALUE` | Change a supported setting from the REPL. |
| `/keys` | Open the interactive API-key menu. |
| `/theme` | Preview and select a terminal theme. |
| `/cloudsync` | Toggle cloud session sync. |

Common `/set` keys and aliases:

```text
/set model sonnet
/set project 42
/set local_only true
/set professional true
/set autosave false
/set output_verbose true
/set budget 100000
/set cloud_sync true
/set live_feed true
/set backend codex
/set cerebras_model gemma
/set cerebras_reasoning_effort high
/set theme ocean
```

`/set local_only` changes the persisted default and takes effect after restart;
it does not replace the active tool catalog in the current process.

### Browser feed and media

| Command | Action |
| --- | --- |
| `/live` | Show live-feed state. |
| `/live on` | Run eligible tests/crawls in the QualityMax Cloud Sandbox and stream them. |
| `/live off` | Disable sandbox live streaming. |
| `/feed` | Open the most recent live browser feed. |
| `/browserfeed URL` | Open a compatible QualityMax Cloud Sandbox noVNC URL as terminal ASCII. |
| `/screenshot` | Capture a screenshot for analysis. |
| `/paste` | Attach clipboard text or an image. |

Image attachments are currently supported by built-in multimodal backends, not
the Claude Code, Codex, or OpenCode subprocess paths.

`/cloudsync`, `/live`, `/feed`, and `/browserfeed` are unavailable in
standalone local-only mode because they depend on QualityMax services.

### General

| Command | Action |
| --- | --- |
| `/help` | Print in-app help. |
| `/quit`, `/exit`, `/q` | Save when configured and exit. |

## Keyboard shortcuts

| Shortcut | Action |
| --- | --- |
| `Ctrl+C` | Cancel the active operation; press twice within one second to exit. |
| `Ctrl+O` | Toggle compact and verbose CLI-agent output. |
| `Ctrl+X` three times | Clear the input line. |
| `Ctrl+Left` / `Ctrl+Right` | Move the cursor by word (Option+arrow in terminals that map it that way). |
| Arrow keys in `/` menu | Navigate slash-command suggestions. |

## MCP server mode

```bash
qmax-code serve --mcp
```

This starts the stdio MCP server used by Claude Code, Codex, and OpenCode
integrations. It is normally launched by the agent's MCP configuration rather
than by a person. Stdout is reserved for JSON-RPC; diagnostics go to stderr.
When `local_only` is enabled or `QMAX_LOCAL_ONLY=1` is present, it advertises
only `read_file`, `run_command`, `edit_file`, and `write_file`.
