# Changelog

All notable changes to qmax-code. Versions follow [Semantic Versioning](https://semver.org/).

## [1.16.2] - 2026-05-08

### Changed
- Phase 2 of the package reorg: extracted `internal/api` (api client, auth, keychain, config). `UploadSessionMessages([]Message)` is now `UploadSessionEvents([]any)` — the api package no longer references `Message`. `LoginInteractive` / `LoginViaBrowser` moved to `interactive_setup.go` since they need TUI helpers. Defensively `url.QueryEscape` the server-supplied auth code in the cli-poll URL. No user-visible behavior change.

## [1.16.1] - 2026-05-07

### Changed
- Internal restructure: extracted `internal/security`, `internal/vnc`, and `internal/sysutil` out of the flat root `package main`. No user-visible behavior change. Sets up follow-up extractions (agent, tui, api, session, tools, mcp) once a shared `internal/core` package lands.

## [1.15.7] - 2026-05-04

### Fixed
- **Cloud session sync was silently dropping all conversation content.** `UploadSessionMessages` posted to `POST /api/agent-sessions/{id}/messages`, which the server doesn't expose (405). Every completed cloud session since 1.15.0 ended with `event_count: 0` and the auto-summary "No agent session events to summarize." Now posts to `/events` with the correct discriminated-union shape `{"events":[{"type":"message","payload":<msg>}, ...]}`. Verified live; server-generated summaries now reflect the actual conversation.

### Added
- `/cloudsync` slash command — native TUI toggle for cloud session sync (Enabled / Disabled), as an alternative to `/set cloudsync ...`. Persists to config and starts the cloud session immediately when enabled.

## [1.15.6] - 2026-05-04

### Fixed
- Write Codex MCP configuration as TOML so Codex CLI/cloud session uploads can read the generated MCP server setup correctly.

## [1.11.0] — 2026-04-16

### Added
- **Full Ollama agent mode** — Gemma handles everything including tool dispatch via prompt-based `<action>` blocks. 10 actions: list_projects, list_test_cases, list_scripts, run_test, start_crawl, review_repo, get_script, get_project_summary, check_test_status, create_pr.
- **Three switchable modes** — `/ollama` cycles: OFF → CHAT → FULL
- **Dual model support** — 4B for chat, 12B for tool dispatch (`ollama_agent_model`)
- **Tool intent detection** — `needsTools()` prevents hallucination in CHAT mode

### Fixed
- Version var for `-ldflags -X`, `/ollama` autocomplete, `/set` usage hint, context canceled on API calls, anti-hallucination prompt

## [1.10.0] — 2026-04-16

### Added
- **Ollama integration** — self-hosted LLM (Gemma 3 4B) as the cheap chat tier. When configured, conversational responses go to your GPU instead of Claude Haiku, saving API costs. Tool orchestration stays on Claude Sonnet.
- **Circuit breaker** — 3 consecutive failures trip a 120s cooldown, then transparent fallback to Claude
- **Runtime toggle** — `/set ollama on|off` to switch mid-session
- **Config support** — `ollama_url` and `ollama_model` via config file or env vars (`OLLAMA_BASE_URL`, `OLLAMA_MODEL`)

## [1.9.0] — 2026-04-16

### Added
- **Review preferences tools** — `get_review_preferences` and `set_review_preferences` now available as agent tools, letting users configure what the AI reviewer focuses on directly from the CLI
- **Capability lanes** — agent detects user persona (vibecoder, founder, pro dev, pro QA) and adapts tool suggestions and verbosity accordingly
- **Discovery nudges** — contextual hints that surface relevant QualityMax features based on what the user is doing

## [1.8.4] — 2026-04-15

### Fixed
- **Language-aware security scanner** (`scanCodeSecurity`) — the pre-fix
  version rejected any script not containing Playwright/Jest markers
  (`test(`, `describe(`, `it(`). This silently blocked every Go, Rust,
  and Python script update with "Security scan failed — No test() or
  describe() found". A live user hit this trying to heal Go tests in
  the Qmax Code project: every update attempt with valid Go (`func
  TestFoo(t *testing.T)`) returned the same error with no path forward.

  Now detects language (Go / Rust / Python / JS) from code shape and
  runs language-appropriate checks:
  - **Go**: `func Test|Benchmark|Fuzz|Example` as the test-declaration
    marker; blocks `os/exec`, `syscall`, `unsafe`, `exec.Command`.
  - **Rust**: `#[test]` / `#[tokio::test]` / `#[cfg(test)]` markers;
    blocks `std::process::Command`, `unsafe {}`.
  - **Python**: `def test_...` / `unittest.TestCase` / `pytest.fixture`
    markers; blocks `subprocess.*`, `eval()`, `exec()`, `os.system`,
    `__import__`.
  - **JS/Playwright**: unchanged — the original dangerous-patterns
    table only runs here now (it was already JS-specific, just wasn't
    scoped).

### Tests
- **13 new tests** in `security_language_test.go` covering:
  language detection for Go (package + testing.T/B/F, function
  prefixes), Rust (`#[test]` / `#[tokio::test]`), Python (pytest /
  unittest), JS (Playwright / Jest); regression tests that a valid
  Go/Rust/pytest file passes with zero violations; per-language
  dangerous patterns fire correctly (`os/exec`, `std::process::Command`,
  `subprocess`); cross-language false-positive guard (`process.env`
  in a Go comment doesn't trip the JS rule); `hasTestDeclaration`
  table covering every language's positive/negative shapes.

## [1.8.3] — 2026-04-14

### Added
- **`qmax-code config` subcommand** — `config show`, `config set KEY VALUE`, `config unset KEY`, `config reset`. Strict validation: rejects invalid frameworks, wrong int/bool types, unknown keys. Replaces the "hand-edit ~/.qmax-code/config.json" workflow called out in the PR #29 review.

### Changed
- **Enriched "framework not supported for local execution" error** — now explains *why* native frameworks run server-side (toolchain weight, `$GOPATH` / `$CARGO_HOME` pollution) so users understand the tradeoff without a support ticket.
- **`pruneToolUse` placeholder consolidation** — typed-vs-map placeholders now share a single `orphanPlaceholderText` constant via `orphanPlaceholderTyped()` / `orphanPlaceholderMap()` helpers. Closes the shape-drift risk flagged in the PR #29 review (adding a new `ContentBlock` field no longer risks the `[]interface{}` path diverging silently).
- **Added `empty-vs-missing framework` comment** in the `generate_test_code` dispatcher so future maintainers know the fallback-to-DefaultFramework semantics are intentional.

### Tests
- **5 new `stripOrphanedToolUse` tests** — empty history, multi-loop pairing, reverse orphan (tool_result without tool_use), nil content, trailing edge case.
- **4 new `detectProjectFramework` tests** — `go.sum` without `go.mod`, symlinked marker files, quadruple-framework priority, non-existent dir.
- **1 new forward-compat test** — loads a pre-1.8.0 config JSON (no `default_framework`) and verifies no crash + all legacy fields preserved + new field defaults to empty.
- **8 new `config set` tests** — happy path, bad value rejection, unset semantics, int parsing, bool forms (true/yes/1/on vs false/no/0/off/""), unknown-key rejection, disk persistence, `parseConfigBool` table.

### Docs
- **CHANGELOG.md added** — versioned changelog for the 1.7.0 → 1.8.x history.

## [1.8.2] — 2026-04-14

### Fixed (security + review findings from PR #29)
- **XSS in Console tab rendering** — `log.type` was unsanitized at 3 DOM sites; now whitelisted to `/^[a-z_-]{1,16}$/` with fallback to `"log"`. (Surfaces via qa-rag-app UI; client-side defense in depth.)
- **Error-code prefix round-trip** — `doRequest` now parses both FastAPI `{"detail": "..."}` and MCP `{"success": false, "error": "[CODE] ..."}` envelopes. `[NOT_FOUND]` / `[FORBIDDEN]` / `[BAD_REQUEST]` prefixes propagate intact so callers can parse intent when HTTP status alone isn't in scope.
- **Client-side framework allow-list** — `validateFramework` + `allowedFrameworks` whitelist. `GenerateTestCode` and `SetupCICD` short-circuit before posting if the framework value is invalid.
- **Wizard confirmation prompt** — first-run framework detection is no longer silently saved. Users explicitly pick "Yes, save it" vs "No, I'll pick per-call".
- **Fallback message on empty detection** — when the wizard can't identify a framework, it now says so and points at `--framework` / config.json instead of silently moving on.
- **Orphaned `tool_use` stripper** (`stripOrphanedToolUse`) — fixes the `API error 400: messages.N.content: Input should be a valid list` crash that hit live sessions when tools failed or users interrupted mid-loop. Detects assistant messages with `tool_use` blocks whose matching `tool_result` isn't in the next message, prunes them (keeping text), inserts a placeholder when stripping would leave the message empty.
- **`runLocalTest` doc comment** — clarifies the pytest/playwright=local vs rust_cargo/go_test=server dispatch semantics.

### Added
- **`run_native_test` MCP tool** — executes Rust (`cargo test`) / Go (`go test -json ./...`) automation scripts via `POST /api/automation/execute` and returns a normalized result (status, passed/failed/total, console_logs, test_output, test_errors).
- **`setup_cicd` MCP tool** — creates a GitHub Actions workflow PR on the linked repo. Auto-detects the framework from the repo's language analysis; for Rust, auto-detects apt packages from Cargo.lock.
- **`framework` param on `generate_test_code`** — previously hardcoded Playwright; now accepts `playwright / pytest / rust_cargo / go_test` and defaults to `DefaultFramework` from config when omitted.
- **First-run wizard framework detection** — `detectProjectFramework` checks cwd for `Cargo.toml`, `go.mod`, `playwright.config.*`, `pyproject.toml` / `pytest.ini` / `tox.ini`. Priority: Rust > Go > Playwright > pytest.
- **`qmax-code config` subcommand** — `config show`, `config set KEY VALUE`, `config unset KEY`, `config reset`. Edit `default_framework`, `default_project`, `default_model`, `professional`, `auto_save`, `max_token_budget` without hand-editing JSON.
- **Enriched "framework not supported for local execution" error** — now explains why native frameworks run server-side (toolchain weight + `$GOPATH` / `$CARGO_HOME` pollution avoidance) and points at `run_native_test`.

### Tests
- **11 new HTTP tests** (`api_client_native_test.go`) — httptest.Server-based coverage of `RunNativeTest`, `SetupCICD`, `GenerateTestCode(framework)` body shapes + error prefix round-trip.
- **6 + 5 new tests** for `stripOrphanedToolUse` (paired no-op, orphan strip, placeholder insertion, `[]interface{}` shape, partial match, trailing, empty history, multi-loop, reverse orphan, nil content).
- **14 + 4 new tests** for `detectProjectFramework` (every marker file, polyglot priority, triple priority, symlinked marker, `go.sum` without `go.mod`, non-existent dir).
- **Config round-trip forward-compat test** — loads a pre-1.8.0 config JSON (missing `default_framework`) and verifies upgrade doesn't crash or lose existing fields.

### Related
- Client-side counterpart of Quality-Max/qamax-rag-app#387 (server-side MCP tools).
- Follow-up on the diff-analysis prompt guards from Quality-Max/qamax-rag-app#385.

## [1.8.1] — 2026-04-14

Internal patch — subsumed by 1.8.2. Shipped only the orphaned `tool_use` stripper to unblock a live session; the full review fix cycle landed in 1.8.2.

## [1.8.0] — 2026-04-14

Internal patch — subsumed by 1.8.2. Shipped the initial Rust/Go support (new tools, API methods, wizard detection).

## [1.7.0] — 2026-04-09

Previous release. Session stability, batch arrays, project lookup, terminal fixes.
