package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/tui"
)

// fakeCloud is a stand-in QualityMax API. Handlers are matched by path
// prefix; unmatched requests get a generic JSON body. Every request is
// recorded as "METHOD /path".
type fakeCloud struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	seen   []string
	bodies []string
	routes map[string]func(w http.ResponseWriter, r *http.Request)
}

func newFakeCloud(t *testing.T) *fakeCloud {
	t.Helper()
	fc := &fakeCloud{t: t, routes: map[string]func(http.ResponseWriter, *http.Request){}}
	fc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		fc.mu.Lock()
		fc.seen = append(fc.seen, r.Method+" "+r.URL.Path)
		fc.bodies = append(fc.bodies, string(body))
		var match func(http.ResponseWriter, *http.Request)
		best := -1
		for prefix, h := range fc.routes {
			if strings.HasPrefix(r.URL.Path, prefix) && len(prefix) > best {
				match, best = h, len(prefix)
			}
		}
		fc.mu.Unlock()
		if match != nil {
			match(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(fc.srv.Close)
	return fc
}

func (fc *fakeCloud) handle(prefix string, h func(http.ResponseWriter, *http.Request)) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.routes[prefix] = h
}

func (fc *fakeCloud) json(prefix, body string) {
	fc.handle(prefix, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
}

func (fc *fakeCloud) requests() []string {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return append([]string(nil), fc.seen...)
}

func (fc *fakeCloud) sawPrefix(prefix string) bool {
	for _, r := range fc.requests() {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

func (fc *fakeCloud) client() *api.APIClient {
	return api.NewAPIClient(&api.AuthConfig{APIKey: "qm-test", CloudURL: fc.srv.URL})
}

// routeAllRequestsTo sends every outbound request, whatever its host, to srv.
// Clients capture http.DefaultTransport when built, so call this first.
func routeAllRequestsTo(t *testing.T, srv *httptest.Server) {
	t.Helper()
	target, _ := url.Parse(srv.URL)
	orig := http.DefaultTransport
	base := orig
	if prev, ok := orig.(rewriteTransport); ok {
		base = prev.base
	}
	http.DefaultTransport = rewriteTransport{target: target, base: base}
	t.Cleanup(func() { http.DefaultTransport = orig })
}

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	clone.Host = rt.target.Host
	return rt.base.RoundTrip(clone)
}

func requirePOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell stubs")
	}
}

func fastPolling(t *testing.T) {
	t.Helper()
	orig := testStatusPollInterval
	testStatusPollInterval = time.Millisecond
	t.Cleanup(func() { testStatusPollInterval = orig })
}

func TestExecuteToolViaAPIDispatchesEveryCloudTool(t *testing.T) {
	withTempHome(t)
	fastPolling(t)
	fc := newFakeCloud(t)
	sctx := &api.SessionContext{API: fc.client(), ProjectID: 9}
	ctx := context.Background()

	cases := []struct {
		tool  string
		input map[string]interface{}
		path  string
	}{
		{"list_projects", nil, "GET /api/projects"},
		{"list_test_cases", map[string]interface{}{"search": "login", "limit": 5}, "GET /api/"},
		{"list_scripts", nil, "GET /api/"},
		{"generate_test_code", map[string]interface{}{"test_case_id": 3}, "POST /api/"},
		{"run_native_test", map[string]interface{}{"script_id": 4}, "POST /api/"},
		{"setup_cicd", map[string]interface{}{"repo_id": 1, "framework": "playwright"}, "POST /api/"},
		{"run_tests_batch", map[string]interface{}{"script_ids": "1,2"}, "POST /api/"},
		{"check_test_status", map[string]interface{}{"execution_id": "e1"}, "GET /api/playwright-execution/status/e1"},
		{"start_crawl", map[string]interface{}{"url": "https://example.com"}, "POST /api/"},
		{"crawl_status", map[string]interface{}{"crawl_id": "c1"}, "GET /api/"},
		{"crawl_results", map[string]interface{}{"crawl_id": "c1"}, "GET /api/"},
		{"list_crawl_jobs", nil, "GET /api/"},
		{"list_repos", nil, "GET /api/"},
		{"review_repo", map[string]interface{}{"repo_id": 2}, "POST /api/"},
		{"get_review_preferences", map[string]interface{}{"repository_id": 2}, "/api/"},
		{"set_review_preferences", map[string]interface{}{"scope": "global", "preferences": map[string]interface{}{"focus": "security"}}, "/api/"},
		{"repo_coverage", map[string]interface{}{"repo_id": 2}, "GET /api/"},
		{"repo_quality", map[string]interface{}{"repo_id": 2}, "GET /api/"},
		{"import_repo", map[string]interface{}{"url": "https://github.com/o/r"}, "POST /api/"},
		{"import_document", map[string]interface{}{"text": "spec"}, "POST /api/"},
		{"create_pr", map[string]interface{}{"repo_id": 2}, "POST /api/repositories/create-pr"},
		{"get_script", map[string]interface{}{"script_id": 5}, "GET /api/automation/scripts/5"},
		{"update_script", map[string]interface{}{"script_id": 5, "content": "test('x', () => {})"}, "PUT /api/"},
		{"check_job_status", map[string]interface{}{"job_id": "j"}, "GET /api/"},
		{"k6_list_scripts", nil, "GET /api/"},
		{"k6_create_script", map[string]interface{}{"name": "load"}, "POST /api/"},
		{"k6_get_script", map[string]interface{}{"script_id": 1}, "GET /api/"},
		{"k6_run_test", map[string]interface{}{"script_id": 1, "vus": 2}, "POST /api/"},
		{"k6_check_status", map[string]interface{}{"execution_id": "k"}, "GET /api/"},
		{"k6_report", map[string]interface{}{"execution_id": "k"}, "GET /api/"},
		{"k6_generate", map[string]interface{}{"target_url": "https://x"}, "POST /api/"},
		{"k6_convert", map[string]interface{}{"source_code": "x"}, "POST /api/"},
		{"create_test_case", map[string]interface{}{"title": "t"}, "POST /api/"},
		{"update_test_case", map[string]interface{}{"test_case_id": 1, "title": "t"}, "/api/"},
		{"delete_test_case", map[string]interface{}{"test_case_id": 1}, "DELETE /api/"},
		{"create_project", map[string]interface{}{"name": "p"}, "POST /api/"},
		{"update_project", map[string]interface{}{"name": "p"}, "/api/"},
		{"delete_project", map[string]interface{}{"project_id": 1}, "DELETE /api/"},
		{"get_project_by_slug", map[string]interface{}{"slug": "demo"}, "GET /api/projects/by-slug/demo"},
		{"get_project_summary", nil, "GET /api/"},
		{"trigger_framework_run", map[string]interface{}{"framework_type": "playwright"}, "POST /api/"},
		{"export_framework", nil, "/api/"},
		{"get_install_command", nil, "GET /api/"},
		{"enhance_test_case", map[string]interface{}{"test_case_id": 1}, "POST /api/"},
		{"generate_gap_tests", map[string]interface{}{"repo_id": 1}, "POST /api/"},
		{"start_crawl_from_test_case", map[string]interface{}{"test_case_id": 1}, "POST /api/"},
		{"export_qtml", nil, "/api/"},
		{"import_qtml", map[string]interface{}{"content": "qtml"}, "POST /api/"},
		{"test_deployed_environment", map[string]interface{}{"url": "https://x"}, "POST /api/"},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			before := len(fc.requests())
			out := executeTool(tc.tool, tc.input, sctx, ctx, nil)
			if out == "" {
				t.Fatalf("%s returned empty output", tc.tool)
			}
			after := fc.requests()[before:]
			found := false
			for _, r := range after {
				if strings.Contains(r, strings.TrimSpace(tc.path)) || strings.HasPrefix(r, tc.path) {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s: no request matching %q; got %v", tc.tool, tc.path, after)
			}
		})
	}

	if got := executeToolViaAPI("no_such_tool", nil, sctx, ctx, nil); got != "" {
		t.Fatalf("unknown tool should fall through, got %q", got)
	}
	if got := executeTool("update_script", map[string]interface{}{"script_id": 5, "code": "require('child_process').exec('rm -rf /')"}, sctx, ctx, nil); !strings.Contains(got, "Security scan failed") {
		t.Fatalf("unsafe script was not blocked: %s", got)
	}
}

func TestGenerateTestCodeUsesConfiguredFramework(t *testing.T) {
	home := withTempHome(t)
	cfg := api.DefaultConfig()
	cfg.DefaultFramework = "go_test"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	_ = home
	fc := newFakeCloud(t)
	executeTool("generate_test_code", map[string]interface{}{"test_case_id": 1}, &api.SessionContext{API: fc.client()}, context.Background(), nil)
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.bodies) == 0 || !strings.Contains(fc.bodies[len(fc.bodies)-1], "go_test") {
		t.Fatalf("framework not sent: %v", fc.bodies)
	}
}

func TestRunTestWithProgressOutcomes(t *testing.T) {
	withTempHome(t)
	fastPolling(t)
	ctx := context.Background()

	t.Run("start error is returned", func(t *testing.T) {
		fc := newFakeCloud(t)
		fc.handle("/api/playwright-execution/run/", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"detail":"no credits"}`, http.StatusPaymentRequired)
		})
		out := runTestWithProgress(ctx, fc.client(), &api.SessionContext{}, 1, true, "", "", nil)
		if !strings.Contains(out, "error") {
			t.Fatalf("out = %s", out)
		}
	})

	t.Run("non-json and missing execution id pass through", func(t *testing.T) {
		fc := newFakeCloud(t)
		fc.json("/api/playwright-execution/run/", `not json`)
		if out := runTestWithProgress(ctx, fc.client(), nil, 1, true, "", "", nil); out != "not json" {
			t.Fatalf("out = %q", out)
		}
		fc.json("/api/playwright-execution/run/", `{"queued":true}`)
		if out := runTestWithProgress(ctx, fc.client(), nil, 1, true, "", "", nil); out != `{"queued":true}` {
			t.Fatalf("out = %q", out)
		}
	})

	t.Run("polls until passed", func(t *testing.T) {
		fc := newFakeCloud(t)
		fc.json("/api/playwright-execution/run/", `{"execution_id":"e1"}`)
		var n int
		var mu sync.Mutex
		fc.handle("/api/playwright-execution/status/", func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			n++
			c := n
			mu.Unlock()
			switch c {
			case 1:
				_, _ = io.WriteString(w, `garbage`)
			case 2:
				_, _ = io.WriteString(w, `{"status":"queued"}`)
			case 3:
				_, _ = io.WriteString(w, `{"status":"running","progress":50,"message":"step 2"}`)
			default:
				_, _ = io.WriteString(w, `{"status":"passed","message":"ok"}`)
			}
		})
		out := captureStdout(t, func() {
			got := runTestWithProgress(ctx, fc.client(), &api.SessionContext{}, 1, true, "chromium", "https://app", nil)
			if !strings.Contains(got, `"passed"`) {
				t.Errorf("final = %s", got)
			}
		})
		_ = out
	})

	t.Run("live feed returns immediately", func(t *testing.T) {
		resetExecIDFileForTest()
		t.Cleanup(resetExecIDFileForTest)
		fc := newFakeCloud(t)
		fc.json("/api/playwright-execution/run/", `{"execution_id":"e2"}`)
		out := runTestWithProgress(ctx, fc.client(), &api.SessionContext{LiveFeed: true}, 1, true, "", "", nil)
		if !strings.Contains(out, "client_note") || !strings.Contains(out, "e2") {
			t.Fatalf("out = %s", out)
		}
	})

	t.Run("stuck progress stops polling", func(t *testing.T) {
		fc := newFakeCloud(t)
		fc.json("/api/playwright-execution/run/", `{"execution_id":"e3"}`)
		fc.json("/api/playwright-execution/status/", `{"status":"running","progress":42}`)
		captureStdout(t, func() {
			out := runTestWithProgress(ctx, fc.client(), &api.SessionContext{}, 1, true, "", "", nil)
			if !strings.Contains(out, "stuck at 42%") {
				t.Errorf("out = %s", out)
			}
		})
	})

	t.Run("persistent progress uses the turn activity line", func(t *testing.T) {
		fc := newFakeCloud(t)
		fc.json("/api/playwright-execution/run/", `{"execution_id":"e4"}`)
		fc.json("/api/playwright-execution/status/", `{"status":"failed","message":"boom"}`)
		out := runTestWithProgress(ctx, fc.client(), &api.SessionContext{}, 1, true, "", "", &tui.Terminal{})
		if !strings.Contains(out, `"failed"`) {
			t.Fatalf("out = %s", out)
		}
	})

	t.Run("times out after max polls", func(t *testing.T) {
		fc := newFakeCloud(t)
		fc.json("/api/playwright-execution/run/", `{"execution_id":"e5"}`)
		var n int
		var mu sync.Mutex
		fc.handle("/api/playwright-execution/status/", func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			n++
			c := n
			mu.Unlock()
			fmt.Fprintf(w, `{"status":"running","progress":%d}`, c%90+1)
		})
		captureStdout(t, func() {
			out := runTestWithProgress(ctx, fc.client(), &api.SessionContext{}, 1, true, "", "", nil)
			if !strings.Contains(out, "Polling stopped after 6 minutes") {
				t.Errorf("out = %s", out)
			}
		})
	})
}

func TestRunLocalTestPaths(t *testing.T) {
	requirePOSIX(t)
	withTempHome(t)
	ctx := context.Background()

	if out := runLocalTest(ctx, &api.SessionContext{}, 1, ""); !strings.Contains(out, "Not connected") {
		t.Fatalf("no API = %s", out)
	}

	fc := newFakeCloud(t)
	sctx := &api.SessionContext{API: fc.client()}
	script := func(body string) {
		fc.json("/api/automation/scripts/", body)
	}

	fc.handle("/api/automation/scripts/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"missing"}`, http.StatusNotFound)
	})
	if out := runLocalTest(ctx, sctx, 1, ""); !strings.Contains(out, "error") {
		t.Fatalf("missing script = %s", out)
	}
	script(`nope`)
	if out := runLocalTest(ctx, sctx, 1, ""); !strings.Contains(out, "Failed to parse script") {
		t.Fatalf("bad json = %s", out)
	}
	script(`{"script":{"framework":"pytest"}}`)
	if out := runLocalTest(ctx, sctx, 1, ""); !strings.Contains(out, "no code") {
		t.Fatalf("no code = %s", out)
	}
	script(`{"code":"x","framework":"cobol"}`)
	if out := runLocalTest(ctx, sctx, 1, ""); !strings.Contains(out, "not supported for local execution") {
		t.Fatalf("unsupported = %s", out)
	}
	script(`{"code":"fn main(){}","framework":"rust_cargo"}`)
	runLocalTest(ctx, sctx, 1, "")
	if !fc.sawPrefix("POST /api/") {
		t.Fatal("native framework did not delegate to the API")
	}

	bin := t.TempDir()
	writeStubIn(t, bin, "python3", `echo "collected 1 item"; echo "test_script.py::test_ok PASSED"; exit 0`)
	writeStubIn(t, bin, "npx", `echo "Error: expect(locator).toBeVisible() failed" >&2; exit 1`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	fc.json("/api/automation/scripts/", `{"script":{"code":"def test_ok(): pass","framework":"pytest","name":"smoke"}}`)
	fc.json("/api/automation/local-result", `{"execution_id":"local-1"}`)
	fc.json("/api/playwright-execution/local", `{"execution_id":"local-1"}`)
	out := runLocalTest(ctx, sctx, 7, "https://staging")
	var res map[string]interface{}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("pytest result not JSON: %s", out)
	}
	if res["passed"] != true || res["name"] != "smoke" || !strings.Contains(res["output"].(string), "PASSED") {
		t.Fatalf("pytest result = %v", res)
	}

	fc.json("/api/automation/scripts/", `{"code":"test('x', async () => {})","framework":"playwright"}`)
	out = runLocalTest(ctx, sctx, 8, "https://staging")
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("playwright result not JSON: %s", out)
	}
	if res["passed"] != false || !strings.Contains(res["output"].(string), "--- stderr ---") {
		t.Fatalf("playwright result = %v", res)
	}
}

func writeStubIn(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecuteToolViaQMaxCLI(t *testing.T) {
	requirePOSIX(t)
	withTempHome(t)
	bin := t.TempDir()
	qmax := writeStubIn(t, bin, "qmax", `printf '{"args":"%s"}' "$*"`)
	sctx := &api.SessionContext{QMaxBin: qmax, ProjectID: 3}
	ctx := context.Background()

	cases := []struct {
		tool  string
		input map[string]interface{}
		want  string
	}{
		{"list_projects", nil, "projects --json"},
		{"list_test_cases", map[string]interface{}{"limit": 2, "search": "x"}, "--search x"},
		{"list_scripts", map[string]interface{}{"limit": 2}, "--limit 2"},
		{"generate_test_code", map[string]interface{}{"test_case_id": 4, "force": true}, "--force"},
		{"run_test", map[string]interface{}{"script_id": 1, "headless": true, "browser": "firefox", "base_url": "u"}, "--browser firefox"},
		{"run_tests_batch", map[string]interface{}{"script_ids": "1,2", "base_url": "u"}, "--script-ids 1,2"},
		{"check_test_status", map[string]interface{}{"execution_id": "e"}, "--execution-id e"},
		{"start_crawl", map[string]interface{}{"url": "u", "depth": 2, "pages": 3, "test_type": "smoke", "instructions": "go"}, "--instructions go"},
		{"crawl_status", map[string]interface{}{"crawl_id": "c"}, "crawl status"},
		{"crawl_results", map[string]interface{}{"crawl_id": "c"}, "crawl results"},
		{"list_crawl_jobs", map[string]interface{}{"limit": 4}, "crawl jobs"},
		{"list_repos", nil, "--project-id 3"},
		{"review_repo", map[string]interface{}{"repo_id": 1}, "repo review"},
		{"repo_coverage", map[string]interface{}{"repo_id": 1}, "repo coverage"},
		{"repo_quality", map[string]interface{}{"repo_id": 1}, "repo quality"},
		{"import_repo", map[string]interface{}{"url": "u", "create_project": true, "project_name": "n", "base_url": "b"}, "--project-name n"},
		{"import_document", map[string]interface{}{"text": "t", "source_name": "s"}, "--source s"},
		{"create_pr", map[string]interface{}{"repo_id": 1}, "pr create"},
	}
	for _, tc := range cases {
		out := executeToolViaQMax(tc.tool, tc.input, sctx, ctx)
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s = %s, want %q", tc.tool, out, tc.want)
		}
	}
	type typed struct {
		ScriptID int `json:"script_id"`
	}
	if out := executeToolViaQMax("check_test_status", typed{ScriptID: 1}, sctx, ctx); !strings.Contains(out, "test status") {
		t.Errorf("struct input = %s", out)
	}
	if out := executeToolViaQMax("nope", nil, sctx, ctx); !strings.Contains(out, "Unknown tool") {
		t.Errorf("unknown = %s", out)
	}
	if out := executeToolViaQMax("run_command", map[string]interface{}{"command": "echo hi"}, sctx, ctx); strings.TrimSpace(out) != "hi" {
		t.Errorf("run_command = %q", out)
	}
	if out := executeToolViaQMax("run_command", map[string]interface{}{"command": "rm -rf /"}, sctx, ctx); !strings.Contains(out, "blocked") {
		t.Errorf("dangerous command = %q", out)
	}
	if out := executeToolViaQMax("run_command", map[string]interface{}{"command": "ls /definitely-missing-qmax-path"}, sctx, ctx); !strings.Contains(out, "definitely-missing") {
		t.Errorf("failing command = %q", out)
	}
	if out := runShell(ctx, "/nonexistent/binary"); !strings.Contains(out, "error") {
		t.Errorf("missing binary = %q", out)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if out := runShell(canceled, "sleep", "1"); !strings.Contains(out, "cancelled") {
		t.Errorf("canceled shell = %q", out)
	}
	if out := runQMax(sctx, canceled, "x"); !strings.Contains(out, "cancelled") {
		t.Errorf("canceled qmax = %q", out)
	}

	failing := writeStubIn(t, bin, "qmax-fail", `echo "auth expired" >&2; exit 1`)
	if out := runQMax(&api.SessionContext{QMaxBin: failing}, ctx); !strings.Contains(out, "auth expired") {
		t.Errorf("stderr error = %q", out)
	}
	silent := writeStubIn(t, bin, "qmax-silent", `exit 2`)
	if out := runQMax(&api.SessionContext{QMaxBin: silent}, ctx); !strings.Contains(out, "exit status 2") {
		t.Errorf("silent error = %q", out)
	}
	stderrOnly := writeStubIn(t, bin, "qmax-stderr", `echo "note" >&2`)
	if out := runQMax(&api.SessionContext{QMaxBin: stderrOnly}, ctx); out != "note" {
		t.Errorf("stderr-only success = %q", out)
	}
	if out := runQMax(&api.SessionContext{}, ctx); !strings.Contains(out, "qmax CLI not found") {
		t.Errorf("missing qmax = %q", out)
	}

	dir := t.TempDir()
	t.Chdir(dir)
	if out := executeToolViaQMax("write_file", map[string]interface{}{"path": "notes.txt", "content": "hello"}, sctx, ctx); !strings.Contains(out, `"success": true`) {
		t.Fatalf("write_file = %s", out)
	}
	if out := executeToolViaQMax("read_file", map[string]interface{}{"path": "notes.txt"}, sctx, ctx); out != "hello" {
		t.Fatalf("read_file = %q", out)
	}
	if out := executeToolViaQMax("write_file", map[string]interface{}{"path": "../escape.txt", "content": "x"}, sctx, ctx); !strings.Contains(out, "error") {
		t.Fatalf("escaping write = %s", out)
	}
}

func TestScriptHealingViaQMaxConfig(t *testing.T) {
	home := withTempHome(t)
	ctx := context.Background()
	fc := newFakeCloud(t)
	fc.handle("/api/automation/scripts/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			_, _ = io.WriteString(w, `{"id":1,"name":"login","code":"updated"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/404") {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"script":{"name":"login","code":"old code"}}`)
	})
	sctx := &api.SessionContext{}
	sctx.QMaxCfg.Token = "tok"
	sctx.QMaxCfg.CloudURL = fc.srv.URL

	if out := fetchScriptCode(&api.SessionContext{}, ctx, "1"); !strings.Contains(out, "not authenticated") {
		t.Fatalf("unauth fetch = %s", out)
	}
	if out := updateScriptCode(&api.SessionContext{}, ctx, "1", "n", "c"); !strings.Contains(out, "not authenticated") {
		t.Fatalf("unauth update = %s", out)
	}
	if out := executeToolViaQMax("get_script", map[string]interface{}{"script_id": 404}, sctx, ctx); !strings.Contains(out, "HTTP 404") {
		t.Fatalf("404 fetch = %s", out)
	}
	if out := executeToolViaQMax("rollback_script", map[string]interface{}{"script_id": 1}, sctx, ctx); !strings.Contains(out, "no backups found") {
		t.Fatalf("rollback without backups = %s", out)
	}
	out := executeToolViaQMax("update_script", map[string]interface{}{"script_id": 1, "name": "login", "content": "test('new code', async () => {})"}, sctx, ctx)
	if !strings.Contains(out, "updated") {
		t.Fatalf("update = %s", out)
	}
	if out := executeToolViaQMax("update_script", map[string]interface{}{"script_id": 1, "code": "eval(atob('x'))"}, sctx, ctx); !strings.Contains(out, "Security scan failed") {
		t.Fatalf("unsafe update = %s", out)
	}
	if out := rollbackScript(sctx, ctx, "2"); !strings.Contains(out, "no backup found for script 2") {
		t.Fatalf("rollback other script = %s", out)
	}
	out = executeToolViaQMax("rollback_script", map[string]interface{}{"script_id": 1}, sctx, ctx)
	if !strings.Contains(out, "updated") || !strings.Contains(lastBody(fc), "old code") {
		t.Fatalf("rollback = %s", out)
	}

	backups := filepath.Join(home, ".qmax-code", "script-backups")
	_ = os.WriteFile(filepath.Join(backups, "3_x.json"), []byte("not json"), 0o600)
	if out := rollbackScript(sctx, ctx, "3"); !strings.Contains(out, "failed to parse backup") {
		t.Fatalf("corrupt backup = %s", out)
	}

	fc.handle("/api/automation/scripts/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	})
	if out := updateScriptCode(sctx, ctx, "1", "n", "c"); !strings.Contains(out, "HTTP 403") {
		t.Fatalf("403 update = %s", out)
	}
	sctx.QMaxCfg.CloudURL = "http://127.0.0.1:1"
	if out := fetchScriptCode(sctx, ctx, "1"); !strings.Contains(out, "error") {
		t.Fatalf("unreachable fetch = %s", out)
	}
	if out := updateScriptCode(sctx, ctx, "1", "n", "c"); !strings.Contains(out, "error") {
		t.Fatalf("unreachable update = %s", out)
	}
	sctx.QMaxCfg.CloudURL = "://bad"
	if out := fetchScriptCode(sctx, ctx, "1"); !strings.Contains(out, "error") {
		t.Fatalf("bad URL fetch = %s", out)
	}
	if out := updateScriptCode(sctx, ctx, "1", "n", "c"); !strings.Contains(out, "error") {
		t.Fatalf("bad URL update = %s", out)
	}
}

func lastBody(fc *fakeCloud) string {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.bodies) == 0 {
		return ""
	}
	return fc.bodies[len(fc.bodies)-1]
}

func TestScreenshotVisionTools(t *testing.T) {
	withTempHome(t)
	fc := newFakeCloud(t)
	client := fc.client()
	sctx := &api.SessionContext{API: client}
	ctx := context.Background()

	if out := executeTool("analyze_screenshot", map[string]interface{}{}, sctx, ctx, nil); !strings.Contains(out, "execution_id is required") {
		t.Fatalf("missing id = %s", out)
	}
	if out := executeTool("get_page_elements", map[string]interface{}{}, sctx, ctx, nil); !strings.Contains(out, "execution_id is required") {
		t.Fatalf("missing id = %s", out)
	}
	fc.json("/api/playwright-execution/status/", `nope`)
	if out := executeTool("analyze_screenshot", map[string]interface{}{"execution_id": "e"}, sctx, ctx, nil); !strings.Contains(out, "Failed to parse execution status") {
		t.Fatalf("bad status = %s", out)
	}
	fc.json("/api/playwright-execution/status/", `{"screenshot_paths":[]}`)
	if out := executeTool("get_page_elements", map[string]interface{}{"execution_id": "e"}, sctx, ctx, nil); !strings.Contains(out, "No screenshots") {
		t.Fatalf("no screenshots = %s", out)
	}
	fc.json("/api/playwright-execution/status/", `{"screenshot_paths":[""]}`)
	if out := executeTool("get_page_elements", map[string]interface{}{"execution_id": "e"}, sctx, ctx, nil); !strings.Contains(out, "Screenshot path is empty") {
		t.Fatalf("empty path = %s", out)
	}

	fc.json("/api/playwright-execution/status/", `{"screenshot_paths":["run/final.png"]}`)
	t.Setenv("ANTHROPIC_API_KEY", "")
	out := executeTool("analyze_screenshot", map[string]interface{}{"execution_id": "e"}, sctx, ctx, nil)
	if !strings.Contains(out, "https://app.qualitymax.io/static/test-executions/run/final.png") || !strings.Contains(out, "ANTHROPIC_API_KEY") {
		t.Fatalf("backend fallback = %s", out)
	}

	vision := newFakeCloud(t)
	routeAllRequestsTo(t, vision.srv)
	client = api.NewAPIClient(&api.AuthConfig{APIKey: "qm", CloudURL: vision.srv.URL})
	sctx = &api.SessionContext{API: client}
	sctx.QMaxCfg.CloudURL = "https://cloud.example"
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	vision.json("/api/playwright-execution/status/", `{"screenshot_paths":["https://cdn.example/s.png"]}`)
	vision.json("/v1/messages", `{"content":[{"type":"text","text":"A login form with an email field."}]}`)
	if out := executeTool("get_page_elements", map[string]interface{}{"execution_id": "e"}, sctx, ctx, nil); out != "A login form with an email field." {
		t.Fatalf("vision = %s", out)
	}
	vision.json("/v1/messages", `{"content":[]}`)
	if out := callVisionAnalysis(sctx, "https://x/s.png", "p"); !strings.Contains(out, "Empty vision response") {
		t.Fatalf("empty vision = %s", out)
	}
	vision.json("/v1/messages", `garbage`)
	if out := callVisionAnalysis(sctx, "https://x/s.png", "p"); !strings.Contains(out, "Failed to parse vision response") {
		t.Fatalf("bad vision = %s", out)
	}
	vision.handle("/v1/messages", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
	})
	if out := callVisionAnalysis(sctx, "https://x/s.png", "p"); !strings.Contains(out, "Vision API error 503") {
		t.Fatalf("vision 503 = %s", out)
	}
}

func TestChainSecurityAuditOnPR(t *testing.T) {
	requirePOSIX(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	withTempHome(t)
	fc := newFakeCloud(t)
	fc.json("/api/", `{"audit":"clean"}`)
	client := fc.client()
	ctx := context.Background()

	if got := chainSecurityAuditOnPR(ctx, client, &api.SessionContext{}, `{"error":"x"}`); got != `{"error":"x"}` {
		t.Fatalf("failed PR passthrough = %s", got)
	}
	noRemote := `{"pr_number": 12}`
	if got := chainSecurityAuditOnPR(ctx, client, &api.SessionContext{}, noRemote); got != noRemote {
		t.Fatalf("no remote passthrough = %s", got)
	}

	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	t.Chdir(repo)
	sctx := &api.SessionContext{GitInfo: &api.GitInfo{RemoteURL: "git@github.com:acme/shop.git"}}
	prResp := `{"pr_url":"https://github.com/acme/shop/pull/34"}`
	if got := chainSecurityAuditOnPR(ctx, client, sctx, prResp); got != prResp {
		t.Fatalf("no base ref passthrough = %s", got)
	}
	cmd := exec.Command("git", "update-ref", "refs/remotes/origin/master", "HEAD")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v %s", err, out)
	}
	got := chainSecurityAuditOnPR(ctx, client, sctx, prResp)
	if !strings.Contains(got, "Security Audit PR Check") || !strings.Contains(got, "clean") {
		t.Fatalf("audit chain = %s", got)
	}

	for in, want := range map[string]string{
		"https://github.com/acme/shop.git": "acme/shop",
		"git@github.com:acme/shop":         "acme/shop",
		"https://gitlab.com/acme/shop":     "",
	} {
		if got := repoSlugFromRemote(in); got != want {
			t.Errorf("repoSlugFromRemote(%q) = %q, want %q", in, got, want)
		}
	}
}
