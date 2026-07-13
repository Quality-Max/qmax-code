// Package skills defines the backend-neutral catalog of qmax QA skills and
// materializes them into the native skill directories of each supported CLI
// backend (Claude Code, Codex, and opencode).
//
// All three CLIs load "agent skills" from a folder containing a SKILL.md file
// with YAML frontmatter (name + description), auto-invoked when a user request
// matches the description. They share that core format but diverge on the
// optional enrichment they understand:
//
//   - Claude Code reads an `allowed-tools:` frontmatter key to gate which tools
//     the skill may call.
//   - Codex ignores `allowed-tools`; it reads an optional sibling
//     `agents/openai.yaml` for UI metadata, MCP dependencies, and invocation
//     policy.
//   - opencode recognizes only name + description in frontmatter (plus optional
//     license/compatibility/metadata); it ignores `allowed-tools` and has no
//     sibling config. (opencode also auto-discovers ~/.claude/skills, but
//     materializing into its native ~/.config/opencode/skills keeps the catalog
//     authoritative for opencode-only users.)
//
// A single Skill in this catalog is the source of truth. Materialize() emits
// the right SKILL.md (and, for Codex, openai.yaml) for whichever backend is
// being installed, so one definition stays in sync across all three CLIs.
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

// playwrightDep is the dependency for the browser/runtime QA skills imported
// from free-qa-skills: they drive a live page load via the Playwright MCP.
var playwrightDep = MCPDep{Value: "playwright", Description: "Playwright MCP for live browser automation"}

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
	// The skills below are pure static-analysis / review passes: like
	// sast-presurgery, they reason over the diff and source and do not drive the
	// qmax test runner, so they declare no MCPDeps and work in either backend.
	{
		Name:             "diff-risk-review",
		Description:      "Review the current git diff for correctness, security, and performance risk before it is committed, producing severity-ranked findings with file:line and a fix. Use before committing or opening a PR, or when the user asks what could be wrong with their changes.",
		ShortDescription: "Risk-review the uncommitted diff",
		bodyFile:         "diff-risk-review.md",
	},
	{
		Name:             "secret-scan",
		Description:      "Scan the working tree or git diff for hardcoded secrets: API keys, tokens, private keys, connection strings, and cloud credentials across common providers. Use before committing, or when the user worries a credential may have leaked into the code.",
		ShortDescription: "Find hardcoded secrets before they're committed",
		bodyFile:         "secret-scan.md",
	},
	{
		Name:             "dependency-audit",
		Description:      "Audit project dependencies for risk: known-vulnerable versions, unpinned ranges, abandoned packages, and badly outdated majors, across npm, pip, Go, Cargo, and Bundler. Use when the user asks about dependency risk, outdated packages, or supply-chain safety.",
		ShortDescription: "Audit deps for vulns and rot",
		bodyFile:         "dependency-audit.md",
	},
	{
		Name:             "dead-code-scan",
		Description:      "Find dead code in a repository: unused exports, unreferenced files, unreachable branches, and unused imports, each with a safe-to-remove confidence. Use when the user wants to clean up, asks what can be deleted, or is reducing maintenance surface.",
		ShortDescription: "Find unused exports, files, and branches",
		bodyFile:         "dead-code-scan.md",
	},
	{
		Name:             "complexity-hotspots",
		Description:      "Rank the most complex code in a repository: long functions, deep nesting, high cyclomatic complexity, and god-files, worst-first with a refactor for each. Use when the user wants to find refactor targets or asks which code is hardest to maintain.",
		ShortDescription: "Rank refactor hotspots by complexity",
		bodyFile:         "complexity-hotspots.md",
	},
	{
		Name:             "error-handling-audit",
		Description:      "Audit a repository or diff for weak error handling: swallowed exceptions, bare catches, floating promises, missing network timeouts/retries, and errors logged but not surfaced. Use when reviewing reliability, or when failures seem to disappear silently.",
		ShortDescription: "Find swallowed errors and missing handling",
		bodyFile:         "error-handling-audit.md",
	},
	{
		Name:             "test-quality-review",
		Description:      "Review an existing test suite for quality rather than coverage: assertion-free tests, weak assertions, skipped/only tests, over-mocking, and missing edge cases. Use when the user asks whether their tests actually test anything, or wants a test-suite quality audit.",
		ShortDescription: "Audit whether tests actually assert anything",
		bodyFile:         "test-quality-review.md",
	},
	{
		Name:             "flaky-selector-scan",
		Description:      "Scan a UI test suite (Playwright, Cypress, Selenium) for brittle locators such as nth-child, absolute XPath, and generated class hashes, and suggest stable role/data-test replacements. Use when UI tests keep breaking on redesigns or the user asks why their tests are flaky.",
		ShortDescription: "Find brittle UI locators, suggest stable ones",
		bodyFile:         "flaky-selector-scan.md",
	},

	// Browser / runtime QA skills imported from Quality-Max/free-qa-skills.
	// Unlike the static-analysis skills above, these drive a live page load and
	// declare a Playwright MCP dependency (surfaced to Codex via openai.yaml).
	{
		Name:             "accessibility-check",
		Description:      "Quick WCAG accessibility scan of any URL — color contrast, missing alt text, keyboard navigation, ARIA labels, heading hierarchy, and focus indicators — producing a graded report. Use when the user wants to check a page's accessibility or WCAG compliance.",
		ShortDescription: "WCAG accessibility scan of a URL",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "accessibility-check.md",
	},
	{
		Name:             "broken-link-scan",
		Description:      "Find broken links on any website: crawl the page and check every link for 404s, redirects, and timeouts, reporting each dead link with its location. Use when the user wants to find broken or dead links on a site.",
		ShortDescription: "Find broken links (404s, redirects) on a site",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "broken-link-scan.md",
	},
	{
		Name:             "cold-load-waterfall",
		Description:      "Profile a cold, cache-empty page load and build a text request waterfall — longest-pole requests, time to first byte, and time to interactive — flagging critical-path bottlenecks. Use when the user wants to know why a page loads slowly on first visit.",
		ShortDescription: "Cold-load request waterfall + bottlenecks",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "cold-load-waterfall.md",
	},
	{
		Name:             "console-error-scan",
		Description:      "Detect JavaScript errors, warnings, and failed network requests on any page by navigating the URL and collecting console output and network failures. Use when the user wants to find runtime JS errors or failing requests on a page.",
		ShortDescription: "Catch JS errors + failed requests on a page",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "console-error-scan.md",
	},
	{
		Name:             "cookie-privacy-scan",
		Description:      "Audit a site's cookies and trackers — inventory every cookie, flag missing Secure/HttpOnly/SameSite, list third-party trackers, and detect tracking that fires before consent. Use when the user wants a cookie, tracker, or consent privacy audit.",
		ShortDescription: "Audit cookies, trackers, consent timing",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "cookie-privacy-scan.md",
	},
	{
		Name:             "core-web-vitals",
		Description:      "Measure Core Web Vitals on any URL — LCP, CLS, INP, TTFB, FCP — using the browser's own performance APIs, grading each against Google's thresholds into an A–F report. Use when the user wants to measure page performance or Core Web Vitals.",
		ShortDescription: "Measure Core Web Vitals (LCP/CLS/INP) A-F",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "core-web-vitals.md",
	},
	{
		Name:             "form-validation-scan",
		Description:      "Probe the forms on a page for validation gaps — missing required-field enforcement, no client-side validation, malformed input accepted, and absent error messaging — reporting per-field findings. Use when the user wants to test form validation.",
		ShortDescription: "Probe forms for validation gaps",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "form-validation-scan.md",
	},
	{
		Name:             "i18n-rtl-audit",
		Description:      "Audit a page for internationalization readiness — layout breaks under long translations, RTL rendering issues, hardcoded UI strings, and missing lang/dir attributes. Use when the user wants an i18n, localization, or RTL readiness review.",
		ShortDescription: "i18n/RTL readiness audit of a page",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "i18n-rtl-audit.md",
	},
	{
		Name:             "mixed-content-scan",
		Description:      "Scan an HTTPS page for mixed content — HTTP scripts, styles, images, iframes, and insecure form actions — separating browser-blocked active mixed content from passive, with the offending URLs. Use when the user wants to find insecure mixed content on an HTTPS page.",
		ShortDescription: "Find HTTP mixed content on an HTTPS page",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "mixed-content-scan.md",
	},
	{
		Name:             "page-weight-budget",
		Description:      "Audit a page's weight against a performance budget — total transfer bytes, request count, render-blocking JS/CSS, and oversized or uncompressed images — producing a pass/fail report. Use when the user wants to check page weight or enforce a performance budget.",
		ShortDescription: "Check page weight vs a perf budget",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "page-weight-budget.md",
	},
	{
		Name:             "responsive-screenshots",
		Description:      "Screenshot any URL at 5 viewport sizes — mobile, tablet, laptop, desktop, and ultrawide — saving PNGs and reporting layout issues. Use when the user wants responsive screenshots or to check how a page renders across screen sizes.",
		ShortDescription: "Screenshot a URL at 5 viewport sizes",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "responsive-screenshots.md",
	},
	{
		Name:             "security-headers-check",
		Description:      "Check HTTP security headers on any URL — CSP, HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy — producing an A–F report with specific missing or misconfigured headers. Use when the user wants to audit a site's security headers.",
		ShortDescription: "Grade HTTP security headers A-F",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "security-headers-check.md",
	},
	{
		Name:             "seo-check",
		Description:      "Quick SEO health check of any URL — meta tags, headings, image alts, structured data, open graph, and common SEO issues. Use when the user wants an SEO audit or to check a page's search-engine readiness.",
		ShortDescription: "SEO health check of a URL",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "seo-check.md",
	},
	{
		Name:             "third-party-bloat",
		Description:      "Inventory third-party scripts on a page — analytics, tag managers, chat widgets, ads, A/B tools — and rank them by transfer size and main-thread cost, flagging the heaviest. Use when the user wants to find what third-party scripts are slowing a page down.",
		ShortDescription: "Rank third-party script bloat by cost",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "third-party-bloat.md",
	},
	{
		Name:             "ui-ux-scan",
		Description:      "Quick UI/UX deficiency scan of any page — touch targets, contrast, font consistency, spacing, empty states, loading indicators, and common UX anti-patterns. Use when the user wants a UI/UX review or to find usability problems on a page.",
		ShortDescription: "Scan a page for UI/UX deficiencies",
		MCPDeps:          []MCPDep{playwrightDep},
		bodyFile:         "ui-ux-scan.md",
	},
}

// SortedCatalog returns the catalog ordered by name for deterministic output.
func SortedCatalog() []Skill {
	out := make([]Skill, len(Catalog))
	copy(out, Catalog)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
