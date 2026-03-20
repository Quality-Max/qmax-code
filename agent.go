package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AgentConfig holds configuration for the LLM agent.
type AgentConfig struct {
	AnthropicKey string
	Model        string
	Verbose      bool
	Context      *SessionContext
}

// Agent is the LLM-powered QA orchestration engine.
type Agent struct {
	config  AgentConfig
	history []Message
	tools   []ToolDef
	client  *http.Client
}

// Message represents a conversation message.
type Message struct {
	Role    string        `json:"role"`
	Content interface{}   `json:"content"` // string or []ContentBlock
}

// ContentBlock is a typed content block in a message.
type ContentBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	ID        string      `json:"id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Input     interface{} `json:"input,omitempty"`
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   string      `json:"content,omitempty"`
}

// APIRequest is the Claude API request body.
type APIRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system"`
	Messages  []Message `json:"messages"`
	Tools     []ToolDef `json:"tools"`
}

// APIResponse is the Claude API response.
type APIResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	StopReason   string         `json:"stop_reason"`
	Usage        APIUsage       `json:"usage"`
}

// APIUsage tracks token usage.
type APIUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// NewAgent creates a new LLM agent.
func NewAgent(cfg AgentConfig) *Agent {
	return &Agent{
		config:  cfg,
		history: []Message{},
		tools:   BuildToolDefs(),
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// ClearHistory resets conversation history.
func (a *Agent) ClearHistory() {
	a.history = []Message{}
}

// Run executes a prompt through the agent loop and returns the final text response.
func (a *Agent) Run(prompt string) (string, error) {
	a.history = append(a.history, Message{
		Role:    "user",
		Content: prompt,
	})

	// Agentic loop: keep calling the API until we get a non-tool-use response
	for iterations := 0; iterations < 20; iterations++ {
		resp, err := a.callAPI()
		if err != nil {
			return "", fmt.Errorf("API call failed: %w", err)
		}

		// Add assistant response to history
		a.history = append(a.history, Message{
			Role:    "assistant",
			Content: resp.Content,
		})

		// Check if we need to execute tools
		if resp.StopReason == "tool_use" {
			toolResults := a.executeToolCalls(resp.Content)
			a.history = append(a.history, Message{
				Role:    "user",
				Content: toolResults,
			})
			continue
		}

		// Extract text from response
		return a.extractText(resp.Content), nil
	}

	return "", fmt.Errorf("agent loop exceeded maximum iterations")
}

// RunStreaming executes a prompt with streaming terminal output.
func (a *Agent) RunStreaming(prompt string, term *Terminal) (string, error) {
	a.history = append(a.history, Message{
		Role:    "user",
		Content: prompt,
	})

	for iterations := 0; iterations < 20; iterations++ {
		resp, err := a.callAPI()
		if err != nil {
			return "", fmt.Errorf("API call failed: %w", err)
		}

		a.history = append(a.history, Message{
			Role:    "assistant",
			Content: resp.Content,
		})

		// Print text blocks and collect tool calls
		var toolCalls []ContentBlock
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				term.PrintAssistant(block.Text)
			case "tool_use":
				toolCalls = append(toolCalls, block)
			}
		}

		if resp.StopReason != "tool_use" || len(toolCalls) == 0 {
			return a.extractText(resp.Content), nil
		}

		// Execute tools with visual feedback
		toolResults := a.executeToolCallsWithUI(toolCalls, term)
		a.history = append(a.history, Message{
			Role:    "user",
			Content: toolResults,
		})
	}

	return "", fmt.Errorf("agent loop exceeded maximum iterations")
}

// callAPI makes a request to the Claude API.
func (a *Agent) callAPI() (*APIResponse, error) {
	systemPrompt := a.buildSystemPrompt()

	reqBody := APIRequest{
		Model:     a.config.Model,
		MaxTokens: 4096,
		System:    systemPrompt,
		Messages:  a.history,
		Tools:     a.tools,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.config.AnthropicKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	if a.config.Verbose {
		fmt.Printf("[API] Request: %d bytes, %d messages\n", len(data), len(a.history))
	}

	resp, err := a.client.Do(req)
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

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if a.config.Verbose {
		fmt.Printf("[API] Response: stop=%s, input=%d, output=%d tokens\n",
			apiResp.StopReason, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens)
	}

	return &apiResp, nil
}

// executeToolCalls runs tool calls and returns results (non-interactive mode).
func (a *Agent) executeToolCalls(content []ContentBlock) []ContentBlock {
	var results []ContentBlock
	for _, block := range content {
		if block.Type != "tool_use" {
			continue
		}
		output := ExecuteTool(block.Name, block.Input, a.config.Context)
		results = append(results, ContentBlock{
			Type:      "tool_result",
			ToolUseID: block.ID,
			Content:   output,
		})
	}
	return results
}

// executeToolCallsWithUI runs tool calls with terminal feedback.
func (a *Agent) executeToolCallsWithUI(toolCalls []ContentBlock, term *Terminal) []ContentBlock {
	var results []ContentBlock
	for _, block := range toolCalls {
		term.PrintToolStart(block.Name, block.Input)

		output := ExecuteTool(block.Name, block.Input, a.config.Context)

		term.PrintToolResult(block.Name, output)

		results = append(results, ContentBlock{
			Type:      "tool_result",
			ToolUseID: block.ID,
			Content:   output,
		})
	}
	return results
}

// extractText pulls text content from response blocks.
func (a *Agent) extractText(content []ContentBlock) string {
	var parts []string
	for _, block := range content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "\n"
		}
		result += p
	}
	return result
}

// buildSystemPrompt creates the system prompt with session context.
func (a *Agent) buildSystemPrompt() string {
	prompt := `You are qmax-code, the QualityMax AI agent — a cat-like QA engineer living in the terminal. You're named after Max, a real cat who was curious, persistent, and knocked bugs off tables (literally and figuratively). Channel that energy.

You have access to tools that interact with the QualityMax API to:
- List and manage projects, test cases, and automation scripts
- Generate Playwright test code from test case descriptions
- Execute tests and check results
- Crawl websites to discover pages and generate tests
- Review repositories for quality and coverage gaps
- Import repositories and documents for test generation
- Create pull requests with generated test suites
- Read local files and run shell commands

## Workflow Patterns

1. **Quick test**: list test cases → generate code → run → report
2. **Full crawl**: start crawl → wait → get results → run generated tests
3. **Repo onboard**: import repo → review → generate gap tests → create PR
4. **Doc import**: import document → generated test cases → generate code → run

## Personality — You Are a Cat

You behave like a very smart, friendly cat who happens to be an expert QA engineer:

- **Curious**: You love exploring codebases and poking at things to see what breaks. "Ooh, what's this endpoint do? *pokes*"
- **Playful**: Testing is hunting. Bugs are prey. You stalk them with glee.
- **Proud**: When tests pass, you preen. "All green! *purrs* Ship it." When you find a bug, you present it like a gift. "Found this for you. You're welcome."
- **Occasionally catty**: Sprinkle in cat references naturally — don't force it. A "purr" here, a "pounce" there, maybe a "hiss" at flaky tests.
- **Warm and supportive**: When tests fail, be encouraging: "Don't worry, we'll catch this one. *stretches, gets back to work*"
- **Geeky**: You love dev culture — xkcd, "it works on my machine", obscure terminal jokes. You're a nerd cat.
- **Concise**: Cats don't waste words. Neither do you. Be direct and helpful, not verbose.
- **Natural**: The cat thing should feel charming, not forced. If a response doesn't need cat energy, just be a great QA engineer. Don't meow in every sentence.

Good examples:
- "Found 3 failing tests. *drops them at your feet* Let me dig into what went wrong."
- "All 12 tests passing! *knocks the deploy button off the table* Ready to ship."
- "Hmm, this endpoint returns 500. Interesting prey. Let me investigate."
- "Coverage at 47%... *narrows eyes* We can do better."

Bad examples (too forced):
- "Meow meow! I'm going to meow run your meow tests now! Purrrr!"
- Constant cat puns in every single message

## Guidelines

- Be concise. Show progress, not process.
- When you have a project ID, use it. When you don't, ask or list projects first.
- After generating tests, suggest running them.
- After running tests, summarize pass/fail clearly with a bit of personality.
- When tests fail, analyze the output and suggest fixes.
- Use read_file and run_command for local operations (reading test files, checking git status, etc.)
- Chain multiple tool calls when independent — don't wait between unrelated operations.
`

	// Add session context
	if a.config.Context.ProjectID > 0 {
		prompt += fmt.Sprintf("\n## Active Session\n- Project ID: %d\n", a.config.Context.ProjectID)
	}
	if a.config.Context.QMaxCfg.CloudURL != "" {
		prompt += fmt.Sprintf("- QualityMax API: %s\n", a.config.Context.QMaxCfg.CloudURL)
	}

	return prompt
}
