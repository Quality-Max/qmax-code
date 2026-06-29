```
                                    ╱|、
                                   (˚ˎ 。7
                                    |、˜〵
                                    じしˍ,)ノ

             ██████╗ ███╗   ███╗ █████╗ ██╗  ██╗
            ██╔═══██╗████╗ ████║██╔══██╗╚██╗██╔╝
            ██║   ██║██╔████╔██║███████║ ╚███╔╝
            ██║▄▄ ██║██║╚██╔╝██║██╔══██║ ██╔██╗
            ╚██████╔╝██║ ╚═╝ ██║██║  ██║██╔╝ ██╗
             ╚══▀▀═╝ ╚═╝     ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝
                             c o d e
```

# qmax-code

[![License: FSL-1.1-ALv2](https://img.shields.io/badge/license-FSL--1.1--ALv2-2ea44f.svg)](LICENSE)
[![Future License: Apache 2.0](https://img.shields.io/badge/future%20license-Apache%202.0-blue.svg)](LICENSE)
[![Made with Go](https://img.shields.io/badge/made%20with-Go-00ADD8.svg)](https://go.dev/)
[![Announcement](https://img.shields.io/badge/announcement-2026--05--01-7c6cf0.svg)](https://qualitymax.io/blog/qmax-code-open-source)

[![Buy Me a Coffee](https://img.shields.io/badge/Buy%20Me%20a%20Coffee-support-yellow?logo=buymeacoffee)](https://buymeacoffee.com/qualitymax)

**AI-powered terminal agent for QualityMax.** Named after Max, the real cat who inspired it all.

qmax-code is the LLM brain that orchestrates the open-source [`qmax`](https://github.com/Quality-Max/qmax-local-agent) CLI. It connects to the Claude API, understands your testing intent in natural language, and translates it into structured CLI operations — crawling sites, generating tests, running scripts, reviewing repos.

> **License:** Source-available under the [Functional Source License (FSL-1.1-ALv2)](LICENSE) — created by [Sentry](https://fsl.software). Free for any non-competing use (internal use, modifications, contributions, education, research, professional services). Two years after each release, the code automatically converts to plain Apache 2.0. The "Other" tag GitHub shows in the sidebar is a quirk of its licensee detector — FSL isn't on the SPDX list.

## How it works

```
  You  →  "test the login flow on staging"
                    │
              qmax-code (Claude API)
                    │
          ┌─────────┼─────────┐
          ▼         ▼         ▼
     qmax crawl  qmax test  qmax test
       start      generate     run
```

Claude picks the right tools, chains them together, and reports back — all in a colorful terminal with cat personality.

## What's new in v1.13

- **Themes** — live-preview color scheme picker: Historic, Ocean, Neon, Ember, Aurora (`/theme`)
- **Thinking spinner** — animated indicator with cat-themed messages while the agent reasons
- **Prompt queue** — type your next prompt while the agent is still running; it processes automatically
- **Input fixes** — long lines wrap correctly, cursor tracking fixed, rune editing fixed

## Install

```bash
curl -sL https://raw.githubusercontent.com/Quality-Max/qmax-code/main/install.sh | bash
```

## Quick start

```bash
# 1. Set your Anthropic API key
export ANTHROPIC_API_KEY=sk-ant-...

# 2. Login to QualityMax
qmax-code login

# Or use a QualityMax API key from Settings > API Keys
qmax-code login --api-key qm-YOUR-API-KEY

# 3. Attach Codex for QualityMax mobile runs (optional)
qmax-code codex connect

# 4. Start using
qmax-code
qmax-code "crawl staging.myapp.com and generate e2e tests"
qmax-code -p "run all tests for project 42"
```

`qmax-code codex connect` runs a fresh `codex login`, reuses the saved
QualityMax login (or opens the one-time browser authorization when needed), and
attaches Codex to the authenticated QualityMax user.

No qmax CLI needed. qmax-code calls the QualityMax API directly.

Get your QualityMax API key at: https://app.qualitymax.io/settings

## Architecture

| File | Purpose |
|------|---------|
| `agent.go` | Claude API agentic loop — streaming, tool-use, history compression |
| `api_client.go` | REST client for the QualityMax cloud API |
| `auth.go` | Authentication — browser login, API key, OS keychain |
| `tools.go` | Tool definitions and ExecuteTool dispatcher |
| `terminal.go` | Output rendering, progress display, theme application |
| `theme.go` | Named color schemes and live-preview theme picker |
| `input.go` | Bubbletea TUI input model and slash-command menu |
| `queue.go` | Prompt queue — accepts input while the agent is running |
| `mcp_server.go` | MCP server mode (native tool-use, no Anthropic tokens consumed) |
| `ollama.go` / `ollama_agent.go` | Ollama local model provider and full-agent mode |
| `context.go` | SessionContext threaded through the agent |
| `main.go` | REPL, flag parsing, slash command handlers |

## Available tools

**Tests:** list_test_cases, list_scripts, generate_test_code, run_test, run_tests_batch, check_test_status

**Crawl:** start_crawl, crawl_status, crawl_results, list_crawl_jobs

**Repos:** list_repos, review_repo, repo_coverage, repo_quality

**Import:** import_repo, import_document

**PR:** create_pr

**Local:** read_file, write_file, run_command, run_local_test

## Gemma 4 on Cerebras (multimodal, ultra-fast)

qmax-code can drive its entire agent loop through **Google DeepMind's Gemma 4 31B** hosted on **Cerebras** — multimodal vision, native function-calling over the full tool set, and optional reasoning, at Cerebras inference speed. Every response surfaces the live `tokens/sec` and time-to-first-token straight from Cerebras's `time_info`, so the speed advantage is visible in the terminal.

```bash
# One-shot activation inside the REPL
> /gemma                 # backend→Cerebras, model→gemma-4-31b, reasoning: low
> /gemma high            # max thinking
> /gemma none            # fastest (reasoning off) — best for a pure speed demo
> /gemma off             # back to the Anthropic API

# Or pre-configure:
qmax-code config set backend cerebras
qmax-code config set cerebras_model gemma             # resolves to gemma-4-31b
qmax-code config set cerebras_reasoning_effort medium
```

**Signature demo — screenshot → Playwright test:** paste a picture of a web page and Gemma 4 reads the pixels (buttons, forms, navigation, the primary user flow) and generates a runnable Playwright e2e test, then runs it.

```
> /gemma
> /screenshot            # capture any web page → Gemma 4 generates + verifies a test
# or
> /paste                 # paste an image from clipboard
```

The multimodal path works because qmax converts image attachments into OpenAI `image_url` base64 data-URI parts (`internal/agent/cerebras.go`), which is exactly the format Cerebras accepts for Gemma 4. Env overrides: `CEREBRAS_API_KEY`, `CEREBRAS_MODEL`, `CEREBRAS_REASONING_EFFORT`.

## Requirements

- Go 1.24+ (for building from source)
- Anthropic API key (`ANTHROPIC_API_KEY`)
- QualityMax account (free at [qualitymax.io](https://qualitymax.io))
- qmax CLI is **optional** — qmax-code works standalone via REST API

## Auth

- Anthropic: set `ANTHROPIC_API_KEY`, pass `--anthropic-api-key`, or save it through the interactive key prompt.
- QualityMax: run `qmax-code login` for browser login, or `qmax-code login --api-key qm-YOUR-API-KEY`.
- QualityMax credentials are stored in `~/.qmax-code/auth.json` with `0600` permissions. Run `/disconnect` in the REPL to remove saved QualityMax auth.
- Anthropic keys saved by the prompt are stored in the OS keychain under the `qmax-code` service; remove them with your platform keychain tool, or use `ANTHROPIC_API_KEY` for session-only auth.
- Known credential patterns are redacted from API errors, command output, local test output, and optional telemetry before display or reporting.

## Local safety

qmax-code is a trusted local terminal agent. Tools such as `read_file`, `write_file`, `run_command`, and `run_local_test` can access your workspace or run local commands with your user permissions. See [SECURITY.md](SECURITY.md) for the trust model and local backup paths.

## Build

```bash
go build -o qmax-code .
```

## Cat personality

Max is a curious explorer, playful bug hunter, and proud test presenter. The agent channels this energy — helpful, occasionally catty, never forced.

```
  /\_/\
 ( o.o )   "knocks bugs off the table"
  > ^ <    "nine lives, zero regressions"
 /|   |\   "if it fits I sits, if it breaks I test it"
(_|   |_)  meow.
```

---

**Open-source CLI for the QualityMax platform. Licensed under [FSL-1.1-ALv2](LICENSE) — free for non-competing use, converts to Apache 2.0 after 2 years.**

## Telemetry

`qmax-code` does not send anything off-machine by default. Crash and error reporting is **opt-in only** and requires both:

- `QMAX_CODE_TELEMETRY=1` — explicit opt-in toggle
- `QMAX_CODE_TELEMETRY_DSN=<sentry-dsn>` — destination DSN you control

When enabled, only structural metadata is sent: backend name, HTTP status codes, model identifiers, input lengths, image counts. Prompt content, file contents, LLM responses, and shell output are **never** transmitted — a `BeforeSend` sanitizer in `error_reporting.go` strips any tag whose name matches a prompt-shaped prefix as defense-in-depth.

To disable, unset either variable. To inspect what would be sent, set `QMAX_CODE_TELEMETRY_DSN` to a Sentry-compatible test endpoint you control.
