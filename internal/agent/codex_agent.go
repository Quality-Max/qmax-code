package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/sysutil"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// CodexAgent orchestrates an OpenAI Codex CLI subprocess for LLM inference.
// Inference runs through the user's ChatGPT/OpenAI subscription — no OpenAI
// API tokens consumed by qmax-code. qmax tools are served via the same MCP
// server used for CC mode.
//
// Per-message flow:
//  1. qmax-code writes ~/.codex/config.toml with the qmax MCP server entry
//  2. qmax-code spawns: codex exec --json [--dangerously-bypass-approvals-and-sandbox] "msg"
//  3. Codex picks up the MCP config and spawns qmax-code serve --mcp
//  4. Codex uses qmax tools natively, runs on OpenAI subscription
//  5. qmax-code streams Codex's stdout to the terminal
//
// Unlike CCAgent, Codex does not expose a rich stream-json format, so output
// is captured as plain text. History is managed by qmax-code itself (passed
// as context in the system/user turn) since Codex has no --resume equivalent.
type CodexAgent struct {
	codexBin       string
	modelID        string // "" = codex default; otherwise passed as -m
	effort         string // "low" | "medium" | "high"
	outputVerbose  bool   // false = compact answer style; true = previous detailed style
	permissionMode string // "standard" (Codex prompts per-action) | "unattended" (--dangerously-bypass-approvals-and-sandbox)
	sctx           *api.SessionContext
	history        []codexTurn // conversation history managed on our side
	lastTurnIn     int         // token usage of the most recent turn, when codex reports it
	lastTurnOut    int
	lastTurnOK     bool
	lastLimitHit   bool      // true if the plan limit was hit this turn
	lastLimitReset time.Time // provider-reported reset (usually zero for codex)
	mu             sync.Mutex
	runMu          sync.Mutex
	runCancel      context.CancelFunc // non-nil while Run() is active
}

type codexTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// FindCodex returns the path to the codex CLI binary, or "" if not installed.
func FindCodex() string {
	if path, err := exec.LookPath("codex"); err == nil {
		return path
	}
	for _, p := range []string{
		"/usr/local/bin/codex",
		"/opt/homebrew/bin/codex",
		filepath.Join(os.Getenv("HOME"), ".local/bin/codex"),
		filepath.Join(os.Getenv("HOME"), ".npm-global/bin/codex"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// NewCodexAgent creates a Codex subprocess orchestrator.
// modelID is passed as -m to the codex CLI (empty = codex default).
// effort is "low" | "medium" | "high" (empty defaults to "high").
// permissionMode is "standard" or "unattended" — Codex has no allowlist primitive,
// so Standard relies on Codex's own sandbox policy and Unattended adds
// --dangerously-bypass-approvals-and-sandbox.
func NewCodexAgent(bin, modelID, effort, permissionMode string, outputVerbose bool, sctx *api.SessionContext) *CodexAgent {
	if effort == "" {
		effort = "high"
	}
	if permissionMode == "" {
		permissionMode = "standard"
	}
	return &CodexAgent{
		codexBin:       bin,
		modelID:        modelID,
		effort:         effort,
		outputVerbose:  outputVerbose,
		permissionMode: permissionMode,
		sctx:           sctx,
	}
}

// WriteMCPConfig writes the qmax MCP server into ~/.codex/config.toml
// so Codex picks it up for every invocation.
func (a *CodexAgent) WriteMCPConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		return err
	}

	env := map[string]string{}
	if a.sctx.ProjectID > 0 {
		env["QMAX_PROJECT_ID"] = fmt.Sprintf("%d", a.sctx.ProjectID)
	}
	// Live-feed plumbing — see WriteMCPConfig in cc_agent.go for context.
	if a.sctx.LiveFeed {
		env["QMAX_LIVE_FEED"] = "1"
	}
	if a.sctx.LocalOnly {
		env[api.LocalOnlyEnv] = "1"
	}
	if path := sysutil.LiveURLFilePath(); path != "" {
		env["QMAX_LIVE_URL_FILE"] = path
	}
	if path := sysutil.ExecIDFilePath(); path != "" {
		env["QMAX_EXEC_ID_FILE"] = path
	}

	cfgPath := filepath.Join(codexDir, "config.toml")
	_, err = WriteCodexMCPEntry(cfgPath, env)
	return err
}

// codexQASystemPrompt is embedded in the initial prompt to Codex, since
// Codex does not support an --append-system-prompt flag like CC does.
const codexQASystemPrompt = `You are QMax, an elite QA engineer integrated with the QualityMax platform
via the "qmax" MCP server. Apply the same methodology as a senior engineer:
always fetch real data before claims, run tests after generating them, diagnose
failures before suggesting fixes, and flag coverage gaps proactively.

Coverage axes: happy path, error/exception paths, boundary conditions,
auth boundaries, concurrent access, state transitions.

Risk priority: HIGH (auth, payments, data integrity), MEDIUM (core flows,
integrations), LOW (UI polish, rarely-used features).

Never guess project IDs, test names, or execution results — always use a tool.
End each response with the next highest-impact action.

`

// Run executes one conversation turn through a Codex subprocess.
func (a *CodexAgent) Run(userMsg string, term *tui.Terminal) (string, error) {
	if err := a.WriteMCPConfig(); err != nil {
		return "", fmt.Errorf("MCP config: %w", err)
	}

	a.mu.Lock()
	a.lastTurnIn, a.lastTurnOut, a.lastTurnOK = 0, 0, false
	a.lastLimitHit, a.lastLimitReset = false, time.Time{}
	a.mu.Unlock()

	// Build the full prompt: QA system prompt + conversation history + current message.
	prompt := a.buildPrompt(userMsg)

	args := []string{"exec", "--json"}
	if a.permissionMode == "unattended" {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	if a.modelID != "" {
		args = append(args, "-m", a.modelID)
	}
	args = append(args, prompt)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	a.runMu.Lock()
	a.runCancel = cancel
	a.runMu.Unlock()
	defer func() {
		a.runMu.Lock()
		a.runCancel = nil
		a.runMu.Unlock()
	}()

	cmd := exec.CommandContext(ctx, a.codexBin, args...)
	cmd.Stdin = strings.NewReader("")
	cmd.Stderr = term.Stderr()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start codex: %w", err)
	}

	// Stream JSONL events from codex exec --json.
	var sb strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	for scanner.Scan() {
		line := scanner.Text()
		a.inspectCodexEvent(line, term)
		text := extractCodexMessage(line)
		if text != "" {
			term.StreamText(text + "\n")
			sb.WriteString(text)
			sb.WriteByte('\n')
		}
	}

	if err := cmd.Wait(); err != nil {
		// Intentional cancel (user pressed Enter to interrupt) — not an error.
		if ctx.Err() != nil {
			return strings.TrimSpace(sb.String()), nil
		}
		if sb.Len() == 0 {
			return "", fmt.Errorf("codex exited with error: %w", err)
		}
	}

	result := strings.TrimSpace(sb.String())
	term.FinishMarkdown(result)

	// Add to our local history for context on subsequent turns.
	a.mu.Lock()
	a.history = append(a.history,
		codexTurn{Role: "user", Content: userMsg},
		codexTurn{Role: "assistant", Content: result},
	)
	// Keep last 10 turns (20 messages) to stay within Codex's context.
	if len(a.history) > 20 {
		a.history = a.history[len(a.history)-20:]
	}
	a.mu.Unlock()

	return result, nil
}

// buildPrompt constructs a full prompt including conversation history and effort level.
func (a *CodexAgent) buildPrompt(userMsg string) string {
	a.mu.Lock()
	history := a.history
	a.mu.Unlock()

	systemPrompt := cliQASystemPrompt(a.sctx, codexQASystemPrompt) + effortDirective(a.effort) + outputStyleDirective(a.outputVerbose) + "\n\n"

	if len(history) == 0 {
		// First turn: include the system prompt.
		return systemPrompt + userMsg
	}

	// Subsequent turns: include compressed history.
	var sb strings.Builder
	sb.WriteString(systemPrompt)
	sb.WriteString("[Previous conversation]\n")
	for _, turn := range history {
		role := "User"
		if turn.Role == "assistant" {
			role = "Assistant"
		}
		content := turn.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		fmt.Fprintf(&sb, "%s: %s\n\n", role, content)
	}
	sb.WriteString("[Current message]\n")
	sb.WriteString(userMsg)
	return sb.String()
}

// ClearHistory resets conversation history (used when the user types /clear).
func (a *CodexAgent) ClearHistory() {
	a.mu.Lock()
	a.history = nil
	a.mu.Unlock()
}

func (a *CodexAgent) SetOutputVerbose(verbose bool) {
	a.mu.Lock()
	a.outputVerbose = verbose
	a.mu.Unlock()
}

// Cancel interrupts a Run call that is in progress. Safe to call from any goroutine.
func (a *CodexAgent) Cancel() {
	a.runMu.Lock()
	if a.runCancel != nil {
		a.runCancel()
	}
	a.runMu.Unlock()
}

// Cleanup is a no-op for CodexAgent (no temp files to remove).
func (a *CodexAgent) Cleanup() {}

// codexEvent is a tolerant view of a `codex exec --json` event line, capturing
// the token-usage and error fields qmax-code needs for the coding-plan window.
// Field names are best-effort across codex versions (confirm against a real
// run); the time-based window works even when none are populated.
type codexEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Error   string `json:"error"`

	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	Usage        *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		Input        int `json:"input"`
		Output       int `json:"output"`
	} `json:"usage"`
}

// inspectCodexEvent folds token usage from a codex event line into the turn
// stats and surfaces plan-limit errors that would otherwise be invisible (codex
// prints them to its JSON stream, not always to stderr).
func (a *CodexAgent) inspectCodexEvent(line string, term *tui.Terminal) {
	var ev codexEvent
	if json.Unmarshal([]byte(line), &ev) != nil {
		return
	}

	in, out := ev.InputTokens, ev.OutputTokens
	if ev.Usage != nil {
		if ev.Usage.InputTokens > 0 || ev.Usage.OutputTokens > 0 {
			in, out = ev.Usage.InputTokens, ev.Usage.OutputTokens
		} else if ev.Usage.Input > 0 || ev.Usage.Output > 0 {
			in, out = ev.Usage.Input, ev.Usage.Output
		}
	}
	if in > 0 || out > 0 {
		a.mu.Lock()
		a.lastTurnIn, a.lastTurnOut, a.lastTurnOK = in, out, true
		a.mu.Unlock()
	}

	if strings.Contains(ev.Type, "error") || ev.Error != "" {
		msg := ev.Error
		if msg == "" {
			msg = ev.Message
		}
		if isPlanLimitMessage(msg) {
			a.mu.Lock()
			a.lastLimitHit = true
			a.mu.Unlock()
			term.PrintError("Coding-plan limit reached — " + msg + " (see /plan)")
		}
	}
}

// LastTurnStats returns codex's token usage for the most recent turn, when its
// stream carried any. Satisfies TurnStatsProvider.
func (a *CodexAgent) LastTurnStats() (inputTokens, outputTokens int, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastTurnIn, a.lastTurnOut, a.lastTurnOK
}

// LastPlanLimit reports whether the plan limit was hit on the most recent turn.
// codex errors carry no reset header, so the reset time is the zero value and
// the window falls back to its time estimate. Satisfies PlanLimitReporter.
func (a *CodexAgent) LastPlanLimit() (reset time.Time, hit bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastLimitReset, a.lastLimitHit
}

// extractCodexMessage parses a JSONL line from `codex exec --json` and returns
// the assistant text if the event is an item.completed with type "agent_message".
// Returns "" for all other event types (turn lifecycle, tool calls, etc.).
func extractCodexMessage(line string) string {
	var event struct {
		Type string `json:"type"`
		Item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	if json.Unmarshal([]byte(line), &event) != nil {
		return ""
	}
	if event.Type == "item.completed" && event.Item.Type == "agent_message" {
		return event.Item.Text
	}
	return ""
}
