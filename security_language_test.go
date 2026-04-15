package main

import (
	"strings"
	"testing"
)

// Regression tests for the "language-aware scanCodeSecurity" fix.
// The bug: the pre-fix scanner required Playwright-style `test(...)`
// calls in every script, which meant Go/Rust/Python tests were all
// rejected with "No test() or describe() found". A live user hit this
// while trying to heal Go tests — every update_script call with a
// syntactically-correct Go test was blocked with "Security scan failed".

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{
			name: "Go with package + testing.T",
			code: "package foo_test\n\nimport \"testing\"\n\nfunc TestBar(t *testing.T) {}",
			want: "go",
		},
		{
			name: "Go with benchmark",
			code: "package foo\n\nimport \"testing\"\n\nfunc BenchmarkBar(b *testing.B) {}",
			want: "go",
		},
		{
			name: "Rust with #[test]",
			code: "#[test]\nfn test_foo() { assert_eq!(1, 1); }",
			want: "rust",
		},
		{
			name: "Rust with #[tokio::test]",
			code: "#[tokio::test]\nasync fn test_async() {}",
			want: "rust",
		},
		{
			name: "Python pytest",
			code: "import pytest\n\ndef test_foo():\n    assert True",
			want: "python",
		},
		{
			name: "Python unittest",
			code: "import unittest\n\nclass TestFoo(unittest.TestCase):\n    def test_bar(self): pass",
			want: "python",
		},
		{
			name: "Playwright test()",
			code: "import { test } from '@playwright/test';\ntest('login', async ({ page }) => {});",
			want: "js",
		},
		{
			name: "Jest describe/it",
			code: "describe('math', () => { it('adds', () => {}); });",
			want: "js",
		},
		{
			name: "Unknown shape",
			code: "hello world",
			want: "",
		},
		{
			name: "Go comment mentioning test() doesn't fool detector",
			// Critical: a Go file with `// test(` in a comment should still
			// detect as Go, not JS. The detector looks for `package X` +
			// `testing.T` markers before falling back to `test(`.
			code: "package foo_test\n\n// this isn't a test(\nimport \"testing\"\nfunc TestX(t *testing.T) {}",
			want: "go",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectLanguage(tc.code)
			if got != tc.want {
				t.Errorf("detectLanguage: got %q, want %q\nCode:\n%s", got, tc.want, tc.code)
			}
		})
	}
}

func TestScanCodeSecurity_GoTestPasses(t *testing.T) {
	// Regression for the exact failure mode the user hit: a minimal,
	// syntactically-valid Go test must NOT produce any violations.
	code := `package main_test

import "testing"

func TestFoo(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("math broken")
	}
}
`
	violations := scanCodeSecurity(code)
	if len(violations) != 0 {
		t.Errorf("Valid Go test wrongly flagged. Violations: %v", violations)
	}
}

func TestScanCodeSecurity_RustTestPasses(t *testing.T) {
	code := `#[test]
fn test_addition() {
    assert_eq!(1 + 1, 2);
}
`
	violations := scanCodeSecurity(code)
	if len(violations) != 0 {
		t.Errorf("Valid Rust test wrongly flagged. Violations: %v", violations)
	}
}

func TestScanCodeSecurity_PytestPasses(t *testing.T) {
	code := `import pytest

def test_addition():
    assert 1 + 1 == 2
`
	violations := scanCodeSecurity(code)
	if len(violations) != 0 {
		t.Errorf("Valid pytest wrongly flagged. Violations: %v", violations)
	}
}

func TestScanCodeSecurity_GoWithOsExecRejected(t *testing.T) {
	// Go gets its own dangerous-pattern table. Shell execution must be
	// blocked even though the JS table wouldn't match `os/exec`.
	code := `package main_test

import (
	"os/exec"
	"testing"
)

func TestEvil(t *testing.T) {
	exec.Command("rm", "-rf", "/").Run()
}
`
	violations := scanCodeSecurity(code)
	hasOsExec := false
	for _, v := range violations {
		if strings.Contains(v, "os/exec") || strings.Contains(v, "shell execution") {
			hasOsExec = true
		}
	}
	if !hasOsExec {
		t.Errorf("Go os/exec should be rejected; violations: %v", violations)
	}
}

func TestScanCodeSecurity_GoOsExecAllowedWithMarker(t *testing.T) {
	// Legitimate CLI-integration tests (e.g. healing tests for qmax-code
	// itself) must be able to spawn the binary they're testing. The
	// `// qmax:allow=os/exec` and `// qmax:allow=exec.Command` markers
	// are the auditable opt-in. Default-deny stays — see the
	// TestScanCodeSecurity_GoWithOsExecRejected case above.
	code := `package main_test

// qmax:allow=os/exec
// qmax:allow=exec.Command
import (
	"os/exec"
	"testing"
)

func TestCLI(t *testing.T) {
	cmd := exec.Command("qmax-code", "--version")
	if err := cmd.Run(); err != nil {
		t.Skip("requires qmax-code on PATH")
	}
}
`
	violations := scanCodeSecurity(code)
	for _, v := range violations {
		if strings.Contains(v, "os/exec") || strings.Contains(v, "exec.Command") {
			t.Errorf("With qmax:allow markers, os/exec + exec.Command must NOT be flagged. Got: %v", violations)
		}
	}
}

func TestScanCodeSecurity_GoOsExecPartialMarker(t *testing.T) {
	// Allowing only os/exec must still block exec.Command. Granular
	// markers — opt-ins are per-rule, not all-or-nothing.
	code := `package main_test

// qmax:allow=os/exec
import (
	"os/exec"
	"testing"
)

func TestX(t *testing.T) {
	exec.Command("rm", "-rf", "/").Run()
}
`
	violations := scanCodeSecurity(code)
	osExecOK, cmdBlocked := true, false
	for _, v := range violations {
		if strings.Contains(v, "os/exec import") {
			osExecOK = false
		}
		if strings.Contains(v, "exec.Command") {
			cmdBlocked = true
		}
	}
	if !osExecOK {
		t.Errorf("os/exec import should be allowed by marker; violations: %v", violations)
	}
	if !cmdBlocked {
		t.Errorf("exec.Command should still be blocked (no marker for it); violations: %v", violations)
	}
}

func TestScanCodeSecurity_GoCommaSeparatedMarker(t *testing.T) {
	code := `package main_test

// qmax:allow=os/exec, exec.Command
import (
	"os/exec"
	"testing"
)

func TestX(t *testing.T) { _ = exec.Command("ls").Run() }
`
	violations := scanCodeSecurity(code)
	for _, v := range violations {
		if strings.Contains(v, "os/exec") || strings.Contains(v, "exec.Command") {
			t.Errorf("Comma-separated marker should grant both rules. Got: %v", violations)
		}
	}
}

func TestParseQmaxAllows(t *testing.T) {
	cases := []struct {
		name string
		code string
		want []string
	}{
		{"none", "package x\n// regular comment\n", nil},
		{"single", "// qmax:allow=os/exec\n", []string{"os/exec"}},
		{"two lines", "// qmax:allow=os/exec\n// qmax:allow=exec.Command\n", []string{"os/exec", "exec.Command"}},
		{"comma list", "// qmax:allow=os/exec, exec.Command, syscall\n", []string{"os/exec", "exec.Command", "syscall"}},
		{"case insensitive prefix", "// QMAX:ALLOW=os/exec\n", []string{"os/exec"}},
		{"trailing whitespace tolerated", "//   qmax:allow=os/exec   \n", []string{"os/exec"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseQmaxAllows(c.code)
			for _, w := range c.want {
				if !got[w] {
					t.Errorf("expected %q in allows; got %v", w, got)
				}
			}
			if len(got) != len(c.want) {
				t.Errorf("expected %d allows, got %d (%v)", len(c.want), len(got), got)
			}
		})
	}
}

func TestScanCodeSecurity_RustProcessCommandRejected(t *testing.T) {
	code := `#[test]
fn test_x() {
    std::process::Command::new("sh").arg("-c").arg("evil").output().unwrap();
}
`
	violations := scanCodeSecurity(code)
	hasProc := false
	for _, v := range violations {
		if strings.Contains(v, "Command") || strings.Contains(v, "shell execution") {
			hasProc = true
		}
	}
	if !hasProc {
		t.Errorf("Rust std::process::Command should be rejected; violations: %v", violations)
	}
}

func TestScanCodeSecurity_PythonSubprocessRejected(t *testing.T) {
	code := `import pytest
import subprocess

def test_x():
    subprocess.run(["evil"])
`
	violations := scanCodeSecurity(code)
	hasSubp := false
	for _, v := range violations {
		if strings.Contains(v, "subprocess") || strings.Contains(v, "shell execution") {
			hasSubp = true
		}
	}
	if !hasSubp {
		t.Errorf("Python subprocess should be rejected; violations: %v", violations)
	}
}

func TestScanCodeSecurity_JSPatternsNotFalseFiredOnGo(t *testing.T) {
	// A Go file mentioning `process.env` in a STRING (e.g. a comment or
	// a log message) would previously trip the JS `process.env` rule
	// even though it's a Go file. With language-aware scoping, JS
	// dangerous patterns don't run on Go code.
	code := `package main_test

import "testing"

// NOTE: docs for future versions may mention process.env compatibility.
func TestFoo(t *testing.T) {}
`
	violations := scanCodeSecurity(code)
	for _, v := range violations {
		if strings.Contains(v, "process.env") {
			t.Errorf("JS process.env rule should NOT fire on Go code; got: %v", violations)
		}
	}
}

func TestHasTestDeclaration(t *testing.T) {
	cases := []struct {
		lang, code string
		want       bool
	}{
		{"go", "func TestFoo(t *testing.T) {}", true},
		{"go", "func BenchmarkFoo(b *testing.B) {}", true},
		{"go", "func main() {}", false},
		{"rust", "#[test]\nfn test_x() {}", true},
		{"rust", "fn main() {}", false},
		{"python", "def test_foo():\n    pass", true},
		{"python", "def main():\n    pass", false},
		{"js", "test('x', () => {})", true},
		{"js", "console.log('hi')", false},
		{"", "test('x', () => {})", true}, // fallback to JS markers
	}
	for _, c := range cases {
		label := c.code
		if len(label) > 20 {
			label = label[:20]
		}
		t.Run(c.lang+"/"+label, func(t *testing.T) {
			got := hasTestDeclaration(c.code, c.lang)
			if got != c.want {
				t.Errorf("hasTestDeclaration(%q, %q) = %v, want %v", c.code, c.lang, got, c.want)
			}
		})
	}
}
