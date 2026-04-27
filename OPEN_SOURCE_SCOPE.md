# Open Source Readiness Scope

This note tracks the first-pass scope for preparing `qmax-code` for a public
repository. It separates code that is probably safe to publish from code that
needs product, security, or legal review before release.

## Intended Source Boundary

`qmax-code` can be public while the main QualityMax monorepo remains closed.
The public repository should be treated as the client/agent boundary; the
closed monorepo remains the product/backend boundary.

Public `qmax-code` may contain:

- Terminal UX, command parsing, install/build/release scripts, and docs.
- Client-side auth flows and cloud API calls that are intentionally supported.
- LLM orchestration, tool definitions, local execution helpers, and safety
  guards that define the CLI behavior.
- Provider adapters for Anthropic/Ollama and public configuration.

Closed QualityMax monorepo should retain:

- Crawl engine implementation, test generation services, repository analysis,
  scoring/coverage algorithms, billing/accounts, and backend authorization.
- Server-side prompts, ranking heuristics, private model routing, and any
  proprietary datasets or training/evaluation logic.
- Unreleased roadmap behavior and experimental endpoints not intended for
  public client support.

Before public release, every cloud route/tool should be classified as:
`public-core`, `public-cloud`, `experimental-gated`, or `private/remove`.

## Phase 0: Repository Hygiene

- Add an explicit `LICENSE`.
- Decide whether generated binaries and release archives stay out of git:
  `qmax-code` and `dist/` are already ignored, but local copies are present.
- Replace "closed-source companion" language in `README.md` when the license
  decision is made.
- Add public-facing `SECURITY.md`, `CONTRIBUTING.md`, and release policy.
- Review asset rights for `assets/max-the-cat.mp4` and any referenced README
  images before publishing.
- Keep generated/customer-specific reports out of source. The local
  `security-roast-report.md` is ignored and should not be part of the public
  repository.

## Phase 1: Secrets and Telemetry

Needs change before opening:

- `error_reporting.go` now requires `QMAX_CODE_TELEMETRY=1` and
  `QMAX_CODE_TELEMETRY_DSN` before initializing Sentry-compatible reporting.
  Before release, confirm this opt-in behavior is documented wherever binaries
  are distributed.
- `auth.go` stores QualityMax API credentials in `~/.qmax-code/auth.json`
  with `0600` permissions. `README.md` now documents storage and `/disconnect`
  cleanup.
- Known credential patterns are redacted from API errors, command output, local
  test output, and optional telemetry before display/reporting.

Likely okay with docs:

- Anthropic keys are loaded from env/keychain and are not stored in JSON.
- QualityMax API keys are sent as bearer tokens over HTTPS.

## Phase 2: API Surface and Product Logic

Needs product review:

- `api_client.go` publishes the complete client-side REST route map for
  QualityMax projects, crawls, repository review, k6, QTML, framework export,
  PR creation, and background jobs.
- `tools.go` publishes the LLM tool schema, capability descriptions, cost
  categories, and agent-facing workflow assumptions.
- `ImportRepo` no longer sends `training_consent` by default. It only sends
  `opt_in` or `opt_out` when the user/tool call explicitly provides one.

Initial API surface inventory:

- Public/core candidate: auth (`/api/me`, CLI login), projects, test cases,
  automation script list/get/update, generated test code, execution status.
- Product-review needed: repository import/review, PR creation, gap tests,
  review preferences, CI/CD setup, coverage/quality analytics.
- Advanced/beta-looking surface: k6 generation/conversion/reporting, QTML
  import/export/convert, framework export/install/run, deployment smoke tests,
  background job health, screenshot/element analysis.
- Local-agent surface: `read_file`, `write_file`, `run_command`,
  `run_local_test`. These are useful but should be documented as trusted-local
  agent powers, not as a remote sandbox.

Proposed launch classification:

| Surface | Suggested class | Notes |
| --- | --- | --- |
| Auth and config | public-core | Browser login, API-key login, env/keychain config. |
| Projects and test cases | public-core | Basic CRUD/listing is expected client behavior. |
| Automation scripts | public-core | List/get/update plus security scanning are core to the agent. |
| Test generation and execution | public-cloud | Public client can call closed generation/execution services. |
| Crawl | public-cloud | Acceptable if AI crawl is an advertised feature. |
| Repository import | public-cloud | Keep explicit consent and clear data-use docs. |
| Review, coverage, quality, gap tests | product-review | Reveals product positioning and analysis categories. |
| PR creation and CI/CD setup | product-review | Public if supported broadly; otherwise gate. |
| k6 | experimental-gated | Looks broad/beta; decide if ready for public support. |
| QTML | experimental-gated | Public only if QTML is meant as a stable format. |
| Framework export/install/run | experimental-gated | Useful, but exposes packaging assumptions. |
| Screenshot/page analysis | product-review | Clarify whether this is local model, Anthropic, or cloud-backed. |
| Background job health | private/remove | More operational than user-facing unless documented. |
| Local file/shell tools | public-core with warning | Must be documented as trusted local agent powers. |

Recommended default launch posture:

- Keep `public-core` and clearly documented `public-cloud` surfaces enabled.
- Hide `experimental-gated` tools from the default tool registry until each has
  public docs and support expectations.
- Remove or keep private any route/tool that exists only for operations,
  debugging, or backend health.

Phase 2 cleanup completed:

- Removed backend-internal service names from comments/user-facing messages.
- Replaced private implementation references with generic QualityMax API /
  execution API language.
- Added tests covering explicit training consent behavior.

Likely okay:

- A public CLI normally needs documented API paths or an SDK-style client.
  The question is not "hide all routes"; it is whether any route names reveal
  private roadmap, pricing, internal service architecture, or unsupported
  behavior.

## Phase 3: Local Execution and Safety

Needs security review:

- The agent exposes `read_file`, `write_file`, and `run_command` tools. There is
  some validation, and `SECURITY.md` now documents the trusted-local model.
- `runLocalTest` downloads test code from QualityMax and executes Python or
  Playwright locally, then reports results back. This is powerful and should be
  treated as a prominent security notice; `SECURITY.md` now calls this out.
- Shell validation now checks the first executable token and blocks shell
  control tokens such as pipes, command substitution, redirection, and chaining.
  This is still not a sandbox.
- Script healing stores backups under `~/.qmax-code/script-backups`; this is now
  documented in `SECURITY.md`.

Phase 3 cleanup completed:

- Added `SECURITY.md` with credential, telemetry, local command, local test, and
  script backup notes.
- Added command validation tests for prefix-confusion and shell-control tokens.
- Tightened `validateCommand` from prefix matching to executable-name matching.

## Phase 4: Prompt and Model Strategy

Needs product/legal review:

- `agent.go` contains the full system prompt and autonomous healing policy.
  This may be acceptable, but it is product-defining behavior and should be
  intentionally open-sourced rather than leaked accidentally.
- The system prompt includes product behavior: capability lanes, review
  preferences, discovery nudges, autonomous healing, retry limits, and code
  generation safety rules. Treat it as public product behavior if this repo is
  opened.

Phase 4 cleanup completed:

- Centralized Anthropic model IDs, API URL, and API version constants.
- Reused the central model constants for shorthand resolution and screenshot
  vision analysis.
- Replaced model-family-specific Ollama comments/status text with generic
  "local model" wording.
- Removed private host examples from Ollama configuration comments.

Likely okay with docs:

- Anthropic and Ollama provider adapters are client behavior and can be public.
- Prompt-based local-model tool dispatch is publishable as long as the enabled
  tool surface is intentionally classified in Phase 2.

## Phase 5: Public Release Prep

- Run a dependency/license review for `go.mod`.
- Add CI that works for forks without private QualityMax secrets.
- Split private release publishing from public release workflows.
- Decide whether public builds include telemetry, direct QualityMax cloud API,
  or only an OSS/local mode.
- Create an issue checklist for any intentionally deferred proprietary cleanup.

## Initial Risk Ranking

High:

- Public exposure of full REST/tool map without product review.
- Local execution of cloud-fetched test code.

Medium:

- Broad API/tool surface still needs product classification before public
  release.
- Workflows that reference private release tokens or QualityMax reporting.
- Telemetry distribution policy, now opt-in via env but still needs release
  notes/docs.
- Redaction is pattern-based; keep expanding tests as new credential formats are
  supported.

Low:

- Keychain/env handling for Anthropic keys.
- Split CLI auth flags: `--anthropic-api-key` for Anthropic, and
  `qmax-code login --api-key` for QualityMax.
- Ignored build artifacts present in the working tree.
- Missing public governance files.
