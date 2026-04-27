package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// HTTP-shape tests for the three methods added in PR #29 — RunNativeTest,
// SetupCICD, and GenerateTestCode(framework). Without these, the client
// trusts string literals for endpoint paths and body keys; a server-side
// rename of /api/automation/execute would silently break the client.

// newTestClient wires an APIClient to a local httptest.Server so we can
// observe the outbound request (method, path, body, headers) and stub any
// response.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*APIClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &APIClient{
		BaseURL: srv.URL,
		APIKey:  "qm-test-key",
		HTTP:    srv.Client(),
	}, srv
}

// ---- RunNativeTest ----

func TestRunNativeTest_PostsToExecuteWithScriptID(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	var gotBody map[string]interface{}

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"status":"passed","passed_tests":3}`))
	})

	out := client.RunNativeTest(context.Background(), 42, "https://staging.example.com")

	if gotMethod != "POST" {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	if gotPath != "/api/automation/execute" {
		t.Errorf("path: got %q, want /api/automation/execute", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer qm-test-key") {
		t.Errorf("auth header missing bearer prefix: got %q", gotAuth)
	}
	if gotBody["script_id"].(float64) != 42 {
		t.Errorf("script_id: got %v, want 42", gotBody["script_id"])
	}
	if gotBody["custom_url"] != "https://staging.example.com" {
		t.Errorf("custom_url: got %v, want https://staging.example.com", gotBody["custom_url"])
	}
	if !strings.Contains(out, "passed_tests") {
		t.Errorf("expected response body in output, got %q", out)
	}
}

func TestRunNativeTest_OmitsCustomUrlWhenEmpty(t *testing.T) {
	var gotBody map[string]interface{}
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	})

	client.RunNativeTest(context.Background(), 42, "")

	if _, ok := gotBody["custom_url"]; ok {
		t.Errorf("custom_url should be omitted when base_url is empty, got %+v", gotBody)
	}
}

func TestRunNativeTest_PropagatesErrorPrefix(t *testing.T) {
	// Server returns an MCP-style envelope with a [NOT_FOUND] prefix.
	// doRequest must preserve the prefix in the returned error string so
	// the agent can parse it.
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"error":"[NOT_FOUND] Script 999 not found"}`))
	})
	out := client.RunNativeTest(context.Background(), 999, "")
	if !strings.Contains(out, "[NOT_FOUND]") {
		t.Errorf("expected error prefix preserved, got %q", out)
	}
	if !strings.Contains(out, "404") {
		t.Errorf("expected HTTP 404 in error, got %q", out)
	}
}

// ---- SetupCICD ----

func TestSetupCICD_PostsToRepoSetupCicd(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]interface{}

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"success":true,"pr_url":"https://github.com/x/y/pull/1"}`))
	})

	out := client.SetupCICD(context.Background(), 184, "rust", "main", "")

	if gotMethod != "POST" {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	if gotPath != "/api/repositories/184/setup-cicd" {
		t.Errorf("path: got %q, want /api/repositories/184/setup-cicd", gotPath)
	}
	if gotBody["framework"] != "rust" {
		t.Errorf("framework: got %v, want rust", gotBody["framework"])
	}
	if gotBody["target_branch"] != "main" {
		t.Errorf("target_branch: got %v, want main", gotBody["target_branch"])
	}
	if _, ok := gotBody["base_url"]; ok {
		t.Errorf("base_url should be omitted when empty, got %+v", gotBody)
	}
	if !strings.Contains(out, "pr_url") {
		t.Errorf("response body not returned, got %q", out)
	}
}

func TestSetupCICD_RejectsInvalidFrameworkBeforeWire(t *testing.T) {
	// The client-side validator must short-circuit before sending —
	// verify the httptest.Server NEVER receives a request.
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{}`))
	})

	out := client.SetupCICD(context.Background(), 1, "../admin", "main", "")
	if calls != 0 {
		t.Errorf("expected no HTTP call for invalid framework, got %d", calls)
	}
	if !strings.Contains(out, "Invalid framework") {
		t.Errorf("expected validator error, got %q", out)
	}
}

// ---- GenerateTestCode ----

func TestGenerateTestCode_SendsFrameworkWhenProvided(t *testing.T) {
	var gotBody map[string]interface{}
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	})

	client.GenerateTestCode(context.Background(), 100, false, "rust_cargo")

	if gotBody["test_case_id"].(float64) != 100 {
		t.Errorf("test_case_id: got %v, want 100", gotBody["test_case_id"])
	}
	if gotBody["framework"] != "rust_cargo" {
		t.Errorf("framework: got %v, want rust_cargo", gotBody["framework"])
	}
	if _, ok := gotBody["force"]; ok {
		t.Errorf("force should be omitted when false, got %+v", gotBody)
	}
}

func TestGenerateTestCode_OmitsFrameworkWhenEmpty(t *testing.T) {
	// Empty-string framework is how callers signal "let the server auto-detect".
	// Make sure we don't POST an empty string field (which would override).
	var gotBody map[string]interface{}
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	})

	client.GenerateTestCode(context.Background(), 100, false, "")

	if _, ok := gotBody["framework"]; ok {
		t.Errorf("framework should be omitted when empty, got %+v", gotBody)
	}
}

func TestGenerateTestCode_RejectsInvalidFramework(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
	})
	out := client.GenerateTestCode(context.Background(), 100, false, "jsinxi")
	if calls != 0 {
		t.Errorf("expected short-circuit, got %d HTTP calls", calls)
	}
	if !strings.Contains(out, "Invalid framework") {
		t.Errorf("expected validator error, got %q", out)
	}
}

// ---- ImportRepo ----

func TestImportRepo_OmitsTrainingConsentByDefault(t *testing.T) {
	var gotPath string
	var gotBody map[string]interface{}
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"success":true}`))
	})

	out := client.ImportRepo(context.Background(), "https://github.com/acme/app", 42, false, "", "", "")

	if gotPath != "/api/repositories/import" {
		t.Errorf("path: got %q, want /api/repositories/import", gotPath)
	}
	if strings.Contains(out, "error") {
		t.Fatalf("unexpected error: %s", out)
	}
	if _, ok := gotBody["training_consent"]; ok {
		t.Errorf("training_consent should be omitted by default, got %+v", gotBody)
	}
}

func TestImportRepo_AcceptsExplicitTrainingConsent(t *testing.T) {
	var gotBody map[string]interface{}
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"success":true}`))
	})

	_ = client.ImportRepo(context.Background(), "https://github.com/acme/app", 42, false, "", "", "opt_out")

	if gotBody["training_consent"] != "opt_out" {
		t.Errorf("training_consent: got %v, want opt_out", gotBody["training_consent"])
	}
}

func TestImportRepo_RejectsInvalidTrainingConsent(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
	})

	out := client.ImportRepo(context.Background(), "https://github.com/acme/app", 42, false, "", "", "yes")

	if calls != 0 {
		t.Errorf("expected no HTTP call for invalid training_consent, got %d", calls)
	}
	if !strings.Contains(out, "training_consent") {
		t.Errorf("expected training_consent validation error, got %q", out)
	}
}

// ---- validateFramework (direct unit test of the allow-list) ----

func TestValidateFramework_AllowList(t *testing.T) {
	for _, good := range []string{"", "playwright", "pytest", "go", "rust", "go_test", "rust_cargo", "cargo"} {
		if err := validateFramework(good); err != "" {
			t.Errorf("%q should be allowed, got error %s", good, err)
		}
	}
	for _, bad := range []string{"../admin", "jsinxi", "RUST", "node", "bash"} {
		if err := validateFramework(bad); err == "" {
			t.Errorf("%q should be rejected, got no error", bad)
		}
	}
}

// ---- Error-prefix round-trip (Blocker #2) ----

func TestDoRequest_PreservesForbiddenPrefix(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"error":"[FORBIDDEN] no team access"}`))
	})
	out := client.SetupCICD(context.Background(), 1, "rust", "main", "")
	if !strings.Contains(out, "[FORBIDDEN]") {
		t.Errorf("FORBIDDEN prefix lost; got %q", out)
	}
	if !strings.Contains(out, "no team access") {
		t.Errorf("detail lost; got %q", out)
	}
}

func TestDoRequest_PreservesFastAPIDetail(t *testing.T) {
	// Fallback path: FastAPI-style envelope {"detail": "..."}. Still
	// handled by doRequest.
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"invalid framework"}`))
	})
	out := client.SetupCICD(context.Background(), 1, "rust", "main", "")
	if !strings.Contains(out, "invalid framework") {
		t.Errorf("detail lost; got %q", out)
	}
	if !strings.Contains(out, "400") {
		t.Errorf("expected HTTP 400 in error, got %q", out)
	}
}
