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
	"github.com/qualitymax/qmax-code/internal/tui"
)

// OpenCodeAgent orchestrates an opencode CLI subprocess for LLM inference.
// Inference runs through whichever provider the user opted into (Z.AI, Groq,
// OpenRouter, …) using the user's own key — qmax-code consumes no QM-held
// tokens. qmax tools are exposed to opencode via an MCP server entry in the
// managed opencode config, so opencode can call them natively.
//
// opencode supports native session resume (--session) and a rich NDJSON event
// stream (opencode run --format json), so this mirrors the CCAgent design
// rather than CodexAgent's self-managed history.
//
// Per-message flow:
//  1. qmax-code writes ~/.qmax-code/opencode.json (provider blocks + qmax MCP)
//  2. qmax-code spawns: opencode run --format json --model <provider>/<model>
//     [--session <id>] [--auto] -- "msg"    with OPENCODE_CONFIG + key env set
//  3. opencode picks up the MCP config and spawns qmax-code serve --mcp
//  4. opencode runs the turn on the user's provider, using qmax tools via MCP
//  5. qmax-code parses opencode's NDJSON and renders it; session id → --session
type OpenCodeAgent struct {
	openCodeBin    string
	modelID        string // "provider/model"; "" lets opencode use its default
	effort         string // "low" | "medium" | "high"
	outputVerbose  bool
	permissionMode string // "standard" | "unattended" (--auto)
	sessionID      string // opencode session id, for --session resume
	cfg            *api.Config
	sctx           *api.SessionContext
	lastToolName   string
	mu             sync.Mutex
	runMu          sync.Mutex
	runCancel      context.CancelFunc // non-nil while Run() is active
}

// FindOpenCode returns the path to the opencode CLI binary, or "" if not found.
func FindOpenCode() string {
	if path, err := exec.LookPath("opencode"); err == nil {
		return path
	}
	for _, p := range []string{
		filepath.Join(os.Getenv("HOME"), ".opencode/bin/opencode"),
		"/usr/local/bin/opencode",
		"/opt/homebrew/bin/opencode",
		filepath.Join(os.Getenv("HOME"), ".local/bin/opencode"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// NewOpenCodeAgent creates an opencode subprocess orchestrator.
// modelID is the full "provider/model" string selected via the picker.
// effort is "low" | "medium" | "high" (empty defaults to "high").
// permissionMode is "standard" or "unattended" (adds --auto).
func NewOpenCodeAgent(bin, modelID, effort, permissionMode string, outputVerbose bool, cfg *api.Config, sctx *api.SessionContext) *OpenCodeAgent {
	if effort == "" {
		effort = "high"
	}
	if permissionMode == "" {
		permissionMode = "standard"
	}
	return &OpenCodeAgent{
		openCodeBin:    bin,
		modelID:        modelID,
		effort:         effort,
		outputVerbose:  outputVerbose,
		permissionMode: permissionMode,
		cfg:            cfg,
		sctx:           sctx,
	}
}

// validOpenCodeSessionID guards the --session argument. opencode session ids
// look like "ses_0a91c2141ffe8FiFOZVFulDUUM": a "ses_" prefix followed by
// alphanumeric characters.
func validOpenCodeSessionID(id string) bool {
	if !strings.HasPrefix(id, "ses_") || len(id) > 64 {
		return false
	}
	rest := id[len("ses_"):]
	if rest == "" {
		return false
	}
	for _, r := range rest {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// Run executes one conversation turn through an opencode subprocess.
func (a *OpenCodeAgent) Run(userMsg string, term *tui.Terminal) (string, error) {
	// Regenerate the managed config each turn so newly enabled/disabled
	// providers (and the permission policy) take effect without a restart.
	configPath, err := WriteOpenCodeConfig(a.cfg, a.sctx, a.permissionMode)
	if err != nil {
		return "", fmt.Errorf("opencode config: %w", err)
	}

	safeUserMsg, err := sanitizeCCUserPrompt(userMsg)
	if err != nil {
		return "", err
	}

	a.mu.Lock()
	sessionID := a.sessionID
	a.mu.Unlock()

	// On the first turn of a session, prepend the QA system prompt + effort/output
	// directives. opencode persists conversation state per session, so later turns
	// resume via --session and don't need it re-injected.
	message := safeUserMsg
	if sessionID == "" {
		message = codexQASystemPrompt + effortDirective(a.effort) + outputStyleDirective(a.outputVerbose) + "\n\n" + safeUserMsg
	}

	args := []string{"run", "--format", "json"}
	if a.modelID != "" {
		args = append(args, "--model", a.modelID)
	}
	// --auto auto-approves anything not explicitly denied. In standard mode the
	// managed config denies edits + destructive shell (openCodeStandardPermission),
	// so --auto is safe there too; unattended has no denies (full autonomy). Both
	// need --auto because `opencode run` is non-interactive — without it, tools
	// that would prompt simply block.
	args = append(args, "--auto")
	if sessionID != "" && validOpenCodeSessionID(sessionID) {
		args = append(args, "--session", sessionID)
	}
	// "--" terminates flag parsing so a message starting with "-" is treated as
	// the positional prompt rather than an unknown flag.
	args = append(args, "--", message)

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

	cmd := exec.CommandContext(ctx, a.openCodeBin, args...)
	cmd.Stdin = strings.NewReader("")
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "OPENCODE_CONFIG="+configPath)
	for k, v := range OpenCodeProviderEnv(a.cfg) {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start opencode: %w", err)
	}

	result := a.parseStream(stdout, term)

	if err := cmd.Wait(); err != nil {
		// Intentional cancel (user pressed Enter to interrupt) — not an error.
		if ctx.Err() != nil {
			return result, nil
		}
		if result == "" {
			return "", fmt.Errorf("opencode exited with error: %w", err)
		}
	}
	return result, nil
}

// --- NDJSON stream parsing ---

type ocEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      ocPart `json:"part"`
}

type ocPart struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
	Tool string `json:"tool"`
}

// parseStream reads opencode's NDJSON output, renders it, captures the session
// id for --session resume, and returns the full text of the final response.
func (a *OpenCodeAgent) parseStream(stdout interface{ Read([]byte) (int, error) }, term *tui.Terminal) string {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	textByPart := map[string]string{} // part id → latest full text
	var order []string                // text part ids in first-seen order
	seenTool := map[string]bool{}     // tool part ids already announced

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev ocEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}

		if ev.SessionID != "" && validOpenCodeSessionID(ev.SessionID) {
			a.mu.Lock()
			a.sessionID = ev.SessionID
			a.mu.Unlock()
		}

		switch {
		case ev.Type == "text" || ev.Part.Type == "text":
			id := ev.Part.ID
			text := ev.Part.Text
			if text == "" {
				continue
			}
			prev, seen := textByPart[id]
			if !seen {
				order = append(order, id)
			}
			// opencode may re-emit a growing snapshot for the same part id;
			// stream only the delta to avoid duplication.
			if strings.HasPrefix(text, prev) {
				if delta := text[len(prev):]; delta != "" {
					term.StreamText(delta)
				}
			} else {
				term.StreamText(text)
			}
			textByPart[id] = text

		case ev.Part.Type == "tool" || ev.Type == "tool":
			if ev.Part.Tool == "" || seenTool[ev.Part.ID] {
				continue
			}
			seenTool[ev.Part.ID] = true
			displayName := stripMCPPrefix(ev.Part.Tool)
			a.mu.Lock()
			a.lastToolName = displayName
			a.mu.Unlock()
			term.PrintToolIcon(displayName)
			if !a.outputVerbose {
				fmt.Println()
			}
		}
	}

	var sb strings.Builder
	for _, id := range order {
		sb.WriteString(textByPart[id])
	}
	finalResult := sb.String()
	term.FinishMarkdown(finalResult)
	return finalResult
}

// ClearSession forgets the opencode session id so the next turn starts fresh
// (used when the user types /clear).
func (a *OpenCodeAgent) ClearSession() {
	a.mu.Lock()
	a.sessionID = ""
	a.mu.Unlock()
}

func (a *OpenCodeAgent) SetOutputVerbose(verbose bool) {
	a.mu.Lock()
	a.outputVerbose = verbose
	a.mu.Unlock()
}

// Cancel interrupts a Run call that is in progress. Safe to call from any goroutine.
func (a *OpenCodeAgent) Cancel() {
	a.runMu.Lock()
	if a.runCancel != nil {
		a.runCancel()
	}
	a.runMu.Unlock()
}

// Cleanup is a no-op: the managed opencode config is persistent and syncable,
// not a per-session temp file.
func (a *OpenCodeAgent) Cleanup() {}
