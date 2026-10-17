package agent

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/tui"
)

func assertHaiku55Request(t *testing.T, req map[string]interface{}) {
	t.Helper()
	thinking, _ := req["thinking"].(map[string]interface{})
	if req["model"] != api.ModelHaiku55 || thinking["type"] != "disabled" {
		t.Fatalf("model=%v thinking=%v", req["model"], req["thinking"])
	}
	for _, key := range []string{"temperature", "top_p", "top_k"} {
		if _, ok := req[key]; ok {
			t.Fatalf("unsupported sampling parameter %s", key)
		}
	}
}

func TestHaiku55StreamingUsage(t *testing.T) {
	body := sseEvent("message_start", map[string]interface{}{"message": map[string]interface{}{"usage": api.APIUsage{
		InputTokens: 1, CacheReadInputTokens: 100_000, OutputTokens: 1,
	}}}) +
		sseEvent("content_block_start", map[string]interface{}{"index": 0, "content_block": map[string]string{"type": "thinking"}}) +
		sseEvent("content_block_delta", map[string]interface{}{"index": 0, "delta": map[string]string{"type": "thinking_delta", "thinking": "internal"}}) +
		sseEvent("content_block_stop", map[string]int{"index": 0}) +
		sseEvent("content_block_start", map[string]interface{}{"index": 1, "content_block": map[string]string{"type": "text"}}) +
		sseEvent("content_block_delta", map[string]interface{}{"index": 1, "delta": map[string]string{"type": "text_delta", "text": "visible"}}) +
		sseEvent("content_block_stop", map[string]int{"index": 1}) +
		sseEvent("message_delta", map[string]interface{}{"usage": api.APIUsage{OutputTokens: 3}}) +
		sseEvent("message_delta", map[string]interface{}{"delta": map[string]string{"stop_reason": "end_turn"}, "usage": api.APIUsage{OutputTokens: 10}})
	ag, sa, _ := newStreamingAgent(t, &api.SessionContext{LocalOnly: true}, body)
	ag.Cfg.Model = api.ModelHaiku55
	var out string
	var err error
	captureStdout(t, func() { out, err = ag.RunStreaming("hello", &tui.Terminal{}) })
	if err != nil || out != "visible" {
		t.Fatalf("response=%q err=%v", out, err)
	}
	assertHaiku55Request(t, sa.requests[0])
	if ag.Usage.InputTokens != 1 || ag.Usage.CacheReadInputTokens != 100_000 || ag.Usage.OutputTokens != 10 || ag.Usage.Requests != 1 || ag.LastContextTokens != 100_001 {
		t.Fatalf("usage=%+v context=%d", ag.Usage, ag.LastContextTokens)
	}
	if got := ag.Usage.EstimatedCost(api.ModelHaiku55); math.Abs(got-0.0050255) > 1e-12 {
		t.Fatalf("cost=%v", got)
	}
}

func TestHaiku55NonStreamingUsage(t *testing.T) {
	ag, _, fc := newStreamingAgent(t, &api.SessionContext{LocalOnly: true})
	ag.Cfg.Model = api.ModelHaiku55
	var sent map[string]interface{}
	fc.handle("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"thinking","thinking":"internal"},{"type":"text","text":"visible"}],"usage":{"input_tokens":1,"cache_creation_input_tokens":100000,"output_tokens":10}}`))
	})
	resp, err := ag.callAPI()
	if err != nil {
		t.Fatal(err)
	}
	assertHaiku55Request(t, sent)
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "visible" {
		t.Fatalf("content=%+v", resp.Content)
	}
	if ag.Usage.CacheCreationInputTokens != 100_000 || ag.Usage.Requests != 1 || ag.LastContextTokens != 100_001 {
		t.Fatalf("usage=%+v context=%d", ag.Usage, ag.LastContextTokens)
	}
	if got := ag.Usage.EstimatedCost(api.ModelHaiku55); math.Abs(got-0.0625255) > 1e-12 {
		t.Fatalf("cost=%v", got)
	}
}

func TestStreamingRecordsPartialUsage(t *testing.T) {
	// A stream can end after message_start, before final output usage arrives.
	// Keep the prompt charge and any initial output reported by the provider.
	body := sseEvent("message_start", map[string]interface{}{"message": map[string]interface{}{"usage": api.APIUsage{
		InputTokens: 60_000, OutputTokens: 2,
	}}})
	ag, _, _ := newStreamingAgent(t, &api.SessionContext{LocalOnly: true}, body)
	_, _, err := ag.callStreamingAPI(&tui.Terminal{}, api.ModelHaiku55)
	if err != nil {
		t.Fatal(err)
	}
	if ag.Usage.Requests != 1 || ag.Usage.OutputTokens != 2 || math.Abs(ag.Usage.EstimatedCost(api.ModelHaiku55)-0.006001) > 1e-12 {
		t.Fatalf("partial usage=%+v", ag.Usage)
	}
}
