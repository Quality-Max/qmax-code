package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/qualitymax/qmax-code/internal/tui"
	"strings"
)

// OllamaAgentMode provides a full Ollama-powered agent that handles both
// chat AND tool-needing requests. Since many local models don't support native
// function calling, qmax-code uses prompt-based tool dispatch:
//
// 1. The local model classifies user intent into an action + params
// 2. Go code maps the action to an actual QualityMax API call
// 3. Results are fed back to the local model for formatting
//
// This allows qmax-code to run entirely on the self-hosted GPU.

// ollamaToolActions is the compact action set the local model can choose from.
// Each maps to one or more real QualityMax API calls.
const ollamaToolPrompt = `

CRITICAL TOOL RULES:
1. You have NO direct knowledge of the user's projects, tests, or data.
2. When the user asks to list, show, run, create, check, or do ANYTHING with their data, you MUST output ONLY an action block — nothing else.
3. Do NOT make up or hallucinate data. You do not know their projects, test cases, or results.
4. For normal chat (greetings, QA concepts, advice), respond normally without action blocks.

Action format — output ONLY this, no other text:
<action>{"name": "ACTION_NAME", "params": {...}}</action>

Available actions:
- list_projects: List all projects. No params.
- list_test_cases: List test cases. Params: {"project_id": int, "search": "optional"}
- list_scripts: List automation scripts. Params: {"project_id": int}
- run_test: Run a test. Params: {"script_id": int}
- start_crawl: AI crawl a site. Params: {"project_id": int, "url": "https://..."}
- review_repo: AI code review. Params: {"repo_id": int}
- get_script: Get script code. Params: {"script_id": int}
- get_project_summary: Project details. Params: {"project_id": int}
- check_test_status: Check execution. Params: {"execution_id": "uuid"}
- create_pr: Create PR with tests. Params: {"repo_id": int, "project_id": int}
`

// RunOllamaAgent runs a full conversation turn using only Ollama.
// Returns the final text response and whether it succeeded.
func (a *Agent) RunOllamaAgent(term *tui.Terminal) (string, bool) {
	if a.ollama == nil || !a.ollama.Available() {
		return "", false
	}

	// Build system prompt with action instructions
	system := a.buildSystemPrompt() + ollamaToolPrompt

	ctx1, cancel1 := context.WithCancel(context.Background())
	a.cancel = cancel1

	// Phase 1: Get the local model response (may contain <action> block)
	// Use the agent model (12B) for better tool dispatch accuracy
	ollamaText, err := a.ollama.ChatStreamingWithModel(ctx1, a.ollama.agentModel, system, a.history, term)
	a.cancel = nil
	cancel1()

	if err != nil || ollamaText == "" {
		return "", false
	}

	// Check if response contains an action block
	action, params, remaining := parseActionBlock(ollamaText)
	if action == "" {
		// Pure chat response — no tool needed
		a.history = append(a.history, Message{
			Role:    "assistant",
			Content: []ContentBlock{{Type: "text", Text: ollamaText}},
		})
		term.FinishMarkdown(ollamaText)
		return ollamaText, true
	}

	// Phase 2: Execute the action via QualityMax API (fresh context)
	if a.config.Verbose {
		fmt.Fprintf(term.Stderr(), "[ollama-agent] action=%s params=%v\n", action, params)
	}
	term.PrintToolIcon(action)
	term.PrintToolStart(action, params)

	apiCtx, apiCancel := context.WithCancel(context.Background())
	defer apiCancel()
	toolResult := a.executeOllamaAction(action, params, apiCtx)
	term.PrintToolResult(action, tui.TruncateStr(toolResult, 200))

	// Phase 3: Feed results back to the local model for formatting.
	a.history = append(a.history, Message{
		Role:    "assistant",
		Content: []ContentBlock{{Type: "text", Text: remaining}},
	})
	a.history = append(a.history, Message{
		Role:    "user",
		Content: fmt.Sprintf("[Tool result for %s]:\n%s\n\nSummarize these results for the user concisely.", action, truncateToolResult(toolResult)),
	})

	ctx2, cancel2 := context.WithCancel(context.Background())
	a.cancel = cancel2
	summary, err := a.ollama.ChatStreamingWithModel(ctx2, a.ollama.agentModel, system, a.history, term)
	a.cancel = nil
	cancel2()

	if err != nil || summary == "" {
		// Fallback: just show raw result
		summary = toolResult
	}

	a.history = append(a.history, Message{
		Role:    "assistant",
		Content: []ContentBlock{{Type: "text", Text: summary}},
	})
	term.FinishMarkdown(summary)
	return summary, true
}

// parseActionBlock extracts an action from the local model response.
// Supports both <action>{...}</action> tags and bare JSON with "name" field.
// Returns the action name, params map, and any text outside the action block.
func parseActionBlock(text string) (string, map[string]interface{}, string) {
	// Try <action> tags first
	startTag := "<action>"
	endTag := "</action>"

	startIdx := strings.Index(text, startTag)
	if startIdx != -1 {
		endIdx := strings.Index(text[startIdx:], endTag)
		if endIdx != -1 {
			jsonStr := text[startIdx+len(startTag) : startIdx+endIdx]
			remaining := strings.TrimSpace(text[:startIdx] + text[startIdx+endIdx+len(endTag):])
			if action, params, ok := tryParseActionJSON(jsonStr); ok {
				return action, params, remaining
			}
		}
	}

	// Fallback: look for bare JSON with "name" field anywhere in the text.
	// Local models sometimes output {"name": "list_projects", "params": {}} without tags.
	jsonStart := strings.Index(text, `{"name"`)
	if jsonStart == -1 {
		jsonStart = strings.Index(text, `{ "name"`)
	}
	if jsonStart != -1 {
		// Find the matching closing brace
		depth := 0
		for i := jsonStart; i < len(text); i++ {
			if text[i] == '{' {
				depth++
			} else if text[i] == '}' {
				depth--
				if depth == 0 {
					jsonStr := text[jsonStart : i+1]
					remaining := strings.TrimSpace(text[:jsonStart] + text[i+1:])
					if action, params, ok := tryParseActionJSON(jsonStr); ok {
						return action, params, remaining
					}
					break
				}
			}
		}
	}

	return "", nil, text
}

// tryParseActionJSON attempts to parse a JSON string as an action.
func tryParseActionJSON(jsonStr string) (string, map[string]interface{}, bool) {
	var action struct {
		Name   string                 `json:"name"`
		Params map[string]interface{} `json:"params"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &action); err != nil || action.Name == "" {
		return "", nil, false
	}
	if action.Params == nil {
		action.Params = map[string]interface{}{}
	}
	return action.Name, action.Params, true
}

// executeOllamaAction maps an action name to a real API call.
func (a *Agent) executeOllamaAction(action string, params map[string]interface{}, ctx context.Context) string {
	api := a.config.Context.API
	if api == nil {
		return `{"error": "Not connected to QualityMax. Run /connect first."}`
	}

	switch action {
	case "list_projects":
		return api.ListProjects(ctx)
	case "list_test_cases":
		projectID := intVal(params, "project_id", a.config.Context.ProjectID)
		search := strVal(params, "search")
		return api.ListTestCases(ctx, projectID, 20, search)
	case "list_scripts":
		projectID := intVal(params, "project_id", a.config.Context.ProjectID)
		return api.ListScripts(ctx, projectID, 20)
	case "run_test":
		scriptID := intVal(params, "script_id", 0)
		if scriptID == 0 {
			return `{"error": "script_id is required"}`
		}
		return api.RunTest(ctx, scriptID, true, "", "", a.config.Context.LiveFeed)
	case "start_crawl":
		projectID := intVal(params, "project_id", a.config.Context.ProjectID)
		url := strVal(params, "url")
		if url == "" {
			return `{"error": "url is required"}`
		}
		return api.StartCrawl(ctx, projectID, url, 2, 10, "", "", a.config.Context.LiveFeed)
	case "review_repo":
		repoID := intVal(params, "repo_id", 0)
		if repoID == 0 {
			return `{"error": "repo_id is required"}`
		}
		return api.ReviewRepo(ctx, repoID)
	case "get_script":
		scriptID := intVal(params, "script_id", 0)
		return api.GetScript(ctx, scriptID)
	case "get_project_summary":
		projectID := intVal(params, "project_id", a.config.Context.ProjectID)
		return api.GetProjectSummary(ctx, projectID)
	case "check_test_status":
		execID := strVal(params, "execution_id")
		return api.CheckTestStatus(ctx, execID)
	case "create_pr":
		repoID := intVal(params, "repo_id", 0)
		projectID := intVal(params, "project_id", a.config.Context.ProjectID)
		return api.CreatePR(ctx, repoID, projectID)
	default:
		return fmt.Sprintf(`{"error": "Unknown action: %s"}`, action)
	}
}

// truncateToolResult keeps tool output to a reasonable size for local model context.
func truncateToolResult(result string) string {
	const maxLen = 4000
	if len(result) <= maxLen {
		return result
	}
	return result[:maxLen] + "\n... (truncated)"
}
