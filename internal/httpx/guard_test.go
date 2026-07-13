package httpx

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoEgressOutsideHttpx is the static Egress Guard. It walks the whole
// qmax-code module and fails if any package other than httpx constructs an HTTP
// client/request/transport or imports a third-party HTTP library. This makes an
// un-receipted egress path impossible to merge — the load-bearing half of the
// "Receipts, not promises" guarantee.
//
// net/http may still be IMPORTED elsewhere for types and constants (http.Request,
// http.MethodPost, http.StatusOK, http.StatusText); only the egress-CREATING
// symbols below are forbidden. Route all outbound HTTP through
// httpx.NewClient / httpx.NewRequest.
func TestNoEgressOutsideHttpx(t *testing.T) {
	// Egress-creating symbols. Their only legitimate home is this package.
	forbiddenSymbols := []string{
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
	// Third-party HTTP clients that would bypass net/http entirely.
	forbiddenImports := []string{
		"go-resty", "levigross/grequests", "h2non/gentleman",
		"valyala/fasthttp", "imroc/req", "jmcvetta/napping",
		"coder/websocket",
	}

	root := filepath.Join("..", "..") // module root: qmax-code/
	var violations []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip sanctioned carve-outs, build output, and hidden/vendor dirs.
			//   httpx — the chokepoint itself (creates the clients).
			//   vnc  — documented WebSocket carve-out (see internal/vnc/stream.go
			//          package doc): live-browser screen streaming carries pixels,
			//          never source/prompts. Reviewed and accepted on QUA-1316.
			name := info.Name()
			if name == "httpx" || name == "vnc" || name == "vendor" || name == "build" || strings.HasPrefix(name, ".") {
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
			// Ignore comments to avoid false positives in prose.
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
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("egress guard: %d violation(s) outside httpx — route all outbound HTTP through httpx.NewClient/NewRequest:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}
