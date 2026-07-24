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

**AI-powered terminal coding and QA agent.** Named after Max, the real cat who inspired it all.

qmax-code can work as a standalone local repository agent with no QualityMax
account, or connect to QualityMax for hosted QA workflows. In connected mode it
can manage projects and test cases, crawl sites, generate and run tests, review
repositories, heal scripts, and prepare CI. It calls the QualityMax API
directly, so the separate `qmax` CLI is optional.

Use the built-in agent with Anthropic, Cerebras, or Ollama, or use
**orchestration mode** to run Claude Code, Codex, or OpenCode with the same qmax
QA tools through MCP.

> **License:** Source-available under the
> [Functional Source License (FSL-1.1-ALv2)](LICENSE), created by
> [Sentry](https://fsl.software). It is free for non-competing use, including
> internal use, modification, contribution, education, research, and
> professional services. Each release converts to Apache 2.0 after two years.

## What qmax-code can do

- **Plan and manage QA work:** list and manage projects and test cases, enhance
  cases, find coverage gaps, and import requirements or repositories.
- **Generate and execute tests:** create Playwright, pytest, Go, and Rust tests;
  run browser tests in QualityMax, run Go/Rust tests on the native runner, or
  execute supported tests locally.
- **Crawl and inspect applications:** discover pages, generate test scenarios,
  analyze screenshots and page elements, and optionally watch test/crawl runs
  through a live terminal browser feed.
- **Review and ship:** analyze repositories for quality, coverage, security, and
  testing risk; honor saved review preferences; create test PRs; and generate a
  GitHub Actions test workflow.
- **Work on local repositories:** read, create, and edit files; search code; run
  allowlisted commands and tests. Standalone mode provides this lane without a
  QualityMax login.
- **Extend coding agents:** install 27 QA skills into Claude Code, Codex, and
  OpenCode, including accessibility, performance, security, dependency,
  usability, flaky-selector, and release-gate workflows.

Some advanced surfaces—k6, QTML, framework export/trigger operations, and
background-job health—remain experimental and are only exposed when
`QMAX_EXPERIMENTAL=1`.

## What is new in v1.21

- **Standalone local-only mode:** start with `--local` (or persist
  `local_only=true`) to skip QualityMax onboarding and expose only workspace
  file, command, and planning tools.
- **Exposure Receipts:** every session that makes an outbound LLM or QualityMax
  API request writes a signed local egress manifest that can be inspected and
  verified offline.
- **OpenCode backend:** opt in to Z.AI Coding Plan, Groq, or OpenRouter and pick
  their models from `/orch`; keys stay in the OS keychain.
- **Expanded orchestration:** one picker now covers the direct Anthropic API,
  Claude Code, Codex, Cerebras, OpenCode, and Ollama, with backend-specific
  model and reasoning/effort choices.
- **Cerebras and Gemma 4:** native function calling across the qmax tool set,
  multimodal input for Gemma 4, optional reasoning effort, and live speed
  metrics.
- **27 managed QA skills:** the catalog is refreshed into Claude Code, Codex,
  and OpenCode and can be inspected or reinstalled with `/skills`.
- **Improved terminal sessions:** a stable input panel, prompt queue, session
  status and cost metrics, compact/verbose output toggle, ten themes, saved
  sessions, and optional cloud sync.

See [CHANGELOG.md](CHANGELOG.md) for the complete release history.

## Install

```bash
curl -sL https://qualitymax.io/static/install-qmax-code.txt | bash
```

To build from source instead:

```bash
git clone https://github.com/Quality-Max/qmax-code.git
cd qmax-code
go build -o qmax-code .
./qmax-code --version
```

Go 1.24 or newer is required for source builds.

## Quick start

### Standalone local-only

Run qmax-code without a QualityMax account:

```bash
# Use an already authenticated coding-agent CLI:
qmax-code --local --backend codex
qmax-code --local --backend cc

# Or use the built-in agent with your selected inference provider:
qmax-code --local
```

The built-in path still needs an inference backend: an Anthropic or Cerebras
key, or a configured Ollama endpoint. `--local` means no QualityMax login,
project, or cloud request; it does not mean that a third-party model provider
is offline. Use Ollama on a local endpoint when you also want inference to stay
on your machine.

To make standalone mode the default:

```bash
qmax-code config set local_only true
qmax-code

# Return to QualityMax-connected startup:
qmax-code config set local_only false
```

Standalone qmax tools are deliberately limited to `read_file`, `edit_file`,
`write_file`, `run_command`, and the built-in agent's `update_plan`. QualityMax
projects, hosted tests, crawls, imports, cloud sessions, live feeds, and the
cloud-backed `run_local_test` workflow are unavailable. CLI backends may also
have their own native coding tools, governed by their permission settings.

### QualityMax-connected

Log in for cloud-backed tools:

```bash
qmax-code login
```

The browser flow is the default. You can instead use an API key from
[QualityMax Settings](https://app.qualitymax.io/settings):

```bash
qmax-code login --api-key qm-YOUR-API-KEY
```

Then start qmax-code and choose an inference backend:

```bash
qmax-code

# Inside the REPL:
> /orch
```

For the direct Anthropic backend, set a session-only key before launch:

```bash
export ANTHROPIC_API_KEY=YOUR_KEY
qmax-code
```

You can also provide a prompt directly:

```bash
qmax-code "crawl staging.myapp.com and generate e2e tests"
qmax-code -p "run all tests for project 42"
qmax-code --backend codex -p "review this repository's test strategy"
```

## Orchestration mode

`/orch` is qmax-code's unified **backend, model, and effort picker**. It is not
a separate model and it does not create several agents. It selects which
inference engine handles the conversation while keeping qmax-code as the host
for terminal UX, QualityMax context, and tools.

```text
you
 │
 ▼
qmax-code REPL ── /orch chooses one backend
 │
 ├─ built-in loop: Anthropic API / Cerebras / Ollama
 │
 └─ CLI agent: Claude Code / Codex / OpenCode
                    │
                    └─ embedded qmax MCP server
                         ├─ connected: QualityMax + local QA tools
                         └─ --local: workspace tools only
```

For CLI backends, qmax-code launches the selected agent as a subprocess and
serves qmax tools through its embedded MCP server. On first activation you
choose one of two permission levels:

- **Standard (recommended):** auto-approves reads, searches, status/diff
  inspection, common test runners, and qmax tools. File edits and destructive
  shell commands remain gated.
- **Unattended:** grants the CLI agent full file and shell autonomy. Use only in
  a trusted repository.

Claude Code and Codex also offer an optional global MCP installation. Accepting
it adds qmax to their user-level configuration so qmax QA tools are available
when those CLIs are launched outside qmax-code. Declining keeps the integration
scoped to qmax-code sessions. OpenCode uses a qmax-managed overlay config rather
than modifying the user's main OpenCode configuration.

Read [Orchestration mode](docs/ORCHESTRATION.md) for backend requirements,
provider setup, permission behavior, installed files, switching, and
troubleshooting.

> qmax-code orchestration is separate from Conductor's parallel-workspace
> orchestration. Conductor can run multiple isolated qmax-code development
> workspaces; `/orch` chooses the inference backend inside one qmax-code
> session.

## Backend guide

| Backend | Select with | Authentication | Notes |
| --- | --- | --- | --- |
| Anthropic API | `/api` or `/orch` | `ANTHROPIC_API_KEY` or OS keychain | Built-in agent loop; tool set follows connected vs. standalone mode. |
| Claude Code | `/cc` or `/orch` | Local Claude Code login | CLI subprocess; qmax tools arrive through MCP. Agent SDK usage may be separately metered by Anthropic. |
| Codex | `/codex` or `/orch` | Local Codex login | CLI subprocess using the user's OpenAI access; qmax tools arrive through MCP. |
| Cerebras | `/gemma`, `/orch`, or `--backend cerebras` | `CEREBRAS_API_KEY` or OS keychain | Built-in native function calling. Gemma 4 supports images and reasoning effort. |
| OpenCode | `/opencode` or `/orch` | Per-provider key in OS keychain | CLI subprocess for opt-in Z.AI, Groq, and OpenRouter providers. |
| Ollama | `/ollama` or `/orch` | Configured Ollama endpoint | Self-hosted inference; configure the URL and model first. |

QualityMax authentication is independent of model-provider authentication. You
can run any backend with `--local` and no QualityMax login. Without `--local`,
qmax-code starts the QualityMax onboarding flow when no supported QualityMax
connection is available. Cloud projects, crawls, hosted test runs, imports, and
repository analysis always require connected mode.

## QA skills

qmax-code ships 27 agent skills:

- **QA workflows:** migration to Playwright, release quality gates,
  pre-change SAST, and failure triage.
- **Static review:** diff risk, secrets, dependencies, dead code, complexity,
  error handling, test quality, and flaky selectors.
- **Browser/runtime audits:** accessibility, broken links, cold-load waterfall,
  console errors, cookies/privacy, Core Web Vitals, form validation, i18n/RTL,
  mixed content, page weight, responsive screenshots, security headers, SEO,
  third-party bloat, and UI/UX.

Use these commands from the REPL:

```text
/skills          Show every skill and its Claude Code/Codex/OpenCode status
/skills install  Refresh the catalog in all supported CLI backends
```

Browser/runtime skills also require a Playwright MCP server in the consuming
agent. qmax-code declares that dependency in the Codex skill metadata.

## Useful commands

```text
/orch                    Pick backend, model, and effort
/providers               List opt-in OpenCode providers
/providers enable groq   Store a provider key and enable its models
/skills                   Show managed QA skills and install status
/live on                  Stream eligible test/crawl browser runs in the terminal
/feed                     Reopen the latest live browser feed
/sessions                 Pick a saved session to resume
/queue <prompt>           Add follow-up work; typing during a turn also queues it
/theme                    Preview and select a terminal theme
/cost                     Show token usage and estimated cost
/config                   Show session configuration
/help                     Show the full in-app command reference
```

See [Command reference](docs/COMMANDS.md) for subcommands, flags, REPL commands,
configuration keys, and keyboard shortcuts.

## Sessions and automation

Interactive sessions auto-save by default. For a one-shot command, use
`--save-session` on the built-in backends if you want it available through
`--resume last`:

```bash
qmax-code --save-session -p "review the current diff"
qmax-code --resume last
qmax-code --list-sessions
```

Claude Code, Codex, and OpenCode manage their own native CLI session and resume
state. qmax-code mirrors successful interactive turns into its in-memory
history, but `--save-session` does not replace a CLI backend's native resume
mechanism.

Cloud session sync is opt-in:

```text
/cloudsync
/set cloud_sync true
/set cloud_sync false
```

Cloud session sync is unavailable in standalone local-only mode. Local session
save/resume and prompt queues continue to work.

## Live browser feed and images

Turn on `/live` to request QualityMax Cloud Sandbox execution for eligible
browser tests and AI crawls. qmax-code displays the stream in the terminal and
keeps the latest feed available through `/feed`.

`/live`, `/feed`, and `/browserfeed` are connected-mode commands and are
unavailable in standalone local-only mode.

Use `/screenshot` to capture a screen and `/paste` to attach clipboard text or
an image. Image attachments are supported by the built-in multimodal path
(including Gemma 4 on Cerebras); CLI subprocess backends currently receive text
only.

## Exposure Receipts

When a session makes outbound requests, qmax-code writes a signed manifest
under `~/.qmax-code/receipts/`. The receipt records LLM and cloud-API egress
without storing prompt bodies, file contents, model responses, shell output, or
credential values.

```bash
qmax-code receipt list
qmax-code receipt show latest
qmax-code receipt verify latest
```

Offline verification proves that the receipt was produced by this agent's
local signing key; it is provenance evidence, not proof that every possible
network path was disclosed. Cross-check receipts against your own egress logs
when that assurance matters.

## Authentication and credential storage

- QualityMax browser/API-key credentials are stored in
  `~/.qmax-code/auth.json` with `0600` permissions. `/disconnect` removes them.
- Anthropic and Cerebras keys saved through qmax-code are stored in the OS
  keychain. Environment variables remain available for session-only or CI use.
- OpenCode provider keys are opt-in per user and stored in the OS keychain.
  Disabling a provider hides it but keeps its key so it can be re-enabled.
- `qmax-code codex connect` starts a fresh Codex OAuth login and securely
  attaches it to the authenticated QualityMax user.
- `qmax-code cc connect` reads the active local Claude Code credentials and
  securely attaches them to the authenticated QualityMax user.
- Known credential patterns are redacted from API errors, command output,
  local test output, and optional telemetry.

## Local safety

qmax-code is a trusted local terminal agent. Local file and command tools run
with your user permissions in the current workspace; they are not a sandbox.
CLI orchestration in unattended mode is broader still and can edit files, run
arbitrary commands, and push commits.

Standalone mode prevents qmax-code from loading QualityMax credentials or
exposing QualityMax tools. It does not sandbox the selected inference backend
or suppress that backend's own network traffic.

Read [SECURITY.md](SECURITY.md) before using local execution or unattended
orchestration in an unfamiliar repository.

## Telemetry

qmax-code does not send telemetry off-machine by default. Crash and error
reporting requires both:

```bash
export QMAX_CODE_TELEMETRY=1
export QMAX_CODE_TELEMETRY_DSN=YOUR_SENTRY_COMPATIBLE_DSN
```

When enabled, reporting is limited to structural metadata such as backend,
status code, model identifier, input length, and image count. Prompt content,
file contents, LLM responses, credentials, and shell output are not included.
Unset either variable to disable reporting.

## Architecture

| Path | Purpose |
| --- | --- |
| `main.go` | Process entry, flags, subcommands, login, backend startup, and one-shot mode |
| `internal/repl/repl.go` | Interactive REPL, slash commands, backend switching, queue, and live feed |
| `internal/agent/agent.go` | Built-in streaming agent loop, tool use, routing, and history compression |
| `internal/agent/tools.go` | Tool schemas, safety gates, dispatch, local execution, and healing |
| `internal/agent/{cc,codex,opencode}_agent.go` | CLI orchestration backends |
| `internal/agent/{cerebras,ollama}_agent.go` | Built-in alternative inference backends |
| `internal/api/` | QualityMax client, auth, provider registry, models, and persistent config |
| `internal/mcp/server.go` | Embedded stdio MCP server for CLI backends |
| `internal/setup/orch.go` | MCP registration and QA-skill installation |
| `internal/skills/` | Backend-neutral 27-skill catalog and materialization |
| `internal/session/` | Local/cloud sessions and prompt queue |
| `internal/tui/` | Terminal rendering, input, themes, media, and model pickers |
| `internal/httpx/` and `receipt.go` | Guarded outbound HTTP and Exposure Receipt integration |

## Development

```bash
go build -o qmax-code .
go test ./...
go vet ./...
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for architecture, development workflow,
testing, security-sensitive areas, and pull-request expectations.

## Cat personality

Max is a curious explorer, playful bug hunter, and proud test presenter.
Use `--professional` or `/set professional true` when you prefer a direct,
personality-free response style.

```text
  /\_/\
 ( o.o )   "knocks bugs off the table"
  > ^ <    "nine lives, zero regressions"
 /|   |\   "if it fits I sits, if it breaks I test it"
(_|   |_)  meow.
```

---

**Source-available CLI for the QualityMax platform. Licensed under
[FSL-1.1-ALv2](LICENSE), free for non-competing use and converting to Apache
2.0 after two years.**
