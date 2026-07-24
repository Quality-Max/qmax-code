# Security Policy

## Trusted Local Agent Model

`qmax-code` is a local terminal agent. When you run it, it can act with the
same filesystem and process permissions as your user account.

The following tools are intentionally powerful:

- `read_file` reads local files requested by the agent.
- `edit_file` makes exact replacements in files inside the current workspace.
- `write_file` writes files inside the current working directory.
- `run_command` runs allowlisted local commands through the shell.
- `run_local_test` downloads test code from QualityMax, executes supported test
  frameworks locally, and reports the result back to QualityMax.

These features are designed for trusted development workspaces. They are not a
remote sandbox, container boundary, or permission system.

## Standalone Local-Only Boundary

Start with `qmax-code --local`, or persist `local_only=true`, to run without a
QualityMax account. In this mode qmax-code does not load QualityMax credentials,
discover the legacy `qmax` CLI, start QualityMax cloud sessions, or expose
QualityMax-backed tools.

The built-in agent receives `update_plan`, `read_file`, `run_command`,
`edit_file`, and `write_file`. The MCP catalog omits `update_plan` because CLI
agents already provide native planning, leaving the four workspace tools.
Direct calls to undisclosed QualityMax tool names are rejected at execution
time as well as hidden during discovery.

`run_local_test` is intentionally excluded: it downloads test code from
QualityMax and reports results back. "Local-only" describes the QualityMax
service boundary, not all network traffic. Anthropic, Cerebras, Claude Code,
Codex, or OpenCode may still send prompts and repository context to their model
provider. A loopback Ollama endpoint is the self-hosted inference option.
CLI-agent native tools also remain governed by that agent's own permissions.

## Orchestration Permissions

`/orch`, `/cc`, `/codex`, and `/opencode` can launch a coding-agent CLI with
qmax tools supplied through MCP.

- **Standard** mode auto-approves reads, searches, repository inspection,
  common test runners, and qmax tools. Other actions remain subject to the
  selected CLI's permission checks.
- **Unattended** mode grants the selected CLI full file and shell autonomy.
  The agent can edit files, run arbitrary commands, and perform git operations.

Only use Unattended mode in a trusted, recoverable workspace. Neither mode
creates a sandbox or worktree boundary.

Claude Code and Codex can optionally receive a user-level qmax MCP entry in
`~/.claude/settings.json` or `~/.codex/config.toml`. That makes qmax tools
available whenever the CLI is launched, not only inside qmax-code. OpenCode
uses a separate qmax-managed overlay at `~/.qmax-code/opencode.json`.

qmax-code also installs managed QA skills into the selected CLI's user-level
skills directory. See [Orchestration mode](docs/ORCHESTRATION.md) for the
complete list of affected paths and scope choices.

## Credential Handling

- QualityMax credentials are stored in `~/.qmax-code/auth.json` with `0600`
  permissions. Use `/disconnect` to remove saved QualityMax auth.
- Anthropic keys saved by the interactive prompt are stored in the OS keychain
  under the `qmax-code` service. You can also use `ANTHROPIC_API_KEY` for
  session-only auth.
- Cerebras and opt-in OpenCode provider keys saved by qmax-code are stored in
  the OS keychain. Environment-variable overrides remain available for
  session-only or CI use.
- Disabling an OpenCode provider removes it from the model picker but retains
  its key for later reuse.
- Telemetry/error reporting is disabled by default. It only initializes when
  both `QMAX_CODE_TELEMETRY=1` and `QMAX_CODE_TELEMETRY_DSN` are set.
- Common credential patterns are redacted before API errors, command output,
  local test output, or optional telemetry are displayed or reported.

## Local Command Limits

`run_command` uses an executable allowlist and blocks shell control tokens such
as pipes, command substitution, redirection, and command chaining. This reduces
accidental damage, but it should not be treated as a security sandbox.

If you need to create or edit files, prefer the `write_file` tool path rather
than shell redirection.

## Exposure Receipts

Each qmax-code process that performs outbound LLM or cloud-API requests writes
a signed local manifest under:

```text
~/.qmax-code/receipts
```

Inspect and verify receipts with:

```bash
qmax-code receipt list
qmax-code receipt show latest
qmax-code receipt verify latest
```

Receipts record structural egress metadata and do not include prompt bodies,
file contents, LLM responses, shell output, or credential values. Offline
signature verification proves local provenance; it does not independently
prove completeness. Cross-check against network or proxy logs when complete
egress accounting is required.

## Script Backups

When qmax-code updates a QualityMax automation script, it stores a local backup
under:

```text
~/.qmax-code/script-backups
```

Review and remove old backups if they contain sensitive test code.

## Reporting Vulnerabilities

Please **do not** file public GitHub issues for security vulnerabilities.

Report vulnerabilities by emailing **strazhnyk@gmail.com**. Include a description
of the issue, steps to reproduce, and any relevant environment details. You will
receive a response within 48 hours. We ask that you give us reasonable time to
address the issue before any public disclosure.
