package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Dangerous patterns that should never appear in test code
var dangerousPatterns = []struct {
	pattern *regexp.Regexp
	reason  string
}{
	{regexp.MustCompile(`require\s*\(\s*['"]child_process['"]\s*\)`), "child_process import — arbitrary command execution"},
	{regexp.MustCompile(`require\s*\(\s*['"]fs['"]\s*\)`), "fs import — file system access"},
	{regexp.MustCompile(`require\s*\(\s*['"]net['"]\s*\)`), "net import — raw network access"},
	{regexp.MustCompile(`require\s*\(\s*['"]http['"]\s*\)`), "http import — raw HTTP access (use Playwright's request API instead)"},
	{regexp.MustCompile(`require\s*\(\s*['"]https['"]\s*\)`), "https import — raw HTTPS access"},
	{regexp.MustCompile(`require\s*\(\s*['"]os['"]\s*\)`), "os import — operating system access"},
	{regexp.MustCompile(`require\s*\(\s*['"]path['"]\s*\)`), "path import — file path manipulation"},
	{regexp.MustCompile(`import\s+.*from\s+['"]child_process['"]\s*`), "child_process ES import"},
	{regexp.MustCompile(`import\s+.*from\s+['"]fs['"]\s*`), "fs ES import"},
	{regexp.MustCompile(`\beval\s*\(`), "eval() — arbitrary code execution"},
	{regexp.MustCompile(`new\s+Function\s*\(`), "new Function() — dynamic code execution"},
	{regexp.MustCompile(`process\.env`), "process.env access — credential leakage risk"},
	{regexp.MustCompile(`process\.exit`), "process.exit — can crash the runner"},
	{regexp.MustCompile(`__dirname|__filename`), "file path globals — path traversal risk"},
	{regexp.MustCompile(`exec\s*\(|execSync\s*\(`), "exec/execSync — shell command execution"},
	{regexp.MustCompile(`spawn\s*\(|spawnSync\s*\(`), "spawn — process spawning"},
	{regexp.MustCompile(`globalThis|global\[`), "global scope manipulation"},
}

// detectLanguage returns one of "js" (Playwright/Jest/Cypress), "go",
// "rust", "python", or "" for an unrecognized shape. We need this because
// the "is this a test file" and "what's dangerous" checks are
// language-specific — `TestFoo(t *testing.T)` is valid Go testing but
// doesn't contain `test(`, so a Playwright-only scanner rejects it.
func detectLanguage(code string) string {
	// Strong markers — earliest match wins. Order matters: Go's `package`
	// keyword can appear inside JS/TS string literals, so we look for the
	// `package X` / `func TestX` combo.
	switch {
	case regexp.MustCompile(`(?m)^\s*package\s+\w+\s*$`).MatchString(code) &&
		(strings.Contains(code, "testing.T") || strings.Contains(code, "testing.B") ||
			strings.Contains(code, "testing.F") || strings.Contains(code, "func Test") ||
			strings.Contains(code, "func Benchmark") || strings.Contains(code, "func Fuzz") ||
			strings.Contains(code, "func Example")):
		return "go"
	case strings.Contains(code, "#[test]") || strings.Contains(code, "#[cfg(test)]") ||
		strings.Contains(code, "#[tokio::test]"):
		return "rust"
	case regexp.MustCompile(`(?m)^\s*(import|from)\s+`).MatchString(code) &&
		(regexp.MustCompile(`\bdef\s+test_\w+`).MatchString(code) ||
			strings.Contains(code, "pytest") || strings.Contains(code, "unittest")):
		return "python"
	case strings.Contains(code, "test(") || strings.Contains(code, "describe(") ||
		strings.Contains(code, "it(") || strings.Contains(code, "@playwright/test"):
		return "js"
	}
	return ""
}

// hasTestDeclaration returns true if the code contains an idiomatic test
// declaration for its detected language. Used as the "this looks like a
// test file, not arbitrary code" gate.
func hasTestDeclaration(code, lang string) bool {
	switch lang {
	case "go":
		// `func TestXxx(t *testing.T)` or `func BenchmarkXxx` or `func FuzzXxx`.
		return regexp.MustCompile(`\bfunc\s+(Test|Benchmark|Fuzz|Example)[A-Z_]\w*\s*\(`).MatchString(code)
	case "rust":
		return strings.Contains(code, "#[test]") || strings.Contains(code, "#[tokio::test]") ||
			strings.Contains(code, "#[cfg(test)]")
	case "python":
		return regexp.MustCompile(`\bdef\s+test_\w+`).MatchString(code) ||
			strings.Contains(code, "unittest.TestCase") ||
			strings.Contains(code, "pytest.fixture")
	case "js", "":
		// Fall back to JS markers for unknown / JS-detected.
		return strings.Contains(code, "test(") || strings.Contains(code, "test.describe(") ||
			strings.Contains(code, "describe(") || strings.Contains(code, "it(")
	}
	return false
}

// scanCodeSecurity checks generated code for dangerous patterns.
// Returns a list of violations, or empty if code is safe.
func scanCodeSecurity(code string) []string {
	var violations []string

	lang := detectLanguage(code)

	// Only apply JS/Node dangerous-pattern checks to JS code. The existing
	// dangerousPatterns table targets `require('fs')`, `process.env`,
	// `eval(` etc. — all JavaScript idioms that don't exist in Go/Rust/Python
	// and never false-match there either. Scoping the check makes the intent
	// obvious and prevents future rule additions from leaking across.
	if lang == "js" || lang == "" {
		for _, dp := range dangerousPatterns {
			if dp.pattern.MatchString(code) {
				violations = append(violations, dp.reason)
			}
		}
	}

	// Go-specific dangerous patterns. Reject shell execution + direct
	// syscalls + arbitrary file writes outside the runner's temp dir.
	//
	// Opt-in escape hatch: legitimate CLI-integration tests (e.g. tests for
	// qmax-code itself, or for any project whose unit-of-test IS the binary)
	// must spawn a subprocess. They can opt in to specific rules with a
	// magic-comment marker like `// qmax:allow=os/exec` or
	// `// qmax:allow=exec.Command` near the top of the file. The marker is
	// auditable in the script content, default-deny stays in effect for
	// everything else, and the runner's binary allow-list remains the
	// actual security boundary at execution time.
	if lang == "go" {
		allows := parseQmaxAllows(code)
		goDangers := []struct {
			pat   *regexp.Regexp
			why   string
			allow string // qmax:allow=<this> disables the rule
		}{
			{regexp.MustCompile(`"os/exec"`), `os/exec import — arbitrary command execution`, "os/exec"},
			{regexp.MustCompile(`\bsyscall\b`), `syscall import — direct system calls`, "syscall"},
			{regexp.MustCompile(`\bunsafe\b`), `unsafe package — memory safety bypass`, "unsafe"},
			{regexp.MustCompile(`exec\.Command\s*\(`), `exec.Command — shell execution`, "exec.Command"},
		}
		for _, d := range goDangers {
			if d.pat.MatchString(code) && !allows[d.allow] {
				violations = append(violations, d.why)
			}
		}
	}

	// Rust-specific dangerous patterns.
	if lang == "rust" {
		rustDangers := []struct {
			pat *regexp.Regexp
			why string
		}{
			{regexp.MustCompile(`std::process::Command`), `std::process::Command — shell execution`},
			{regexp.MustCompile(`unsafe\s*\{`), `unsafe block — memory safety bypass`},
		}
		for _, d := range rustDangers {
			if d.pat.MatchString(code) {
				violations = append(violations, d.why)
			}
		}
	}

	// Python-specific dangerous patterns.
	if lang == "python" {
		pyDangers := []struct {
			pat *regexp.Regexp
			why string
		}{
			{regexp.MustCompile(`\bsubprocess\.(run|call|Popen|check_output|check_call)\s*\(`), `subprocess — shell execution`},
			{regexp.MustCompile(`\beval\s*\(`), `eval() — arbitrary code execution`},
			{regexp.MustCompile(`\bexec\s*\(`), `exec() — arbitrary code execution`},
			{regexp.MustCompile(`\bos\.system\s*\(`), `os.system — shell execution`},
			{regexp.MustCompile(`\b__import__\s*\(`), `__import__ — dynamic import`},
		}
		for _, d := range pyDangers {
			if d.pat.MatchString(code) {
				violations = append(violations, d.why)
			}
		}
	}

	// Check for suspicious URLs (data exfiltration) — language-agnostic.
	urlPattern := regexp.MustCompile(`https?://[^\s'"]+`)
	urls := urlPattern.FindAllString(code, -1)
	for _, url := range urls {
		lower := strings.ToLower(url)
		// Allow common test targets
		if strings.Contains(lower, "localhost") ||
			strings.Contains(lower, "127.0.0.1") ||
			strings.Contains(lower, "qualitymax") ||
			strings.Contains(lower, "example.com") ||
			strings.Contains(lower, "playwright.dev") {
			continue
		}
		// Flag requests to unknown external services that look like data exfiltration
		if strings.Contains(lower, "webhook.site") ||
			strings.Contains(lower, "requestbin") ||
			strings.Contains(lower, "ngrok") ||
			strings.Contains(lower, "burpcollaborator") {
			violations = append(violations, "Suspicious URL detected: "+url+" — possible data exfiltration")
		}
	}

	// Check code length (unreasonably long code is suspicious)
	if len(code) > 100000 {
		violations = append(violations, "Code exceeds 100KB — suspiciously large")
	}

	// Must look like a test file — per-language markers. This is the bug
	// that blocked a live user session healing Go tests: the previous
	// check only accepted Playwright/Jest patterns (`test(`, `describe(`,
	// `it(`) and falsely rejected every Go / Rust / Python test file.
	if !hasTestDeclaration(code, lang) {
		violations = append(violations, "No test declaration found — not a valid test file")
	}

	return violations
}

// qmaxAllowRE matches comment-marker opt-ins of the form
// `// qmax:allow=<rule>` (case-insensitive on the `qmax:allow` part,
// case-sensitive on the rule name to avoid surprises). Multiple rules
// can be granted on separate lines or comma-separated:
//   // qmax:allow=os/exec
//   // qmax:allow=os/exec, exec.Command
// Only `//`-style line comments are recognized — block comments are
// intentionally ignored to keep the marker visible to grep + reviewers.
var qmaxAllowRE = regexp.MustCompile(`(?i)//\s*qmax:allow=([^\r\n]+)`)

// parseQmaxAllows extracts the set of rule names the file opts into via
// `// qmax:allow=...` markers. Returned as a set keyed by the exact rule
// name (e.g. `os/exec`, `exec.Command`). Missing markers => empty set =>
// default-deny stays in effect.
func parseQmaxAllows(code string) map[string]bool {
	allows := make(map[string]bool)
	for _, m := range qmaxAllowRE.FindAllStringSubmatch(code, -1) {
		for _, raw := range strings.Split(m[1], ",") {
			name := strings.TrimSpace(raw)
			if name != "" {
				allows[name] = true
			}
		}
	}
	return allows
}

// Command allowlist — only these command prefixes are safe
var allowedCommands = []string{
	"git ",
	"git\t",
	"ls ",
	"ls\t",
	"ls\n",
	"pwd",
	"cat ",
	"head ",
	"tail ",
	"wc ",
	"find ",
	"grep ",
	"npm ",
	"npx ",
	"node ",
	"go ",
	"python ",
	"python3 ",
	"pip ",
	"echo ",
	"which ",
	"env",
	"uname",
	"date",
	"whoami",
	"qmax ",
}

// Dangerous shell operators that should be blocked
var dangerousShellOps = []string{
	"rm -rf",
	"rm -f /",
	"mkfs",
	"dd if=",
	"> /dev/",
	"chmod 777",
	"curl.*|.*sh",
	"wget.*|.*sh",
	"sudo ",
	"su -",
	"passwd",
	"shutdown",
	"reboot",
	"kill -9",
	"killall",
	":(){ ", // fork bomb
}

// validateCommand checks if a shell command is safe to execute.
// Returns empty string if safe, or reason if blocked.
func validateCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)

	// Block empty commands
	if cmd == "" {
		return "empty command"
	}

	// Check for dangerous operators first
	lower := strings.ToLower(cmd)
	for _, dangerous := range dangerousShellOps {
		if strings.Contains(lower, dangerous) {
			return fmt.Sprintf("dangerous operation: %s", dangerous)
		}
	}

	// Check against allowlist
	// The command must start with one of the allowed prefixes
	allowed := false
	for _, prefix := range allowedCommands {
		if strings.HasPrefix(cmd, prefix) || cmd == strings.TrimSpace(prefix) {
			allowed = true
			break
		}
	}

	if !allowed {
		return "command not in allowlist. Allowed: git, ls, cat, npm, npx, node, go, python, qmax, etc."
	}

	return "" // safe
}
