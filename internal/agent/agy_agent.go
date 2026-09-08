package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// AgyAgent orchestrates an Antigravity CLI (`agy`) subprocess for LLM
// inference. Authentication is Google OAuth: run interactive `agy` once so
// the CLI can open a browser sign-in (the same Google account as AI Studio).
// qmax-code does not ask for a Gemini API key and does not set modelProvider=gemini.
//
// Per-message flow:
//  1. qmax-code merges the qmax MCP server into ~/.gemini/config/mcp_config.json
//  2. qmax-code spawns: agy --output-format stream-json --add-dir <cwd>
//     [--conversation <id>] [-p prompt]
//  3. agy spawns qmax-code serve --mcp and runs the native agent harness
//  4. qmax-code parses NDJSON events and renders them
//  5. conversation_id is kept for --conversation on the next turn
type AgyAgent struct {
	TurnTranscript
	agyBin         string
	modelID        string // "" = agy default; otherwise --model
	effort         string // "low" | "medium" | "high"
	outputVerbose  bool
	permissionMode string // "standard" | "unattended"
	conversationID string
	sctx           *api.SessionContext
	lastTurnIn     int
	lastTurnOut    int
	lastTurnOK     bool
	lastLimitHit   bool
	lastLimitReset time.Time
	lastStderr     string
	mu             sync.Mutex
	runMu          sync.Mutex
	runCancel      context.CancelFunc
}

const agyQASystemPrompt = `You are QMax, an elite QA engineer running inside Google Antigravity.
You inherit Antigravity's native tools (shell, file read/write, search) AND the
QualityMax platform via the "qmax" MCP server. Use both toolsets.

Coverage axes: happy path, error/exception paths, boundary conditions,
auth boundaries, concurrent access, state transitions.

Risk priority: HIGH (auth, payments, data integrity), MEDIUM (core flows,
integrations), LOW (UI polish, rarely-used features).

Never guess project IDs, test names, or execution results — always use a tool.
End each response with the next highest-impact action.
`

// FindAgy returns the path to the Antigravity CLI binary, or "" if not found.
func FindAgy() string {
	if path, err := exec.LookPath("agy"); err == nil {
		return path
	}
	home := os.Getenv("HOME")
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "agy"),
		"/usr/local/bin/agy",
		"/opt/homebrew/bin/agy",
	} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// AgyOAuthTokenPath is the file-storage fallback for Google OAuth tokens
// (~/.gemini/antigravity-cli/antigravity-oauth-token). On macOS the token
// usually lives in the OS keyring instead; this path is the Linux/CI fallback.
func AgyOAuthTokenPath(home string) string {
	return filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
}

// AgyFileTokenPresent reports whether the file-backed Google OAuth token
// exists. A false result does not mean the user is logged out: macOS Keychain
// credentials are invisible here.
func AgyFileTokenPresent() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	info, err := os.Stat(AgyOAuthTokenPath(home))
	return err == nil && info.Size() > 0
}

// NewAgyAgent creates an Antigravity subprocess orchestrator.
func NewAgyAgent(bin, modelID, effort, permissionMode string, outputVerbose bool, sctx *api.SessionContext) *AgyAgent {
	if effort == "" {
		effort = "high"
	}
	if permissionMode == "" {
		permissionMode = "standard"
	}
	return &AgyAgent{
		agyBin:         bin,
		modelID:        modelID,
		effort:         effort,
		outputVerbose:  outputVerbose,
		permissionMode: permissionMode,
		sctx:           sctx,
	}
}

// agyMCPConfigPath is ~/.gemini/config/mcp_config.json.
func agyMCPConfigPath(home string) string {
	return filepath.Join(home, ".gemini", "config", "mcp_config.json")
}

// WriteMCPConfig merges the qmax MCP server into Antigravity's global MCP
// config. agy has no per-invocation --mcp-config flag, so this is the attach
// path (same class as Codex writing ~/.codex/config.toml).
func (a *AgyAgent) WriteMCPConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".gemini", "config")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	env := map[string]string{}
	if a.sctx != nil && a.sctx.ProjectID > 0 {
		env["QMAX_PROJECT_ID"] = fmt.Sprintf("%d", a.sctx.ProjectID)
	}
	if a.sctx != nil && a.sctx.LiveFeed {
		env["QMAX_LIVE_FEED"] = "1"
	}
	if a.sctx != nil && a.sctx.LocalOnly {
		env[api.LocalOnlyEnv] = "1"
	}
	if path := sysutil.LiveURLFilePath(); path != "" {
		env["QMAX_LIVE_URL_FILE"] = path
	}
	if path := sysutil.ExecIDFilePath(); path != "" {
		env["QMAX_EXEC_ID_FILE"] = path
	}
	_, err = WriteAgyMCPEntry(agyMCPConfigPath(home), env)
	return err
}

// WriteAgyMCPEntry merges the qmax MCP stdio server into an Antigravity
// mcp_config.json, preserving unrelated servers.
func WriteAgyMCPEntry(path string, env map[string]string) (alreadyHad bool, err error) {
	cfg := map[string]any{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	mcpServers, _ := cfg["mcpServers"].(map[string]any)
	if mcpServers == nil {
		mcpServers = map[string]any{}
	}
	if existing, ok := mcpServers["qmax"]; ok && existing != nil {
		alreadyHad = true
	}
	entry := map[string]any{
		"command": QmaxMCPCommand,
		"args":    []string{"serve", "--mcp"},
	}
	if len(env) > 0 {
		entry["env"] = env
	}
	mcpServers["qmax"] = entry
	cfg["mcpServers"] = mcpServers
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return alreadyHad, err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return alreadyHad, err
	}
	return alreadyHad, nil
}

func validateAgyConversationID(id string) error {
	if id == "" {
		return fmt.Errorf("empty Antigravity conversation ID")
	}
	if len(id) != 36 {
		return fmt.Errorf("invalid Antigravity conversation ID")
	}
	for i, r := range id {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return fmt.Errorf("invalid Antigravity conversation ID")
			}
		default:
			if !((r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || (r >= '0' && r <= '9')) {
				return fmt.Errorf("invalid Antigravity conversation ID")
			}
		}
	}
	return nil
}

// buildAgyArgs constructs the print-mode argv. -p and the prompt must be last.
func (a *AgyAgent) buildAgyArgs(prompt, cwd, conversationID string) ([]string, error) {
	safe, err := sanitizeCCUserPrompt(prompt)
	if err != nil {
		return nil, err
	}
	args := []string{
		"--output-format", "stream-json",
		"--print-timeout", "30m",
	}
	if cwd != "" {
		args = append(args, "--add-dir", cwd)
	}
	if a.modelID != "" && a.modelID != "auto" {
		args = append(args, "--model", a.modelID)
	}
	if a.effort != "" {
		args = append(args, "--effort", a.effort)
	}
	if a.permissionMode == "unattended" {
		args = append(args, "--dangerously-skip-permissions")
	}
	if conversationID != "" {
		if err := validateAgyConversationID(conversationID); err != nil {
			return nil, err
		}
		args = append(args, "--conversation", conversationID)
	}
	args = append(args, "-p", safe)
	return args, nil
}

// Run executes one conversation turn through an agy subprocess.
func (a *AgyAgent) Run(userMsg string, term *tui.Terminal) (string, error) {
	if err := a.WriteMCPConfig(); err != nil {
		return "", fmt.Errorf("MCP config: %w", err)
	}

	a.mu.Lock()
	a.lastTurnIn, a.lastTurnOut, a.lastTurnOK = 0, 0, false
	a.lastLimitHit, a.lastLimitReset = false, time.Time{}
	a.lastStderr = ""
	conversationID := a.conversationID
	a.mu.Unlock()

	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		cwd = "."
	}

	message := effortDirective(a.effort) + outputStyleDirective(a.outputVerbose) + "\n\n" + userMsg
	if conversationID == "" {
		message = cliQASystemPrompt(a.sctx, agyQASystemPrompt) + effortDirective(a.effort) + outputStyleDirective(a.outputVerbose) + "\n\n" + userMsg
	}

	args, err := a.buildAgyArgs(message, cwd, conversationID)
	if err != nil {
		return "", err
	}

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

	cmd := exec.CommandContext(ctx, a.agyBin, args...)
	cmd.Dir = cwd
	var stderrBuf bytes.Buffer
	if term != nil {
		cmd.Stderr = io.MultiWriter(term.Stderr(), &stderrBuf)
	} else {
		cmd.Stderr = &stderrBuf
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start agy: %w", err)
	}

	result := a.parseStream(stdout, term)
	waitErr := cmd.Wait()
	a.mu.Lock()
	a.lastStderr = stderrBuf.String()
	a.mu.Unlock()

	if waitErr != nil {
		if ctx.Err() != nil {
			return result, nil
		}
		if result == "" {
			return "", a.wrapAgyExit(waitErr)
		}
	}
	return result, nil
}

func (a *AgyAgent) wrapAgyExit(err error) error {
	a.mu.Lock()
	stderr := a.lastStderr
	a.mu.Unlock()
	lower := strings.ToLower(stderr + err.Error())
	if strings.Contains(lower, "authentication required") || strings.Contains(lower, "not authenticated") {
		return fmt.Errorf("Antigravity is not signed in with Google. Run `agy`, sign in with Google in the browser, then exit and retry.\n  Use the same Google account as AI Studio. An API key is not required.\n  (%v)", err)
	}
	if strings.Contains(lower, "usage") && strings.Contains(lower, "limit") {
		a.mu.Lock()
		a.lastLimitHit = true
		a.mu.Unlock()
	}
	tail := strings.TrimSpace(stderr)
	if len(tail) > 400 {
		tail = tail[len(tail)-400:]
	}
	if tail != "" {
		return fmt.Errorf("agy exited with error: %w\n%s", err, tail)
	}
	return fmt.Errorf("agy exited with error: %w", err)
}

func (a *AgyAgent) Cancel() {
	a.runMu.Lock()
	if a.runCancel != nil {
		a.runCancel()
	}
	a.runMu.Unlock()
}

func (a *AgyAgent) Cleanup() {}

func (a *AgyAgent) SetOutputVerbose(verbose bool) {
	a.mu.Lock()
	a.outputVerbose = verbose
	a.mu.Unlock()
}

func (a *AgyAgent) ResetConversation() {
	a.mu.Lock()
	a.conversationID = ""
	a.mu.Unlock()
}

func (a *AgyAgent) LastTurnStats() (inputTokens, outputTokens int, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastTurnIn, a.lastTurnOut, a.lastTurnOK
}

func (a *AgyAgent) LastPlanLimit() (reset time.Time, hit bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastLimitReset, a.lastLimitHit
}

// agyEvent is one NDJSON line from `agy --output-format stream-json`.
type agyEvent struct {
	Event          string         `json:"event"`
	ConversationID string         `json:"conversation_id,omitempty"`
	StepUpdate     *agyStepUpdate `json:"step_update,omitempty"`
	Result         *agyResult     `json:"result,omitempty"`
}

type agyStepUpdate struct {
	ConversationID string       `json:"conversation_id"`
	StepIndex      int          `json:"step_index"`
	State          string       `json:"state"`
	StepType       string       `json:"step_type"`
	ToolName       string       `json:"tool_name,omitempty"`
	TextDelta      string       `json:"text_delta,omitempty"`
	Usage          *agyUsage    `json:"usage,omitempty"`
	ToolInfo       *agyToolInfo `json:"tool_info,omitempty"`
}

type agyResult struct {
	ConversationID string    `json:"conversation_id"`
	Status         string    `json:"status"`
	Response       string    `json:"response"`
	Error          string    `json:"error,omitempty"`
	Usage          *agyUsage `json:"usage,omitempty"`
}

type agyUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type agyToolInfo struct {
	Name       string         `json:"name"`
	Parameters map[string]any `json:"parameters"`
	Output     string         `json:"output"`
	Error      *agyToolError  `json:"error,omitempty"`
}

type agyToolError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// agyToolName is the label used for both ACTIVE and DONE tool events so
// verbose start/result lines stay aligned when ToolName and ToolInfo.Name differ.
func agyToolName(su *agyStepUpdate) string {
	if su == nil {
		return ""
	}
	name := su.ToolName
	if su.ToolInfo != nil && su.ToolInfo.Name != "" {
		name = su.ToolInfo.Name
	}
	return stripMCPPrefix(name)
}

func (a *AgyAgent) parseStream(stdout io.Reader, term *tui.Terminal) string {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	var finalResult string
	seenTool := map[int]bool{}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var event agyEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		if event.ConversationID != "" && validateAgyConversationID(event.ConversationID) == nil {
			a.mu.Lock()
			a.conversationID = event.ConversationID
			a.mu.Unlock()
		}

		switch event.Event {
		case "init":
			// conversation_id is on the envelope.
		case "step_update":
			if event.StepUpdate == nil {
				continue
			}
			su := event.StepUpdate
			if su.ConversationID != "" && validateAgyConversationID(su.ConversationID) == nil {
				a.mu.Lock()
				a.conversationID = su.ConversationID
				a.mu.Unlock()
			}
			switch su.StepType {
			case "agent_response":
				if su.TextDelta != "" && term != nil {
					term.StreamText(su.TextDelta)
				}
			case "tool":
				if su.ToolInfo != nil && su.State == "DONE" {
					a.record("assistant", su.ToolInfo)
				}
				name := agyToolName(su)
				if su.State == "ACTIVE" && !seenTool[su.StepIndex] {
					seenTool[su.StepIndex] = true
					if term != nil {
						term.PrintToolIcon(name)
						if a.outputVerbose && su.ToolInfo != nil {
							term.PrintToolStart(name, su.ToolInfo.Parameters)
						} else {
							term.EndLine()
						}
					}
				}
				if su.State == "DONE" && term != nil {
					if a.outputVerbose && su.ToolInfo != nil {
						out := su.ToolInfo.Output
						if su.ToolInfo.Error != nil && su.ToolInfo.Error.Message != "" {
							out = su.ToolInfo.Error.Message
						}
						term.PrintToolResult(name, tui.TruncateStr(out, 200))
					} else {
						term.StartThinking()
					}
				}
			}
			if su.Usage != nil {
				a.mu.Lock()
				a.lastTurnIn = su.Usage.InputTokens
				a.lastTurnOut = su.Usage.OutputTokens
				a.lastTurnOK = su.Usage.InputTokens > 0 || su.Usage.OutputTokens > 0
				a.mu.Unlock()
			}
		case "result":
			if event.Result == nil {
				continue
			}
			finalResult = event.Result.Response
			if event.Result.ConversationID != "" && validateAgyConversationID(event.Result.ConversationID) == nil {
				a.mu.Lock()
				a.conversationID = event.Result.ConversationID
				a.mu.Unlock()
			}
			if event.Result.Usage != nil {
				a.mu.Lock()
				a.lastTurnIn = event.Result.Usage.InputTokens
				a.lastTurnOut = event.Result.Usage.OutputTokens
				a.lastTurnOK = event.Result.Usage.InputTokens > 0 || event.Result.Usage.OutputTokens > 0
				a.mu.Unlock()
			}
			if event.Result.Status != "" && !strings.EqualFold(event.Result.Status, "SUCCESS") && event.Result.Error != "" && term != nil {
				term.PrintError("Antigravity: " + event.Result.Error)
			}
		}
	}
	if term != nil {
		term.FinishMarkdown(finalResult)
	}
	return finalResult
}
