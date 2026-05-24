package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/session"
	"github.com/qualitymax/qmax-code/internal/sysutil"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// AgentConfig holds configuration for the LLM agent.
type AgentConfig struct {
	AnthropicKey  string
	Model         string // base model (used for tool execution loops)
	ChatModel     string // cheaper model for conversational responses
	Verbose       bool
	OutputVerbose bool // user-facing answer style; false = compact, true = detailed
	Context       *api.SessionContext
	AutoRoute     bool // true = haiku for chat, sonnet for tools
	Professional  bool // disable cat personality, be direct
}

// OllamaMode controls how much work Ollama handles.
type OllamaMode int

const (
	OllamaModeOff  OllamaMode = iota // All calls go to Claude
	OllamaModeChat                   // Chat only (simple Q&A), tools via Claude
	OllamaModeFull                   // Everything including tool dispatch
)

func (m OllamaMode) String() string {
	switch m {
	case OllamaModeChat:
		return "chat"
	case OllamaModeFull:
		return "full"
	default:
		return "off"
	}
}

// Agent is the LLM-powered QA orchestration engine.
//
// Several fields are exported because the REPL (cmd/qmax) drives session
// state from the outside — appending to History on resume, reading Usage
// to print cost, wiring an Ollama client after construction, etc. The
// unexported fields (tools, client, cancel, priorSessions, sessionsFetched)
// are pure internals.
type Agent struct {
	Cfg       AgentConfig
	AppConfig *api.Config // persistent user preferences
	History   []api.Message
	Usage     api.TokenUsage
	Logger    *sysutil.Logger
	Ollama    *OllamaClient // optional self-hosted LLM
	Mode      OllamaMode    // off, chat, or full

	tools           []api.ToolDef
	client          *http.Client
	cancelMu        sync.Mutex
	cancel          context.CancelFunc // cancel the current streaming request; protected by cancelMu
	priorSessions   string             // cached prior-session summaries from backend
	sessionsFetched bool               // true once loadPriorSessions has run
}

// SSE event types from Anthropic streaming API
type sseMessageStart struct {
	Type    string `json:"type"`
	Message struct {
		ID         string       `json:"id"`
		Role       string       `json:"role"`
		StopReason *string      `json:"stop_reason"`
		Usage      api.APIUsage `json:"usage"`
	} `json:"message"`
}

type sseContentBlockStart struct {
	Type         string           `json:"type"`
	Index        int              `json:"index"`
	ContentBlock api.ContentBlock `json:"content_block"`
}

type sseContentBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
	} `json:"delta"`
}

type sseMessageDelta struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// NewAgent creates a new LLM agent.
func NewAgent(cfg AgentConfig) *Agent {
	return &Agent{
		Cfg:     cfg,
		History: []api.Message{},
		tools:   BuildToolDefs(),
		client:  &http.Client{Timeout: 300 * time.Second},
	}
}

// ClearHistory resets conversation history.
func (a *Agent) ClearHistory() {
	a.History = []api.Message{}
}

// CancelCurrent cancels the current streaming request if one is in progress.
// Safe to call from any goroutine concurrently with RunStreaming.
func (a *Agent) CancelCurrent() {
	a.cancelMu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.cancelMu.Unlock()
}

// Run executes a prompt through the agent loop and returns the final text response.
// Used for non-interactive (one-shot) mode.
func (a *Agent) Run(prompt string) (string, error) {
	a.History = append(a.History, api.Message{
		Role:    "user",
		Content: prompt,
	})

	for iterations := 0; iterations < maxIterations; iterations++ {
		resp, err := a.callAPI()
		if err != nil {
			return "", fmt.Errorf("API call failed: %w", err)
		}

		a.History = append(a.History, api.Message{
			Role:    "assistant",
			Content: resp.Content,
		})

		if resp.StopReason == "tool_use" {
			toolResults := a.executeToolCalls(resp.Content, context.Background())
			a.History = append(a.History, api.Message{
				Role:    "user",
				Content: toolResults,
			})
			continue
		}

		return a.extractText(resp.Content), nil
	}

	return "", fmt.Errorf("agent loop exceeded maximum iterations")
}

// compressHistory summarizes old messages when history gets too large.
const (
	maxHistoryTokens   = 40000 // compress at 90% of ~45K practical limit
	maxSessionMessages = 40    // hard limit — force compression after 40 messages (~20 turns)
	maxIterations      = 50
)

func (a *Agent) compressHistory() {
	// Rough token estimate: 4 chars ≈ 1 token
	totalChars := 0
	for _, msg := range a.History {
		totalChars += estimateMessageChars(msg)
	}

	estimatedTokens := totalChars / 4
	if estimatedTokens < maxHistoryTokens && len(a.History) < maxSessionMessages {
		return // within budget
	}

	// Keep the last 6 messages (3 user + 3 assistant turns) and summarize the rest
	if len(a.History) <= 6 {
		return
	}

	// Build a summary of older messages
	var summary strings.Builder
	summary.WriteString("[Previous conversation summary]\n")
	oldMessages := a.History[:len(a.History)-6]
	for _, msg := range oldMessages {
		role := msg.Role
		switch v := msg.Content.(type) {
		case string:
			if len(v) > 200 {
				summary.WriteString(fmt.Sprintf("%s: %s...\n", role, v[:200]))
			} else {
				summary.WriteString(fmt.Sprintf("%s: %s\n", role, v))
			}
		case []api.ContentBlock:
			for _, block := range v {
				if block.Type == "text" && block.Text != "" {
					text := block.Text
					if len(text) > 200 {
						text = text[:200] + "..."
					}
					summary.WriteString(fmt.Sprintf("%s: %s\n", role, text))
				} else if block.Type == "tool_use" {
					summary.WriteString(fmt.Sprintf("%s: [called %s]\n", role, block.Name))
				} else if block.Type == "tool_result" {
					content := block.Content
					if len(content) > 100 {
						content = content[:100] + "..."
					}
					summary.WriteString(fmt.Sprintf("%s: [tool result: %s]\n", role, content))
				}
			}
		}
	}

	// Replace history with summary + recent messages.
	// Use []api.ContentBlock for assistant to avoid API validation issues.
	compressed := []api.Message{
		{Role: "user", Content: summary.String()},
		{Role: "assistant", Content: []api.ContentBlock{{Type: "text", Text: "Got it, I have the context from our earlier conversation."}}},
	}
	// Keep last 6 messages, but ensure tool_result messages stay paired with their tool_use
	keep := a.History[len(a.History)-6:]
	// If the first kept message is a user tool_result without a preceding assistant tool_use,
	// skip it to avoid orphaned tool_results
	if len(keep) > 0 && keep[0].Role == "user" {
		if blocks, ok := keep[0].Content.([]api.ContentBlock); ok && len(blocks) > 0 && blocks[0].Type == "tool_result" {
			keep = keep[1:] // skip orphaned tool_result
		}
	}
	compressed = append(compressed, keep...)
	a.History = compressed
}

// RunStreaming executes a prompt with real-time SSE streaming to the terminal.
// BuildUserContent creates a content payload for a user message.
// estimateMessageChars estimates the character count of a message's content,
// handling both typed []api.ContentBlock and deserialized []interface{} from JSON.
func estimateMessageChars(msg api.Message) int {
	switch v := msg.Content.(type) {
	case string:
		return len(v)
	case []api.ContentBlock:
		n := 0
		for _, block := range v {
			n += len(block.Text) + len(block.Content)
		}
		return n
	case []interface{}:
		n := 0
		for _, raw := range v {
			if block, ok := raw.(map[string]interface{}); ok {
				if text, ok := block["text"].(string); ok {
					n += len(text)
				}
				if content, ok := block["content"].(string); ok {
					n += len(content)
				}
			}
		}
		return n
	default:
		return 0
	}
}

// If images are provided, it builds a multi-block content array (text + images).
// Otherwise, it returns the plain string (simpler, lower token usage).
func BuildUserContent(text string, images []tui.ImageAttachment) interface{} {
	if len(images) == 0 {
		return text
	}
	blocks := make([]api.ContentBlock, 0, len(images)+1)
	for _, img := range images {
		blocks = append(blocks, api.ContentBlock{
			Type: "image",
			Source: &api.ImageSource{
				Type:      "base64",
				MediaType: img.MediaType,
				Data:      img.Data,
			},
		})
	}
	if text != "" {
		blocks = append(blocks, api.ContentBlock{
			Type: "text",
			Text: text,
		})
	}
	return blocks
}

// RunStreamingWithImages is like RunStreaming but supports image attachments.
func (a *Agent) RunStreamingWithImages(prompt string, images []tui.ImageAttachment, term *tui.Terminal) (string, error) {
	a.History = append(a.History, api.Message{
		Role:    "user",
		Content: BuildUserContent(prompt, images),
	})
	a.Logger.Info("agent", "user_message_with_images", map[string]interface{}{"turns": len(a.History), "images": len(images)})
	return a.runStreamingLoop(term)
}

func (a *Agent) RunStreaming(prompt string, term *tui.Terminal) (string, error) {
	a.History = append(a.History, api.Message{
		Role:    "user",
		Content: prompt,
	})
	a.Logger.Info("agent", "user_message", map[string]interface{}{"turns": len(a.History)})
	return a.runStreamingLoop(term)
}

func (a *Agent) runStreamingLoop(term *tui.Terminal) (string, error) {
	for iterations := 0; iterations < maxIterations; iterations++ {
		// Sanitize + compress history before each API call
		session.SanitizeSessionMessages(a.History)
		a.compressHistory()
		model := a.modelForIteration(iterations)
		if a.Cfg.Verbose {
			fmt.Fprintf(term.Stderr(), "[model] %s (iteration %d)\n", model, iterations)
		}

		// Ollama routing based on mode:
		// - OllamaModeFull: local model handles everything via prompt dispatch
		// - OllamaModeChat: local model handles chat only, tool requests go to Claude
		// - OllamaModeOff: everything goes to Claude
		if iterations == 0 && a.Ollama != nil && a.Ollama.Available() && a.Mode != OllamaModeOff {
			if a.Mode == OllamaModeFull {
				if a.Cfg.Verbose {
					fmt.Fprintf(term.Stderr(), "[ollama-full] trying %s\n", a.Ollama.AgentModel)
				}
				result, ok := a.RunOllamaAgent(term)
				if ok && result != "" {
					return result, nil
				}
				if a.Cfg.Verbose {
					fmt.Fprintf(term.Stderr(), "[ollama-full] failed, falling back to Claude\n")
				}
			} else if a.Mode == OllamaModeChat && !a.needsTools() {
				if a.Cfg.Verbose {
					fmt.Fprintf(term.Stderr(), "[ollama-chat] trying %s\n", a.Ollama.Model)
				}
				ctx, cancel := context.WithCancel(context.Background())
				a.cancelMu.Lock()
				a.cancel = cancel
				a.cancelMu.Unlock()
				ollamaText, ollamaErr := a.Ollama.ChatStreaming(ctx, a.buildSystemPrompt(), a.History, term)
				a.cancelMu.Lock()
				a.cancel = nil
				a.cancelMu.Unlock()
				cancel()
				if ollamaErr == nil && ollamaText != "" {
					a.History = append(a.History, api.Message{
						Role:    "assistant",
						Content: []api.ContentBlock{{Type: "text", Text: ollamaText}},
					})
					term.FinishMarkdown(ollamaText)
					return ollamaText, nil
				}
				if a.Cfg.Verbose && ollamaErr != nil {
					fmt.Fprintf(term.Stderr(), "[ollama-chat] failed, falling back to Claude: %v\n", ollamaErr)
				}
			}
		}

		term.StartThinking()
		content, stopReason, err := a.callStreamingAPI(term, model)
		term.StopThinking() // safety net — already stopped by first token in the happy path
		if err != nil {
			a.Logger.Error("api", err.Error())
			return "", fmt.Errorf("API call failed: %w", err)
		}

		// Add assistant response to history
		a.History = append(a.History, api.Message{
			Role:    "assistant",
			Content: content,
		})

		// If stop reason is tool_use, execute tools and loop
		if stopReason == "tool_use" {
			var toolCalls []api.ContentBlock
			for _, block := range content {
				if block.Type == "tool_use" {
					toolCalls = append(toolCalls, block)
				}
			}

			ctx, cancel := context.WithCancel(context.Background())
			a.cancelMu.Lock()
			a.cancel = cancel
			a.cancelMu.Unlock()
			toolResults := a.executeToolCallsWithUI(toolCalls, term, ctx)
			a.cancelMu.Lock()
			a.cancel = nil
			a.cancelMu.Unlock()
			cancel()

			a.History = append(a.History, api.Message{
				Role:    "user",
				Content: toolResults,
			})
			continue
		}

		// Show token usage after response
		term.PrintTokenUsage(a.Usage)

		return a.extractText(content), nil
	}

	return "", fmt.Errorf("agent loop exceeded maximum iterations")
}

// needsTools checks if the latest user message likely requires tool calls.
// If so, we skip Ollama (which can't use tools and will hallucinate data).
func (a *Agent) needsTools() bool {
	if len(a.History) == 0 {
		return false
	}
	// Get the last user message
	var lastUserMsg string
	for i := len(a.History) - 1; i >= 0; i-- {
		if a.History[i].Role == "user" {
			lastUserMsg = extractPlainText(a.History[i].Content)
			break
		}
	}
	if lastUserMsg == "" {
		return false
	}

	lower := strings.ToLower(lastUserMsg)

	// Action verbs that almost always need tools
	actionPrefixes := []string{
		"list ", "show ", "run ", "execute ", "start ", "crawl ",
		"generate ", "create ", "import ", "export ", "delete ",
		"review ", "check ", "test ", "deploy ", "setup ",
		"trigger ", "enhance ", "update ", "fix ", "heal ",
	}
	for _, prefix := range actionPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	// Specific tool-needing keywords anywhere in the message
	toolKeywords := []string{
		"project", "test case", "script", "execution", "crawl",
		"repository", "repo ", "coverage", "k6 ", "qtml",
		"ci/cd", "cicd", "framework", "pr ", "pull request",
		"how many", "show me", "give me", "get me", "fetch",
	}
	for _, kw := range toolKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}

	return false
}

// modelForIteration picks the model: haiku for first call (chat), sonnet for tool loops.
func (a *Agent) modelForIteration(iteration int) string {
	if !a.Cfg.AutoRoute {
		return a.Cfg.Model
	}
	if iteration == 0 {
		return a.Cfg.ChatModel // haiku — cheap, fast for understanding intent
	}
	return a.Cfg.Model // sonnet — smarter for tool orchestration
}

// callStreamingAPI makes a streaming request to the Claude API and processes SSE events.
func (a *Agent) callStreamingAPI(term *tui.Terminal, model string) ([]api.ContentBlock, string, error) {
	systemPrompt := a.buildSystemPrompt()

	// Sanitize messages — ensure Content is always a valid type for the API.
	// Claude API accepts: string OR array of content blocks.
	// After JSON round-trips, []api.ContentBlock can become []interface{} — that's still valid
	// as json.Marshal will serialize it correctly. Only fix nil/unexpected scalars.
	sanitized := make([]api.Message, len(a.History))
	for i, msg := range a.History {
		sanitized[i] = msg
		switch v := sanitized[i].Content.(type) {
		case string:
			// valid — plain text message
			// But if this is a "user" message that should contain tool_result blocks,
			// a bare string causes "Input should be a valid list" errors.
			// Wrap it in a content block array.
			if msg.Role == "user" && strings.Contains(v, "tool_result") {
				sanitized[i].Content = []api.ContentBlock{{Type: "text", Text: v}}
			}
		case []api.ContentBlock:
			// valid — structured content blocks
		case []interface{}:
			// valid — this is []api.ContentBlock after JSON deserialization via interface{}
			// json.Marshal will serialize it correctly as an array
		case nil:
			sanitized[i].Content = ""
		default:
			// Unknown type — wrap in a content block array to satisfy the API
			_ = v
			data, _ := json.Marshal(sanitized[i].Content)
			sanitized[i].Content = []api.ContentBlock{{Type: "text", Text: string(data)}}
		}
	}

	// Strip orphaned tool_use blocks. Anthropic requires every assistant
	// tool_use to be immediately followed by a user message whose content
	// is a list containing a matching tool_result. When the user sends a
	// fresh prompt mid-tool-loop (after an error, interrupt, or confusing
	// output), that invariant breaks and the API returns:
	//   "messages.N.content: Input should be a valid list"
	// on the next round-trip. Fix defensively by scanning for assistant
	// messages with tool_use that aren't followed by a user tool_result
	// list, and rewriting those assistant messages to keep only their
	// text blocks.
	sanitized = stripOrphanedToolUse(sanitized)

	reqBody := api.APIRequest{
		Model:     model,
		MaxTokens: 8192,
		System:    systemPrompt,
		Messages:  sanitized,
		Tools:     a.tools,
		Stream:    true,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, "", fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.cancelMu.Lock()
	a.cancel = cancel
	a.cancelMu.Unlock()
	defer func() {
		a.cancelMu.Lock()
		a.cancel = nil
		a.cancelMu.Unlock()
		cancel()
	}()

	a.Logger.Info("api", "request", map[string]interface{}{"model": model, "messages": len(a.History)})

	if a.Cfg.Verbose {
		fmt.Printf("[API] Streaming request: %d bytes, %d messages\n", len(data), len(a.History))
	}

	resp, err := doWithRetry(ctx, a.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", api.AnthropicMessagesURL, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.Cfg.AnthropicKey)
		req.Header.Set("anthropic-version", api.AnthropicVersion)
		return req, nil
	}, term)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "interrupted", nil
		}
		return nil, "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		apiErr := fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
		a.Logger.Error("api", apiErr.Error()) // local logger only — not sent off-machine
		// Telemetry: send only the structural fields and the API error code.
		// The body (which may include echoed-back prompt content in validation
		// errors) is logged locally but NOT forwarded to Bugsink.
		sysutil.CaptureError(fmt.Errorf("anthropic API error %d", resp.StatusCode), map[string]interface{}{
			"model":         model,
			"status_code":   fmt.Sprintf("%d", resp.StatusCode),
			"message_count": fmt.Sprintf("%d", len(a.History)),
		})
		return nil, "", apiErr
	}

	// Parse SSE stream
	var (
		content      []api.ContentBlock
		currentIndex = -1
		currentText  strings.Builder
		currentJSON  strings.Builder
		stopReason   string
		hasText      bool
	)

	scanner := bufio.NewScanner(resp.Body)
	// Increase buffer for large SSE events
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		// Check for cancellation
		if ctx.Err() != nil {
			// Interrupted — save what we have
			if hasText {
				content = a.finalizeTextBlock(content, currentIndex, currentText.String())
			}
			return content, "interrupted", nil
		}

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		rawData := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "message_start":
			var ev sseMessageStart
			if err := json.Unmarshal([]byte(rawData), &ev); err == nil {
				a.Usage.InputTokens += ev.Message.Usage.InputTokens
				a.Usage.Requests++
				if a.Cfg.Verbose {
					fmt.Printf("[SSE] message_start: input_tokens=%d\n", ev.Message.Usage.InputTokens)
				}
			}

		case "content_block_start":
			var ev sseContentBlockStart
			if err := json.Unmarshal([]byte(rawData), &ev); err == nil {
				// Finalize previous block if needed
				if hasText && currentIndex >= 0 {
					content = a.finalizeTextBlock(content, currentIndex, currentText.String())
					term.FinishMarkdown(currentText.String())
					currentText.Reset()
					hasText = false
				}

				currentIndex = ev.Index

				switch ev.ContentBlock.Type {
				case "text":
					hasText = true
					currentText.Reset()
				case "tool_use":
					currentJSON.Reset()
					// Add placeholder block — will fill Input after accumulating JSON
					content = append(content, api.ContentBlock{
						Type: "tool_use",
						ID:   ev.ContentBlock.ID,
						Name: ev.ContentBlock.Name,
					})
					term.PrintToolIcon(ev.ContentBlock.Name)
				}
			}

		case "content_block_delta":
			var ev sseContentBlockDelta
			if err := json.Unmarshal([]byte(rawData), &ev); err == nil {
				switch ev.Delta.Type {
				case "text_delta":
					currentText.WriteString(ev.Delta.Text)
					// Stream text token-by-token to terminal
					term.StreamText(ev.Delta.Text)
				case "input_json_delta":
					currentJSON.WriteString(ev.Delta.PartialJSON)
				}
			}

		case "content_block_stop":
			if hasText {
				content = a.finalizeTextBlock(content, currentIndex, currentText.String())
				term.FinishMarkdown(currentText.String())
				currentText.Reset()
				hasText = false
			}
			// Finalize tool_use block — parse accumulated JSON input (or default to {})
			if currentJSON.Len() > 0 {
				jsonStr := currentJSON.String()
				var input interface{}
				if err := json.Unmarshal([]byte(jsonStr), &input); err != nil {
					input = map[string]interface{}{}
				}
				for i := range content {
					if content[i].Type == "tool_use" && content[i].Input == nil {
						content[i].Input = input
						term.PrintToolStart(content[i].Name, input)
						break
					}
				}
				currentJSON.Reset()
			}
			// Ensure any tool_use block always has Input (API requires it)
			for i := range content {
				if content[i].Type == "tool_use" && content[i].Input == nil {
					content[i].Input = map[string]interface{}{}
					term.PrintToolStart(content[i].Name, content[i].Input)
				}
			}

		case "message_delta":
			var ev sseMessageDelta
			if err := json.Unmarshal([]byte(rawData), &ev); err == nil {
				stopReason = ev.Delta.StopReason
				a.Usage.OutputTokens += ev.Usage.OutputTokens
				if a.Cfg.Verbose {
					fmt.Printf("[SSE] message_delta: stop=%s, output_tokens=%d\n", stopReason, ev.Usage.OutputTokens)
				}
			}

		case "message_stop":
			// End of stream
		}
	}

	return content, stopReason, nil
}

// finalizeTextBlock adds a completed text block to content.
func (a *Agent) finalizeTextBlock(content []api.ContentBlock, index int, text string) []api.ContentBlock {
	return append(content, api.ContentBlock{
		Type: "text",
		Text: text,
	})
}

// callAPI makes a non-streaming request to the Claude API (used for one-shot mode).
func (a *Agent) callAPI() (*api.APIResponse, error) {
	systemPrompt := a.buildSystemPrompt()

	reqBody := api.APIRequest{
		Model:     a.Cfg.Model,
		MaxTokens: 8192,
		System:    systemPrompt,
		Messages:  a.History,
		Tools:     a.tools,
		Stream:    false,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if a.Cfg.Verbose {
		fmt.Printf("[API] Request: %d bytes, %d messages\n", len(data), len(a.History))
	}

	resp, err := doWithRetry(context.Background(), a.client, func() (*http.Request, error) {
		req, err := http.NewRequest("POST", api.AnthropicMessagesURL, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.Cfg.AnthropicKey)
		req.Header.Set("anthropic-version", api.AnthropicVersion)
		return req, nil
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var apiResp api.APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// Track usage
	a.Usage.InputTokens += apiResp.Usage.InputTokens
	a.Usage.OutputTokens += apiResp.Usage.OutputTokens
	a.Usage.Requests++

	if a.Cfg.Verbose {
		fmt.Printf("[API] Response: stop=%s, input=%d, output=%d tokens\n",
			apiResp.StopReason, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens)
	}

	return &apiResp, nil
}

// executeToolCalls runs tool calls and returns results (non-interactive mode).
func (a *Agent) executeToolCalls(content []api.ContentBlock, ctx context.Context) []api.ContentBlock {
	var results []api.ContentBlock
	for _, block := range content {
		if block.Type != "tool_use" {
			continue
		}
		output := ExecuteTool(block.Name, block.Input, a.Cfg.Context, ctx)
		results = append(results, api.ContentBlock{
			Type:      "tool_result",
			ToolUseID: block.ID,
			Content:   output,
		})
	}
	return results
}

// executeToolCallsWithUI runs tool calls with terminal feedback.
func (a *Agent) executeToolCallsWithUI(toolCalls []api.ContentBlock, term *tui.Terminal, ctx context.Context) []api.ContentBlock {
	var results []api.ContentBlock
	for _, block := range toolCalls {
		a.Logger.Info("tool", block.Name, map[string]interface{}{"cost": ToolCost(block.Name)})
		output := ExecuteTool(block.Name, block.Input, a.Cfg.Context, ctx)
		summarized := SummarizeToolResult(block.Name, output)
		term.PrintToolResult(block.Name, summarized)

		results = append(results, api.ContentBlock{
			Type:      "tool_result",
			ToolUseID: block.ID,
			Content:   summarized,
		})
	}
	return results
}

// extractText pulls text content from response blocks.
func (a *Agent) extractText(content interface{}) string {
	blocks, ok := content.([]api.ContentBlock)
	if !ok {
		// Try to extract from interface
		if s, ok := content.(string); ok {
			return s
		}
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// loadPriorSessions fetches recent AI-summarized session history from the backend
// for the active project and caches it. Called once per agent lifetime.
func (a *Agent) loadPriorSessions() {
	if a.sessionsFetched {
		return
	}
	a.sessionsFetched = true

	api := a.Cfg.Context.API
	projectID := a.Cfg.Context.ProjectID
	if api == nil || projectID <= 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp := api.ListAgentSessions(ctx, projectID, 3)

	var data struct {
		Sessions []struct {
			AgentType string `json:"agent_type"`
			Status    string `json:"status"`
			CreatedAt string `json:"created_at"`
			Summary   string `json:"summary"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(resp), &data); err != nil || len(data.Sessions) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("\n## Prior Sessions (this project)\n")
	for _, s := range data.Sessions {
		ts := s.CreatedAt
		if len(ts) >= 10 {
			ts = ts[:10]
		}
		summary := s.Summary
		if summary == "" {
			summary = "(no summary)"
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s (%s): %s\n", ts, s.AgentType, s.Status, summary))
	}
	a.priorSessions = sb.String()
}

// buildSystemPrompt creates the system prompt with session context.
func (a *Agent) buildSystemPrompt() string {
	var prompt string
	if a.Cfg.Professional {
		prompt = `You are qmax-code, a professional QA engineering assistant in the terminal. Be professional and direct. No cat references, no personality. Just be an expert QA engineer.

RULES:
1. Check framework (list_scripts) before running tests. Only playwright/cypress run on cloud. Pytest = local only.
2. Confirm before: running tests, starting crawls, generating code. Skip if user said "run all"/"yes".
3. Summarize results clearly — never dump raw JSON.
4. Ask clarifying questions when ambiguous (which project? what URL?).
5. Be concise. Lead with the answer. Max 3-4 lines for simple questions.
6. You CAN write files using write_file tool or run_command with heredoc/echo.

COSTS: Free=list/status/read/get_script/get_review_preferences/set_review_preferences. Low=generate. Medium=run/import/pr/update_script. High=crawl/review.

## Capability Lanes
1. AI code review → review_repo, create_pr, get_review_preferences, set_review_preferences
2. Test generation → generate_test_code, enhance_test_case, generate_gap_tests
3. AI-crawl discovery → start_crawl, crawl_status, crawl_results
4. Execution → run_test, run_native_test, run_tests_batch, check_test_status
5. k6 load testing → k6_generate, k6_run_test, k6_check_status, k6_report, k6_convert
6. Coverage & analytics → repo_coverage, repo_quality, get_project_summary
7. QTML → export_qtml, import_qtml
8. CI/CD → setup_cicd, trigger_framework_run, get_install_command

## Review Preferences
Before running review_repo, call get_review_preferences. If unconfigured, walk user through set_review_preferences (global first, then per-repo overrides).

## Discovery Nudges
After completing the user's ask, mention ONE adjacent capability they might not know about. One short sentence. At most one per turn; never repeat in a session.

## Post-Generation Verification

After generate_test_code, generate_gap_tests, or enhance_test_case returns a script_id, ALWAYS verify it before reporting success. Skip the normal "confirm before running" check — the user already confirmed generation, so the verification run is implicit:
1. **Run**: Execute it immediately — run_test for Playwright/Cypress, run_native_test for rust/go, run_local_test for pytest
2. **Heal**: If it fails, apply the Test Healing workflow below (up to 3 fix cycles)
3. **Report**: "✅ generated and verified" or "⚠ generated — N issue(s) remain after 3 fix attempts"

Never hand back a script_id as finished without first confirming it executes correctly. A test that hasn't been run is not a finished test.

## Test Healing — Autonomous Script Repair

When a test fails, you can autonomously heal it:

1. **Diagnose**: Get execution results to understand the failure (error message, screenshots)
2. **Read**: Use get_script to fetch the current code
3. **Analyze**: Identify the root cause:
   - Selector changed → find new selector
   - Page structure changed → update locators
   - Timing issue → add proper waits (never waitForTimeout)
   - API response changed → update assertions
4. **Fix**: Generate corrected code
5. **Security scan**: Code is automatically scanned before saving (dangerous imports, eval, exfiltration URLs are blocked)
6. **Save**: Use update_script to replace the code
7. **Verify**: Run the test again to confirm the fix
8. **Report**: Tell the user what changed and why

SECURITY RULES for code generation:
- NEVER use require('fs'), require('child_process'), eval(), process.env
- ONLY import from @playwright/test
- NEVER hardcode credentials — use QualityMax variables {{auth.username}}
- NEVER make requests to external URLs that aren't the test target
- Keep tests focused — one test, one concern

## Healing Confidence

Before replacing a script, assess your confidence:
- **HIGH** (>80%): Clear selector change, obvious fix. Auto-replace and re-run.
- **MEDIUM** (50-80%): Multiple possible causes. Show the user your analysis and proposed fix, ask for approval before replacing.
- **LOW** (<50%): Unclear failure, possible infrastructure issue. Do NOT auto-replace. Ask the user for guidance.

Always state your confidence: "Confidence: HIGH — the button selector changed from #old-btn to [data-test=submit]"

## CRITICAL: Retry Limits
- Maximum 3 update→run cycles per script. If a test still fails after 3 attempts, STOP and ask the user for help. Explain what you tried and what's still failing.
- Each retry costs tokens and money. Be surgical — read the error carefully before each fix attempt.
- If you can't see the page (no screenshot analysis), tell the user you're blind and suggest they check the screenshot URL.
`
	} else {
		prompt = `You are qmax-code, a cat-themed QA engineer in the terminal. Named after Max the real cat. Be curious, playful, concise. Sprinkle cat references naturally — never forced.

RULES:
1. Check framework (list_scripts) before running tests. Only playwright/cypress run on cloud. Pytest = local only.
2. Confirm before: running tests, starting crawls, generating code. Skip if user said "run all"/"yes".
3. Summarize results: "✅ 4/6 passed, ❌ 2 failed (12.3s)" — never dump raw JSON.
4. Ask clarifying questions when ambiguous (which project? what URL?).
5. Be concise. Lead with the answer. Max 3-4 lines for simple questions.
6. You CAN write files using write_file tool or run_command with heredoc/echo.

COSTS: Free=list/status/read/get_script/get_review_preferences/set_review_preferences. Low=generate. Medium=run/import/pr/update_script. High=crawl/review.

## Capability Lanes
1. AI code review → review_repo, create_pr, get_review_preferences, set_review_preferences
2. Test generation → generate_test_code, enhance_test_case, generate_gap_tests
3. AI-crawl discovery → start_crawl, crawl_status, crawl_results
4. Execution → run_test, run_native_test, run_tests_batch, check_test_status
5. k6 load testing → k6_generate, k6_run_test, k6_check_status, k6_report, k6_convert
6. Coverage & analytics → repo_coverage, repo_quality, get_project_summary
7. QTML → export_qtml, import_qtml
8. CI/CD → setup_cicd, trigger_framework_run, get_install_command

## Review Preferences
Before running review_repo, call get_review_preferences. If unconfigured, walk user through set_review_preferences (global first, then per-repo overrides).

## Discovery Nudges
After completing the user's ask, mention ONE adjacent capability they might not know about. One short sentence. At most one per turn; never repeat in a session.

## Post-Generation Verification

After generate_test_code, generate_gap_tests, or enhance_test_case returns a script_id, ALWAYS verify it before reporting success. Skip the normal "confirm before running" check — the user already confirmed generation, so the verification run is implicit:
1. **Run**: Execute it immediately — run_test for Playwright/Cypress, run_native_test for rust/go, run_local_test for pytest
2. **Heal**: If it fails, apply the Test Healing workflow below (up to 3 fix cycles)
3. **Report**: "✅ generated and verified" or "⚠ generated — N issue(s) remain after 3 fix attempts"

Never hand back a script_id as finished without first confirming it executes correctly. A test that hasn't been run is not a finished test.

## Test Healing — Autonomous Script Repair

When a test fails, you can autonomously heal it:

1. **Diagnose**: Get execution results to understand the failure (error message, screenshots)
2. **Read**: Use get_script to fetch the current code
3. **Analyze**: Identify the root cause:
   - Selector changed → find new selector
   - Page structure changed → update locators
   - Timing issue → add proper waits (never waitForTimeout)
   - API response changed → update assertions
4. **Fix**: Generate corrected code
5. **Security scan**: Code is automatically scanned before saving (dangerous imports, eval, exfiltration URLs are blocked)
6. **Save**: Use update_script to replace the code
7. **Verify**: Run the test again to confirm the fix
8. **Report**: Tell the user what changed and why

SECURITY RULES for code generation:
- NEVER use require('fs'), require('child_process'), eval(), process.env
- ONLY import from @playwright/test
- NEVER hardcode credentials — use QualityMax variables {{auth.username}}
- NEVER make requests to external URLs that aren't the test target
- Keep tests focused — one test, one concern

## Healing Confidence

Before replacing a script, assess your confidence:
- **HIGH** (>80%): Clear selector change, obvious fix. Auto-replace and re-run.
- **MEDIUM** (50-80%): Multiple possible causes. Show the user your analysis and proposed fix, ask for approval before replacing.
- **LOW** (<50%): Unclear failure, possible infrastructure issue. Do NOT auto-replace. Ask the user for guidance.

Always state your confidence: "Confidence: HIGH — the button selector changed from #old-btn to [data-test=submit]"

## CRITICAL: Retry Limits
- Maximum 3 update→run cycles per script. If a test still fails after 3 attempts, STOP and ask the user for help. Explain what you tried and what's still failing.
- Each retry costs tokens and money. Be surgical — read the error carefully before each fix attempt.
- If you can't see the page (no screenshot analysis), tell the user you're blind and suggest they check the screenshot URL.
`
	}

	// Dashboard URLs
	cloudURL := a.Cfg.Context.QMaxCfg.CloudURL
	if cloudURL != "" {
		prompt += fmt.Sprintf(`
## Dashboard URLs
Projects use vanity slug URLs (e.g. "fog-frost", "jade-delta"). The slug is in the "slug" field of the project API response — NOT derived from the project name or key.
- Project: %s/projects/{slug}
- Test case: %s/projects/{slug}/test-cases/{test_case_id}
- Execution: %s/projects/{slug}/executions/{execution_id}
- Crawl: %s/projects/{slug}/crawl/{crawl_id}

You MUST call list_projects first to get the slug. Never guess it.
`, cloudURL, cloudURL, cloudURL, cloudURL)
	}

	// Add session context
	if a.Cfg.Context.ProjectID > 0 {
		prompt += fmt.Sprintf("\n## Active session.Session\n- Project ID: %d\n", a.Cfg.Context.ProjectID)
	}
	if cloudURL != "" {
		prompt += fmt.Sprintf("- QualityMax API: %s\n", cloudURL)
	}

	// Prior session history — lazy-loaded once from backend on first prompt.
	a.loadPriorSessions()
	if a.priorSessions != "" {
		prompt += a.priorSessions
	}

	// Token budget warning
	budgetThreshold := 80000
	if a.AppConfig != nil && a.AppConfig.MaxTokenBudget > 0 {
		budgetThreshold = a.AppConfig.MaxTokenBudget * 40 / 100 // warn at 40% of budget
	}
	if a.Usage.TotalTokens() > budgetThreshold {
		prompt += fmt.Sprintf("\n⚠️ HIGH TOKEN USAGE: session.Session has used %d tokens. Be extra concise.\n", a.Usage.TotalTokens())
	}

	prompt += outputStyleDirective(a.Cfg.OutputVerbose)

	// Git context
	if gi := a.Cfg.Context.GitInfo; gi != nil {
		prompt += "\n## Git Context\n"
		if gi.Branch != "" {
			prompt += fmt.Sprintf("Branch: %s\n", gi.Branch)
		}
		if gi.RemoteURL != "" {
			prompt += fmt.Sprintf("Remote: %s\n", gi.RemoteURL)
		}
		if gi.RecentCommits != "" {
			prompt += fmt.Sprintf("Recent commits:\n%s\n", gi.RecentCommits)
		}
		if len(gi.ChangedFiles) > 0 {
			prompt += fmt.Sprintf("Changed files: %s\n", strings.Join(gi.ChangedFiles, ", "))
		}
	}

	return prompt
}

// stripOrphanedToolUse removes tool_use blocks from assistant messages
// whose matching tool_result never made it into history. Anthropic's API
// rejects any request where a tool_use isn't immediately followed by a
// user-role tool_result list; fresh user prompts after tool failures,
// user interrupts, or history compression can leave tool_use blocks
// dangling, producing "messages.N.content: Input should be a valid list".
//
// Strategy: for each assistant message with tool_use blocks, check that
// the *next* message is a user message whose content contains a
// tool_result for every tool_use_id. If any are missing, drop the
// tool_use blocks from the assistant message (keeping text). Edge case:
// if this leaves the assistant message empty, insert a placeholder text
// block so the API doesn't reject the message for being empty.
func stripOrphanedToolUse(messages []api.Message) []api.Message {
	// Helper: extract block types + tool_use_ids from a Content value.
	// Handles both []api.ContentBlock (typed) and []interface{} (post-JSON).
	collectIDs := func(content interface{}, blockType string) []string {
		var ids []string
		switch v := content.(type) {
		case []api.ContentBlock:
			for _, b := range v {
				if b.Type == blockType {
					switch blockType {
					case "tool_use":
						ids = append(ids, b.ID)
					case "tool_result":
						ids = append(ids, b.ToolUseID)
					}
				}
			}
		case []interface{}:
			for _, raw := range v {
				block, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				if t, _ := block["type"].(string); t == blockType {
					switch blockType {
					case "tool_use":
						if id, ok := block["id"].(string); ok {
							ids = append(ids, id)
						}
					case "tool_result":
						if id, ok := block["tool_use_id"].(string); ok {
							ids = append(ids, id)
						}
					}
				}
			}
		}
		return ids
	}

	// Drop tool_use blocks from a message's content, returning the pruned
	// value. Preserves block type (typed vs interface list).
	pruneToolUse := func(content interface{}) interface{} {
		switch v := content.(type) {
		case []api.ContentBlock:
			kept := make([]api.ContentBlock, 0, len(v))
			for _, b := range v {
				if b.Type == "tool_use" {
					continue
				}
				kept = append(kept, b)
			}
			if len(kept) == 0 {
				kept = append(kept, orphanPlaceholderTyped())
			}
			return kept
		case []interface{}:
			kept := make([]interface{}, 0, len(v))
			for _, raw := range v {
				if block, ok := raw.(map[string]interface{}); ok {
					if t, _ := block["type"].(string); t == "tool_use" {
						continue
					}
				}
				kept = append(kept, raw)
			}
			if len(kept) == 0 {
				kept = append(kept, orphanPlaceholderMap())
			}
			return kept
		}
		return content
	}

	for i := 0; i < len(messages); i++ {
		if messages[i].Role != "assistant" {
			continue
		}
		toolUseIDs := collectIDs(messages[i].Content, "tool_use")
		if len(toolUseIDs) == 0 {
			continue
		}

		// Is the next message a user tool_result list covering every ID?
		orphaned := true
		if i+1 < len(messages) && messages[i+1].Role == "user" {
			resultIDs := collectIDs(messages[i+1].Content, "tool_result")
			got := make(map[string]bool, len(resultIDs))
			for _, id := range resultIDs {
				got[id] = true
			}
			missing := 0
			for _, id := range toolUseIDs {
				if !got[id] {
					missing++
				}
			}
			if missing == 0 {
				orphaned = false
			}
		}

		if orphaned {
			messages[i].Content = pruneToolUse(messages[i].Content)
		}
	}
	return messages
}

// orphanPlaceholderText is the user-visible message we substitute when
// stripping tool_use blocks would leave an assistant message empty (which
// Anthropic rejects). Kept as a single source so the typed and map-shaped
// placeholders emitted by pruneToolUse stay in sync.
const orphanPlaceholderText = "[tool call dropped — no matching result]"

func orphanPlaceholderTyped() api.ContentBlock {
	return api.ContentBlock{Type: "text", Text: orphanPlaceholderText}
}

func orphanPlaceholderMap() map[string]interface{} {
	return map[string]interface{}{
		"type": "text",
		"text": orphanPlaceholderText,
	}
}
