// Package skills defines the backend-neutral catalog of qmax QA skills and
// materializes them into the native skill directories of each supported CLI
// backend (Claude Code and Codex).
//
// Both Claude Code and Codex load "agent skills" from a folder containing a
// SKILL.md file with YAML frontmatter (name + description), auto-invoked when a
// user request matches the description. The two CLIs share that core format but
// diverge on the optional enrichment they understand:
//
//   - Claude Code reads an `allowed-tools:` frontmatter key to gate which tools
//     the skill may call.
//   - Codex ignores `allowed-tools`; it reads an optional sibling
//     `agents/openai.yaml` for UI metadata, MCP dependencies, and invocation
//     policy.
//
// A single Skill in this catalog is the source of truth. Materialize() emits
// the right SKILL.md (and, for Codex, openai.yaml) for whichever backend is
// being installed, so one definition stays in sync across both CLIs.
package skills

import (
	"embed"
	"fmt"
	"sort"
)

//go:embed catalog/*.md
var catalogFS embed.FS

// MCPDep declares an MCP server a skill depends on. It is surfaced to Codex via
// agents/openai.yaml so the harness can warn when the dependency is missing.
// Claude Code does not consume this directly (the qmax MCP is already wired in
// via the global install), but it documents intent in either backend.
type MCPDep struct {
	// Value is the MCP server identifier as registered in the CLI config,
	// e.g. "qmax".
	Value string
	// Description is a human-readable explanation of why the skill needs it.
	Description string
}

// Skill is one backend-neutral QA skill. The same definition renders to a
// Claude Code skill folder and a Codex skill folder.
type Skill struct {
	// Name is the skill slug: lowercase, hyphenated, unique. It is the folder
	// name and the token used to invoke the skill (`$name` in Codex).
	Name string
	// Description is the single most important field: both CLIs use it to decide
	// when to auto-invoke the skill, so it must describe what the skill does and
	// when to use it.
	Description string
	// ShortDescription is an optional <=64-char blurb for UI lists. Falls back to
	// Description when empty.
	ShortDescription string
	// AllowedTools gates tool access in Claude Code. Empty means "inherit all".
	AllowedTools []string
	// MCPDeps are the MCP servers the skill relies on, surfaced to Codex.
	MCPDeps []MCPDep
	// bodyFile is the embedded markdown body under catalog/.
	bodyFile string
}

// Body returns the markdown instructions for the skill (everything after the
// frontmatter). It panics on a missing embed because the catalog is compiled
// into the binary — a missing file is a build-time programming error, not a
// runtime condition.
func (s Skill) Body() string {
	data, err := catalogFS.ReadFile("catalog/" + s.bodyFile)
	if err != nil {
		panic(fmt.Sprintf("skills: embedded body %q missing: %v", s.bodyFile, err))
	}
	return string(data)
}

// qmaxDep is the dependency every catalog skill shares: the qmax MCP server
// that orch installs into both CLIs.
var qmaxDep = MCPDep{Value: "qmax", Description: "qmax QA tools (list, run, generate, review)"}

// Catalog is the full set of qmax QA skills shipped with qmax-code. Adding a
// skill is: drop a catalog/<name>.md body and append an entry here.
var Catalog = []Skill{
	{
		Name:             "migrate-to-playwright",
		Description:      "Migrate a test-automation framework from Cypress or Selenium to Playwright. Use when the user wants to port, convert, or migrate an existing E2E/UI test suite to Playwright, or modernize a legacy Cypress/Selenium project.",
		ShortDescription: "Port a Cypress/Selenium suite to Playwright",
		MCPDeps:          []MCPDep{qmaxDep},
		bodyFile:         "migrate-to-playwright.md",
	},
	{
		Name:             "qa-quality-gate",
		Description:      "Enforce a release quality gate on a code change: generate tests for the diff, run them, review results, and emit a pass/fail verdict with blocking findings. Use before merging or shipping, or when the user asks whether a change is safe to release.",
		ShortDescription: "Pass/fail release gate for a diff",
		MCPDeps:          []MCPDep{qmaxDep},
		bodyFile:         "qa-quality-gate.md",
	},
	{
		Name:             "sast-presurgery",
		Description:      "Run a focused static-analysis security pass on the code about to be changed, before the edit lands. Use when planning a change to security-sensitive code, or when the user wants vulnerabilities found pre-emptively rather than in post-merge review.",
		ShortDescription: "Pre-change SAST on the surgical site",
		// No MCPDeps: this is a pure static-analysis skill that reasons over the
		// diff and source; it does not drive the qmax test runner like the others.
		bodyFile: "sast-presurgery.md",
	},
	{
		Name:             "qa-triage",
		Description:      "Triage a failing test run or bug report into a diagnosis: real defect vs. flaky test vs. environment issue, with the next concrete action. Use when a test fails, a suite is red, a bug comes in, or the user asks why something is failing.",
		ShortDescription: "Diagnose a failure: defect vs flake vs env",
		MCPDeps:          []MCPDep{qmaxDep},
		bodyFile:         "qa-triage.md",
	},
}

// SortedCatalog returns the catalog ordered by name for deterministic output.
func SortedCatalog() []Skill {
	out := make([]Skill, len(Catalog))
	copy(out, Catalog)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
