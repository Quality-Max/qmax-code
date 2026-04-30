# Security Policy

## Trusted Local Agent Model

`qmax-code` is a local terminal agent. When you run it, it can act with the
same filesystem and process permissions as your user account.

The following tools are intentionally powerful:

- `read_file` reads local files requested by the agent.
- `write_file` writes files inside the current working directory.
- `run_command` runs allowlisted local commands through the shell.
- `run_local_test` downloads test code from QualityMax, executes supported test
  frameworks locally, and reports the result back to QualityMax.

These features are designed for trusted development workspaces. They are not a
remote sandbox, container boundary, or permission system.

## Credential Handling

- QualityMax credentials are stored in `~/.qmax-code/auth.json` with `0600`
  permissions. Use `/disconnect` to remove saved QualityMax auth.
- Anthropic keys saved by the interactive prompt are stored in the OS keychain
  under the `qmax-code` service. You can also use `ANTHROPIC_API_KEY` for
  session-only auth.
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

## Script Backups

When qmax-code updates a QualityMax automation script, it stores a local backup
under:

```text
~/.qmax-code/script-backups
```

Review and remove old backups if they contain sensitive test code.

## Reporting Vulnerabilities

Before the public release process is finalized, report vulnerabilities directly
to the repository maintainers. Do not file public issues for active credential
leaks or exploitable vulnerabilities.
