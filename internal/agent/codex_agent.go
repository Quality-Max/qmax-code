package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/qualitymax/qmax-code/codexrunner"
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
//  2. qmax-code starts or resumes a Codex thread through codexrunner
//  3. Codex picks up the MCP config and spawns qmax-code serve --mcp
//  4. Codex uses qmax tools natively, runs on OpenAI subscription
//  5. qmax-code streams Codex's stdout to the terminal
type CodexAgent struct {
	codexBin       string
	effort         string // "low" | "medium" | "high"
	outputVerbose  bool   // false = compact answer style; true = previous detailed style
	sctx           *api.SessionContext
	continuity     *codexrunner.Continuity
	lastTurnIn     int // token usage of the most recent turn, when codex reports it
	lastTurnOut    int
	lastTurnOK     bool
	lastLimitHit   bool      // true if the plan limit was hit this turn
	lastLimitReset time.Time // provider-reported reset (usually zero for codex)
	mu             sync.Mutex
	turnMu         sync.Mutex // keeps checkpoint selection and execution atomic
	runMu          sync.Mutex
	runCancel      context.CancelFunc // non-nil while Run() is active
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

// NewCodexAgent creates a Codex subprocess orchestrator. Model, approval, and
// sandbox choices come from Codex's own configuration because the public
// runner intentionally uses a fixed command line.
func NewCodexAgent(bin, effort string, outputVerbose bool, sctx *api.SessionContext) *CodexAgent {
	if effort == "" {
		effort = "high"
	}
	return &CodexAgent{
		codexBin:      bin,
		effort:        effort,
		outputVerbose: outputVerbose,
		sctx:          sctx,
		continuity:    codexrunner.NewContinuity(codexrunner.New(codexrunner.Options{Executable: bin})),
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
	a.turnMu.Lock()
	defer a.turnMu.Unlock()

	if err := a.WriteMCPConfig(); err != nil {
		return "", fmt.Errorf("MCP config: %w", err)
	}

	a.mu.Lock()
	a.lastTurnIn, a.lastTurnOut, a.lastTurnOK = 0, 0, false
	a.lastLimitHit, a.lastLimitReset = false, time.Time{}
	a.mu.Unlock()

	continuity := a.getContinuity()
	isInitialTurn := continuity.Checkpoint().ThreadID == ""
	prompt := a.buildPrompt(userMsg, isInitialTurn)

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

	result, err := continuity.Run(ctx, prompt, codexrunner.Hooks{
		Presenter: codexrunner.PresenterFunc(func(_ context.Context, presentation codexrunner.Presentation) error {
			if term == nil {
				return nil
			}
			switch presentation.Kind {
			case codexrunner.PresentationText:
				term.StreamText(presentation.Text + "\n")
			case codexrunner.PresentationPlanLimit:
				term.PrintError("Coding-plan limit reached (see /plan)")
			}
			return nil
		}),
	})

	a.mu.Lock()
	if result.Usage.InputTokens > 0 || result.Usage.OutputTokens > 0 {
		a.lastTurnIn = result.Usage.InputTokens
		a.lastTurnOut = result.Usage.OutputTokens
		a.lastTurnOK = true
	}
	a.lastLimitHit = result.PlanLimit
	a.mu.Unlock()

	if result.Canceled {
		return result.Response, nil
	}
	if err != nil {
		return result.Response, fmt.Errorf("codex turn: %w", err)
	}
	if term != nil {
		term.FinishMarkdown(result.Response)
	}
	return result.Response, nil
}

func (a *CodexAgent) getContinuity() *codexrunner.Continuity {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.continuity == nil {
		a.continuity = codexrunner.NewContinuity(codexrunner.New(codexrunner.Options{Executable: a.codexBin}))
	}
	return a.continuity
}

// buildPrompt constructs the current turn without replaying local transcripts.
// Codex's validated native thread is the sole conversation continuity source,
// so the QA scaffold is sent only when starting that thread.
func (a *CodexAgent) buildPrompt(userMsg string, initial bool) string {
	a.mu.Lock()
	effort := a.effort
	outputVerbose := a.outputVerbose
	a.mu.Unlock()
	prefix := effortDirective(effort) + outputStyleDirective(outputVerbose)
	if initial {
		prefix = cliQASystemPrompt(a.sctx, codexQASystemPrompt) + prefix
	}
	return prefix + "\n\n" + userMsg
}

// ClearHistory resets native Codex continuity (used when the user types /clear).
func (a *CodexAgent) ClearHistory() {
	a.getContinuity().Reset()
}

// ResetConversation implements ConversationResetter.
func (a *CodexAgent) ResetConversation() {
	a.ClearHistory()
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
