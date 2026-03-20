package main

import (
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

// scanCodeSecurity checks generated code for dangerous patterns.
// Returns a list of violations, or empty if code is safe.
func scanCodeSecurity(code string) []string {
	var violations []string

	for _, dp := range dangerousPatterns {
		if dp.pattern.MatchString(code) {
			violations = append(violations, dp.reason)
		}
	}

	// Check for suspicious URLs (data exfiltration)
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

	// Must contain at least one test() or describe() call
	if !strings.Contains(code, "test(") && !strings.Contains(code, "test.describe(") &&
		!strings.Contains(code, "describe(") && !strings.Contains(code, "it(") {
		violations = append(violations, "No test() or describe() found — not a valid test file")
	}

	return violations
}
