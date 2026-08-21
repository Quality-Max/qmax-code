package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
	fileSnaps      map[string]fileSnapshot // opencode tool part id → pre-edit snapshot
	lastTurnIn     int  // token usage of the most recent turn (from opencode's stream)
	lastTurnOut    int
	lastTurnOK     bool      // true once a usage event carried tokens this turn
	lastLimitHit   bool      // true if the plan limit was hit this turn
	lastLimitReset time.Time // provider-reported reset time, zero if unknown
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

// autoFlag caches the one-time probe for `opencode run --auto` support so we
// don't shell out to `--help` on every turn.
var (
	autoFlagOnce      sync.Once
	autoFlagSupported bool
)

// openCodeSupportsAutoFlag reports whether the installed opencode accepts the
// `run --auto` flag. Older opencode used --auto to auto-approve tool calls in
// non-interactive `run` mode; opencode 1.x removed it and governs approvals
// through the config `permission` block instead. Passing --auto to a version
// that no longer knows it makes opencode print usage and exit 1 with no output,
// so probe `run --help` once and only pass the flag when it is advertised.
func openCodeSupportsAutoFlag(bin string) bool {
	if bin == "" {
		return false
	}
	autoFlagOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, bin, "run", "--help").CombinedOutput()
		autoFlagSupported = strings.Contains(string(out), "--auto")
	})
	return autoFlagSupported
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
	a.lastTurnIn, a.lastTurnOut, a.lastTurnOK = 0, 0, false
	a.lastLimitHit, a.lastLimitReset = false, time.Time{}
	sessionID := a.sessionID
	a.mu.Unlock()

	// On the first turn of a session, prepend the QA system prompt + effort/output
	// directives. opencode persists conversation state per session, so later turns
	// resume via --session and don't need it re-injected.
	message := safeUserMsg
	if sessionID == "" {
		message = cliQASystemPrompt(a.sctx, codexQASystemPrompt) + effortDirective(a.effort) + outputStyleDirective(a.outputVerbose) + "\n\n" + safeUserMsg
	}

	args := []string{"run", "--format", "json"}
	if a.modelID != "" {
		args = append(args, "--model", a.modelID)
	}
	// --auto auto-approves anything not explicitly denied. In standard mode the
	// managed config denies edits + destructive shell (openCodeStandardPermission),
	// so --auto is safe there too; unattended has no denies (full autonomy).
	// Older opencode needed --auto because `opencode run` is non-interactive —
	// without it, tools that would prompt simply block. Newer opencode (1.x)
	// REMOVED --auto and governs approvals purely through the config `permission`
	// block; passing --auto there makes opencode print usage and exit 1 with no
	// output. So only add it when the installed opencode still advertises it.
	if openCodeSupportsAutoFlag(a.openCodeBin) {
		args = append(args, "--auto")
	}
	if sessionID != "" && validOpenCodeSessionID(sessionID) {
		args = append(args, "--session", sessionID)
	}
	// "--" terminates flag parsing so a message starting with "-" is treated as
	// the positional prompt rather than an unknown flag. On Windows, opencode is
	// typically an npm ".cmd" shim; Go's os/exec routes it through cmd.exe, which
	// swallows the "--" separator and drops the positional message after it
	// ("You must provide a message or a command"). There we pass the message with
	// no "--" — sanitizeCCUserPrompt already stripped control bytes, and a lone
	// positional is taken as the message.
	if runtime.GOOS == "windows" {
		args = append(args, message)
	} else {
		args = append(args, "--", message)
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

	cmd := exec.CommandContext(ctx, a.openCodeBin, args...)
	cmd.Stdin = strings.NewReader("")
	cmd.Stderr = term.Stderr()
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
	Type      string   `json:"type"`
	Timestamp int64    `json:"timestamp"`
	SessionID string   `json:"sessionID"`
	Part      ocPart   `json:"part"`
	Error     *ocError `json:"error,omitempty"`
	// Token usage may appear at the top level of a completion/step event or on
	// the message part; opencode/provider field names vary by version, so both
	// the "tokens" and "usage" shapes are captured and whichever is populated
	// wins. (Confirm exact names against a successful `opencode run --format
	// json` sample; the time-based window works regardless of these.)
	Tokens *ocTokens `json:"tokens,omitempty"`
	Usage  *ocTokens `json:"usage,omitempty"`
}

type ocPart struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Text   string          `json:"text"`
	Tool   string          `json:"tool"`
	State  string          `json:"state,omitempty"`  // tool parts: pending|running|completed|error
	Input  json.RawMessage `json:"input,omitempty"`  // tool parts: tool input (has file path)
	Tokens *ocTokens       `json:"tokens,omitempty"`
	Usage  *ocTokens       `json:"usage,omitempty"`
}

// ocTokens tolerates the common token-count field names emitted by opencode and
// its providers (input/output vs prompt/completion).
type ocTokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
}

// tokens returns the one canonical usage payload carried by an event. The four
// locations are the same numbers reported by different opencode/provider
// versions, so the first populated shape wins rather than being summed.
func (e *ocEvent) tokens() (in, out int, ok bool) {
	for _, tk := range []*ocTokens{e.Tokens, e.Usage, e.Part.Tokens, e.Part.Usage} {
		if i, o := tk.in(), tk.out(); i > 0 || o > 0 {
			return i, o, true
		}
	}
	return 0, 0, false
}

// usageKey identifies the step an event's usage belongs to, so a re-emitted
// step is not counted twice. An empty result means the event carries nothing
// stable to key on and must be counted as its own step — undercounting a
// multi-step turn is the failure this accumulation exists to prevent.
func (e *ocEvent) usageKey() string {
	if e.Part.ID != "" {
		return "part:" + e.Part.ID
	}
	if e.Timestamp != 0 {
		return "ts:" + e.Type + ":" + strconv.FormatInt(e.Timestamp, 10)
	}
	return ""
}

func (t *ocTokens) in() int {
	if t == nil {
		return 0
	}
	if t.Input > 0 {
		return t.Input
	}
	return t.Prompt
}

func (t *ocTokens) out() int {
	if t == nil {
		return 0
	}
	if t.Output > 0 {
		return t.Output
	}
	return t.Completion
}

// ocError is the payload of a `{"type":"error", ...}` event. opencode emits
// these when the provider refuses a turn — a retired model, an auth failure,
// or (the case that matters for plan tracking) a 429 when the coding-plan usage
// limit is reached. Before this was parsed, such events fell through the stream
// switch and the turn ended with nothing shown to the user.
type ocError struct {
	Name string      `json:"name"`
	Data ocErrorData `json:"data"`
}

type ocErrorData struct {
	Message         string            `json:"message"`
	StatusCode      int               `json:"statusCode"`
	ResponseHeaders map[string]string `json:"responseHeaders"`
	ResponseBody    string            `json:"responseBody"`
}

// parseStream reads opencode's NDJSON output, renders it, captures the session
// id for --session resume, and returns the full text of the final response.
func (a *OpenCodeAgent) parseStream(stdout interface{ Read([]byte) (int, error) }, term *tui.Terminal) string {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	textByPart := map[string]string{} // part id → latest full text
	var order []string                // text part ids in first-seen order
	seenTool := map[string]bool{}     // tool part ids already announced
	countedUsage := map[string]bool{} // usage keys already folded into the turn total

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

		// Surface provider errors that would otherwise be silently dropped,
		// and detect a coding-plan limit hit for the usage-window tracker.
		if ev.Error != nil {
			a.handleOCError(ev.Error, term)
		}

		// Accumulate token usage across the turn. opencode reports usage once
		// per step (step-finish), so a turn that calls tools carries several
		// token-bearing events; overwriting would keep only the final step and
		// undercount every multi-step turn. The four shapes below are the same
		// usage in different places rather than separate counts, so exactly one
		// canonical payload is taken per event, and any step already counted is
		// skipped in case opencode re-emits it.
		if in, out, ok := ev.tokens(); ok {
			key := ev.usageKey()
			if key == "" || !countedUsage[key] {
				if key != "" {
					countedUsage[key] = true
				}
				a.mu.Lock()
				a.lastTurnIn += in
				a.lastTurnOut += out
				a.lastTurnOK = true
				a.mu.Unlock()
			}
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
				if len(order) > 0 {
					// A new part starting after another already streamed (e.g. GLM's
					// separate reasoning/commentary steps) has no separator of its
					// own; without one its first word runs directly into the
					// previous part's last word on screen.
					term.StreamText("\n\n")
				}
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
			if ev.Part.Tool == "" {
				continue
			}
			// A tool part reaching a terminal state may have changed a file —
			// render the live diff before the next output block streams.
			if ev.Part.State == "completed" || ev.Part.State == "error" {
				a.mu.Lock()
				snap, haveSnap := a.fileSnaps[ev.Part.ID]
				delete(a.fileSnaps, ev.Part.ID)
				a.mu.Unlock()
				if haveSnap {
					printFileDiff(term, snap)
				}
			}
			if seenTool[ev.Part.ID] {
				continue
			}
			seenTool[ev.Part.ID] = true
			displayName := stripMCPPrefix(ev.Part.Tool)
			a.mu.Lock()
			a.lastToolName = displayName
			// Snapshot only while the edit is still ahead of us; a part first
			// seen in a terminal state has already run (nothing to diff).
			if ev.Part.State != "completed" && ev.Part.State != "error" {
				if snap := takeFileSnapshotRaw(ev.Part.Tool, toolPathFromRaw(ev.Part.Input)); snap.path != "" {
					if a.fileSnaps == nil {
						a.fileSnaps = map[string]fileSnapshot{}
					}
					a.fileSnaps[ev.Part.ID] = snap
				}
			}
			a.mu.Unlock()
			term.PrintToolIcon(displayName)
			if !a.outputVerbose {
				term.EndLine()
			}
		}
	}

	var sb strings.Builder
	for i, id := range order {
		if i > 0 {
			// Match the separator streamed live between parts above so the
			// returned/rendered result doesn't run parts together either.
			sb.WriteString("\n\n")
		}
		sb.WriteString(textByPart[id])
	}
	finalResult := sb.String()
	term.FinishMarkdown(finalResult)
	return finalResult
}

// handleOCError renders an opencode error event and, when it signals the
// subscription plan's usage limit, records the hit (and any reset time) so the
// REPL can mark the coding-plan window exhausted.
func (a *OpenCodeAgent) handleOCError(e *ocError, term *tui.Terminal) {
	msg := strings.TrimSpace(e.Data.Message)
	if msg == "" {
		msg = e.Name
	}
	if e.Data.StatusCode == 429 || isPlanLimitMessage(msg) || isPlanLimitMessage(e.Data.ResponseBody) {
		reset := parseResetTime(e.Data.ResponseHeaders)
		a.mu.Lock()
		a.lastLimitHit = true
		a.lastLimitReset = reset
		a.mu.Unlock()
		term.PrintError("Coding-plan limit reached — " + msg + " (see /plan)")
		return
	}
	if e.Data.StatusCode > 0 {
		term.PrintError(fmt.Sprintf("opencode provider error (%d): %s", e.Data.StatusCode, msg))
		return
	}
	// opencode 1.0.x emits benign internal errors even on successful turns — e.g.
	// an "UnknownError" schema-validation gripe on the trailing step event — with
	// no HTTP status code. Real provider failures (auth, quota, 5xx) all carry a
	// status code and are handled above, so surfacing these status-code-less
	// events on every turn would just be noise. Show them only in verbose mode.
	if a.outputVerbose {
		term.PrintError("opencode error: " + msg)
	}
}

// LastTurnStats returns opencode's token usage for the most recent turn, when
// its stream carried any. Satisfies TurnStatsProvider.
func (a *OpenCodeAgent) LastTurnStats() (inputTokens, outputTokens int, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastTurnIn, a.lastTurnOut, a.lastTurnOK
}

// LastPlanLimit reports whether the plan limit was hit on the most recent turn
// and, when known, the provider-reported reset time. Satisfies PlanLimitReporter.
func (a *OpenCodeAgent) LastPlanLimit() (reset time.Time, hit bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastLimitReset, a.lastLimitHit
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
