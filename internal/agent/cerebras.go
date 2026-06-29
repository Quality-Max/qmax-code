package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
)

// Cerebras integration — drive the full native agent loop through Cerebras's
// OpenAI-compatible /v1/chat/completions endpoint using native function
// calling. Unlike the Ollama path (prompt-based dispatch over ~10 actions),
// this exposes the complete qmax tool set, so Cerebras can handle all coding
// tasks directly.

// CerebrasClient wraps HTTP calls to a Cerebras (OpenAI-compatible) endpoint.
type CerebrasClient struct {
	BaseURL         string // e.g. "https://api.cerebras.ai/v1"
	Model           string // e.g. "gpt-oss-120b" or "gemma-4-31b"
	APIKey          string
	ReasoningEffort string // "", "none", "low", "medium", "high" (Gemma 4 thinking)
	HTTP            *http.Client
}

// NewCerebrasClient builds a client from config, or returns nil if no API key
// is configured. Base URL and model fall back to the package defaults.
func NewCerebrasClient(cfg *api.Config) *CerebrasClient {
	if cfg == nil || cfg.CerebrasKey == "" {
		return nil
	}
	base := cfg.CerebrasBaseURL
	if base == "" {
		base = api.CerebrasAPIBase
	}
	model := api.ResolveCerebrasModel(cfg.CerebrasModel)
	return &CerebrasClient{
		BaseURL:         strings.TrimRight(base, "/"),
		Model:           model,
		APIKey:          cfg.CerebrasKey,
		ReasoningEffort: api.NormalizeCerebrasReasoningEffort(cfg.CerebrasReasoningEffort),
		HTTP:            &http.Client{Timeout: 300 * time.Second},
	}
}

// ── OpenAI-compatible wire types ──────────────────────────────────────────

type oaiTool struct {
	Type     string      `json:"type"` // always "function"
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type oaiToolCallFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded string
}

type oaiToolCall struct {
	ID       string        `json:"id"`
	Type     string        `json:"type"` // "function"
	Function oaiToolCallFn `json:"function"`
}

type oaiMessage struct {
	Role       string        `json:"role"` // system | user | assistant | tool
	Content    interface{}   `json:"content"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"` // for role="tool"
}

type oaiContentPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *oaiImageURL `json:"image_url,omitempty"`
}

type oaiImageURL struct {
	URL string `json:"url"`
}

type oaiChatRequest struct {
	Model           string       `json:"model"`
	Messages        []oaiMessage `json:"messages"`
	Tools           []oaiTool    `json:"tools,omitempty"`
	MaxTokens       int          `json:"max_tokens,omitempty"`
	Temperature     float64      `json:"temperature"`
	Stream          bool         `json:"stream"`
	ReasoningEffort string       `json:"reasoning_effort,omitempty"` // Gemma 4 thinking: low|medium|high (none omitted)
}

type oaiChatResponse struct {
	Choices []struct {
		Message struct {
			Content   string        `json:"content"`
			ToolCalls []oaiToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	// TimeInfo carries Cerebras-specific request timing (tokens/sec, TTFT,
	// total time). Parsed loosely as a map so key-name changes don't break us.
	TimeInfo map[string]interface{} `json:"time_info,omitempty"`
}

// reasoningEffortIsOn reports whether the configured effort will actually
// engage Gemma 4 thinking (i.e. one of low/medium/high).
func reasoningEffortIsOn(effort string) bool {
	switch effort {
	case "low", "medium", "high":
		return true
	}
	return false
}

func effectiveCerebrasReasoningEffort(model, effort string) string {
	effort = api.NormalizeCerebrasReasoningEffort(effort)
	if !api.IsCerebrasGemma4Model(model) || !reasoningEffortIsOn(effort) {
		return ""
	}
	return effort
}

// CerebrasTokensPerSecond pulls a tokens/sec figure from a Cerebras
// time_info object, trying common key names. Returns 0 if unavailable.
func CerebrasTokensPerSecond(ti map[string]interface{}) float64 {
	if ti == nil {
		return 0
	}
	for _, k := range []string{
		"tokens_per_second",
		"output_tokens_per_second",
		"completion_tokens_per_second",
		"generation_tokens_per_second",
		"tps",
	} {
		if v, ok := ti[k]; ok {
			if f, ok := toFloat64(v); ok && f > 0 {
				return f
			}
		}
	}
	return 0
}

// CerebrasTTFTSec pulls time-to-first-token (in seconds) from time_info if present.
func CerebrasTTFTSec(ti map[string]interface{}) float64 {
	if ti == nil {
		return 0
	}
	for _, k := range []string{"time_to_first_token_sec", "ttft_sec", "time_to_first_token"} {
		if v, ok := ti[k]; ok {
			if f, ok := toFloat64(v); ok && f >= 0 {
				return f
			}
		}
	}
	// Some responses report TTFT in milliseconds.
	for _, k := range []string{"time_to_first_token_ms", "ttft_ms"} {
		if v, ok := ti[k]; ok {
			if f, ok := toFloat64(v); ok && f > 0 {
				return f / 1000.0
			}
		}
	}
	return 0
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

// Chat performs a single non-streaming chat completion. Cerebras is fast
// enough (~1000+ tok/s) that non-streaming keeps the tool loop simple and the
// tool-call parsing reliable.
func (c *CerebrasClient) Chat(ctx context.Context, msgs []oaiMessage, tools []oaiTool) (*oaiChatResponse, error) {
	reqBody := c.chatRequest(msgs, tools)
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cerebras request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cerebras error %d: %s", resp.StatusCode, string(body))
	}

	var out oaiChatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *CerebrasClient) chatRequest(msgs []oaiMessage, tools []oaiTool) oaiChatRequest {
	reasoningEffort := effectiveCerebrasReasoningEffort(c.Model, c.ReasoningEffort)
	// Gemma 4 thinking consumes output tokens; give the model more room when
	// reasoning is engaged so high-effort answers don't truncate. The hackathon
	// grants a 32K completion cap, so 16K is a safe default for reasoning turns.
	maxTokens := 8192
	if reasoningEffortIsOn(reasoningEffort) {
		maxTokens = 16384
	}
	return oaiChatRequest{
		Model:           c.Model,
		Messages:        msgs,
		Tools:           tools,
		MaxTokens:       maxTokens,
		Temperature:     0,
		Stream:          false,
		ReasoningEffort: reasoningEffort,
	}
}

// toolDefsToOpenAI converts qmax tool definitions (Anthropic-shaped:
// name/description/input_schema) into OpenAI function-calling tools.
func toolDefsToOpenAI(defs []api.ToolDef) []oaiTool {
	out := make([]oaiTool, 0, len(defs))
	for _, d := range defs {
		params := d.InputSchema
		if params == nil {
			params = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}
		out = append(out, oaiTool{
			Type: "function",
			Function: oaiFunction{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  params,
			},
		})
	}
	return out
}

// normalizeContent flattens a message Content (string, []api.ContentBlock, or
// []interface{} after a JSON round-trip) into typed blocks. The bool return is
// true when the content was a plain string (text returned in the string).
func normalizeContent(content interface{}) ([]api.ContentBlock, string, bool) {
	switch v := content.(type) {
	case string:
		return nil, v, true
	case []api.ContentBlock:
		return v, "", false
	case []interface{}:
		blocks := make([]api.ContentBlock, 0, len(v))
		for _, raw := range v {
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			b := api.ContentBlock{}
			b.Type, _ = m["type"].(string)
			b.Text, _ = m["text"].(string)
			b.ID, _ = m["id"].(string)
			b.Name, _ = m["name"].(string)
			b.ToolUseID, _ = m["tool_use_id"].(string)
			b.Content, _ = m["content"].(string)
			if in, ok := m["input"]; ok {
				b.Input = in
			}
			if source, ok := m["source"].(map[string]interface{}); ok {
				img := &api.ImageSource{}
				img.Type, _ = source["type"].(string)
				img.MediaType, _ = source["media_type"].(string)
				img.Data, _ = source["data"].(string)
				if img.MediaType != "" && img.Data != "" {
					b.Source = img
				}
			}
			blocks = append(blocks, b)
		}
		return blocks, "", false
	case nil:
		return nil, "", true
	}
	return nil, "", true
}

// historyToOpenAI converts qmax conversation history (Anthropic Messages
// shape) into OpenAI chat messages, mapping tool_use → assistant tool_calls
// and tool_result → role:"tool" messages. User image blocks are converted to
// OpenAI content parts so multimodal Cerebras models can inspect screenshots
// and pasted images.
func historyToOpenAI(system string, history []api.Message) []oaiMessage {
	msgs := make([]oaiMessage, 0, len(history)+1)
	if system != "" {
		msgs = append(msgs, oaiMessage{Role: "system", Content: system})
	}

	for _, m := range history {
		blocks, plain, isString := normalizeContent(m.Content)
		if isString {
			msgs = append(msgs, oaiMessage{Role: m.Role, Content: plain})
			continue
		}

		switch m.Role {
		case "assistant":
			var text strings.Builder
			var toolCalls []oaiToolCall
			for _, b := range blocks {
				switch b.Type {
				case "text":
					text.WriteString(b.Text)
				case "tool_use":
					args := "{}"
					if b.Input != nil {
						if raw, err := json.Marshal(b.Input); err == nil {
							args = string(raw)
						}
					}
					toolCalls = append(toolCalls, oaiToolCall{
						ID:       b.ID,
						Type:     "function",
						Function: oaiToolCallFn{Name: b.Name, Arguments: args},
					})
				}
			}
			msgs = append(msgs, oaiMessage{
				Role:      "assistant",
				Content:   text.String(),
				ToolCalls: toolCalls,
			})

		default: // user (and any other role with block content)
			var hasToolResult bool
			for _, b := range blocks {
				if b.Type == "tool_result" {
					hasToolResult = true
					msgs = append(msgs, oaiMessage{
						Role:       "tool",
						ToolCallID: b.ToolUseID,
						Content:    b.Content,
					})
				}
			}
			if !hasToolResult {
				msgs = append(msgs, oaiMessage{Role: m.Role, Content: contentBlocksToOpenAI(blocks)})
			}
		}
	}
	return msgs
}

func contentBlocksToOpenAI(blocks []api.ContentBlock) interface{} {
	var text strings.Builder
	var parts []oaiContentPart
	hasImage := false

	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text == "" {
				continue
			}
			text.WriteString(b.Text)
			parts = append(parts, oaiContentPart{Type: "text", Text: b.Text})
		case "image":
			if b.Source == nil || b.Source.MediaType == "" || b.Source.Data == "" {
				continue
			}
			hasImage = true
			parts = append(parts, oaiContentPart{
				Type: "image_url",
				ImageURL: &oaiImageURL{
					URL: "data:" + b.Source.MediaType + ";base64," + b.Source.Data,
				},
			})
		}
	}

	if hasImage {
		if len(parts) == 0 {
			return []oaiContentPart{{Type: "text", Text: ""}}
		}
		return parts
	}
	return text.String()
}
