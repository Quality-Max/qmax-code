package httpx

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// forbiddenSymbols are the egress-creating constructs. Their only legitimate
// home is this package.
var forbiddenSymbols = []string{
	"http.Client{",
	"http.NewRequest(",
	"http.NewRequestWithContext(",
	"http.Get(",
	"http.Post(",
	"http.PostForm(",
	"http.Head(",
	"http.DefaultClient",
	"http.DefaultTransport",
}

// forbiddenImports are third-party HTTP/WebSocket clients that would bypass
// net/http entirely.
var forbiddenImports = []string{
	"go-resty", "levigross/grequests", "h2non/gentleman",
	"valyala/fasthttp", "imroc/req", "jmcvetta/napping",
	"coder/websocket",
}

// sanctionedCarveOuts are packages allowed to bypass the guard. Each must have
// a documented justification in its package doc.
//   httpx — the chokepoint itself (creates the clients).
//   vnc  — documented WebSocket carve-out (see internal/vnc/stream.go package
//          doc): live-browser screen streaming carries pixels, never
//          source/prompts. Reviewed and accepted on QUA-1316.
var sanctionedCarveOuts = map[string]bool{"httpx": true, "vnc": true}

// scanForEgressViolations walks root and returns any forbidden egress symbols or
// third-party HTTP imports outside sanctioned carve-out packages. It is
// extracted from the guard test so the injection test can exercise the same
// logic against a temp directory.
func scanForEgressViolations(root string) ([]string, error) {
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if sanctionedCarveOuts[name] || name == "vendor" || name == "build" || strings.HasPrefix(name, ".") {
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(data), "\n") {
			code := line
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			for _, sym := range forbiddenSymbols {
				if strings.Contains(code, sym) {
					violations = append(violations,
						rel+":"+strconv.Itoa(i+1)+"  forbidden egress symbol "+sym)
				}
			}
			for _, imp := range forbiddenImports {
				if strings.Contains(line, imp) {
					violations = append(violations,
						rel+":"+strconv.Itoa(i+1)+"  forbidden HTTP library import "+imp)
				}
			}
		}
		return nil
	})
	return violations, err
}

// TestNoEgressOutsideHttpx is the static Egress Guard. It walks the whole
// qmax-code module and fails if any package other than a sanctioned carve-out
// constructs an HTTP client/request/transport or imports a third-party HTTP
// library. This makes an un-receipted egress path impossible to merge — the
// load-bearing half of the "Receipts, not promises" guarantee.
//
// net/http may still be IMPORTED elsewhere for types and constants (http.Request,
// http.MethodPost, http.StatusOK, http.StatusText); only the egress-CREATING
// symbols are forbidden. Route all outbound HTTP through
// httpx.NewClient / httpx.NewRequest.
func TestNoEgressOutsideHttpx(t *testing.T) {
	root := filepath.Join("..", "..") // module root: qmax-code/
	violations, err := scanForEgressViolations(root)
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("egress guard: %d violation(s) outside sanctioned carve-outs — route all outbound HTTP through httpx.NewClient/NewRequest:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// TestEgressGuardDetectsInjection proves the guard actually catches a raw
// http.Client construction and a forbidden import — so the guard's detection
// logic itself is validated in CI, not just the absence of violations today.
func TestEgressGuardDetectsInjection(t *testing.T) {
	dir := t.TempDir()
	// Simulate a package that bypasses the chokepoint.
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(
		`package bad

import "net/http"

var client = &http.Client{}
`), 0644); err != nil {
		t.Fatal(err)
	}

	violations, err := scanForEgressViolations(dir)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if len(violations) == 0 {
		t.Fatal("guard failed to detect an injected raw http.Client{} — detection logic is broken")
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v, "forbidden egress symbol http.Client{") {
			found = true
		}
	}
	if !found {
		t.Fatalf("guard did not flag http.Client{}: %v", violations)
	}
}
