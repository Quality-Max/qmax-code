# Dependency Audit

Audit project dependencies for risk — known-vulnerable versions, unpinned
ranges, abandoned packages, and badly outdated majors.

## Workflow

1. **Detect the ecosystem** by manifest: `package.json`/lockfile (npm/pnpm/yarn),
   `requirements.txt`/`pyproject.toml` (pip), `go.mod` (Go), `Cargo.toml` (Rust),
   `Gemfile` (Ruby).
2. **Run the native auditor** when the toolchain is present and parse it:
   `npm audit --json`, `pip-audit -f json`, `go list -m -u all`, `cargo audit`.
   Fall back to reasoning over the manifest if no auditor is available.
3. **Classify** each dependency:
   - **Vulnerable** — flagged by the auditor (CVE/advisory).
   - **Unpinned** — `*`/`latest`/no lock entry; non-reproducible builds.
   - **Wide range** — `^`/`~` on a 0.x package, where any minor can break.
   - **Outdated** — several majors behind current.
   - **Abandoned** — archived upstream / no release in a long time.
4. **Report** worst-first (vulnerabilities lead), each with the concrete bump or
   replacement command and a note on breaking-change risk.

Distinguish "must fix now" (exploitable vulnerability) from "hygiene" (pin,
upgrade) so the reader knows what blocks a release.
