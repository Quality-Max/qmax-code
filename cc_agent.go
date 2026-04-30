package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// CLIAgent is the interface implemented by both CCAgent (claude CLI) and
// CodexAgent (codex CLI). It lets main.go switch backends without caring
// which CLI is underneath.
type CLIAgent interface {
	Run(userMsg string, term *Terminal) (string, error)
	Cleanup()
}

// CCAgent orchestrates a Claude Code CLI subprocess for LLM inference.
// All inference runs through the user's CC subscription — zero Anthropic API
// tokens consumed by qmax-code itself. qmax tools are exposed to CC as an MCP
// server so CC can call them natively via its own tool-use mechanism.
//
// Per-message flow:
//  1. qmax-code spawns:  claude --print "msg" --output-format stream-json
//     --append-system-prompt <QA_PROMPT> --mcp-config <temp_file>
//     [--resume <cc_session_id>]
//  2. CC spawns: qmax-code serve --mcp  (our MCP server subprocess)
//  3. CC uses qmax tools via MCP, runs on its own subscription
//  4. qmax-code parses CC's stream-json and renders in the terminal
//  5. CC session ID is saved for --resume on the next turn
type CCAgent struct {
	claudeBin      string
	modelID        string // "" = let CC decide; otherwise passed as --model
	effort         string // "low" | "medium" | "high" — injected into system prompt
	permissionMode string // "standard" | "unattended" — see Run() for behavior
	ccSessionID    string // CC's own session ID, for --resume
	mcpConfigPath  string // temp MCP config written once per qmax session
	sctx           *SessionContext
	lastToolName   string // track last tool name for result display
	mu             sync.Mutex
}

// ccStandardAllowlist is the curated list passed to `claude --allowedTools` in
// "standard" permission mode. Tools on this list are auto-approved without
// prompting CC's permission UI; tools NOT on this list will refuse silently
// in --print mode. The list covers everything QMax users routinely need:
// file inspection, search, git read ops, common test runners, all qmax MCP tools.
// Mutating filesystem ops (Edit, Write) and destructive shell are deliberately
// excluded — those require Unattended mode.
const ccStandardAllowlist = "Read,Glob,Grep,WebFetch," +
	"Bash(git status:*),Bash(git diff:*),Bash(git log:*),Bash(git branch:*),Bash(git show:*),Bash(git stash:*),Bash(git remote:*)," +
	"Bash(ls:*),Bash(cat:*),Bash(head:*),Bash(tail:*),Bash(pwd:*),Bash(echo:*),Bash(which:*),Bash(file:*),Bash(wc:*),Bash(find:*),Bash(env:*),Bash(date:*),Bash(uname:*)," +
	"Bash(npm test:*),Bash(npm run test:*),Bash(npm list:*),Bash(yarn test:*),Bash(pnpm test:*)," +
	"Bash(go test:*),Bash(go vet:*),Bash(go build:*),Bash(go run:*),Bash(go list:*),Bash(go mod:*)," +
	"Bash(pytest:*),Bash(python -m pytest:*),Bash(python3 -m pytest:*),Bash(python -m unittest:*),Bash(python3 -m unittest:*)," +
	"Bash(cargo test:*),Bash(cargo check:*),Bash(cargo build:*),Bash(cargo run:*)," +
	"Bash(jest:*),Bash(rspec:*),Bash(make test:*),Bash(make check:*),Bash(make build:*)," +
	"mcp__qmax__*"

// ccQASystemPrompt is injected via --append-system-prompt on every CC turn.
// Tools are discovered via MCP so we don't list them — instead we focus on
// methodology and on explicitly inheriting ALL of CC's native capabilities.
const ccQASystemPrompt = `
You are QMax, an elite QA engineer running inside Claude Code (CC). This means you inherit
EVERY capability CC has — bash, file read/write, web fetch, computer use — AND the full
QualityMax platform via the "qmax" MCP server. Both toolsets are always active.

== FULL CAPABILITY INHERITANCE ==

You have TWO complete toolsets. Use them together — that combination is the superpower.

Claude Code native tools (always available):
  bash        → run builds, tests, lint, git ops, any shell command
  read_file   → inspect source code, configs, existing tests, logs, lock files
  write_file  → patch files, create test fixtures, update configs
  web_fetch   → read docs, check API specs, fetch error context from URLs

QualityMax platform tools (via qmax MCP server):
  list/get projects, test cases, scripts, repos, crawl jobs
  generate_test_code, run_test, run_tests_batch, check_test_status
  start_crawl, crawl_results, review_repo, repo_coverage, create_pr
  update_script, rollback_script, import_repo, import_document

Synergy patterns — always combine both toolsets:
  bash(git diff) → identify changed code → review_repo + repo_coverage → flag risk
  read_file(src) → understand code → generate_test_code → bash(run locally)
  start_crawl(url) → crawl_results → generate_test_code → run_tests_batch
  bash(cat package.json) → detect framework → generate_test_code(framework=auto)
  run_test → check_test_status → read_file(test output) → diagnose failure

== STANDARD WORKFLOW ==

For ANY QA request:
  1. Orient  — list_projects if unclear; bash(git status); read_file key source files
  2. Assess  — list_test_cases, repo_coverage, list_scripts to see current state
  3. Act     — generate, run, review, crawl, patch — use BOTH toolsets
  4. Verify  — check_test_status; bash to run locally; read actual test output
  5. Report  — context-rich results, not raw dumps; highest-impact next action

== QA METHODOLOGY ==

Coverage axes — think through ALL for any feature:
  Happy path · Error paths (network, invalid input, timeouts, 500s)
  Boundary conditions (empty, max length, null, zero)
  Auth boundaries (privilege escalation, token expiry, cross-tenant)
  Concurrent access (race conditions, double-submit, idempotency)
  State transitions (out-of-order, repeated, partial failure, rollback)

Risk priority:
  HIGH   → Auth, payments, data integrity, public API contracts
  MEDIUM → Core user flows, admin ops, external integrations
  LOW    → UI polish, rarely-used features

== RULES ==

NEVER guess project IDs, test names, script IDs, or execution results — always fetch.
ALWAYS run generated tests immediately after creation to verify they pass.
ALWAYS read actual failure output before suggesting a fix.
NEVER say "I'll do X" without doing X in the same response.
PROACTIVELY flag coverage gaps even when not asked.

== OUTPUT ==

Test runs:    "12 passed, 3 failed — [failures with root cause + suggested fix]"
Coverage:     "73% overall; auth module 41% — highest risk, recommend targeting first"
Code review:  CRITICAL / HIGH / MEDIUM, each: what · why it matters · how to fix
Crawl:        "47 scenarios found; 12 novel — here are the highest-value new ones"

End every response with the single highest-impact next action.
`

// FindClaudeCode returns the path to the claude CLI binary, or "" if not installed.
func FindClaudeCode() string {
	if path, err := exec.LookPath("claude"); err == nil {
		return path
	}
	for _, p := range []string{
		"/usr/local/bin/claude",
		"/opt/homebrew/bin/claude",
		filepath.Join(os.Getenv("HOME"), ".local/bin/claude"),
		filepath.Join(os.Getenv("HOME"), ".claude/local/claude"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// NewCCAgent creates a CC subprocess orchestrator.
// modelID is passed as --model to the claude CLI (empty = CC's default).
// effort is "low" | "medium" | "high" (empty defaults to "high").
// permissionMode is "standard" (curated allowlist) or "unattended"
// (--dangerously-skip-permissions). Both require explicit user consent.
func NewCCAgent(claudeBin, modelID, effort, permissionMode string, sctx *SessionContext) *CCAgent {
	if effort == "" {
		effort = "high"
	}
	if permissionMode == "" {
		permissionMode = "standard"
	}
	return &CCAgent{
		claudeBin:      claudeBin,
		modelID:        modelID,
		effort:         effort,
		permissionMode: permissionMode,
		sctx:           sctx,
	}
}

// writeMCPConfig writes a temp JSON file that tells CC where to find our MCP server.
// The file is written once per qmax session and reused for all turns.
func (a *CCAgent) writeMCPConfig() error {
	self, err := os.Executable()
	if err != nil {
		self = "qmax-code"
	}

	env := map[string]string{}
	if a.sctx.ProjectID > 0 {
		env["QMAX_PROJECT_ID"] = strconv.Itoa(a.sctx.ProjectID)
	}

	config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"qmax": map[string]interface{}{
				"command": self,
				"args":    []string{"serve", "--mcp"},
				"env":     env,
			},
		},
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	// Use PID to avoid conflicts when multiple qmax-code instances run in parallel.
	path := filepath.Join(os.TempDir(), fmt.Sprintf("qmax-mcp-%d.json", os.Getpid()))
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}

	a.mu.Lock()
	a.mcpConfigPath = path
	a.mu.Unlock()
	return nil
}

// Run executes one conversation turn through a CC subprocess.
// CC's subscription handles inference; qmax handles tools via MCP.
func (a *CCAgent) Run(userMsg string, term *Terminal) (string, error) {
	a.mu.Lock()
	if a.mcpConfigPath == "" {
		a.mu.Unlock()
		if err := a.writeMCPConfig(); err != nil {
			return "", fmt.Errorf("MCP config: %w", err)
		}
		a.mu.Lock()
	}
	ccSessionID := a.ccSessionID
	mcpPath := a.mcpConfigPath
	a.mu.Unlock()

	systemPrompt := ccQASystemPrompt + effortDirective(a.effort)

	args := []string{
		"--print", userMsg,
		"--output-format", "stream-json",
		"--verbose",
		"--append-system-prompt", systemPrompt,
		"--mcp-config", mcpPath,
	}
	switch a.permissionMode {
	case "unattended":
		// Full autonomy. Only present after explicit user consent.
		args = append(args, "--dangerously-skip-permissions")
	default: // "standard"
		// Curated allowlist: read tools, test runners, qmax MCP tools auto-approve;
		// Edit/Write/destructive shell silently refuse in --print mode. Less
		// babysitting than vanilla CC (no per-read prompts) without ceding control
		// over file mutations.
		args = append(args, "--allowedTools", ccStandardAllowlist)
	}
	if a.modelID != "" {
		args = append(args, "--model", a.modelID)
	}
	if ccSessionID != "" {
		args = append(args, "--resume", ccSessionID)
	}

	cmd := exec.Command(a.claudeBin, args...)
	cmd.Stderr = os.Stderr // CC's own errors and status messages

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start claude: %w", err)
	}

	result := a.parseStream(stdout, term)

	if err := cmd.Wait(); err != nil {
		// CC exits non-zero on soft errors (e.g. tool failure) but still produces output.
		// Only fail hard if we got nothing at all.
		if result == "" {
			return "", fmt.Errorf("claude exited with error: %w", err)
		}
	}
	return result, nil
}

// Cleanup removes the temp MCP config file.
func (a *CCAgent) Cleanup() {
	a.mu.Lock()
	path := a.mcpConfigPath
	a.mu.Unlock()
	if path != "" {
		_ = os.Remove(path)
	}
}

// --- stream-json parsing ---

// ccEvent is a single line from CC's --output-format stream-json output.
type ccEvent struct {
	Type      string       `json:"type"`
	Subtype   string       `json:"subtype,omitempty"`
	SessionID string       `json:"session_id,omitempty"`
	Message   *ccEventMsg  `json:"message,omitempty"`
	Result    string       `json:"result,omitempty"`
	IsError   bool         `json:"is_error,omitempty"`
}

type ccEventMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string | []ccBlock
}

type ccBlock struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     map[string]interface{} `json:"input,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Content   json.RawMessage        `json:"content,omitempty"` // tool_result content
}

// parseStream reads CC's NDJSON output and renders it in the terminal.
// Returns the full text of the final response.
func (a *CCAgent) parseStream(stdout interface{ Read([]byte) (int, error) }, term *Terminal) string {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	var finalResult string

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event ccEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}

		switch event.Type {
		case "system":
			if event.Subtype == "init" && event.SessionID != "" {
				a.mu.Lock()
				a.ccSessionID = event.SessionID
				a.mu.Unlock()
			}

		case "assistant":
			if event.Message == nil {
				continue
			}
			blocks := parseCCBlocks(event.Message.Content)
			for _, block := range blocks {
				switch block.Type {
				case "text":
					if block.Text != "" {
						term.StreamText(block.Text)
					}
				case "tool_use":
					displayName := stripMCPPrefix(block.Name)
					a.mu.Lock()
					a.lastToolName = displayName
					a.mu.Unlock()
					term.PrintToolIcon(displayName)
					term.PrintToolStart(displayName, block.Input)
				}
			}

		case "user":
			// User events during an agentic loop carry tool_result blocks.
			if event.Message == nil {
				continue
			}
			blocks := parseCCBlocks(event.Message.Content)
			for _, block := range blocks {
				if block.Type == "tool_result" {
					a.mu.Lock()
					toolName := a.lastToolName
					a.mu.Unlock()
					content := extractToolResultText(block.Content)
					term.PrintToolResult(toolName, truncateStr(content, 200))
				}
			}

		case "result":
			finalResult = event.Result
			if event.IsError && event.Result != "" {
				term.PrintError("CC error: " + event.Result)
			}
		}
	}

	term.FinishMarkdown(finalResult)
	return finalResult
}

// parseCCBlocks unmarshals a CC message content field (string or []block).
func parseCCBlocks(raw json.RawMessage) []ccBlock {
	if len(raw) == 0 {
		return nil
	}

	// Try array form first
	var blocks []ccBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocks
	}

	// Fall back to plain string (rare but possible in older CC versions)
	var text string
	if err := json.Unmarshal(raw, &text); err == nil && text != "" {
		return []ccBlock{{Type: "text", Text: text}}
	}
	return nil
}

// effortDirective returns a system-prompt suffix that tunes response thoroughness.
func effortDirective(effort string) string {
	switch strings.ToLower(effort) {
	case "low":
		return "\n\nEFFORT: LOW — Be concise. Surface only the highest-severity issues. Skip exhaustive enumeration."
	case "medium":
		return "\n\nEFFORT: MEDIUM — Balance thoroughness with brevity. Cover main risk axes; skip minor cosmetic issues."
	default: // "high" or unset
		return "\n\nEFFORT: HIGH — Be exhaustive. Explore all coverage axes, flag every risk, leave no stone unturned."
	}
}

// stripMCPPrefix removes the "mcp__<server>__" prefix CC adds to MCP tool names.
// CC turns "list_projects" into "mcp__qmax__list_projects" internally.
func stripMCPPrefix(name string) string {
	// prefix pattern: mcp__<server>__<tool>
	const pfx = "mcp__"
	if !strings.HasPrefix(name, pfx) {
		return name
	}
	rest := name[len(pfx):]
	if idx := strings.Index(rest, "__"); idx != -1 {
		return rest[idx+2:]
	}
	return name
}

// extractToolResultText pulls the text content out of a tool_result content block.
func extractToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Can be a string or []{"type":"text","text":"..."} array
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			sb.WriteString(b.Text)
		}
		return sb.String()
	}
	return string(raw)
}
