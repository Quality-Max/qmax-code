package agent

import (
	"encoding/json"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestValidateCerebrasKey(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		wantErr   bool
		wantLooks bool
	}{
		{"valid csk key", "csk-vv52c6kwywjmejmnp8xd8c2p4", false, true},
		{"valid but no prefix", "abc123def456", false, false},
		{"empty", "", true, false},
		{"pasted shell command", "cp /Users/x/qmax-code /usr/local/bin/qmax-code", true, false},
		{"trailing space", "csk-abc ", true, false},
		{"internal tab", "csk-\tabc", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			looks, err := api.ValidateCerebrasKey(c.key)
			if (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr %v", err, c.wantErr)
			}
			if looks != c.wantLooks {
				t.Errorf("looksLikeKey = %v, want %v", looks, c.wantLooks)
			}
		})
	}
}

func TestNewCerebrasClientNilWithoutKey(t *testing.T) {
	if c := NewCerebrasClient(&api.Config{}); c != nil {
		t.Fatalf("expected nil client without CerebrasKey, got %+v", c)
	}
	if c := NewCerebrasClient(nil); c != nil {
		t.Fatalf("expected nil client for nil config, got %+v", c)
	}
}

func TestNewCerebrasClientDefaults(t *testing.T) {
	c := NewCerebrasClient(&api.Config{CerebrasKey: "csk-test"})
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.BaseURL != api.CerebrasAPIBase {
		t.Errorf("BaseURL = %q, want default %q", c.BaseURL, api.CerebrasAPIBase)
	}
	if c.Model != api.CerebrasDefaultModel {
		t.Errorf("Model = %q, want default %q", c.Model, api.CerebrasDefaultModel)
	}
}

func TestNewCerebrasClientOverrides(t *testing.T) {
	c := NewCerebrasClient(&api.Config{
		CerebrasKey:     "csk-test",
		CerebrasModel:   "zai-glm-4.7",
		CerebrasBaseURL: "https://proxy.internal/v1/", // trailing slash must be trimmed
	})
	if c.Model != "zai-glm-4.7" {
		t.Errorf("Model = %q, want override", c.Model)
	}
	if c.BaseURL != "https://proxy.internal/v1" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", c.BaseURL)
	}
}

func TestToolDefsToOpenAI(t *testing.T) {
	defs := []api.ToolDef{
		{
			Name:        "list_projects",
			Description: "List all projects",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{Name: "nilschema", Description: "no schema"}, // InputSchema nil → defaulted
	}
	out := toolDefsToOpenAI(defs)
	if len(out) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(out))
	}
	if out[0].Type != "function" || out[0].Function.Name != "list_projects" {
		t.Errorf("tool 0 mismatch: %+v", out[0])
	}
	if out[1].Function.Parameters == nil {
		t.Errorf("nil InputSchema should be defaulted to a non-nil object schema")
	}
	if out[1].Function.Parameters["type"] != "object" {
		t.Errorf("defaulted schema type = %v, want object", out[1].Function.Parameters["type"])
	}
}

func TestHistoryToOpenAIPlainStrings(t *testing.T) {
	hist := []api.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	msgs := historyToOpenAI("system prompt", hist)
	if len(msgs) != 3 {
		t.Fatalf("expected system+2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "system prompt" {
		t.Errorf("system message mismatch: %+v", msgs[0])
	}
	if msgs[1].Content != "hello" || msgs[2].Content != "hi there" {
		t.Errorf("content mismatch: %+v %+v", msgs[1], msgs[2])
	}
}

func TestHistoryToOpenAIToolRoundTrip(t *testing.T) {
	// assistant tool_use → OpenAI assistant.tool_calls; user tool_result → role:"tool".
	hist := []api.Message{
		{Role: "user", Content: "list my projects"},
		{Role: "assistant", Content: []api.ContentBlock{
			{Type: "text", Text: "Let me check."},
			{Type: "tool_use", ID: "call_1", Name: "list_projects", Input: map[string]interface{}{"limit": 10}},
		}},
		{Role: "user", Content: []api.ContentBlock{
			{Type: "tool_result", ToolUseID: "call_1", Content: `{"projects":[]}`},
		}},
	}
	msgs := historyToOpenAI("", hist)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(msgs), msgs)
	}

	asst := msgs[1]
	if asst.Role != "assistant" || asst.Content != "Let me check." {
		t.Errorf("assistant text mismatch: %+v", asst)
	}
	if len(asst.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(asst.ToolCalls))
	}
	tc := asst.ToolCalls[0]
	if tc.ID != "call_1" || tc.Type != "function" || tc.Function.Name != "list_projects" {
		t.Errorf("tool call fields mismatch: %+v", tc)
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v (%q)", err, tc.Function.Arguments)
	}
	if args["limit"] != float64(10) {
		t.Errorf("arguments = %v, want limit=10", args)
	}

	toolMsg := msgs[2]
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "call_1" || toolMsg.Content != `{"projects":[]}` {
		t.Errorf("tool result message mismatch: %+v", toolMsg)
	}
}

func TestHistoryToOpenAIImageBlocksBecomeOpenAIContentParts(t *testing.T) {
	hist := []api.Message{
		{Role: "user", Content: []api.ContentBlock{
			{Type: "image", Source: &api.ImageSource{
				Type:      "base64",
				MediaType: "image/png",
				Data:      "iVBORw0KGgo=",
			}},
			{Type: "text", Text: "What is visible?"},
		}},
	}
	msgs := historyToOpenAI("", hist)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	parts, ok := msgs[0].Content.([]oaiContentPart)
	if !ok {
		t.Fatalf("content type = %T, want []oaiContentPart", msgs[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("expected image+text parts, got %d (%+v)", len(parts), parts)
	}
	if parts[0].Type != "image_url" || parts[0].ImageURL == nil {
		t.Fatalf("first part should be image_url: %+v", parts[0])
	}
	if got := parts[0].ImageURL.URL; got != "data:image/png;base64,iVBORw0KGgo=" {
		t.Fatalf("image URL = %q", got)
	}
	if parts[1].Type != "text" || parts[1].Text != "What is visible?" {
		t.Fatalf("text part mismatch: %+v", parts[1])
	}
}

func TestHistoryToOpenAIPostJSONInterfaceBlocks(t *testing.T) {
	// After a session JSON round-trip, []api.ContentBlock becomes []interface{}
	// of map[string]interface{}. The converter must handle that shape too.
	hist := []api.Message{
		{Role: "assistant", Content: []interface{}{
			map[string]interface{}{"type": "tool_use", "id": "call_x", "name": "run_test", "input": map[string]interface{}{"script_id": 5}},
		}},
		{Role: "user", Content: []interface{}{
			map[string]interface{}{"type": "tool_result", "tool_use_id": "call_x", "content": "passed"},
		}},
	}
	msgs := historyToOpenAI("", hist)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if len(msgs[0].ToolCalls) != 1 || msgs[0].ToolCalls[0].Function.Name != "run_test" {
		t.Errorf("interface tool_use not converted: %+v", msgs[0])
	}
	if msgs[1].Role != "tool" || msgs[1].Content != "passed" {
		t.Errorf("interface tool_result not converted: %+v", msgs[1])
	}
}
