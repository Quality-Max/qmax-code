package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/sysutil"
)

// Wire-shape tests for the one-line endpoint wrappers on APIClient. Each
// entry pins the HTTP method, path, query string, and body subset so a
// server-side route rename fails here instead of in a user session.

// requestCapture records the wire shape of a single inbound request: method,
// path, raw query, bearer token, and the decoded JSON body (nil for
// body-less requests).
type requestCapture struct {
	Method   string
	Path     string
	RawQuery string
	Auth     string
	Body     map[string]any
}

// recordingHandler captures the next request into got and replies with a
// generic JSON envelope.
func recordingHandler(got *requestCapture) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		*got = requestCapture{
			Method:   r.Method,
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
			Auth:     r.Header.Get("Authorization"),
			Body:     body,
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

// assertBodySubset checks that every entry of want matches the captured body.
func assertBodySubset(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	for k, v := range want {
		if !reflect.DeepEqual(got[k], v) {
			t.Errorf("body[%q] = %#v, want %#v", k, got[k], v)
		}
	}
}

func assertBodyAbsent(t *testing.T, got map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := got[k]; ok {
			t.Errorf("body[%q] should be omitted, got %#v", k, got[k])
		}
	}
}

type endpointCase struct {
	name        string
	call        func(c *APIClient) string
	wantMethod  string
	wantPath    string
	wantQuery   string // exact RawQuery match ("" = no query string)
	wantBody    map[string]any
	wantAbsent  []string
	emptyBodyOK bool // true = a request body may be absent entirely
}

func runEndpointTable(t *testing.T, cases []endpointCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got requestCapture
			client, _ := newTestClient(t, recordingHandler(&got))

			out := tc.call(client)

			if got.Method != tc.wantMethod {
				t.Errorf("method = %q, want %q", got.Method, tc.wantMethod)
			}
			if got.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", got.Path, tc.wantPath)
			}
			if got.RawQuery != tc.wantQuery {
				t.Errorf("query = %q, want %q", got.RawQuery, tc.wantQuery)
			}
			if len(tc.wantBody) == 0 {
				if !tc.emptyBodyOK && len(got.Body) != 0 {
					t.Errorf("expected no body fields, got %+v", got.Body)
				}
			} else {
				assertBodySubset(t, got.Body, tc.wantBody)
			}
			assertBodyAbsent(t, got.Body, tc.wantAbsent...)
			if strings.Contains(out, `"error"`) {
				t.Errorf("unexpected error envelope: %s", out)
			}
		})
	}
}

// ---- GET/DELETE endpoints ----

func TestClientGetAndDeleteEndpoints(t *testing.T) {
	runEndpointTable(t, []endpointCase{
		{"ListProjects", func(c *APIClient) string { return c.ListProjects(context.Background()) },
			"GET", "/api/projects", "limit=200", nil, nil, false},
		{"ListTestCases with limit and search", func(c *APIClient) string {
			return c.ListTestCases(context.Background(), 7, 10, "login")
		}, "GET", "/api/test-cases", "project_id=7&limit=10&search=login", nil, nil, false},
		{"ListTestCases without optional params", func(c *APIClient) string {
			return c.ListTestCases(context.Background(), 7, 0, "")
		}, "GET", "/api/test-cases", "project_id=7", nil, nil, false},
		{"ListScripts with limit", func(c *APIClient) string {
			return c.ListScripts(context.Background(), 3, 5)
		}, "GET", "/api/automation/scripts", "project_id=3&limit=5", nil, nil, false},
		{"ListScripts without limit", func(c *APIClient) string {
			return c.ListScripts(context.Background(), 3, 0)
		}, "GET", "/api/automation/scripts", "project_id=3", nil, nil, false},
		{"CheckTestStatus", func(c *APIClient) string {
			return c.CheckTestStatus(context.Background(), "exec-9")
		}, "GET", "/api/playwright-execution/status/exec-9", "", nil, nil, false},
		{"GetExecutionArtifact", func(c *APIClient) string {
			return c.GetExecutionArtifact(context.Background(), "exec-9", "video")
		}, "GET", "/api/playwright-execution/artifacts/exec-9/video", "", nil, nil, false},
		{"CrawlStatus", func(c *APIClient) string {
			return c.CrawlStatus(context.Background(), "crawl-1")
		}, "GET", "/api/ai-crawl/status/crawl-1", "", nil, nil, false},
		{"CrawlResults", func(c *APIClient) string {
			return c.CrawlResults(context.Background(), "crawl-1")
		}, "GET", "/api/ai-crawl/results/crawl-1", "", nil, nil, false},
		{"ListCrawlJobs with limit", func(c *APIClient) string {
			return c.ListCrawlJobs(context.Background(), 5)
		}, "GET", "/api/ai-crawl/jobs", "limit=5", nil, nil, false},
		{"ListCrawlJobs without limit", func(c *APIClient) string {
			return c.ListCrawlJobs(context.Background(), 0)
		}, "GET", "/api/ai-crawl/jobs", "", nil, nil, false},
		{"ListRepos", func(c *APIClient) string { return c.ListRepos(context.Background(), 4) },
			"GET", "/api/repositories/project/4", "", nil, nil, false},
		{"RepoCoverage", func(c *APIClient) string { return c.RepoCoverage(context.Background(), 4) },
			"GET", "/api/repositories/4/coverage", "", nil, nil, false},
		{"RepoQuality", func(c *APIClient) string { return c.RepoQuality(context.Background(), 4) },
			"GET", "/api/repositories/4/quality", "", nil, nil, false},
		{"ListAgentSessions", func(c *APIClient) string {
			return c.ListAgentSessions(context.Background(), 6, 25)
		}, "GET", "/api/agent-sessions", "project_id=6&limit=25", nil, nil, false},
		{"GetScript", func(c *APIClient) string { return c.GetScript(context.Background(), 8) },
			"GET", "/api/automation/scripts/8", "", nil, nil, false},
		{"K6ListScripts", func(c *APIClient) string { return c.K6ListScripts(context.Background(), 9) },
			"GET", "/api/k6/scripts", "project_id=9", nil, nil, false},
		{"K6GetScript", func(c *APIClient) string { return c.K6GetScript(context.Background(), 10) },
			"GET", "/api/k6/scripts/10", "", nil, nil, false},
		{"K6CheckStatus", func(c *APIClient) string {
			return c.K6CheckStatus(context.Background(), "k6exec-1")
		}, "GET", "/api/k6/status/k6exec-1", "", nil, nil, false},
		{"K6Report", func(c *APIClient) string {
			return c.K6Report(context.Background(), "k6exec-1")
		}, "GET", "/api/k6/executions/k6exec-1/report", "", nil, nil, false},
		{"GetProjectSummary", func(c *APIClient) string { return c.GetProjectSummary(context.Background(), 11) },
			"GET", "/api/projects/11", "", nil, nil, false},
		{"ExportFramework with framework", func(c *APIClient) string {
			return c.ExportFramework(context.Background(), 12, "pytest")
		}, "GET", "/api/frameworks/12/export", "framework=pytest", nil, nil, false},
		{"ExportFramework without framework", func(c *APIClient) string {
			return c.ExportFramework(context.Background(), 12, "")
		}, "GET", "/api/frameworks/12/export", "", nil, nil, false},
		{"GetInstallCommand", func(c *APIClient) string { return c.GetInstallCommand(context.Background(), 12) },
			"GET", "/api/frameworks/12/install-command", "", nil, nil, false},
		{"ExportQTML", func(c *APIClient) string { return c.ExportQTML(context.Background(), 13) },
			"GET", "/api/qtml/export", "project_id=13", nil, nil, false},
		{"CheckBackgroundJob", func(c *APIClient) string {
			return c.CheckBackgroundJob(context.Background(), "job-1")
		}, "GET", "/api/job-health/background/job-1", "", nil, nil, false},
		{"DeleteTestCase", func(c *APIClient) string { return c.DeleteTestCase(context.Background(), 14) },
			"DELETE", "/api/test-cases/14", "", nil, nil, false},
		{"DeleteProject", func(c *APIClient) string { return c.DeleteProject(context.Background(), 15) },
			"DELETE", "/api/projects/15", "", nil, nil, false},
	})
}

// ---- POST/PUT endpoints with request bodies ----

func TestClientPostEndpoints(t *testing.T) {
	hold := float64(sysutil.LiveFeedHoldSeconds)
	runEndpointTable(t, []endpointCase{
		{"RunTest minimal", func(c *APIClient) string {
			return c.RunTest(context.Background(), 16, false, "", "", false)
		}, "POST", "/api/playwright-execution/run/16", "",
			map[string]any{"headless": false, "use_browserbase": false, "live_feed_hold_seconds": hold},
			[]string{"use_e2b", "browser", "base_url"}, false},
		{"RunTest with sandbox browser and baseURL", func(c *APIClient) string {
			return c.RunTest(context.Background(), 16, true, "firefox", "https://staging.example.com", true)
		}, "POST", "/api/playwright-execution/run/16", "",
			map[string]any{"headless": true, "use_e2b": true, "browser": "firefox",
				"base_url": "https://staging.example.com", "live_feed_hold_seconds": hold},
			nil, false},
		{"RunTestsBatch parses ids and skips junk", func(c *APIClient) string {
			return c.RunTestsBatch(context.Background(), " 7 , 8 ,junk,9", "https://stg", true)
		}, "POST", "/api/automation/execute-batch", "",
			map[string]any{"script_ids": []any{float64(7), float64(8), float64(9)},
				"use_e2b": true, "base_url": "https://stg", "live_feed_hold_seconds": hold},
			nil, false},
		{"RunTestsBatch minimal", func(c *APIClient) string {
			return c.RunTestsBatch(context.Background(), "5", "", false)
		}, "POST", "/api/automation/execute-batch", "",
			map[string]any{"script_ids": []any{float64(5)}, "live_feed_hold_seconds": hold},
			[]string{"use_e2b", "base_url"}, false},
		{"ReportLocalResult", func(c *APIClient) string {
			return c.ReportLocalResult(context.Background(), 17, "passed", "all green", "go_test", 1.5)
		}, "POST", "/api/playwright-execution/report-local", "",
			map[string]any{"script_id": float64(17), "status": "passed", "output": "all green",
				"framework": "go_test", "duration": 1.5}, nil, false},
		{"StartCrawl minimal", func(c *APIClient) string {
			return c.StartCrawl(context.Background(), 5, "https://site.example.com", 0, 0, "", "", false)
		}, "POST", "/api/ai-crawl/start", "",
			map[string]any{"project_id": float64(5), "url": "https://site.example.com",
				"live_feed_hold_seconds": hold},
			[]string{"use_e2b", "depth", "pages_limit", "test_type", "custom_instructions"}, false},
		{"StartCrawl with all options", func(c *APIClient) string {
			return c.StartCrawl(context.Background(), 5, "https://site.example.com", 3, 10, "functional", "focus login", true)
		}, "POST", "/api/ai-crawl/start", "",
			map[string]any{"project_id": float64(5), "url": "https://site.example.com", "use_e2b": true,
				"depth": float64(3), "pages_limit": float64(10), "test_type": "functional",
				"custom_instructions": "focus login", "live_feed_hold_seconds": hold},
			nil, false},
		{"ReviewRepo", func(c *APIClient) string { return c.ReviewRepo(context.Background(), 18) },
			"POST", "/api/repositories/18/ai-review", "", nil, nil, true},
		{"GetReviewPreferences without repo", func(c *APIClient) string {
			return c.GetReviewPreferences(context.Background(), 0)
		}, "POST", "/api/mcp/tool/get_review_preferences", "", nil, nil, true},
		{"GetReviewPreferences with repo", func(c *APIClient) string {
			return c.GetReviewPreferences(context.Background(), 19)
		}, "POST", "/api/mcp/tool/get_review_preferences", "",
			map[string]any{"repository_id": float64(19)}, nil, false},
		{"SetReviewPreferences global scope", func(c *APIClient) string {
			return c.SetReviewPreferences(context.Background(), "global", 0, map[string]any{"focus": "security"})
		}, "POST", "/api/mcp/tool/set_review_preferences", "",
			map[string]any{"scope": "global", "preferences": map[string]any{"focus": "security"}},
			[]string{"repository_id"}, false},
		{"SetReviewPreferences repo scope", func(c *APIClient) string {
			return c.SetReviewPreferences(context.Background(), "repo", 20, map[string]any{"strictness": "high"})
		}, "POST", "/api/mcp/tool/set_review_preferences", "",
			map[string]any{"scope": "repo", "repository_id": float64(20),
				"preferences": map[string]any{"strictness": "high"}}, nil, false},
		{"ImportDocument with source name", func(c *APIClient) string {
			return c.ImportDocument(context.Background(), 21, "release notes", "notes.md")
		}, "POST", "/api/import/document/text", "",
			map[string]any{"project_id": float64(21), "text_content": "release notes",
				"source_name": "notes.md"}, nil, false},
		{"ImportDocument without source name", func(c *APIClient) string {
			return c.ImportDocument(context.Background(), 21, "release notes", "")
		}, "POST", "/api/import/document/text", "",
			map[string]any{"project_id": float64(21), "text_content": "release notes"},
			[]string{"source_name"}, false},
		{"CreatePR", func(c *APIClient) string { return c.CreatePR(context.Background(), 22, 23) },
			"POST", "/api/repositories/create-pr", "",
			map[string]any{"repo_id": float64(22), "project_id": float64(23)}, nil, false},
		{"SecurityAuditPRCheck", func(c *APIClient) string {
			return c.SecurityAuditPRCheck(context.Background(), "acme/app", 7, "sha-base", "sha-head")
		}, "POST", "/api/security-audit/pr-check", "",
			map[string]any{"repo_slug": "acme/app", "pr_number": float64(7),
				"base_sha": "sha-base", "head_sha": "sha-head"}, nil, false},
		{"GenerateTestCode with force", func(c *APIClient) string {
			return c.GenerateTestCode(context.Background(), 100, true, "rust_cargo")
		}, "POST", "/api/automation/generate", "",
			map[string]any{"test_case_id": float64(100), "force": true, "framework": "rust_cargo"}, nil, false},
		{"SetupCICD with baseURL", func(c *APIClient) string {
			return c.SetupCICD(context.Background(), 1, "rust", "main", "https://stg.example.com")
		}, "POST", "/api/repositories/1/setup-cicd", "",
			map[string]any{"framework": "rust", "target_branch": "main",
				"base_url": "https://stg.example.com"}, nil, false},
		{"ImportRepo with all options", func(c *APIClient) string {
			return c.ImportRepo(context.Background(), "https://github.com/acme/app", 35, true, "New Project", "https://stg", "opt_in")
		}, "POST", "/api/repositories/import", "",
			map[string]any{"repo_url": "https://github.com/acme/app", "project_id": float64(35),
				"create_new_project": true, "new_project_name": "New Project",
				"base_url": "https://stg", "training_consent": "opt_in"}, nil, false},
		{"K6CreateScript without code", func(c *APIClient) string {
			return c.K6CreateScript(context.Background(), 25, "smoke", "load", "https://api.example.com", "")
		}, "POST", "/api/k6/scripts", "",
			map[string]any{"project_id": float64(25), "name": "smoke", "test_type": "load",
				"target_url": "https://api.example.com"}, []string{"code"}, false},
		{"K6CreateScript with code", func(c *APIClient) string {
			return c.K6CreateScript(context.Background(), 25, "smoke", "load", "https://api.example.com", "import http from 'k6/http'")
		}, "POST", "/api/k6/scripts", "",
			map[string]any{"project_id": float64(25), "name": "smoke", "test_type": "load",
				"target_url": "https://api.example.com", "code": "import http from 'k6/http'"}, nil, false},
		{"K6RunTest minimal", func(c *APIClient) string {
			return c.K6RunTest(context.Background(), 26, 0, "")
		}, "POST", "/api/k6/run/26", "",
			map[string]any{"script_id": float64(26)}, []string{"vus", "duration"}, false},
		{"K6RunTest with vus and duration", func(c *APIClient) string {
			return c.K6RunTest(context.Background(), 26, 5, "30s")
		}, "POST", "/api/k6/run/26", "",
			map[string]any{"script_id": float64(26), "vus": float64(5), "duration": "30s"}, nil, false},
		{"K6Generate without endpoints", func(c *APIClient) string {
			return c.K6Generate(context.Background(), 27, "https://api.example.com", "load", "")
		}, "POST", "/api/k6/generate", "",
			map[string]any{"project_id": float64(27), "target_url": "https://api.example.com",
				"test_type": "load"}, []string{"endpoints"}, false},
		{"K6Generate with endpoints", func(c *APIClient) string {
			return c.K6Generate(context.Background(), 27, "https://api.example.com", "load", "/api/a,/api/b")
		}, "POST", "/api/k6/generate", "",
			map[string]any{"project_id": float64(27), "target_url": "https://api.example.com",
				"test_type": "load", "endpoints": "/api/a,/api/b"}, nil, false},
		{"K6Convert", func(c *APIClient) string {
			return c.K6Convert(context.Background(), "const x = 1", "playwright", "spike")
		}, "POST", "/api/k6/convert", "",
			map[string]any{"source_code": "const x = 1", "source_framework": "playwright",
				"test_type": "spike"}, nil, false},
		{"CreateProject generates key from name", func(c *APIClient) string {
			return c.CreateProject(context.Background(), "My Project 42!", "demo project", "https://acme.example.com")
		}, "POST", "/api/projects", "",
			map[string]any{"name": "My Project 42!", "key": "MYPROJECT4", "description": "demo project",
				"main_url": "https://acme.example.com"}, nil, false},
		{"CreateProject falls back to PROJ key", func(c *APIClient) string {
			return c.CreateProject(context.Background(), "!!", "", "")
		}, "POST", "/api/projects", "",
			map[string]any{"name": "!!", "key": "PROJ"}, []string{"description", "main_url"}, false},
		{"TriggerFrameworkRun with type", func(c *APIClient) string {
			return c.TriggerFrameworkRun(context.Background(), 27, "pytest")
		}, "POST", "/api/frameworks/27/run", "",
			map[string]any{"framework_type": "pytest"}, nil, false},
		{"TriggerFrameworkRun without type", func(c *APIClient) string {
			return c.TriggerFrameworkRun(context.Background(), 27, "")
		}, "POST", "/api/frameworks/27/run", "", nil, nil, true},
		{"AddScriptToFramework", func(c *APIClient) string {
			return c.AddScriptToFramework(context.Background(), 27, 29)
		}, "POST", "/api/frameworks/27/add-script", "",
			map[string]any{"script_id": float64(29)}, nil, false},
		{"EnhanceTestCase", func(c *APIClient) string { return c.EnhanceTestCase(context.Background(), 30) },
			"POST", "/api/test-cases/30/enhance", "", nil, nil, true},
		{"GenerateGapTests", func(c *APIClient) string { return c.GenerateGapTests(context.Background(), 31) },
			"POST", "/api/repositories/31/generate-gap-tests", "", nil, nil, true},
		{"StartCrawlFromTestCase", func(c *APIClient) string { return c.StartCrawlFromTestCase(context.Background(), 32) },
			"POST", "/api/ai-crawl/start-from-test-case/32", "", nil, nil, true},
		{"ImportQTML", func(c *APIClient) string {
			return c.ImportQTML(context.Background(), 33, "<qtml><case/></qtml>")
		}, "POST", "/api/qtml/import", "",
			map[string]any{"project_id": float64(33), "content": "<qtml><case/></qtml>"}, nil, false},
		{"ConvertQTMLToPlaywright", func(c *APIClient) string {
			return c.ConvertQTMLToPlaywright(context.Background(), "<qtml><case/></qtml>")
		}, "POST", "/api/qtml/convert-to-playwright", "",
			map[string]any{"content": "<qtml><case/></qtml>"}, nil, false},
		{"TestDeployedEnvironment", func(c *APIClient) string {
			return c.TestDeployedEnvironment(context.Background(), 34, "https://deploy.example.com")
		}, "POST", "/api/automation/test-deployed", "",
			map[string]any{"project_id": float64(34), "url": "https://deploy.example.com"}, nil, false},
	})
}

func TestClientPutEndpoints(t *testing.T) {
	runEndpointTable(t, []endpointCase{
		{"UpdateScript", func(c *APIClient) string {
			return c.UpdateScript(context.Background(), 24, "renamed", "new code")
		}, "PUT", "/api/automation/scripts/24", "",
			map[string]any{"name": "renamed", "code": "new code"}, nil, false},
		{"UpdateProject all fields", func(c *APIClient) string {
			return c.UpdateProject(context.Background(), 28, "renamed", "updated", "https://new.example.com")
		}, "PUT", "/api/projects/28", "",
			map[string]any{"name": "renamed", "description": "updated",
				"base_url": "https://new.example.com"}, nil, false},
		{"UpdateTestCase all fields", func(c *APIClient) string {
			return c.UpdateTestCase(context.Background(), 36, "new title", "new desc", "smoke", "high", "passed")
		}, "PUT", "/api/test-cases/36", "",
			map[string]any{"title": "new title", "description": "new desc", "category": "smoke",
				"priority": float64(2), "status": "passed"}, nil, false},
	})
}

// ---- NewAPIClient ----

func TestNewAPIClient_NilAuthReturnsNil(t *testing.T) {
	if NewAPIClient(nil) != nil {
		t.Error("NewAPIClient(nil) should return nil")
	}
}

func TestNewAPIClient_UsesAuthConfig(t *testing.T) {
	auth := &AuthConfig{APIKey: "qm-key", CloudURL: "https://cloud.example.com/"}
	c := NewAPIClient(auth)
	if c == nil {
		t.Fatal("expected client")
	}
	if c.BaseURL != "https://cloud.example.com" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", c.BaseURL)
	}
	if c.APIKey != "qm-key" {
		t.Errorf("APIKey = %q, want qm-key", c.APIKey)
	}
	if c.HTTP == nil {
		t.Error("HTTP client must be initialized")
	}
}

// ---- GetProjectBySlug fallback chain ----

func TestGetProjectBySlug_ExactHitMakesSingleRequest(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"project":{"id":1,"slug":"acme"}}`))
	})

	out := client.GetProjectBySlug(context.Background(), "acme")

	if calls != 1 {
		t.Errorf("exact slug hit should need exactly 1 request, got %d", calls)
	}
	if !strings.Contains(out, `"slug":"acme"`) {
		t.Errorf("expected project payload, got %q", out)
	}
}

func TestGetProjectBySlug_FuzzyFallbackMatchesName(t *testing.T) {
	var bySlugCalls []string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/projects/by-slug/"):
			bySlugCalls = append(bySlugCalls, r.URL.Path)
			if r.URL.Path == "/api/projects/by-slug/acme-app" {
				_, _ = w.Write([]byte(`{"project":{"id":3,"slug":"acme-app"}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Project not found"}`))
		case r.URL.Path == "/api/projects":
			_, _ = w.Write([]byte(`{"projects":[{"id":2,"name":"Other","key":"OTH","slug":"other"},{"id":3,"name":"Acme App","key":"ACME","slug":"acme-app"}]}`))
		default:
			http.NotFound(w, r)
		}
	})

	out := client.GetProjectBySlug(context.Background(), "acme")

	want := []string{"/api/projects/by-slug/acme", "/api/projects/by-slug/acme-app"}
	if !reflect.DeepEqual(bySlugCalls, want) {
		t.Errorf("by-slug calls = %v, want %v", bySlugCalls, want)
	}
	if !strings.Contains(out, `"slug":"acme-app"`) {
		t.Errorf("expected fuzzy-matched project, got %q", out)
	}
}

func TestGetProjectBySlug_FuzzyFallbackMatchesKey(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/projects/by-slug/ACME":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Project not found"}`))
		case r.URL.Path == "/api/projects/by-slug/acme-app":
			_, _ = w.Write([]byte(`{"project":{"id":3,"slug":"acme-app"}}`))
		case r.URL.Path == "/api/projects":
			_, _ = w.Write([]byte(`{"projects":[{"id":3,"name":"Unrelated Name","key":"ACME","slug":"acme-app"}]}`))
		default:
			http.NotFound(w, r)
		}
	})

	out := client.GetProjectBySlug(context.Background(), "ACME")
	if !strings.Contains(out, `"slug":"acme-app"`) {
		t.Errorf("expected key-matched project, got %q", out)
	}
}

func TestGetProjectBySlug_NoFuzzyMatchReturnsOriginal404(t *testing.T) {
	total := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		total++
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/projects/by-slug/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Project not found"}`))
		default:
			_, _ = w.Write([]byte(`{"projects":[{"id":9,"name":"Zebras","key":"ZEB","slug":"zebras"}]}`))
		}
	})

	out := client.GetProjectBySlug(context.Background(), "acme")

	if total != 2 {
		t.Errorf("expected by-slug + list requests only, got %d", total)
	}
	if !strings.Contains(out, "404") {
		t.Errorf("expected original 404 preserved, got %q", out)
	}
}

func TestGetProjectBySlug_UnparseableListReturnsOriginal404(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/projects/by-slug/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Project not found"}`))
		default:
			_, _ = w.Write([]byte(`not json at all`))
		}
	})

	out := client.GetProjectBySlug(context.Background(), "acme")
	if !strings.Contains(out, "404") {
		t.Errorf("expected original 404 when list is not JSON, got %q", out)
	}
}

// ---- HTTP helper error branches ----

func TestHTTPHelpersReturnJSONErrorOnInvalidURL(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	client.BaseURL = "http://exa mple.com" // space forces httpx.NewRequest parse failure

	calls := map[string]func() string{
		"get":    func() string { return client.get(context.Background(), "/x") },
		"post":   func() string { return client.post(context.Background(), "/x", map[string]any{}) },
		"put":    func() string { return client.put(context.Background(), "/x", map[string]any{}) },
		"patch":  func() string { return client.patch(context.Background(), "/x", map[string]any{}) },
		"delete": func() string { return client.delete(context.Background(), "/x") },
	}
	for name, fn := range calls {
		if out := fn(); !strings.HasPrefix(out, `{"error":`) {
			t.Errorf("%s: expected JSON error envelope, got %q", name, out)
		}
	}
}

func TestDoRequest_ReturnsJSONErrorOnTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	client := &APIClient{BaseURL: srv.URL, APIKey: "qm-test-key", HTTP: srv.Client()}
	out := client.ListProjects(context.Background())

	if !strings.HasPrefix(out, `{"error":`) {
		t.Errorf("expected JSON error envelope, got %q", out)
	}
	if !strings.Contains(out, "request failed") {
		t.Errorf("expected transport error detail, got %q", out)
	}
}
