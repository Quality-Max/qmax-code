package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func authenticatedTestState(cloudURL string) *serverState {
	return newServerState(&api.SessionContext{
		Auth: &api.AuthConfig{
			APIKey:   "test-key",
			CloudURL: cloudURL,
		},
		ProjectID: 73,
	}, "test-version")
}

func writeRPCResponse(t *testing.T, w http.ResponseWriter, id interface{}, result interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(okResp(id, result)); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func TestAuthenticatedInitializePropagatesSafeClientIdentity(t *testing.T) {
	var got request
	var gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		if r.URL.Path != cloudMCPPath {
			t.Errorf("path = %q, want %q", r.URL.Path, cloudMCPPath)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("missing authorization header")
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeRPCResponse(t, w, got.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "qualitymax-cloud", "version": "cloud"},
		})
	}))
	defer server.Close()

	req := request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: json.RawMessage(`{
			"protocolVersion":"2024-11-05",
			"capabilities":{},
			"clientInfo":{"name":"codex-cli","version":"2.4.1"}
		}`),
	}
	resp := dispatchWithState(req, authenticatedTestState(server.URL))
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}

	var params initializeParams
	if err := json.Unmarshal(got.Params, &params); err != nil {
		t.Fatalf("decode forwarded params: %v", err)
	}
	if params.ClientInfo != (clientInfo{Name: "codex-cli", Version: "2.4.1"}) {
		t.Fatalf("forwarded clientInfo = %+v", params.ClientInfo)
	}
	if gotUserAgent != "qmax-code/test-version downstream/codex-cli/2.4.1" {
		t.Fatalf("User-Agent = %q", gotUserAgent)
	}

	result := resp.Result.(map[string]interface{})
	serverInfo := result["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "qmax-code" || serverInfo["version"] != "test-version" {
		t.Fatalf("downstream serverInfo = %+v", serverInfo)
	}
}

func TestSafeIdentityPartRejectsCredentialShapes(t *testing.T) {
	for _, value := range []string{
		"Bearer credential",
		"qm-placeholder",
		"sk-placeholder",
		"longheader.longpayload.signaturepart",
		"github_pat_placeholderplaceholder",
		"ghp_placeholderplaceholderplaceholder",
		"xoxb-placeholderplaceholder",
		"AKIAIOSFODNN7EXAMPLE",
		"abcdefghijklmnopqrstuvwxyz012345",
	} {
		if got := safeIdentityPart(value, "unknown"); got != "unknown" {
			t.Errorf("safeIdentityPart(%q) = %q, want unknown", value, got)
		}
	}
}

func TestAuthenticatedToolDiscoveryUsesCloudRegistryWithCompatibilityAliases(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		var params struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(req.Params, &params)
		if params.Cursor == "cloud-page-2" {
			writeRPCResponse(t, w, req.ID, map[string]interface{}{
				"tools": []toolDef{
					{Name: "ai_review", InputSchema: map[string]interface{}{"type": "object"}},
				},
			})
			return
		}
		writeRPCResponse(t, w, req.ID, map[string]interface{}{
			"tools": []map[string]interface{}{
				{
					"name":           "generate_code_for_test_case",
					"title":          "Generate test code",
					"description":    "Generate code",
					"inputSchema":    map[string]interface{}{"type": "object"},
					"outputSchema":   map[string]interface{}{"type": "object"},
					"annotations":    map[string]interface{}{"readOnlyHint": false},
					"_meta":          map[string]interface{}{"cloud": true},
					"futureProtocol": map[string]interface{}{"preserved": true},
				},
				{"name": "run_tests", "inputSchema": map[string]interface{}{"type": "object"}},
				{"name": "whoami", "inputSchema": map[string]interface{}{"type": "object"}},
			},
			"nextCursor": "cloud-page-2",
		})
	}))
	defer server.Close()

	resp := dispatchWithState(request{JSONRPC: "2.0", ID: 2, Method: "tools/list"}, authenticatedTestState(server.URL))
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	tools := resp.Result.(map[string]interface{})["tools"].([]toolDef)
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}

	for _, want := range []string{
		"generate_code_for_test_case",
		"run_tests",
		"whoami",
		"generate_test_code",
		"run_test",
		"run_tests_batch",
		"ai_review",
		"review_repo",
		"read_file",
		"run_command",
		"edit_file",
		"write_file",
	} {
		if !names[want] {
			t.Errorf("discovered tools missing %q", want)
		}
	}
	if names["list_projects"] {
		t.Error("blindly exposed static Go tool list_projects without cloud discovery")
	}
	if calls.Load() != 2 {
		t.Fatalf("cloud tools/list calls = %d, want 2 pages", calls.Load())
	}
	result := resp.Result.(map[string]interface{})
	if _, ok := result["nextCursor"]; ok {
		t.Fatalf("aggregated tools/list unexpectedly exposed nextCursor = %#v", result["nextCursor"])
	}
	first := tools[0]
	if first.Title != "Generate test code" ||
		first.OutputSchema["type"] != "object" ||
		first.Annotations["readOnlyHint"] != false ||
		first.Meta["cloud"] != true {
		t.Fatalf("cloud tool protocol fields were not preserved: %+v", first)
	}
	if string(first.Extra["futureProtocol"]) != `{"preserved":true}` {
		t.Fatalf("future cloud tool field was not preserved: %s", first.Extra["futureProtocol"])
	}
	localCount := 0
	for _, tool := range tools {
		if tool.Name == "read_file" {
			localCount++
		}
	}
	if localCount != 1 {
		t.Fatalf("read_file discovery count = %d, want 1 after pagination aggregation", localCount)
	}
}

func TestAuthenticatedCloudNativeAliasNameWinsAfterDiscovery(t *testing.T) {
	var got callParams
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		switch req.Method {
		case "tools/list":
			writeRPCResponse(t, w, req.ID, map[string]interface{}{
				"tools": []toolDef{
					{Name: "run_test", InputSchema: map[string]interface{}{"type": "object"}},
					{Name: "run_tests", InputSchema: map[string]interface{}{"type": "object"}},
				},
			})
		case "tools/call":
			if err := json.Unmarshal(req.Params, &got); err != nil {
				t.Errorf("decode params: %v", err)
			}
			writeRPCResponse(t, w, req.ID, map[string]interface{}{"content": []interface{}{}})
		default:
			t.Errorf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()

	state := authenticatedTestState(server.URL)
	listResp := dispatchWithState(request{JSONRPC: "2.0", ID: 201, Method: "tools/list"}, state)
	if listResp.Error != nil {
		t.Fatalf("tools/list error: %+v", listResp.Error)
	}
	tools := listResp.Result.(map[string]interface{})["tools"].([]toolDef)
	runTestCount := 0
	for _, tool := range tools {
		if tool.Name == "run_test" {
			runTestCount++
		}
	}
	if runTestCount != 1 {
		t.Fatalf("run_test discovery count = %d, want native cloud tool exactly once", runTestCount)
	}

	callResp := dispatchWithState(request{
		JSONRPC: "2.0",
		ID:      202,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"run_test","arguments":{"native":true}}`),
	}, state)
	if callResp.Error != nil {
		t.Fatalf("tools/call error: %+v", callResp.Error)
	}
	if got.Name != "run_test" || got.Arguments["native"] != true {
		t.Fatalf("native cloud call was translated: %+v", got)
	}
}

func TestAuthenticatedAliasExecutesExactlyOnceThroughCloudMCP(t *testing.T) {
	var calls atomic.Int32
	var got callParams
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if err := json.Unmarshal(req.Params, &got); err != nil {
			t.Errorf("decode params: %v", err)
		}
		writeRPCResponse(t, w, req.ID, map[string]interface{}{
			"content": []map[string]interface{}{{"type": "text", "text": `{"execution_id":"example"}`}},
			"isError": false,
		})
	}))
	defer server.Close()

	req := request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"run_test","arguments":{"script_id":41,"headless":true}}`),
	}
	resp := dispatchWithState(req, authenticatedTestState(server.URL))
	if resp.Error != nil {
		t.Fatalf("tools/call error: %+v", resp.Error)
	}
	if calls.Load() != 1 {
		t.Fatalf("cloud MCP calls = %d, want exactly 1", calls.Load())
	}
	if got.Name != "run_tests" {
		t.Fatalf("forwarded tool name = %q, want run_tests", got.Name)
	}
	ids, ok := got.Arguments["script_ids"].([]interface{})
	if !ok || len(ids) != 1 || ids[0] != "41" {
		t.Fatalf("forwarded script_ids = %#v, want [\"41\"]", got.Arguments["script_ids"])
	}
	if got.Arguments["headless"] != true {
		t.Fatalf("forwarded headless = %#v, want true", got.Arguments["headless"])
	}
}

func TestAuthenticatedArgumentlessCallForwardsEmptyObject(t *testing.T) {
	var got callParams
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if err := json.Unmarshal(req.Params, &got); err != nil {
			t.Errorf("decode params: %v", err)
		}
		writeRPCResponse(t, w, req.ID, map[string]interface{}{"content": []interface{}{}})
	}))
	defer server.Close()

	resp := dispatchWithState(request{
		JSONRPC: "2.0",
		ID:      31,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"whoami"}`),
	}, authenticatedTestState(server.URL))
	if resp.Error != nil {
		t.Fatalf("tools/call error: %+v", resp.Error)
	}
	if got.Name != "whoami" || got.Arguments == nil || len(got.Arguments) != 0 {
		t.Fatalf("forwarded call = %+v, want whoami with an empty arguments object", got)
	}
}

func TestAuthenticatedLocalToolNeverCallsCloud(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeRPCResponse(t, w, 1, map[string]interface{}{})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "local.txt")
	if err := os.WriteFile(path, []byte("local-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	rawParams, err := json.Marshal(callParams{
		Name:      "read_file",
		Arguments: map[string]interface{}{"path": path},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := dispatchWithState(request{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "tools/call",
		Params:  rawParams,
	}, authenticatedTestState(server.URL))
	if resp.Error != nil {
		t.Fatalf("local tools/call error: %+v", resp.Error)
	}
	if calls.Load() != 0 {
		t.Fatalf("cloud MCP calls = %d, want 0 for a local tool", calls.Load())
	}
	data, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "local-only") {
		t.Fatalf("local tool result = %s", data)
	}
}

func TestAuthenticatedNotificationIsForwardedWithoutIDOrResponse(t *testing.T) {
	var calls atomic.Int32
	var hasID bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var message map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, hasID = message["id"]
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	_, ok := handleLineWithState(
		[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`),
		authenticatedTestState(server.URL),
	)
	if ok {
		t.Fatal("notification should not produce a downstream response")
	}
	if calls.Load() != 1 {
		t.Fatalf("cloud MCP calls = %d, want 1", calls.Load())
	}
	if hasID {
		t.Fatal("forwarded JSON-RPC notification unexpectedly contained an id")
	}
}

func TestCloudMCPClientDecodesSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: message\n" +
				"data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1}}\n\n" +
				"event: message\n" +
				"data: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"wrong\":true}}\n\n" +
				"event: message\n" +
				"data: {\"jsonrpc\":\"2.0\",\"id\":8,\"result\":{\"ok\":true}}\n\n",
		))
	}))
	defer server.Close()

	client := authenticatedTestState(server.URL).cloud
	resp, ok, err := client.call(t.Context(), request{JSONRPC: "2.0", ID: 8, Method: "tools/list"})
	if err != nil {
		t.Fatalf("call() error = %v", err)
	}
	if !ok || resp.Error != nil {
		t.Fatalf("call() = (%+v, %v), want successful response", resp, ok)
	}
	result, valid := resp.Result.(map[string]interface{})
	if !valid || result["ok"] != true {
		t.Fatalf("SSE result = %#v", resp.Result)
	}
}

func TestCloudMCPClientNeverFollowsRedirects(t *testing.T) {
	var redirectTargetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectTargetCalls.Add(1)
		writeRPCResponse(t, w, 9, map[string]interface{}{"unexpected": true})
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+cloudMCPPath, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client := authenticatedTestState(source.URL).cloud
	_, _, err := client.call(t.Context(), request{
		JSONRPC: "2.0",
		ID:      9,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"mutating_tool","arguments":{}}`),
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("call() error = %v, want an HTTP 307 rejection", err)
	}
	if redirectTargetCalls.Load() != 0 {
		t.Fatalf("redirect target POSTs = %d, want 0", redirectTargetCalls.Load())
	}
}
