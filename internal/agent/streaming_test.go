package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/sysutil"
	"github.com/Quality-Max/qmax-code/internal/tui"
)

// sseEvent renders one Anthropic server-sent event.
func sseEvent(event string, data interface{}) string {
	b, _ := json.Marshal(data)
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, b)
}

func sseTextTurn(text string) string {
	return sseEvent("message_start", map[string]interface{}{"type": "message_start", "message": map[string]interface{}{"usage": map[string]int{"input_tokens": 50}}}) +
		sseEvent("content_block_start", map[string]interface{}{"index": 0, "content_block": map[string]string{"type": "text"}}) +
		sseEvent("content_block_delta", map[string]interface{}{"index": 0, "delta": map[string]string{"type": "text_delta", "text": text}}) +
		sseEvent("content_block_stop", map[string]interface{}{"index": 0}) +
		sseEvent("message_delta", map[string]interface{}{"delta": map[string]string{"stop_reason": "end_turn"}, "usage": map[string]int{"output_tokens": 7}}) +
		sseEvent("message_stop", map[string]interface{}{}) +
		"data: not-json-ignored\n\n" + ": comment line\n\n"
}

func sseToolTurn(name, inputJSON string) string {
	return sseEvent("message_start", map[string]interface{}{"message": map[string]interface{}{"usage": map[string]int{"input_tokens": 30}}}) +
		sseEvent("content_block_start", map[string]interface{}{"index": 0, "content_block": map[string]string{"type": "text"}}) +
		sseEvent("content_block_delta", map[string]interface{}{"index": 0, "delta": map[string]string{"type": "text_delta", "text": "Let me check."}}) +
		sseEvent("content_block_start", map[string]interface{}{"index": 1, "content_block": map[string]string{"type": "tool_use", "id": "tu_1", "name": name}}) +
		sseEvent("content_block_delta", map[string]interface{}{"index": 1, "delta": map[string]string{"type": "input_json_delta", "partial_json": inputJSON}}) +
		sseEvent("content_block_stop", map[string]interface{}{"index": 1}) +
		sseEvent("content_block_start", map[string]interface{}{"index": 2, "content_block": map[string]string{"type": "tool_use", "id": "tu_2", "name": "update_plan"}}) +
		sseEvent("content_block_stop", map[string]interface{}{"index": 2}) +
		sseEvent("message_delta", map[string]interface{}{"delta": map[string]string{"stop_reason": "tool_use"}, "usage": map[string]int{"output_tokens": 12}})
}

// scriptedAnthropic serves the given SSE bodies in order and records requests.
type scriptedAnthropic struct {
	mu       sync.Mutex
	bodies   []string
	requests []map[string]interface{}
}

func (s *scriptedAnthropic) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req map[string]interface{}
	_ = json.Unmarshal(raw, &req)
	s.mu.Lock()
	s.requests = append(s.requests, req)
	var body string
	if len(s.bodies) > 0 {
		body, s.bodies = s.bodies[0], s.bodies[1:]
	} else {
		body = sseTextTurn("fallback")
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, body)
}

func newStreamingAgent(t *testing.T, sctx *api.SessionContext, bodies ...string) (*Agent, *scriptedAnthropic, *fakeCloud) {
	t.Helper()
	withTempHome(t)
	fc := newFakeCloud(t)
	sa := &scriptedAnthropic{bodies: bodies}
	fc.handle("/v1/messages", sa.handler)
	routeAllRequestsTo(t, fc.srv)
	ag := NewAgent(AgentConfig{AnthropicKey: "sk-ant-test", Model: api.ModelSonnet, ChatModel: api.ModelHaiku, Context: sctx})
	ag.Logger = sysutil.NewLogger("test")
	t.Cleanup(ag.Logger.Close)
	return ag, sa, fc
}

func TestRunStreamingToolLoop(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello from the repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	sctx := &api.SessionContext{LocalOnly: true}
	ag, sa, _ := newStreamingAgent(t, sctx,
		sseToolTurn("read_file", `{"path":"README.md"}`),
		sseTextTurn("The README says hello."),
	)
	ag.Cfg.Verbose = true
	ag.Cfg.AutoRoute = true

	var out string
	var err error
	captureStdout(t, func() { out, err = ag.RunStreaming("what does the readme say?", &tui.Terminal{}) })
	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	if out != "The README says hello." {
		t.Fatalf("final text = %q", out)
	}
	if len(sa.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(sa.requests))
	}
	if sa.requests[0]["model"] != api.ModelHaiku || sa.requests[1]["model"] != api.ModelSonnet {
		t.Fatalf("auto-route models = %v, %v", sa.requests[0]["model"], sa.requests[1]["model"])
	}
	toolResults, _ := json.Marshal(sa.requests[1]["messages"])
	if !strings.Contains(string(toolResults), "hello from the repo") {
		t.Fatalf("tool result not sent back: %s", toolResults)
	}
	if ag.Usage.InputTokens != 80 || ag.Usage.OutputTokens != 19 || ag.LastContextTokens != 50 {
		t.Fatalf("usage = %+v last=%d", ag.Usage, ag.LastContextTokens)
	}
}

func TestRunStreamingAPIErrors(t *testing.T) {
	ag, _, fc := newStreamingAgent(t, &api.SessionContext{})
	fc.handle("/v1/messages", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"type":"invalid_request_error"}}`, http.StatusBadRequest)
	})
	var err error
	captureStdout(t, func() { _, err = ag.RunStreaming("hi", &tui.Terminal{}) })
	if err == nil || !strings.Contains(err.Error(), "API error 400") {
		t.Fatalf("err = %v", err)
	}

	// Malformed history entries are sanitized before the request.
	ag.History = append(ag.History,
		api.Message{Role: "user", Content: "tool_result leftovers"},
		api.Message{Role: "assistant", Content: nil},
		api.Message{Role: "user", Content: 42},
		api.Message{Role: "assistant", Content: []interface{}{map[string]interface{}{"type": "text", "text": "x"}}},
	)
	var sent map[string]interface{}
	fc.handle("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)
		_, _ = io.WriteString(w, sseTextTurn("ok"))
	})
	captureStdout(t, func() { _, err = ag.RunStreaming("again", &tui.Terminal{}) })
	if err != nil {
		t.Fatalf("sanitized request failed: %v", err)
	}
	msgs, _ := json.Marshal(sent["messages"])
	if strings.Contains(string(msgs), "null") {
		t.Fatalf("nil content leaked into the request: %s", msgs)
	}
}

func TestRunStreamingCerebrasFailure(t *testing.T) {
	ag, _, fc := newStreamingAgent(t, &api.SessionContext{})
	fc.handle("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	})
	ag.Cerebras = &CerebrasClient{BaseURL: fc.srv.URL, APIKey: "csk", Model: api.CerebrasDefaultModel, HTTP: ag.client}
	var err error
	captureStdout(t, func() { _, err = ag.RunStreaming("hi", &tui.Terminal{}) })
	if err == nil {
		t.Fatal("expected cerebras failure")
	}
}

// ollamaStream renders an OpenAI-compatible streaming completion.
func ollamaStream(chunks ...string) string {
	var b strings.Builder
	for _, c := range chunks {
		data, _ := json.Marshal(map[string]interface{}{"choices": []map[string]interface{}{{"delta": map[string]string{"content": c}}}})
		fmt.Fprintf(&b, "data: %s\n\n", data)
	}
	b.WriteString("data: {bad json}\n\ndata: [DONE]\n\n")
	return b.String()
}

func newOllamaAgent(t *testing.T, sctx *api.SessionContext, mode OllamaMode, responses ...string) (*Agent, *fakeCloud) {
	t.Helper()
	ag, _, fc := newStreamingAgent(t, sctx, sseTextTurn("from claude"))
	var mu sync.Mutex
	fc.handle("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if len(responses) == 0 {
			http.Error(w, "exhausted", http.StatusInternalServerError)
			return
		}
		body := responses[0]
		responses = responses[1:]
		_, _ = io.WriteString(w, body)
	})
	cfg := api.DefaultConfig()
	cfg.OllamaURL = strings.Replace(fc.srv.URL, "http://", "http://user:pass@", 1)
	cfg.OllamaModel = "llama3.2:3b"
	ag.Ollama = NewOllamaClient(cfg)
	ag.Mode = mode
	return ag, fc
}

func TestOllamaChatMode(t *testing.T) {
	ag, _ := newOllamaAgent(t, &api.SessionContext{}, OllamaModeChat, ollamaStream("Hi ", "there!"))
	ag.Cfg.Verbose = true
	var out string
	captureStdout(t, func() { out, _ = ag.RunStreaming("hello", &tui.Terminal{}) })
	if out != "Hi there!" {
		t.Fatalf("ollama chat = %q", out)
	}

	// A failed local response falls back to Claude.
	ag, _ = newOllamaAgent(t, &api.SessionContext{}, OllamaModeChat)
	ag.Cfg.Verbose = true
	captureStdout(t, func() { out, _ = ag.RunStreaming("hello", &tui.Terminal{}) })
	if out != "from claude" {
		t.Fatalf("fallback = %q", out)
	}
}

func TestOllamaFullModeActions(t *testing.T) {
	sctx := &api.SessionContext{ProjectID: 4}
	ag, fc := newOllamaAgent(t, sctx, OllamaModeFull,
		ollamaStream(`Sure. <action>{"name": "list_projects", "params": {}}</action>`),
		ollamaStream("You have 2 projects."),
	)
	sctx.API = fc.client()
	fc.json("/api/projects", `[{"id":1},{"id":2}]`)
	ag.Cfg.Verbose = true
	var out string
	captureStdout(t, func() { out, _ = ag.RunStreaming("list my projects", &tui.Terminal{}) })
	if out != "You have 2 projects." {
		t.Fatalf("ollama full = %q", out)
	}

	// Pure chat in full mode.
	ag, _ = newOllamaAgent(t, &api.SessionContext{}, OllamaModeFull, ollamaStream("Just chatting."))
	captureStdout(t, func() { out, _ = ag.RunStreaming("hi", &tui.Terminal{}) })
	if out != "Just chatting." {
		t.Fatalf("full chat = %q", out)
	}

	// Summary failure falls back to the raw tool result.
	sctx = &api.SessionContext{}
	ag, fc = newOllamaAgent(t, sctx, OllamaModeFull, ollamaStream(`{"name": "get_project_summary", "params": {"project_id": 3}}`))
	sctx.API = fc.client()
	fc.json("/api/", `{"summary":"ok"}`)
	captureStdout(t, func() { out, _ = ag.RunStreaming("summarize project 3", &tui.Terminal{}) })
	if !strings.Contains(out, "summary") {
		t.Fatalf("raw fallback = %q", out)
	}

	// Standalone with no Anthropic fallback reports the failure.
	ag, _ = newOllamaAgent(t, &api.SessionContext{LocalOnly: true}, OllamaModeFull)
	ag.Cfg.AnthropicKey = ""
	var err error
	captureStdout(t, func() { _, err = ag.RunStreaming("hi", &tui.Terminal{}) })
	if err == nil || !strings.Contains(err.Error(), "standalone") {
		t.Fatalf("standalone failure = %v", err)
	}
}

func TestExecuteOllamaActionTable(t *testing.T) {
	withTempHome(t)
	fc := newFakeCloud(t)
	sctx := &api.SessionContext{API: fc.client(), ProjectID: 2}
	ag := NewAgent(AgentConfig{Context: sctx})
	ctx := t.Context()

	cases := []struct {
		action string
		params map[string]interface{}
		want   string
	}{
		{"list_projects", nil, "ok"},
		{"list_test_cases", map[string]interface{}{"search": "login"}, "ok"},
		{"list_scripts", nil, "ok"},
		{"run_test", map[string]interface{}{}, "script_id is required"},
		{"run_test", map[string]interface{}{"script_id": 3}, "ok"},
		{"start_crawl", map[string]interface{}{}, "url is required"},
		{"start_crawl", map[string]interface{}{"url": "https://x"}, "ok"},
		{"review_repo", map[string]interface{}{}, "repo_id is required"},
		{"review_repo", map[string]interface{}{"repo_id": 1}, "ok"},
		{"get_script", map[string]interface{}{"script_id": 1}, "ok"},
		{"get_project_summary", nil, "ok"},
		{"check_test_status", map[string]interface{}{"execution_id": "e"}, "ok"},
		{"create_pr", map[string]interface{}{"repo_id": 1}, "ok"},
		{"teleport", nil, "Unknown action"},
	}
	for _, tc := range cases {
		if got := ag.executeOllamaAction(tc.action, tc.params, ctx); !strings.Contains(got, tc.want) {
			t.Errorf("%s(%v) = %s, want %q", tc.action, tc.params, got, tc.want)
		}
	}

	ag.Cfg.Context = &api.SessionContext{}
	if got := ag.executeOllamaAction("list_projects", nil, ctx); !strings.Contains(got, "Not connected") {
		t.Errorf("disconnected = %s", got)
	}
	ag.Cfg.Context = &api.SessionContext{LocalOnly: true}
	if got := ag.executeOllamaAction("list_projects", nil, ctx); !strings.Contains(got, "standalone") {
		t.Errorf("standalone cloud action = %s", got)
	}
}

func TestOllamaClientEdges(t *testing.T) {
	var nilClient *OllamaClient
	if nilClient.Available() {
		t.Fatal("nil client reported available")
	}
	c := &OllamaClient{cooldownSeconds: 0}
	for i := 0; i < ollamaMaxFailures; i++ {
		c.recordFailure()
	}
	if !c.Available() {
		t.Fatal("zero cooldown should reopen the breaker")
	}
	c.cooldownSeconds = 3600
	for i := 0; i < ollamaMaxFailures; i++ {
		c.recordFailure()
	}
	if c.Available() {
		t.Fatal("tripped breaker reported available")
	}

	if got := extractPlainText([]interface{}{map[string]interface{}{"type": "text", "text": "a"}, map[string]interface{}{"type": "image"}, "x"}); got != "a" {
		t.Fatalf("extractPlainText(map blocks) = %q", got)
	}
	if got := extractPlainText([]api.ContentBlock{{Type: "text", Text: "a"}, {Type: "text", Text: "b"}}); got != "a\nb" {
		t.Fatalf("extractPlainText(blocks) = %q", got)
	}
	if got := extractPlainText(42); got != "" {
		t.Fatalf("extractPlainText(int) = %q", got)
	}
}
