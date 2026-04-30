package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Ollama integration — use a self-hosted local model for chat and,
// optionally, tool dispatch.
// Ollama exposes an OpenAI-compatible /v1/chat/completions endpoint.

// OllamaClient wraps HTTP calls to an Ollama instance with a circuit breaker.
type OllamaClient struct {
	baseURL    string // e.g. "https://user:pass@llm.example.com"
	model      string // e.g. "gemma3:4b-it-q4_K_M" (fast, for chat)
	agentModel string // e.g. "gemma3:12b-it-q4_K_M" (smarter, for tool dispatch)
	http       *http.Client

	mu              sync.Mutex
	failures        int
	lastFailure     time.Time
	cooldownSeconds int
}

const (
	ollamaMaxFailures = 3
	ollamaCooldownSec = 120
)

// NewOllamaClient creates a client if URL and model are configured.
// Returns nil if not configured.
func NewOllamaClient(cfg *Config) *OllamaClient {
	if cfg.OllamaURL == "" || cfg.OllamaModel == "" {
		return nil
	}
	agentModel := cfg.OllamaAgentModel
	if agentModel == "" {
		agentModel = cfg.OllamaModel // fall back to same model
	}
	return &OllamaClient{
		baseURL:         strings.TrimRight(cfg.OllamaURL, "/"),
		model:           cfg.OllamaModel,
		agentModel:      agentModel,
		http:            &http.Client{Timeout: 120 * time.Second},
		cooldownSeconds: ollamaCooldownSec,
	}
}

// ChatStreamingWithModel is like ChatStreaming but uses a specific model.
func (o *OllamaClient) ChatStreamingWithModel(ctx context.Context, model, system string, history []Message, term *Terminal) (string, error) {
	savedModel := o.model
	o.model = model
	defer func() { o.model = savedModel }()
	return o.ChatStreaming(ctx, system, history, term)
}

// Available returns true if the circuit breaker is closed (not tripped).
func (o *OllamaClient) Available() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failures < ollamaMaxFailures {
		return true
	}
	// Check if cooldown has elapsed
	if time.Since(o.lastFailure) > time.Duration(o.cooldownSeconds)*time.Second {
		o.failures = 0 // reset
		return true
	}
	return false
}

func (o *OllamaClient) recordFailure() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failures++
	o.lastFailure = time.Now()
}

func (o *OllamaClient) recordSuccess() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failures = 0
}

// ollamaChatRequest is the OpenAI-compatible request format.
type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ollamaChatChunk is a streaming chunk from the OpenAI-compatible endpoint.
type ollamaChatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// ChatStreaming sends a chat request to Ollama and streams the response.
// Returns the full text, or an error (caller should fall back to Claude).
func (o *OllamaClient) ChatStreaming(ctx context.Context, system string, history []Message, term *Terminal) (string, error) {
	// Convert Anthropic-style messages to OpenAI-style
	messages := make([]ollamaChatMessage, 0, len(history)+1)
	if system != "" {
		messages = append(messages, ollamaChatMessage{Role: "system", Content: system})
	}
	for _, msg := range history {
		text := extractPlainText(msg.Content)
		if text == "" {
			continue
		}
		messages = append(messages, ollamaChatMessage{Role: msg.Role, Content: text})
	}

	reqBody := ollamaChatRequest{
		Model:    o.model,
		Messages: messages,
		Stream:   true,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	reqURL := o.baseURL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	// Basic auth is embedded in the URL — Go's http client handles it
	if u, err := url.Parse(o.baseURL); err == nil && u.User != nil {
		pass, _ := u.User.Password()
		req.SetBasicAuth(u.User.Username(), pass)
		// Rewrite URL without credentials for the request
		clean := *u
		clean.User = nil
		req.URL, _ = url.Parse(clean.String() + "/v1/chat/completions")
	}

	resp, err := o.http.Do(req)
	if err != nil {
		o.recordFailure()
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		o.recordFailure()
		return "", fmt.Errorf("ollama error %d: %s", resp.StatusCode, string(body))
	}

	// Parse SSE stream
	var fullText strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}

		var chunk ollamaChatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				fullText.WriteString(choice.Delta.Content)
				term.StreamText(choice.Delta.Content)
			}
		}
	}

	result := fullText.String()
	if result == "" {
		o.recordFailure()
		return "", fmt.Errorf("ollama returned empty response")
	}

	o.recordSuccess()
	return result, nil
}

// extractPlainText pulls text from a message Content (string or []ContentBlock).
func extractPlainText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []ContentBlock:
		var parts []string
		for _, b := range v {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	case []interface{}:
		var parts []string
		for _, raw := range v {
			if block, ok := raw.(map[string]interface{}); ok {
				if t, _ := block["type"].(string); t == "text" {
					if text, ok := block["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// maskURL hides credentials in a URL for display.
func maskURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "****")
	}
	return u.String()
}
