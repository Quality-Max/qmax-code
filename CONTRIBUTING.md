# Contributing

Thanks for helping improve `qmax-code`.

## Development

Run the test suite before opening a PR:

```bash
go test ./...
```

If your local environment cannot write to the default Go build cache, use a
temporary cache:

```bash
GOCACHE=/tmp/qmax-code-gocache go test ./...
```

## Security-Sensitive Changes

Please read [SECURITY.md](SECURITY.md) before changing:

- auth or credential storage,
- telemetry/error reporting,
- `read_file`, `write_file`, `run_command`, or `run_local_test`,
- script healing, backup, or rollback behavior,
- API error handling or log output.

## Public Source Boundary

`qmax-code` is intended to be the public client/agent boundary. The main
QualityMax backend can remain closed source. Avoid adding backend implementation
details, private service names, unpublished roadmap behavior, or proprietary
scoring/review heuristics to this repository.

See [OPEN_SOURCE_SCOPE.md](OPEN_SOURCE_SCOPE.md) for the current readiness
checklist and API/tool surface classification.

## Pull Requests

- Keep changes focused.
- Add or update tests for behavior changes.
- Update README/security docs when user-facing behavior changes.
- Do not commit generated binaries, release archives, customer reports, local
  credentials, or test artifacts.

## License

`qmax-code` is released under the [Functional Source License, Version 1.1,
ALv2 Future License (FSL-1.1-ALv2)](LICENSE). By contributing, you agree that
your contributions will be licensed under the same terms. The license permits
all non-competing use and automatically converts to Apache 2.0 two years after
each release.
