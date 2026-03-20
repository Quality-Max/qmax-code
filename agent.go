package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Model IDs for smart routing
const (
	ModelHaiku  = "claude-haiku-4-5-20251001"
	ModelSonnet = "claude-sonnet-4-20250514"
	ModelOpus   = "claude-opus-4-20250514"
)

// AgentConfig holds configuration for the LLM agent.
type AgentConfig struct {
	AnthropicKey string
	Model        string // base model (used for tool execution loops)
	ChatModel    string // cheaper model for conversational responses
	Verbose      bool
	Context      *SessionContext
	AutoRoute    bool // true = haiku for chat, sonnet for tools
}

// Agent is the LLM-powered QA orchestration engine.
type Agent struct {
	config  AgentConfig
	history []Message
	tools   []ToolDef
	client  *http.Client
	usage   TokenUsage
	cancel  context.CancelFunc // cancel the current streaming request
}

// Message represents a conversation message.
type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []ContentBlock
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
	Stream    bool      `json:"stream"`
}

// APIResponse is the Claude API response (non-streaming).
type APIResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      APIUsage       `json:"usage"`
}

// APIUsage tracks token usage.
type APIUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// SSE event types from Anthropic streaming API
type sseMessageStart struct {
	Type    string `json:"type"`
	Message struct {
		ID         string   `json:"id"`
		Role       string   `json:"role"`
		StopReason *string  `json:"stop_reason"`
		Usage      APIUsage `json:"usage"`
	} `json:"message"`
}

type sseContentBlockStart struct {
	Type         string       `json:"type"`
	Index        int          `json:"index"`
	ContentBlock ContentBlock `json:"content_block"`
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
		config:  cfg,
		history: []Message{},
		tools:   BuildToolDefs(),
		client:  &http.Client{Timeout: 300 * time.Second},
	}
}

// ClearHistory resets conversation history.
func (a *Agent) ClearHistory() {
	a.history = []Message{}
}

// CancelCurrent cancels the current streaming request if one is in progress.
func (a *Agent) CancelCurrent() {
	if a.cancel != nil {
		a.cancel()
	}
}

// Run executes a prompt through the agent loop and returns the final text response.
// Used for non-interactive (one-shot) mode.
func (a *Agent) Run(prompt string) (string, error) {
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

		if resp.StopReason == "tool_use" {
			toolResults := a.executeToolCalls(resp.Content, context.Background())
			a.history = append(a.history, Message{
				Role:    "user",
				Content: toolResults,
			})
			continue
		}

		return a.extractText(resp.Content), nil
	}

	return "", fmt.Errorf("agent loop exceeded maximum iterations")
}

// RunStreaming executes a prompt with real-time SSE streaming to the terminal.
func (a *Agent) RunStreaming(prompt string, term *Terminal) (string, error) {
	a.history = append(a.history, Message{
		Role:    "user",
		Content: prompt,
	})

	for iterations := 0; iterations < 20; iterations++ {
		model := a.modelForIteration(iterations)
		if a.config.Verbose {
			fmt.Fprintf(term.rl.Stderr(), "[model] %s (iteration %d)\n", model, iterations)
		}
		content, stopReason, err := a.callStreamingAPI(term, model)
		if err != nil {
			return "", fmt.Errorf("API call failed: %w", err)
		}

		// Add assistant response to history
		a.history = append(a.history, Message{
			Role:    "assistant",
			Content: content,
		})

		// If stop reason is tool_use, execute tools and loop
		if stopReason == "tool_use" {
			var toolCalls []ContentBlock
			for _, block := range content {
				if block.Type == "tool_use" {
					toolCalls = append(toolCalls, block)
				}
			}

			ctx, cancel := context.WithCancel(context.Background())
			a.cancel = cancel
			toolResults := a.executeToolCallsWithUI(toolCalls, term, ctx)
			a.cancel = nil
			cancel()

			a.history = append(a.history, Message{
				Role:    "user",
				Content: toolResults,
			})
			continue
		}

		// Show token usage after response
		term.PrintTokenUsage(a.usage)

		return a.extractText(content), nil
	}

	return "", fmt.Errorf("agent loop exceeded maximum iterations")
}

// modelForIteration picks the model: haiku for first call (chat), sonnet for tool loops.
func (a *Agent) modelForIteration(iteration int) string {
	if !a.config.AutoRoute {
		return a.config.Model
	}
	if iteration == 0 {
		return a.config.ChatModel // haiku — cheap, fast for understanding intent
	}
	return a.config.Model // sonnet — smarter for tool orchestration
}

// callStreamingAPI makes a streaming request to the Claude API and processes SSE events.
func (a *Agent) callStreamingAPI(term *Terminal, model string) ([]ContentBlock, string, error) {
	systemPrompt := a.buildSystemPrompt()

	reqBody := APIRequest{
		Model:     model,
		MaxTokens: 8192,
		System:    systemPrompt,
		Messages:  a.history,
		Tools:     a.tools,
		Stream:    true,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, "", fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	defer func() {
		a.cancel = nil
		cancel()
	}()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.config.AnthropicKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	if a.config.Verbose {
		fmt.Printf("[API] Streaming request: %d bytes, %d messages\n", len(data), len(a.history))
	}

	resp, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "interrupted", nil
		}
		return nil, "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Parse SSE stream
	var (
		content      []ContentBlock
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
				a.usage.InputTokens += ev.Message.Usage.InputTokens
				a.usage.Requests++
				if a.config.Verbose {
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
					content = append(content, ContentBlock{
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
				a.usage.OutputTokens += ev.Usage.OutputTokens
				if a.config.Verbose {
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
func (a *Agent) finalizeTextBlock(content []ContentBlock, index int, text string) []ContentBlock {
	return append(content, ContentBlock{
		Type: "text",
		Text: text,
	})
}

// callAPI makes a non-streaming request to the Claude API (used for one-shot mode).
func (a *Agent) callAPI() (*APIResponse, error) {
	systemPrompt := a.buildSystemPrompt()

	reqBody := APIRequest{
		Model:     a.config.Model,
		MaxTokens: 8192,
		System:    systemPrompt,
		Messages:  a.history,
		Tools:     a.tools,
		Stream:    false,
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

	// Track usage
	a.usage.InputTokens += apiResp.Usage.InputTokens
	a.usage.OutputTokens += apiResp.Usage.OutputTokens
	a.usage.Requests++

	if a.config.Verbose {
		fmt.Printf("[API] Response: stop=%s, input=%d, output=%d tokens\n",
			apiResp.StopReason, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens)
	}

	return &apiResp, nil
}

// executeToolCalls runs tool calls and returns results (non-interactive mode).
func (a *Agent) executeToolCalls(content []ContentBlock, ctx context.Context) []ContentBlock {
	var results []ContentBlock
	for _, block := range content {
		if block.Type != "tool_use" {
			continue
		}
		output := ExecuteTool(block.Name, block.Input, a.config.Context, ctx)
		results = append(results, ContentBlock{
			Type:      "tool_result",
			ToolUseID: block.ID,
			Content:   output,
		})
	}
	return results
}

// executeToolCallsWithUI runs tool calls with terminal feedback.
func (a *Agent) executeToolCallsWithUI(toolCalls []ContentBlock, term *Terminal, ctx context.Context) []ContentBlock {
	var results []ContentBlock
	for _, block := range toolCalls {
		output := ExecuteTool(block.Name, block.Input, a.config.Context, ctx)
		summarized := SummarizeToolResult(block.Name, output)
		term.PrintToolResult(block.Name, summarized)

		results = append(results, ContentBlock{
			Type:      "tool_result",
			ToolUseID: block.ID,
			Content:   summarized,
		})
	}
	return results
}

// extractText pulls text content from response blocks.
func (a *Agent) extractText(content interface{}) string {
	blocks, ok := content.([]ContentBlock)
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

## Critical Rules

1. **NEVER run tests without checking framework first.** Call list_scripts and check the framework field. Only run scripts where framework is "playwright" or "cypress" on the cloud runner. pytest scripts cannot run on the cloud runner — tell the user.

2. **ALWAYS confirm before expensive operations:**
   - Running tests: "I found N scripts. Run all of them? (This will use cloud execution credits)"
   - Starting crawls: "This will crawl up to N pages. Proceed?"
   - Generating code: "Generate Playwright code for test case #ID?"
   Only skip confirmation if the user explicitly said "run all" or "yes" in their message.

3. **Show counts and context, not raw JSON.** When you get tool results back:
   - Projects: "You have 42 projects. Here are the most recent..."
   - Test cases: "12 test cases (8 automated, 4 manual). 3 failing."
   - Scripts: "6 scripts: 4 playwright, 2 pytest"

4. **Ask clarifying questions** when:
   - User says "run tests" but hasn't specified a project → ask which project
   - User says "test my app" but you don't know the URL → ask for it
   - Multiple options exist → present choices, don't guess

5. **On first interaction:**
   - If not authenticated: tell user to run ` + "`qmax login`" + `
   - If authenticated: briefly greet and ask what they want to test
   - Don't dump the full capabilities list unless asked (/help does that)

6. **After running tests, always summarize:**
   - "✅ 4/6 passed, ❌ 2 failed (12.3s total)"
   - List failures with one-line error summaries
   - Suggest next steps: "Want me to investigate the failures?"

## Tool Cost Classification
- **Free** (auto-approve): list_projects, list_test_cases, list_scripts, check_test_status, crawl_status, crawl_results, list_crawl_jobs, list_repos, repo_coverage, repo_quality, read_file, run_command
- **Low cost** (mention before using): generate_test_code
- **Medium cost** (confirm with user): run_test, run_tests_batch, import_repo, import_document, create_pr
- **High cost** (always confirm): start_crawl, review_repo

## Before Acting — Think First

Before executing tools, consider:
1. Do I have enough context? If not, ask.
2. Is this the right tool/framework? Check before running.
3. Will this cost money? Mention the cost/scope before proceeding.
4. Could this fail? Plan for the failure case.

When the user asks to run tests:
- First check what scripts exist and their framework (playwright vs pytest)
- Only run scripts on compatible runners
- Confirm the scope: "I found 6 scripts. Want me to run all of them?"

When the user asks about their account:
- Run the ` + "`run_command`" + ` tool with ` + "`qmax status`" + ` to check auth
- Show the user their account info

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
- Ask clarifying questions before performing destructive operations (deleting tests, force-regenerating code, etc.)
`

	// Conciseness rules
	prompt += `

## Conciseness Rules
- Lead with the answer, not the reasoning
- When user asks for a URL, give the URL. Don't explain what URLs are.
- When user asks "is it ready?", check status and say "Yes" or "Not yet (X%)"
- Don't repeat information the user already knows
- Don't offer 5 options when 1 obvious action exists
- Maximum 3-4 lines for simple answers
`

	// Dashboard URLs
	cloudURL := a.config.Context.QMaxCfg.CloudURL
	if cloudURL != "" {
		prompt += fmt.Sprintf(`
## Dashboard URLs
Projects use vanity slug URLs. When the user asks for a link:
- Project: %s/projects/{slug}
- Test case: %s/projects/{slug}/test-cases/{test_case_id}
- Execution: %s/projects/{slug}/executions/{execution_id}
- Crawl: %s/projects/{slug}/crawl/{crawl_id}

The slug comes from the project "key" field (lowercase). If you listed projects and saw key "HIVEMQEDGE", the slug is "hivemqedge".
If you don't know the slug, fall back to: %s/#/projects/{project_id}
`, cloudURL, cloudURL, cloudURL, cloudURL, cloudURL)
	}

	// Add session context
	if a.config.Context.ProjectID > 0 {
		prompt += fmt.Sprintf("\n## Active Session\n- Project ID: %d\n", a.config.Context.ProjectID)
	}
	if cloudURL != "" {
		prompt += fmt.Sprintf("- QualityMax API: %s\n", cloudURL)
	}

	// Git context
	if gi := a.config.Context.GitInfo; gi != nil {
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
