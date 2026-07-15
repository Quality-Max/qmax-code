package httpx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenLibraryImports are client libraries that create egress outside the
// standard library. New entries require a dedicated httpx wrapper.
var forbiddenLibraryImports = map[string]bool{
	"github.com/go-resty/resty":      true,
	"github.com/levigross/grequests": true,
	"github.com/h2non/gentleman":     true,
	"github.com/valyala/fasthttp":    true,
	"github.com/imroc/req":           true,
	"github.com/jmcvetta/napping":    true,
}

// scanForEgressViolations parses production Go source rather than matching
// strings. It catches import aliases, zero-value http.Client declarations,
// new(http.Client), raw transports, default clients, direct WebSocket dials,
// and net.Dial calls.
func scanForEgressViolations(root string) ([]string, error) {
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			name := info.Name()
			if name == "httpx" || name == "vendor" || name == "build" || strings.HasPrefix(name, ".") {
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		violations = append(violations, scanFileForEgressViolations(file, fset, rel)...)
		return nil
	})
	return violations, err
}

func scanFileForEgressViolations(file *ast.File, fset *token.FileSet, rel string) []string {
	imports := map[string]string{}
	var violations []string
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = path
		if forbiddenLibraryImports[path] {
			violations = append(violations, violation(rel, fset, spec.Pos(), "forbidden egress library "+path))
		}
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.ValueSpec:
			if selectorMatches(n.Type, imports, "net/http", "Client") || selectorMatches(n.Type, imports, "net/http", "Transport") {
				violations = append(violations, violation(rel, fset, n.Pos(), "raw net/http client or transport declaration"))
			}
		case *ast.CompositeLit:
			if selectorMatches(n.Type, imports, "net/http", "Client") || selectorMatches(n.Type, imports, "net/http", "Transport") {
				violations = append(violations, violation(rel, fset, n.Pos(), "raw net/http client or transport construction"))
			}
		case *ast.CallExpr:
			if isNewHTTPClientOrTransport(n, imports) {
				violations = append(violations, violation(rel, fset, n.Pos(), "new(net/http Client or Transport)"))
			}
			if callMatches(n, imports, "net/http", map[string]bool{
				"NewRequest": true, "NewRequestWithContext": true, "Get": true,
				"Post": true, "PostForm": true, "Head": true,
			}) {
				violations = append(violations, violation(rel, fset, n.Pos(), "raw net/http request creation"))
			}
			if callMatches(n, imports, "net", map[string]bool{
				"Dial": true, "DialTimeout": true, "DialTCP": true, "DialUDP": true,
			}) {
				violations = append(violations, violation(rel, fset, n.Pos(), "raw net dial"))
			}
			if callMatches(n, imports, "github.com/coder/websocket", map[string]bool{"Dial": true}) {
				violations = append(violations, violation(rel, fset, n.Pos(), "direct WebSocket dial; use httpx.DialWebSocket"))
			}
		case *ast.SelectorExpr:
			if selectorMatches(n, imports, "net/http", "DefaultClient") || selectorMatches(n, imports, "net/http", "DefaultTransport") {
				violations = append(violations, violation(rel, fset, n.Pos(), "raw net/http default client or transport"))
			}
		}
		return true
	})
	return violations
}

func selectorMatches(expr ast.Expr, imports map[string]string, packagePath, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && imports[ident.Name] == packagePath
}

func callMatches(call *ast.CallExpr, imports map[string]string, packagePath string, names map[string]bool) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !names[selector.Sel.Name] {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && imports[ident.Name] == packagePath
}

func isNewHTTPClientOrTransport(call *ast.CallExpr, imports map[string]string) bool {
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "new" || len(call.Args) != 1 {
		return false
	}
	return selectorMatches(call.Args[0], imports, "net/http", "Client") ||
		selectorMatches(call.Args[0], imports, "net/http", "Transport")
}

func violation(rel string, fset *token.FileSet, pos token.Pos, message string) string {
	return rel + ":" + fset.Position(pos).String()[len(fset.Position(pos).Filename)+1:] + "  " + message
}

func TestNoEgressOutsideHTTPX(t *testing.T) {
	root := filepath.Join("..", "..")
	violations, err := scanForEgressViolations(root)
	if err != nil {
		t.Fatalf("scan egress: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("egress guard: %d violation(s) outside httpx:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

func TestEgressGuardDetectsAliasAndConstructorBypasses(t *testing.T) {
	dir := t.TempDir()
	bad := `package bad

import (
    h "net/http"
    "net"
    ws "github.com/coder/websocket"
)

var client h.Client
var transport = new(h.Transport)

func f() {
    _, _ = net.Dial("tcp", "example.com:80")
    _, _, _ = ws.Dial(nil, "ws://example.com", nil)
}
`
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	violations, err := scanForEgressViolations(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(violations) != 4 {
		t.Fatalf("violations = %d, want 4: %v", len(violations), violations)
	}
}
