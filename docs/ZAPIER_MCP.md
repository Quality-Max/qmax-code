# Zapier MCP through Codex

This branch adds an opt-in, read-only Zapier connection for the Codex backend.
It uses the installed Codex CLI for OAuth, credential storage, and MCP transport.
It does not require a QualityMax account. Other qmax-code backends have not been
qualified for this connection.

## Connect

Use an already authenticated Codex CLI. The implementation is being qualified
with `codex-cli 0.144.5`; status checks record the actual client version.

```sh
qmax-code zapier connect
qmax-code zapier status
qmax-code --local --backend codex
```

Complete OAuth in the browser opened by Codex. Choose the intended account and
app actions in Zapier yourself. Enabling an app action can expand what that
connection can access; qmax-code does not enable actions or manage connections.
Ask the agent to read one specific requirement or spreadsheet range, and retain
its source identifier and revision when drafting tests.

The selected Codex home is `$CODEX_HOME`, otherwise `~/.codex`. qmax setup and
runtime registration use that same home. A fresh home gets keyring-only MCP
OAuth storage. An existing configuration must already explicitly select
`mcp_oauth_credentials_store = "keyring"`. qmax refuses to change an existing
credential-storage policy because other connections may depend on it. Check
those connections before changing the policy yourself, or choose a fresh home:

```sh
export CODEX_HOME="$HOME/.codex-qmax-zapier"
mkdir -p "$CODEX_HOME"
qmax-code zapier connect
```

A separate home may also need its own Codex model login. Do not copy credential
files between homes. A separate home isolates configuration, not necessarily
MCP OAuth credentials. In Codex 0.144.5, keyring entries are keyed by server name
and URL, independently of `CODEX_HOME`. Logging out or revoking the managed
`zapier` connection can therefore affect other Codex homes using that entry.
Use a separate provider/client identity for destructive authentication tests;
do not treat a fresh home as sufficient credential isolation. See the
[qualified Codex credential-key implementation](https://github.com/openai/codex/blob/rust-v0.144.5/codex-rs/rmcp-client/src/oauth.rs).

If the OS keyring is unavailable, authentication fails;
qmax does not fall back to plaintext MCP tokens. OAuth output belongs in your
terminal, not in receipts or issue comments.

## Scope and configuration preservation

The managed server uses the public OAuth endpoint
`https://mcp.zapier.com/api/v1/connect` and enables exactly these tools:

- `inspect_zapier_actions`
- `execute_zapier_read_action`

Write dispatch, action enablement, and connection management are excluded from
this server's Codex tool allowlist. Existing app actions in Zapier are unchanged.
Custom Zapier configuration is refused rather than overwritten. Other server
tables, including qmax and 9lives, retain their source text. Updates are atomic
and serialized between qmax processes using a retained `config.toml.qmax.lock`
sidecar. Editors and other programs that do not use this lock can still race
with qmax configuration updates.

The allowlist applies to this managed server. Check for other direct Zapier
entries in your chosen home before claiming that an entire agent session is
restricted. A local coding agent can also execute commands and edit files;
this integration does not sandbox an untrusted local agent or replace Codex's
own approval and sandbox settings.

Business documents and tool results are untrusted data. The Codex session prompt
instructs the agent to ignore embedded commands, credential requests, requests
to expand access, and requests to send data elsewhere. This instruction is not
a deterministic authorization broker.

## Inspect and disconnect

`qmax-code zapier status` performs structural MCP discovery without a model turn
or app action. It reports qmax/Codex versions, recognized server names, auth
states, and tool names. It omits provider descriptions, arguments, URLs, and raw
errors. Discovery rejects interactive requests and unexpected Zapier tools.
Missing authentication, missing tools, connection failure, or an incompatible
client produces a nonzero exit. An authenticated inventory does not prove that
a selected requirement was read or that an expired token will refresh.

```sh
qmax-code zapier disconnect
```

Disconnect disables the managed server for new Codex sessions. End active
sessions to unload their tools. It does not revoke OAuth or disable app actions;
use `codex mcp logout zapier` in the same home to remove stored OAuth.
This removes local credentials, rather than proving provider-side revocation
or token-expiry handling. The shared-keyring limitation above still applies.

## Qualification and remaining epic work

QUA-2406 covers connection, coexistence, scoped tools, and one agent-mediated
source read. Local tests cover preservation, invalid/custom configuration,
credential-policy refusal, concurrent disconnect, discovery protocol failures,
missing authentication/tools, cancellation, and refusal of approval requests.
Mock discovery is distinct from live qualification. Provider-side OAuth
revocation and subsequent reconnect were verified on 2026-11-16. After the
user-authorized revocation, Codex 0.144.5 retained the `oAuth` status but
returned no Zapier tools; qmax exited nonzero. Its diagnostic includes a
reconnect instruction because a cached auth label does not establish current
access. After native OAuth login, status exited zero and discovered exactly
the two allowed tools alongside qmax and 9lives. No app action ran during this
cycle. Natural token expiry and automatic refresh were not exercised.

Local live discovery on 2026-11-16 used qmax `dev-qua-2406`, Codex 0.144.5,
and checksum-verified 9lives 0.6.0 in a separate configuration home. The actual catalog
contained four standalone qmax MCP tools and seven 9lives tools. Zapier reported
`notLoggedIn` with no tools, and status exited nonzero with an authentication
instruction. After the user completed OAuth, native discovery reported `oAuth`
and exactly the two permitted Zapier tools alongside qmax and 9lives. A native
`qmax-code --local --backend codex` agent turn then executed Google Sheets
`get_data_range` for one selected `Sheet1!A4:K4` range and returned one row.
No business-app write or new test run was performed. The returned row's
completed/passed result is historical demo data. Its source identifiers were
verified directly against the tool result; the agent's final summary omitted
fields and confused an upstream GitHub revision with a sheet revision, which
remains unknown. Deterministic provenance is still QUA-2408 work.

`9l doctor` reported missing Playwright workspace
dependencies at the Go repository root; no test-run readiness is claimed there.

QUA-2407 qualifies 9lives independently. QUA-2408 adds source provenance and
meaningful before/after test evidence. QUA-2409 adds an exact, durable approval
broker for result writes, including duplicate and ambiguous-outcome handling.
QUA-2410 captures final delivery evidence. None of these follow from discovery
alone. This branch contains no result-write command or production write broker.

The earlier private v1.40.2 demo used separate local scripts and a manually
approved broker. Its completed Sheets append must not be retried, and it is
not evidence that the released terminal agent includes this native integration.
