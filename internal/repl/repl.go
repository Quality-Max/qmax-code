// Package repl implements the interactive read-eval-print loop for qmax-code.
// It owns slash-command dispatch, prompt queueing, signal handling, live-feed
// auto-launch, and the bridge between the LLM agent (internal/agent) and the
// terminal UI (internal/tui).
package repl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/session"
	"github.com/qualitymax/qmax-code/internal/setup"
	"github.com/qualitymax/qmax-code/internal/sysutil"
	"github.com/qualitymax/qmax-code/internal/tui"
	"github.com/qualitymax/qmax-code/internal/vnc"
)

// Run is the REPL entrypoint. version is the qmax-code build version,
// surfaced in the welcome banner.
func Run(ag *agent.Agent, cliAgent agent.CLIAgent, quietMode bool, version string) {
	term := tui.NewTerminal()
	defer term.Close()
	if cliAgent != nil {
		defer cliAgent.Cleanup()
	}

	// Prompt queue — collects prompts typed while the agent is running.
	pq := &session.PromptQueue{}

	sessionID := session.GenerateSessionID()

	// Initialize structured logger
	ag.Logger = sysutil.NewLogger(sessionID)
	defer ag.Logger.Close()

	// Cloud session tracking — created once when projectID is known.
	var tracker session.CloudSessionTracker
	startCloudSession := func() {
		api := ag.Cfg.Context.API
		projectID := ag.Cfg.Context.ProjectID
		if api == nil || projectID == 0 {
			return
		}
		cfg := ag.AppConfig
		// First eligible session: ask the user once and persist their choice.
		if cfg != nil && cfg.CloudSync == nil {
			session.PromptCloudSyncConsent(cfg, term.ReadConsent)
		}
		if cfg == nil || cfg.CloudSync == nil || !*cfg.CloudSync {
			return
		}
		tracker.Start(api, projectID, ag.Cfg.Model)
	}
	completeCloudSession := func() {
		cfg := ag.AppConfig
		if cfg == nil || cfg.CloudSync == nil || !*cfg.CloudSync {
			return
		}
		tracker.Complete(ag.Cfg.Context.API, ag.Usage.TotalTokens(), session.SummaryFor(ag.History), ag.History)
	}

	// Graceful interrupt handling
	var (
		sigMu       sync.Mutex
		lastSigTime time.Time
	)

	autoSave := func() {
		if len(ag.History) > 0 && (ag.AppConfig == nil || ag.AppConfig.AutoSave) {
			_ = session.SaveSession(sessionID, ag.History, ag.Cfg.Context.ProjectID, ag.Usage, ag.Cfg.Model)
		}
	}

	saveAndExit := func() {
		_ = session.SaveSession(sessionID, ag.History, ag.Cfg.Context.ProjectID, ag.Usage, ag.Cfg.Model)
		completeCloudSession()
		fmt.Fprintf(os.Stderr, "Session %s saved.\n", sessionID)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for sig := range sigCh {
			sigMu.Lock()
			now := time.Now()
			if sig == syscall.SIGINT {
				// If agent is streaming or executing tools, cancel it
				ag.CancelCurrent()

				// Double Ctrl+C within 1 second: exit
				if now.Sub(lastSigTime) < time.Second {
					sigMu.Unlock()
					saveAndExit()
					fmt.Println("Goodbye!")
					term.Close()
					os.Exit(0)
				}
				lastSigTime = now
			} else {
				// SIGTERM: exit
				sigMu.Unlock()
				saveAndExit()
				fmt.Println("Goodbye!")
				term.Close()
				os.Exit(0)
			}
			sigMu.Unlock()
		}
	}()

	// Welcome
	if !quietMode {
		term.PrintBanner(version, ag.Cfg.Context)
		fmt.Printf("  %sSession: %s%s\n", tui.ColorDim, sessionID, tui.ColorReset)

		// Hint about recent session if one exists
		if recent, err := session.ListSessions(1); err == nil && len(recent) > 0 {
			age := time.Since(recent[0].UpdatedAt)
			if age < 24*time.Hour {
				fmt.Printf("  %sRecent session: %s (%d turns, %s ago) — type /resume to continue%s\n",
					tui.ColorDim, recent[0].ID, recent[0].Turns, formatDuration(age), tui.ColorReset)
			}
		}

		// QUA-578: surface the 2026-06-15 Anthropic Agent SDK credit cutover
		// for cc-backend users. Shown at most once per local day to avoid
		// nagging. The two phrasings (pre/post cutover) reflect the actual
		// billing change documented at
		// https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan.
		if ag.Cfg.Context != nil && ag.Cfg.Context.Backend == "cc" {
			maybePrintSDKCreditBanner()
		}

		fmt.Println()
	}
	term.SetSessionPrompt(sessionID)

	var inputHistory []string
	var lastCtrlC time.Time

	// Prompt consent and open cloud session at startup (idempotent — safe to call again later).
	startCloudSession()

	for {
		var input string
		inputWasPasted := false

		// Drain the prompt queue before blocking on interactive input.
		// This processes prompts the user typed while the agent was running.
		if queued, ok := pq.Pop(); ok {
			input = queued
			remaining := pq.Len()
			fmt.Println()
			if remaining > 0 {
				term.PrintSystem(fmt.Sprintf("processing queued prompt  (%d more in queue)", remaining))
			} else {
				term.PrintSystem("processing queued prompt")
			}
			fmt.Println()
			inputHistory = append(inputHistory, input)
		} else {
			result := tui.ReadInput(term.Prompt(), inputHistory, ag.Cfg.OutputVerbose)

			if result.OutputToggle {
				toggleOutputVerbose(ag, cliAgent, term)
				continue
			}

			// Handle Ctrl+C: double-tap within 1s exits
			if result.CtrlC {
				now := time.Now()
				if now.Sub(lastCtrlC) < time.Second {
					saveAndExit()
					fmt.Fprintf(os.Stderr, "Goodbye!\n")
					return
				}
				lastCtrlC = now
				continue
			}

			input = strings.TrimSpace(result.Text)
			if input == "" {
				continue
			}
			inputWasPasted = result.Pasted
			inputHistory = append(inputHistory, input)
		}

		// Built-in commands
		switch {
		case input == "/quit" || input == "/exit" || input == "/q":
			saveAndExit()
			fmt.Fprintf(os.Stderr, "Goodbye!\n")
			return
		case input == "/help":
			printHelp()
			continue
		case input == "/clear":
			ag.ClearHistory()
			term.PrintSystem("Conversation cleared.")
			continue
		case strings.HasPrefix(input, "/project "):
			id := strings.TrimPrefix(input, "/project ")
			var pid int
			if _, err := fmt.Sscanf(id, "%d", &pid); err == nil {
				ag.Cfg.Context.ProjectID = pid
				term.PrintSystem(fmt.Sprintf("Project set to #%d", pid))
			} else {
				term.PrintError("Invalid project ID")
			}
			continue
		case input == "/context":
			printContext(ag.Cfg.Context, term)
			continue
		case input == "/connect":
			handleConnect(ag, term)
			continue
		case input == "/disconnect":
			handleDisconnect(ag, term)
			continue
		case input == "/reconnect":
			reconnectMCPTransport(cliAgent, term)
			continue
		case input == "/status":
			term.PrintStatusInfo(ag.Cfg.Context, ag.Usage, ag.Cfg.Model)
			continue
		case input == "/cost":
			term.PrintCostSummary(ag.Usage, ag.Cfg.Model)
			continue
		case input == "/resume" || strings.HasPrefix(input, "/resume "):
			resumeTarget := strings.TrimPrefix(input, "/resume ")
			resumeTarget = strings.TrimSpace(resumeTarget)
			var sess *session.Session
			var loadErr error
			if resumeTarget == "" || resumeTarget == "/resume" || resumeTarget == "last" {
				sess, loadErr = session.LoadLastSession()
			} else if !session.IsValidSessionID(resumeTarget) {
				// Block path traversal (e.g. "../etc/passwd") at the call
				// site so the user input never reaches the filesystem layer.
				loadErr = fmt.Errorf("invalid session ID %q", resumeTarget)
			} else {
				sess, loadErr = session.LoadSession(resumeTarget)
			}
			if loadErr != nil {
				term.PrintError(fmt.Sprintf("Cannot resume: %v", loadErr))
				term.PrintSystem("Use /sessions to see available sessions")
			} else {
				session.SanitizeSessionMessages(sess.Messages)
				ag.History = sess.Messages
				ag.Usage = sess.Usage
				sessionID = sess.ID
				if sess.ProjectID > 0 {
					ag.Cfg.Context.ProjectID = sess.ProjectID
				}
				term.SetSessionPrompt(sessionID)
				term.PrintSystem(fmt.Sprintf("Resumed session %s (%d turns, project #%d)",
					sess.ID, sess.Turns, sess.ProjectID))
			}
			continue
		case input == "/sessions":
			sessions, err := session.ListSessions(10)
			if err != nil || len(sessions) == 0 {
				term.PrintSystem("No saved sessions.")
				continue
			}
			items := make([]tui.SessionPickerItem, len(sessions))
			for i, s := range sessions {
				items[i] = tui.SessionPickerItem{ID: s.ID, UpdatedAt: s.UpdatedAt, Turns: s.Turns, Tokens: s.Tokens, ProjectID: s.ProjectID, Model: s.Model}
			}
			chosenID, ok := tui.ShowSessionPicker(items, sessionID)
			if !ok {
				continue
			}
			// Belt-and-suspenders: chosenID came from session.ListSessions()
			// (filenames we wrote ourselves) but validate before LoadSession
			// to make the no-traversal invariant visible to SAST.
			if !session.IsValidSessionID(chosenID) {
				term.PrintError(fmt.Sprintf("Invalid session ID %q", chosenID))
				continue
			}
			sess, loadErr := session.LoadSession(chosenID)
			if loadErr != nil {
				term.PrintError(fmt.Sprintf("Cannot resume: %v", loadErr))
			} else {
				session.SanitizeSessionMessages(sess.Messages)
				ag.History = sess.Messages
				ag.Usage = sess.Usage
				sessionID = sess.ID
				if sess.ProjectID > 0 {
					ag.Cfg.Context.ProjectID = sess.ProjectID
				}
				term.SetSessionPrompt(sessionID)
				term.PrintSystem(fmt.Sprintf("Resumed session %s (%d turns, project #%d)",
					sess.ID, sess.Turns, sess.ProjectID))
			}
			continue
		case input == "/save":
			if err := session.SaveSession(sessionID, ag.History, ag.Cfg.Context.ProjectID, ag.Usage, ag.Cfg.Model); err != nil {
				term.PrintError(fmt.Sprintf("Failed to save: %v", err))
			} else {
				term.PrintSystem(fmt.Sprintf("Session %s saved.", sessionID))
			}
			continue
		case input == "/config":
			printConfigInfo(ag.AppConfig, term)
			continue
		case input == "/skills":
			setup.PrintSkillsStatus(term)
			continue
		case input == "/skills install":
			setup.InstallSkillsBoth(term)
			continue
		case input == "/set":
			handleSetCommand(input, ag, term)
			startCloudSession()
			continue

		case input == "/queue":
			items := pq.Peek()
			if len(items) == 0 {
				term.PrintSystem("Queue is empty. Type /queue <prompt> to add, or just type while the agent is running.")
			} else {
				term.PrintSystem(fmt.Sprintf("%d prompt(s) queued:", len(items)))
				for i, it := range items {
					truncated := it
					if len(truncated) > 80 {
						truncated = truncated[:77] + "..."
					}
					fmt.Printf("  %s[%d]%s %s\n", tui.ColorDim, i+1, tui.ColorReset, truncated)
				}
			}
			continue

		case input == "/queue clear":
			n := pq.Clear()
			if n == 0 {
				term.PrintSystem("Queue was already empty.")
			} else {
				term.PrintSystem(fmt.Sprintf("Cleared %d queued prompt(s).", n))
			}
			continue

		case strings.HasPrefix(input, "/queue "):
			queued := strings.TrimSpace(strings.TrimPrefix(input, "/queue "))
			if queued != "" {
				pq.Push(queued)
				term.PrintSystem(fmt.Sprintf("queued [%d]: %s", pq.Len(), queued))
			}
			continue
		case input == "/orch":
			// Show the unified model + effort TUI picker and apply the selection instantly.
			cfg := ag.AppConfig
			if cfg == nil {
				term.PrintError("api.Config not loaded.")
				continue
			}

			// Determine the picker's "currentBackend" — include ollama as a backend value.
			currentBackend := cfg.Backend
			if ag.Mode == agent.OllamaModeFull {
				currentBackend = "ollama"
			}
			currentModelID := cfg.ModelOverride
			currentEffort := cfg.Effort
			if currentBackend == "cerebras" {
				currentModelID = api.ResolveCerebrasModel(cfg.CerebrasModel)
				currentEffort = api.NormalizeCerebrasReasoningEffort(cfg.CerebrasReasoningEffort)
				if currentEffort == "" || currentEffort == "none" {
					currentEffort = "low"
				}
			}
			result := tui.ShowModelPicker(tui.ModelPickerOpts{
				CurrentBackend: currentBackend,
				CurrentModelID: currentModelID,
				Effort:         currentEffort,
				OllamaURL:      cfg.OllamaURL,
				OllamaModel:    cfg.OllamaModel,
				CCInstalled:    agent.FindClaudeCode() != "",
				CodexInstalled: agent.FindCodex() != "",
				CerebrasKeySet: cfg.CerebrasKey != "",
			})
			if !result.Confirmed {
				continue
			}

			// ── Ollama selected ───────────────────────────────────────────────
			if result.Backend == "ollama" {
				if cfg.OllamaURL == "" || cfg.OllamaModel == "" {
					term.PrintError("Ollama not configured. Set ollama_url and ollama_model first.")
					term.PrintSystem("  qmax-code config set ollama_url https://llm2.qualitymax.io")
					term.PrintSystem("  qmax-code config set ollama_model llama3.2:3b")
					continue
				}
				// Reject malformed/unsupported URL schemes (file://, javascript:, …)
				// before they reach the HTTP client.
				if err := agent.ValidateOllamaURL(cfg.OllamaURL); err != nil {
					term.PrintError(fmt.Sprintf("Ollama URL rejected: %v", err))
					continue
				}
				if ag.Ollama == nil {
					ag.Ollama = agent.NewOllamaClient(cfg)
				}
				if cliAgent != nil {
					cliAgent.Cleanup()
					cliAgent = nil
				}
				ag.Mode = agent.OllamaModeFull
				ag.Cerebras = nil
				cfg.Backend = ""
				ag.Cfg.Context.Backend = ""
				_ = cfg.Save()
				term.PrintSystem(fmt.Sprintf("Backend: Ollama  model: %s  endpoint: %s", cfg.OllamaModel, sysutil.MaskURL(cfg.OllamaURL)))
				continue
			}

			// ── Cerebras selected ─────────────────────────────────────────────
			if result.Backend == "cerebras" {
				// Prompt for the API key now if it isn't configured yet. Optional —
				// leaving it blank cancels the switch without changing anything.
				if cfg.CerebrasKey == "" {
					term.PrintSystem("Cerebras needs an API key (get one at https://cloud.cerebras.ai).")
					key := setup.ReadSecret("  Paste your Cerebras key (blank to cancel): ")
					if key == "" {
						term.PrintSystem("No key entered — backend not changed.")
						continue
					}
					looks, verr := api.ValidateCerebrasKey(key)
					if verr != nil {
						term.PrintError(fmt.Sprintf("That doesn't look like an API key (%v). Backend not changed.", verr))
						continue
					}
					if !looks {
						term.PrintSystem("  Note: key doesn't start with \"csk-\" — saving anyway.")
					}
					if err := api.SaveCerebrasKey(key); err != nil {
						term.PrintError(fmt.Sprintf("Could not save key: %v", err))
						continue
					}
					cfg.CerebrasKey = key
					term.PrintSystem("  Saved to OS keychain.")
				}
				// Apply the chosen Cerebras model.
				if result.ModelID != "" {
					cfg.CerebrasModel = api.ResolveCerebrasModel(result.ModelID)
				}
				if api.IsCerebrasGemma4Model(cfg.CerebrasModel) {
					cfg.CerebrasReasoningEffort = api.NormalizeCerebrasReasoningEffort(result.Effort)
				}
				// Tear down any active CLI agent / Ollama mode.
				if cliAgent != nil {
					cliAgent.Cleanup()
					cliAgent = nil
				}
				ag.Mode = agent.OllamaModeOff
				ag.Cerebras = agent.NewCerebrasClient(cfg)
				cfg.Backend = "cerebras"
				ag.Cfg.Context.Backend = "cerebras"
				_ = cfg.Save()
				if api.IsCerebrasGemma4Model(cfg.CerebrasModel) {
					term.PrintSystem(fmt.Sprintf("Backend: Cerebras  model: %s  reasoning: %s", cfg.CerebrasModel, cfg.CerebrasReasoningEffort))
				} else {
					term.PrintSystem(fmt.Sprintf("Backend: Cerebras  model: %s", cfg.CerebrasModel))
				}
				continue
			}

			// ── Validate the chosen CLI is actually installed ─────────────────
			switch result.Backend {
			case "cc":
				if agent.FindClaudeCode() == "" {
					term.PrintError("Claude Code ('claude') not found. Install it first.")
					term.PrintSystem("  https://claude.ai/download")
					continue
				}
			case "codex":
				if agent.FindCodex() == "" {
					term.PrintError("Codex CLI ('codex') not found.")
					term.PrintSystem("  npm install -g @openai/codex")
					continue
				}
			}

			// Consent gate: required before activating CC/Codex (autonomous shell + edits).
			if result.Backend != "" {
				consent := setup.PromptOrchConsent(cfg, result.Backend)
				if !consent.Proceed {
					term.PrintSystem("Backend not changed.")
					continue
				}
				cfg.OrchPermissionMode = consent.PermissionMode
				cfg.OrchGlobalInstall = consent.GlobalInstall
				if consent.GlobalInstall {
					if !setup.IsOrchInstalled(result.Backend) {
						setup.RunOrch(result.Backend, term)
					}
					setup.InstallSkillsReport(result.Backend, term)
				}
			}

			// Tear down current CLI agent and disable Ollama/Cerebras if switching away.
			if cliAgent != nil {
				cliAgent.Cleanup()
				cliAgent = nil
			}
			deactivateEmbeddedBackends(ag)

			// Spin up the new agent with selected model + effort.
			switch result.Backend {
			case "cc":
				cliAgent = agent.NewCCAgent(agent.FindClaudeCode(), result.ModelID, result.Effort, cfg.OrchPermissionMode, cfg.OutputVerbose, ag.Cfg.Context)
				term.PrintSystem(fmt.Sprintf("Backend: Claude Code  model: %s  effort: %s", result.ModelID, result.Effort))
			case "codex":
				ca := agent.NewCodexAgent(agent.FindCodex(), result.ModelID, result.Effort, cfg.OrchPermissionMode, cfg.OutputVerbose, ag.Cfg.Context)
				if err := ca.WriteMCPConfig(); err != nil {
					term.PrintSystem(fmt.Sprintf("Warning: Codex MCP config: %v", err))
				}
				cliAgent = ca
				term.PrintSystem(fmt.Sprintf("Backend: Codex  model: %s  effort: %s", result.ModelID, result.Effort))
			default:
				term.PrintSystem(fmt.Sprintf("Backend: Anthropic API  model: %s", result.ModelID))
			}

			cfg.Backend = result.Backend
			cfg.ModelOverride = result.ModelID
			cfg.Effort = result.Effort
			ag.Cfg.Context.Backend = result.Backend
			_ = cfg.Save()
			continue

		case input == "/theme":
			cfg := ag.AppConfig
			if cfg == nil {
				term.PrintError("api.Config not loaded.")
				continue
			}
			chosen, ok := tui.ShowThemePicker(cfg.Theme)
			if !ok {
				continue
			}
			if err := tui.SaveTheme(cfg, chosen); err != nil {
				term.PrintError(fmt.Sprintf("Failed to save config: %v", err))
			} else {
				tui.ApplyTheme(tui.ThemeByName(chosen))
				term.PrintSystem(fmt.Sprintf("Theme set to: %s", chosen))
			}
			continue

		case input == "/cloudsync":
			cfg := ag.AppConfig
			if cfg == nil {
				term.PrintError("api.Config not loaded.")
				continue
			}
			enabled, ok := tui.ShowCloudSyncPicker(cfg.CloudSync)
			if !ok {
				continue
			}
			v := enabled
			cfg.CloudSync = &v
			if err := cfg.Save(); err != nil {
				term.PrintError(fmt.Sprintf("Failed to save config: %v", err))
				continue
			}
			if enabled {
				term.PrintSystem("Cloud session sync enabled.")
				// Open the cloud session immediately so the rest of this run is tracked.
				startCloudSession()
			} else {
				term.PrintSystem("Cloud session sync disabled.")
			}
			continue

		case input == "/cc", input == "/codex", input == "/api":
			// Instant backend switching — no restart needed.
			cfg := ag.AppConfig
			if cfg == nil {
				term.PrintError("api.Config not loaded.")
				continue
			}

			var wantBackend string
			switch input {
			case "/cc":
				wantBackend = "cc"
			case "/codex":
				wantBackend = "codex"
			case "/api":
				wantBackend = ""
			}

			// If same backend requested, turn it off (toggle behaviour).
			if cfg.Backend == wantBackend && wantBackend != "" {
				wantBackend = ""
			}

			// Tear down current CLI agent.
			if cliAgent != nil {
				cliAgent.Cleanup()
				cliAgent = nil
			}
			deactivateEmbeddedBackends(ag)

			switch wantBackend {
			case "cc":
				bin := agent.FindClaudeCode()
				if bin == "" {
					term.PrintError("'claude' CLI not found. Install Claude Code first.")
					term.PrintSystem("  https://claude.ai/download")
					continue
				}
				consent := setup.PromptOrchConsent(cfg, "cc")
				if !consent.Proceed {
					term.PrintSystem("Backend not changed.")
					continue
				}
				cfg.OrchPermissionMode = consent.PermissionMode
				cfg.OrchGlobalInstall = consent.GlobalInstall
				if consent.GlobalInstall {
					if !setup.IsOrchInstalled("cc") {
						setup.RunOrch("cc", term)
					}
					setup.InstallSkillsReport("cc", term)
				}
				cliAgent = agent.NewCCAgent(bin, cfg.ModelOverride, cfg.Effort, cfg.OrchPermissionMode, cfg.OutputVerbose, ag.Cfg.Context)
				cfg.Backend = "cc"
				_ = cfg.Save()
				term.PrintSystem(fmt.Sprintf("Backend → Claude Code (%s) · %s mode", bin, cfg.OrchPermissionMode))

			case "codex":
				bin := agent.FindCodex()
				if bin == "" {
					term.PrintError("'codex' CLI not found.")
					term.PrintSystem("  npm install -g @openai/codex")
					continue
				}
				consent := setup.PromptOrchConsent(cfg, "codex")
				if !consent.Proceed {
					term.PrintSystem("Backend not changed.")
					continue
				}
				cfg.OrchPermissionMode = consent.PermissionMode
				cfg.OrchGlobalInstall = consent.GlobalInstall
				if consent.GlobalInstall {
					if !setup.IsOrchInstalled("codex") {
						setup.RunOrch("codex", term)
					}
					setup.InstallSkillsReport("codex", term)
				}
				ca := agent.NewCodexAgent(bin, cfg.ModelOverride, cfg.Effort, cfg.OrchPermissionMode, cfg.OutputVerbose, ag.Cfg.Context)
				if err := ca.WriteMCPConfig(); err != nil {
					term.PrintSystem(fmt.Sprintf("Warning: MCP config: %v", err))
				}
				cliAgent = ca
				cfg.Backend = "codex"
				_ = cfg.Save()
				term.PrintSystem(fmt.Sprintf("Backend → Codex (%s) · %s mode", bin, cfg.OrchPermissionMode))

			default:
				cfg.Backend = ""
				_ = cfg.Save()
				term.PrintSystem("Backend → Anthropic API (direct)")
			}
			ag.Cfg.Context.Backend = cfg.Backend
			continue

		case input == "/gemma" || strings.HasPrefix(input, "/gemma "):
			// Activate Gemma 4 31B on Cerebras in one shot — the Cerebras +
			// Gemma 4 hackathon entrypoint. Selects the multimodal model,
			// sets reasoning_effort, and flips the live backend. Optional
			// argument: none|low|medium|high (default low). "/gemma off"
			// returns to the direct Anthropic API backend.
			cfg := ag.AppConfig
			if cfg == nil {
				term.PrintError("api.Config not loaded.")
				continue
			}
			arg := strings.TrimSpace(strings.TrimPrefix(input, "/gemma"))

			if strings.EqualFold(arg, "off") || strings.EqualFold(arg, "api") {
				deactivateEmbeddedBackends(ag)
				if cliAgent != nil {
					cliAgent.Cleanup()
					cliAgent = nil
				}
				cfg.Backend = ""
				ag.Cfg.Context.Backend = ""
				_ = cfg.Save()
				term.PrintSystem("Gemma disabled → Anthropic API (direct)")
				continue
			}

			effort := "low" // default: thinking on, but fast
			if arg != "" {
				if !api.ValidCerebrasReasoningEffort(arg) {
					term.PrintError(fmt.Sprintf("Usage: /gemma [none|low|medium|high|off]; %q is invalid.", arg))
					continue
				}
				effort = api.NormalizeCerebrasReasoningEffort(arg)
			}

			if cfg.CerebrasKey == "" {
				term.PrintSystem("Cerebras needs an API key (get one at https://cloud.cerebras.ai).")
				key := setup.ReadSecret("  Paste your Cerebras key (blank to cancel): ")
				if key == "" {
					term.PrintSystem("No key entered — Gemma not activated.")
					continue
				}
				looks, verr := api.ValidateCerebrasKey(key)
				if verr != nil {
					term.PrintError(fmt.Sprintf("That doesn't look like an API key (%v). Gemma not activated.", verr))
					continue
				}
				if !looks {
					term.PrintSystem("  Note: key doesn't start with \"csk-\" — saving anyway.")
				}
				if err := api.SaveCerebrasKey(key); err != nil {
					term.PrintError(fmt.Sprintf("Could not save key: %v", err))
					continue
				}
				cfg.CerebrasKey = key
				term.PrintSystem("  Saved to OS keychain.")
			}

			cfg.CerebrasModel = api.CerebrasGemma4Model
			cfg.CerebrasReasoningEffort = effort
			if cliAgent != nil {
				cliAgent.Cleanup()
				cliAgent = nil
			}
			ag.Mode = agent.OllamaModeOff
			ag.Cerebras = agent.NewCerebrasClient(cfg)
			cfg.Backend = "cerebras"
			ag.Cfg.Context.Backend = "cerebras"
			_ = cfg.Save()

			reasoningLabel := "reasoning off"
			if effort == "low" || effort == "medium" || effort == "high" {
				reasoningLabel = "reasoning: " + effort
			}
			term.PrintSystem(fmt.Sprintf("Backend: Cerebras · model: %s · %s", cfg.CerebrasModel, reasoningLabel))
			term.PrintSystem("Multimodal: /screenshot or /paste a page → Gemma 4 reads it and generates a Playwright test.")
			continue

		case input == "/ollama":
			// Cycle through modes: off → chat → full → off
			cfg := ag.AppConfig
			if cfg == nil || cfg.OllamaURL == "" {
				term.PrintError("Ollama not configured. Set it first:")
				term.PrintSystem("  qmax-code config set ollama_url https://user:pass@llm.example.com")
				term.PrintSystem("  qmax-code config set ollama_model gemma3:4b-it-q4_K_M")
				continue
			}
			if err := agent.ValidateOllamaURL(cfg.OllamaURL); err != nil {
				term.PrintError(fmt.Sprintf("Ollama URL rejected: %v", err))
				continue
			}
			if ag.Ollama == nil {
				ag.Ollama = agent.NewOllamaClient(cfg)
			}
			if cliAgent != nil {
				cliAgent.Cleanup()
				cliAgent = nil
			}
			ag.Cerebras = nil
			switch ag.Mode {
			case agent.OllamaModeOff:
				ag.Mode = agent.OllamaModeChat
				term.PrintSystem(fmt.Sprintf("Ollama: CHAT mode (%s) — chat via local model, tools via Claude", ag.Ollama.Model))
			case agent.OllamaModeChat:
				ag.Mode = agent.OllamaModeFull
				term.PrintSystem(fmt.Sprintf("Ollama: FULL mode (%s) — everything via local model (no Claude)", ag.Ollama.AgentModel))
			case agent.OllamaModeFull:
				ag.Mode = agent.OllamaModeOff
				term.PrintSystem("Ollama: OFF — all calls via Claude")
			}
			continue
		case strings.HasPrefix(input, "/set "):
			handleSetCommand(input, ag, term)
			startCloudSession()
			continue
		case input == "/keys":
			handleKeys(ag, term)
			continue
		case input == "/browserfeed" || strings.HasPrefix(input, "/browserfeed "):
			arg := strings.TrimSpace(strings.TrimPrefix(input, "/browserfeed"))
			mode := blockModeQuarter
			if strings.HasPrefix(arg, "--half ") || arg == "--half" {
				mode = blockModeHalf
				arg = strings.TrimSpace(strings.TrimPrefix(arg, "--half"))
			}
			if arg == "" {
				term.PrintSystem("Usage: /browserfeed [--half] <noVNC URL>")
				term.PrintSystem("  e.g. /browserfeed https://<host>/vnc.html?... (from a QM Cloud Sandbox run)")
				term.PrintSystem("  --half : use 2-pixel half-blocks (more portable, lower res)")
				continue
			}
			if err := showBrowserFeed(arg, mode); err != nil {
				term.PrintError(fmt.Sprintf("browserfeed: %v", err))
			}
			continue
		case input == "/live" || strings.HasPrefix(input, "/live "):
			arg := strings.TrimSpace(strings.TrimPrefix(input, "/live"))
			cfg := ag.AppConfig
			if cfg == nil {
				term.PrintError("api.Config not loaded.")
				continue
			}
			applyLiveFeed := func(next bool) {
				cfg.LiveFeed = next
				ag.Cfg.Context.LiveFeed = next
				if err := cfg.Save(); err != nil {
					term.PrintError(fmt.Sprintf("Failed to save config: %v", err))
					return
				}
				if next {
					term.PrintSystem("Live feed enabled. Test runs and AI crawls will execute in QM Cloud Sandbox; the feed auto-opens at the end of each agent turn.")
				} else {
					term.PrintSystem("Live feed disabled. Future test runs and crawls will use the standard pooled runner.")
				}
			}
			switch strings.ToLower(arg) {
			case "":
				// No argument → open the TUI picker. Matches the /cloudsync
				// pattern so toggles feel consistent across the app.
				next, ok := tui.ShowLiveFeedPicker(cfg.LiveFeed)
				if !ok {
					continue // user cancelled; leave current value alone
				}
				if next == cfg.LiveFeed {
					state := "off"
					if cfg.LiveFeed {
						state = "on"
					}
					term.PrintSystem(fmt.Sprintf("Live feed unchanged (%s).", state))
					continue
				}
				applyLiveFeed(next)
			case "on", "true", "1", "yes":
				applyLiveFeed(true)
			case "off", "false", "0", "no":
				applyLiveFeed(false)
			case "status", "show":
				state := "off"
				if cfg.LiveFeed {
					state = "on"
				}
				term.PrintSystem(fmt.Sprintf("Live feed is %s.", state))
			default:
				term.PrintError("Usage: /live (interactive) | /live on | /live off")
			}
			continue
		case input == "/feed":
			url := ag.Cfg.Context.LastLiveURL
			if url == "" {
				term.PrintSystem("No live feed URL captured yet.")
				if !ag.Cfg.Context.LiveFeed {
					term.PrintSystem("  Enable with: /live on  (then run a test or crawl)")
				} else {
					term.PrintSystem("  Run a test or crawl, then try /feed again.")
				}
				continue
			}
			if err := showBrowserFeed(url, blockModeQuarter); err != nil {
				term.PrintError(fmt.Sprintf("browserfeed: %v", err))
			}
			continue
		case input == "/screenshot":
			img, err := tui.CaptureScreenshot()
			if err != nil {
				term.PrintError(err.Error())
				continue
			}
			term.PrintSystem(fmt.Sprintf("Screenshot captured (%s)", img.FileName))
			llmResult, err := ag.RunStreamingWithImages("Analyze this screenshot.", []tui.ImageAttachment{*img}, term)
			if err != nil {
				term.PrintError(err.Error())
			}
			if llmResult != "" {
				fmt.Println()
			}
			autoSave()
			continue
		case input == "/paste":
			// Try image first, then text
			img, imgErr := tui.PasteImageFromClipboard()
			if imgErr == nil {
				term.PrintSystem(fmt.Sprintf("Pasted image from clipboard (%s)", img.FileName))
				llmResult, err := ag.RunStreamingWithImages("Analyze this pasted image.", []tui.ImageAttachment{*img}, term)
				if err != nil {
					term.PrintError(err.Error())
				}
				if llmResult != "" {
					fmt.Println()
				}
				autoSave()
				continue
			}
			// Fall back to text paste
			text, textErr := tui.PasteTextFromClipboard()
			if textErr != nil || text == "" {
				term.PrintError("Nothing in clipboard")
				continue
			}
			term.PrintSystem(fmt.Sprintf("Pasted %d chars from clipboard", len(text)))
			input = text // fall through to normal processing
			inputWasPasted = true
		}

		if tui.IsLargePastedText(input, inputWasPasted) {
			path, err := tui.SavePastedTextFile(input)
			if err != nil {
				term.PrintError(fmt.Sprintf("Could not save pasted_file: %v", err))
				continue
			}
			term.PrintSystem(fmt.Sprintf("Large paste saved as pasted_file: %s (%d bytes)", path, len(input)))
			input = tui.PastedFilePrompt(path, len(input))
			if len(inputHistory) > 0 {
				inputHistory[len(inputHistory)-1] = input
			}
		}

		// Detect image file paths dragged/pasted into input
		cleanInput, images := tui.DetectAndLoadImages(input)

		// Ensure a cloud session exists for this conversation (no-op after first call).
		startCloudSession()

		// Reset turn-scoped diagnostic flags. captureLiveURL uses these so
		// we log "live URL captured" / "fell back to pooled runner" once
		// per turn rather than once per poll.
		if c := ag.Cfg.Context; c != nil {
			c.LiveURLLogged = false
			c.SandboxModeLogged = false
			c.SandboxFallbackSeen = false
		}

		// Run through the LLM agent with streaming.
		// Start the queue reader so the user can type the next prompt while
		// the agent is working.  It is stopped (and fully drained) before
		// the next tui.ReadInput call so stdin is never shared between readers.
		// Pressing Enter while typing cancels the running agent so the queued
		// prompt is processed on the very next iteration.
		var cancelCurrent func()
		if cliAgent != nil {
			cancelCurrent = cliAgent.Cancel
		} else {
			cancelCurrent = ag.CancelCurrent
		}
		stopQueueReader := session.StartQueueReader(pq, term, cancelCurrent)

		// CC mode: delegate entirely to Claude Code subprocess. This does not
		// require a QM Anthropic API key, but `claude --print` usage draws from
		// the user's Agent SDK credit starting 2026-06-15.
		// Normal mode: use direct Anthropic API.
		var llmResult string
		var err error

		// In CC/Codex mode, start a pre-connect goroutine that watches for a
		// live_browser_url in the side-channel file every second. When one
		// appears (mid-turn, while the test is still running), we immediately
		// dial VNC and hold the WebSocket open. An active connection prevents
		// the E2B sandbox from tearing down websockify, so the feed is still
		// available after the agent turn ends even without server-side keepalive.
		type preConnResult struct {
			url    string
			stream *vnc.VNCStream // nil on dial failure
		}
		preConnChan := make(chan preConnResult, 1)
		// watchCtx is only used to stop the polling ticker (abort a pending
		// drainLiveURLFromChild loop). It is intentionally NOT passed to
		// DialVNC — the stream needs a context that outlives the goroutine.
		watchCtx, watchCancel := context.WithCancel(context.Background())
		defer watchCancel() // ensures cancel on any return/continue path
		if cliAgent != nil {
			go func() {
				// Do NOT defer watchCancel here — it is called by the main
				// goroutine after the agent turn ends. If we cancelled it here
				// on exit, DialVNC (which uses context.Background()) is fine,
				// but stopping the ticker via Done() would still work.
				ticker := time.NewTicker(1 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-watchCtx.Done():
						return
					case <-ticker.C:
						url := sysutil.DrainLiveURLFromChild()
						if url == "" {
							continue
						}
						// Use context.Background() so the stream's lifetime is
						// NOT tied to watchCtx. Cancelling watchCtx (at end of
						// turn) must not kill an already-established connection.
						stream, dialErr := vnc.DialVNC(context.Background(), url, 10)
						res := preConnResult{url: url}
						if dialErr == nil {
							res.stream = stream
						}
						select {
						case preConnChan <- res:
						default:
							if dialErr == nil {
								stream.Close()
							}
						}
						return
					}
				}
			}()
		} else {
			watchCancel()
		}

		if cliAgent != nil {
			if len(images) > 0 {
				term.PrintSystem("Note: image attachments are not supported in CLI backend mode.")
			}
			term.StartThinking()
			llmResult, err = cliAgent.Run(cleanInput, term)
			term.StopThinking()
			if err == nil {
				// Mirror the turn into ag.History so autoSave records it.
				// agent.CCAgent/agent.CodexAgent manage their own subprocess state; qmax's
				// history would otherwise stay empty and autoSave would no-op.
				ag.History = append(ag.History,
					api.Message{Role: "user", Content: cleanInput},
					api.Message{Role: "assistant", Content: llmResult},
				)
			}
		} else if len(images) > 0 {
			names := make([]string, len(images))
			for i, img := range images {
				names[i] = img.FileName
			}
			term.PrintSystem(fmt.Sprintf("Attached %d image(s): %s", len(images), strings.Join(names, ", ")))
			if cleanInput == "" {
				cleanInput = "Analyze these images."
			}
			llmResult, err = ag.RunStreamingWithImages(cleanInput, images, term)
		} else {
			llmResult, err = ag.RunStreaming(input, term)
		}

		// Stop queue reader and wait for the goroutine to exit before we
		// touch stdin again (either via the queue loop or tui.ReadInput).
		// If the user was mid-typing when the agent finished — i.e. typed a
		// partial line but didn't press Enter before the response came back —
		// the queue reader returns that text. Push it onto the queue so it
		// isn't silently lost (QUA-577). The user can /queue clear if it was
		// stray input.
		partial := strings.TrimSpace(stopQueueReader())
		if partial != "" {
			pq.Push(partial)
			fmt.Println()
			term.PrintSystem(fmt.Sprintf("↩ partial input recovered → queued [%d]: %s  (type /queue clear to discard)", pq.Len(), partial))
		}

		// If prompts were queued during this run, surface them.
		if n := pq.Len(); n > 0 {
			fmt.Println()
			term.PrintSystem(fmt.Sprintf("%d prompt(s) queued — processing next automatically", n))
		}
		if err != nil {
			term.PrintError(err.Error())
			// Telemetry policy: never capture prompt content, file content, or model
			// output. Only structural metadata that helps diagnose without revealing
			// what the user was working on.
			backendTag := "api"
			if cliAgent != nil {
				backendTag = ag.Cfg.Context.Backend
			}
			sysutil.CaptureError(err, map[string]interface{}{
				"backend":     backendTag,
				"input_len":   fmt.Sprintf("%d", len(input)),
				"image_count": fmt.Sprintf("%d", len(images)),
			})
			autoSave() // save even on error — preserves context
			continue
		}

		if llmResult != "" {
			fmt.Println()
		}

		// Auto-save after every exchange for crash safety
		autoSave()

		// Stop the pre-connect watcher (no-op if it already returned) and
		// collect any stream it established mid-turn.
		watchCancel()
		var preConn preConnResult
		select {
		case preConn = <-preConnChan:
		default:
		}

		// In CC/Codex mode, captureLiveURL ran inside a child `qmax-code
		// serve --mcp` subprocess; the URL it stored sits on that
		// process's sctx, not ours. Prefer the URL the pre-connect goroutine
		// already drained; fall back to a final drain for non-cliAgent runs.
		if cliAgent != nil {
			if preConn.url != "" {
				ag.Cfg.Context.LastLiveURL = preConn.url
				ag.Cfg.Context.SandboxModeLogged = true
			} else if url := sysutil.DrainLiveURLFromChild(); url != "" {
				ag.Cfg.Context.LastLiveURL = url
				ag.Cfg.Context.SandboxModeLogged = true
			}
		}

		// Check whether run_test returned early and wrote an execution_id for
		// us to poll directly (fast-return path, avoids 60–90s LLM block).
		pendingExecID := sysutil.DrainExecIDFromChild()

		// End-of-turn live-feed auto-launch. If a tool call surfaced a
		// live_browser_url during this turn (and the user opted in via
		// /live on), open the feed now — the agent has finished talking
		// so taking over the alt screen is safe. /feed remains as a
		// manual replay if the user dismisses it.
		maybeLaunchLiveFeed(ag.Cfg.Context, term, preConn.stream, pendingExecID)
	}
}

// handleConnect runs the browser-based auth flow from within the REPL.
func handleConnect(ag *agent.Agent, term *tui.Terminal) {
	ctx := ag.Cfg.Context

	// Already connected?
	if ctx.Auth != nil && ctx.Auth.IsAuthenticated() {
		term.PrintSystem(fmt.Sprintf("Already connected as %s", ctx.Auth.Email))
		term.PrintSystem("Run /disconnect first to switch accounts.")
		return
	}

	tui.AnimateMax(tui.MoodWaving, "Let's connect you to QualityMax!")
	fmt.Println()

	auth, err := setup.LoginViaBrowser()
	if err != nil {
		tui.AnimateMax(tui.MoodSad, "Connection failed: "+err.Error())
		fmt.Println()
		term.PrintSystem("You can also paste an API key:")
		term.PrintSystem("  /set apikey qm-YOUR-API-KEY")
		return
	}

	// Update session context in-place
	ctx.Auth = auth
	ctx.API = api.NewAPIClient(auth)

	tui.AnimateMax(tui.MoodHappy, fmt.Sprintf("Connected as %s", auth.Email))
	fmt.Println()
}

// handleDisconnect removes auth and API client from the session.
func handleDisconnect(ag *agent.Agent, term *tui.Terminal) {
	ctx := ag.Cfg.Context

	if ctx.Auth == nil || !ctx.Auth.IsAuthenticated() {
		term.PrintSystem("Not connected.")
		return
	}

	email := ctx.Auth.Email
	ctx.Auth = nil
	ctx.API = nil

	// Remove saved auth files (both new and legacy)
	home, _ := os.UserHomeDir()
	if home != "" {
		_ = os.Remove(filepath.Join(home, api.QmaxCodeConfigDir, "auth.json"))
		// Also clear legacy ~/.qamax/config.json token to prevent auto-reconnect
		legacyPath := filepath.Join(home, ".qamax", "config.json")
		if data, err := os.ReadFile(legacyPath); err == nil {
			var legacy map[string]interface{}
			if json.Unmarshal(data, &legacy) == nil {
				legacy["token"] = ""
				legacy["api_key"] = ""
				if updated, err := json.MarshalIndent(legacy, "", "  "); err == nil {
					_ = os.WriteFile(legacyPath, updated, 0600)
				}
			}
		}
	}

	tui.AnimateMax(tui.MoodNeutral, fmt.Sprintf("Disconnected from %s", email))
	fmt.Println()
	term.PrintSystem("Run /connect to log in again.")
}

func toggleOutputVerbose(ag *agent.Agent, cliAgent agent.CLIAgent, term *tui.Terminal) {
	if ag == nil {
		return
	}
	cfg := ag.AppConfig
	if cfg == nil {
		cfg = api.DefaultConfig()
		ag.AppConfig = cfg
	}
	cfg.OutputVerbose = !cfg.OutputVerbose
	ag.Cfg.OutputVerbose = cfg.OutputVerbose
	if cliAgent != nil {
		cliAgent.SetOutputVerbose(cfg.OutputVerbose)
	}

	mode := "compact"
	if cfg.OutputVerbose {
		mode = "verbose"
	}
	if err := cfg.Save(); err != nil {
		term.PrintError(fmt.Sprintf("Output mode changed to %s but could not save config: %v", mode, err))
		return
	}
	term.PrintSystem(fmt.Sprintf("Output mode: %s (Ctrl+O toggles)", mode))
}

// handleKeys provides an interactive TUI for managing API keys.
func handleKeys(ag *agent.Agent, term *tui.Terminal) {
	fmt.Println()

	// Show current key status
	anthropicKey := ag.Cfg.AnthropicKey
	if anthropicKey == "" {
		anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	qmaxConnected := ag.Cfg.Context.Auth != nil && ag.Cfg.Context.Auth.IsAuthenticated()

	fmt.Printf("  %s API Keys %s\n\n", "\033[1m", "\033[0m")

	if anthropicKey != "" {
		masked := anthropicKey[:7] + "..." + anthropicKey[len(anthropicKey)-4:]
		fmt.Printf("  Anthropic:   %s● Set%s (%s)\n", "\033[32m", "\033[0m", masked)
	} else {
		fmt.Printf("  Anthropic:   %s● Not set%s\n", "\033[33m", "\033[0m")
	}

	if qmaxConnected {
		fmt.Printf("  QualityMax:  %s● Connected%s (%s)\n", "\033[32m", "\033[0m", ag.Cfg.Context.Auth.Email)
	} else {
		fmt.Printf("  QualityMax:  %s● Not connected%s\n", "\033[33m", "\033[0m")
	}
	fmt.Println()

	choice := setup.PromptChoice("  What would you like to do?", []string{
		"Set Anthropic API key",
		"Connect to QualityMax (browser)",
		"Disconnect from QualityMax",
		"Cancel",
	})

	switch choice {
	case 0: // Anthropic key
		fmt.Println()
		fmt.Println("  Get your key at: https://console.anthropic.com/settings/keys")
		fmt.Println()
		key := setup.ReadSecret("  Paste your Anthropic key: ")
		if key == "" {
			term.PrintSystem("Cancelled.")
			return
		}
		os.Setenv("ANTHROPIC_API_KEY", key)
		ag.Cfg.AnthropicKey = key
		if err := api.SaveAnthropicKey(key); err != nil {
			term.PrintSystem(fmt.Sprintf("Key set for this session (keychain unavailable: %s)", err))
		} else {
			tui.AnimateMax(tui.MoodHappy, "Key saved to OS keychain!")
			fmt.Println()
		}
	case 1: // QualityMax connect
		handleConnect(ag, term)
	case 2: // Disconnect
		handleDisconnect(ag, term)
	case 3: // Cancel
		return
	}
}

func printHelp() {
	fmt.Println(`
Commands:
  /connect       Log in to QualityMax (opens browser)
  /disconnect    Log out and clear saved credentials
  /reconnect     Restore the active CC/Codex MCP transport
  /status        Connection status + session info
  /project <id>  Set the active project
  /context       Show current session context
  /cost          Show session token usage and estimated cost
  /orch          Cycle orchestration backend: off → CC → Codex → off
  /theme         Live-preview color scheme picker
  /cloudsync     Toggle cloud session sync (enabled/disabled)
   /cc            Switch to Claude Code backend (no QM API key; Agent SDK credit)
   /codex         Switch to Codex CLI backend (OpenAI subscription, no API tokens)
   /api           Switch back to direct Anthropic API
   /gemma [none|low|medium|high|off]
                  Activate Gemma 4 31B on Cerebras (multimodal + reasoning)
   /ollama        Toggle Ollama on/off (self-hosted LLM for chat)
  /set output_verbose true|false
                 Toggle compact vs detailed Codex/CC answers
  /config        Show current config settings
  /keys          Set API keys (interactive menu)
  /screenshot    Capture a screenshot and analyze it
  /paste         Paste from clipboard (image or text)
  /set <k> <v>   Update config (model, project, professional, autosave, cloud_sync, budget, ollama)
  /save          Save current session
  /sessions      List recent sessions
  /resume [id]   Resume a session (default: last)
  /clear         Clear conversation history
  /help          Show this help
  /quit          Exit

api.Config examples:
  /set model sonnet         Change default model
  /set project 42           Change default project
  /set professional true    Disable cat personality
  /set autosave false       Disable auto-save on exit
  /set budget 100000        Set max token budget warning
  /set cloud_sync true       Enable cloud session sync
  /set cloud_sync false      Disable cloud session sync
  /set ollama on            Enable self-hosted LLM for chat (saves API costs)
  /set ollama off           Disable Ollama, use Claude for all calls
  /set backend cc           Use Claude Code login (Agent SDK credit for --print)
  /set backend codex        Use OpenAI Codex subscription (no API key needed)
  /set backend api          Use Anthropic API directly (default)
  /set theme ocean          Switch color theme (historic, ocean, neon, ember, aurora · paper, sky, sparkling, radiance, goldenhour)

Queue:
  /queue                    Show pending queue
  /queue <prompt>           Add a prompt to the queue immediately
  /queue clear              Discard all queued prompts
  (type while agent runs)   Prompts entered during processing are auto-queued

Shortcuts:
  Ctrl+C         Cancel current operation (double-tap to exit)
  Ctrl+O         Toggle compact/verbose agent output
  Ctrl+X x3      Clear the whole input line
  Ctrl+←/→       Move by word

Models (--model flag):
  auto            Smart routing: haiku for chat, sonnet for tools (default)
  sonnet          Claude Sonnet (all requests)
  opus            Claude Opus (most capable, all requests)
  haiku           Claude Haiku (cheapest, all requests)

Flags:
  --professional  Disable cat personality for this session

Examples:
  "test the login flow"
  "what's our test coverage?"
  "crawl staging.myapp.com and generate tests"
  "run all tests and show failures"
  "import https://github.com/user/repo and review it"
  "create a PR with the generated tests"`)
}

func reconnectMCPTransport(cliAgent agent.CLIAgent, term *tui.Terminal) {
	switch a := cliAgent.(type) {
	case *agent.CCAgent:
		if err := a.WriteMCPConfig(); err != nil {
			term.PrintError(fmt.Sprintf("Could not restore Claude Code MCP transport: %v", err))
			return
		}
		term.PrintSystem("QMax MCP transport restored for Claude Code.")
	case *agent.CodexAgent:
		if err := a.WriteMCPConfig(); err != nil {
			term.PrintError(fmt.Sprintf("Could not restore Codex MCP transport: %v", err))
			return
		}
		term.PrintSystem("QMax MCP transport restored for Codex.")
	default:
		term.PrintSystem("No CC/Codex MCP transport is active. Use /cc or /codex first.")
	}
}

func printConfigInfo(cfg *api.Config, term *tui.Terminal) {
	if cfg == nil {
		term.PrintSystem("No config loaded (using defaults).")
		return
	}
	fmt.Println()
	fmt.Printf("  %s\n", "qmax-code api.Config (~/.qmax-code/config.json)")
	fmt.Printf("  %-20s %s\n", "Default model:", cfg.DefaultModel)
	fmt.Printf("  %-20s %d\n", "Default project:", cfg.DefaultProject)
	fmt.Printf("  %-20s %v\n", "Professional:", cfg.Professional)
	fmt.Printf("  %-20s %v\n", "Auto-save:", cfg.AutoSave)
	outputMode := "compact"
	if cfg.OutputVerbose {
		outputMode = "verbose"
	}
	fmt.Printf("  %-20s %s\n", "Output mode:", outputMode)
	cloudSyncVal := "not set (will prompt)"
	if cfg.CloudSync != nil {
		if *cfg.CloudSync {
			cloudSyncVal = "enabled"
		} else {
			cloudSyncVal = "disabled"
		}
	}
	fmt.Printf("  %-20s %s\n", "Cloud sync:", cloudSyncVal)
	liveFeedVal := "off"
	if cfg.LiveFeed {
		liveFeedVal = "on (test/crawl runs in QM Cloud Sandbox with live feed)"
	}
	fmt.Printf("  %-20s %s\n", "Live feed:", liveFeedVal)
	fmt.Printf("  %-20s %d\n", "Token budget:", cfg.MaxTokenBudget)
	if cfg.OllamaURL != "" {
		fmt.Printf("  %-20s %s\n", "Ollama URL:", sysutil.MaskURL(cfg.OllamaURL))
		fmt.Printf("  %-20s %s\n", "Ollama model:", cfg.OllamaModel)
	} else {
		fmt.Printf("  %-20s %s\n", "Ollama:", "(not configured)")
	}
	fmt.Println()
}

func handleSetCommand(input string, ag *agent.Agent, term *tui.Terminal) {
	parts := strings.Fields(input)
	if len(parts) < 3 {
		term.PrintError("Usage: /set <key> <value>")
		term.PrintSystem("Keys: model, project, professional, autosave, cloud_sync, live_feed, output_verbose, budget, apikey, ollama, backend, cerebras_model, cerebras_reasoning_effort, theme")
		return
	}
	key := strings.ToLower(parts[1])
	value := parts[2]
	cfg := ag.AppConfig
	if cfg == nil {
		cfg = api.DefaultConfig()
		ag.AppConfig = cfg
	}

	switch key {
	case "model":
		if !api.IsValidClaudeModelName(value) {
			term.PrintError("Valid models: " + api.ValidClaudeModelsHelp())
			return
		}
		cfg.DefaultModel = api.ResolveClaudeModel(value)
		term.PrintSystem(fmt.Sprintf("Default model set to: %s", cfg.DefaultModel))

	case "project":
		var pid int
		if _, err := fmt.Sscanf(value, "%d", &pid); err != nil || pid < 0 {
			term.PrintError("Project ID must be a non-negative integer.")
			return
		}
		cfg.DefaultProject = pid
		ag.Cfg.Context.ProjectID = pid
		term.PrintSystem(fmt.Sprintf("Default project set to: #%d", pid))

	case "professional":
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on":
			cfg.Professional = true
			ag.Cfg.Professional = true
			term.PrintSystem("Professional mode enabled. Cat personality disabled.")
		case "false", "0", "no", "off":
			cfg.Professional = false
			ag.Cfg.Professional = false
			term.PrintSystem("Professional mode disabled. Cat personality restored.")
		default:
			term.PrintError("Value must be true or false.")
			return
		}

	case "autosave":
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on":
			cfg.AutoSave = true
			term.PrintSystem("Auto-save enabled.")
		case "false", "0", "no", "off":
			cfg.AutoSave = false
			term.PrintSystem("Auto-save disabled.")
		default:
			term.PrintError("Value must be true or false.")
			return
		}

	case "output_verbose", "output-verbose", "output":
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on", "verbose":
			cfg.OutputVerbose = true
			ag.Cfg.OutputVerbose = true
			term.PrintSystem("Output mode set to verbose.")
		case "false", "0", "no", "off", "compact":
			cfg.OutputVerbose = false
			ag.Cfg.OutputVerbose = false
			term.PrintSystem("Output mode set to compact.")
		default:
			term.PrintError("Value must be compact/verbose or true/false.")
			return
		}

	case "cloudsync":
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on":
			v := true
			cfg.CloudSync = &v
			term.PrintSystem("Cloud session sync enabled.")
		case "false", "0", "no", "off":
			v := false
			cfg.CloudSync = &v
			term.PrintSystem("Cloud session sync disabled.")
		default:
			term.PrintError("Value must be true or false.")
			return
		}

	case "live_feed", "live-feed", "livefeed":
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on":
			cfg.LiveFeed = true
			ag.Cfg.Context.LiveFeed = true
			term.PrintSystem("Live feed enabled — test runs and AI crawls will stream in QM Cloud Sandbox.")
		case "false", "0", "no", "off":
			cfg.LiveFeed = false
			ag.Cfg.Context.LiveFeed = false
			term.PrintSystem("Live feed disabled.")
		default:
			term.PrintError("Value must be true or false.")
			return
		}

	case "budget":
		var budget int
		if _, err := fmt.Sscanf(value, "%d", &budget); err != nil || budget < 0 {
			term.PrintError("Budget must be a non-negative integer (token count).")
			return
		}
		cfg.MaxTokenBudget = budget
		term.PrintSystem(fmt.Sprintf("Token budget set to: %d", budget))

	case "apikey":
		// Allow pasting API key directly: /set apikey qm-...
		auth, err := api.LoginWithAPIKey(value)
		if err != nil {
			term.PrintError(fmt.Sprintf("Invalid API key: %v", err))
			return
		}
		ag.Cfg.Context.Auth = auth
		ag.Cfg.Context.API = api.NewAPIClient(auth)
		tui.AnimateMax(tui.MoodHappy, fmt.Sprintf("Connected as %s", auth.Email))
		fmt.Println()
		return // auth.json handles persistence

	case "ollama":
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on", "enabled":
			if cfg.OllamaURL == "" {
				term.PrintError("No Ollama URL configured. Set it first:")
				term.PrintSystem("  qmax-code config set ollama_url https://user:pass@llm.example.com")
				term.PrintSystem("  qmax-code config set ollama_model gemma3:4b-it-q4_K_M")
				return
			}
			if err := agent.ValidateOllamaURL(cfg.OllamaURL); err != nil {
				term.PrintError(fmt.Sprintf("Ollama URL rejected: %v", err))
				return
			}
			ag.Ollama = agent.NewOllamaClient(cfg)
			term.PrintSystem(fmt.Sprintf("Ollama enabled: %s (%s)", sysutil.MaskURL(cfg.OllamaURL), cfg.OllamaModel))
		case "false", "0", "no", "off", "disabled":
			ag.Ollama = nil
			term.PrintSystem("Ollama disabled. Using Claude for all calls.")
		default:
			term.PrintError("Value must be true/false (or enabled/disabled).")
			return
		}
		return // no config persistence needed — runtime toggle

	case "backend":
		// /set backend cc|codex|api — persist backend choice.
		// For live switching use /cc, /codex, or /api instead.
		switch strings.ToLower(value) {
		case "cc":
			if bin := agent.FindClaudeCode(); bin == "" {
				term.PrintError("'claude' CLI not found. Install Claude Code first.")
				term.PrintSystem("  https://claude.ai/download")
				return
			}
			cfg.Backend = "cc"
			term.PrintSystem("Backend set to CC. Use /cc to switch live, or restart to apply.")
		case "codex":
			if bin := agent.FindCodex(); bin == "" {
				term.PrintError("'codex' CLI not found.")
				term.PrintSystem("  npm install -g @openai/codex")
				return
			}
			cfg.Backend = "codex"
			term.PrintSystem("Backend set to Codex. Use /codex to switch live, or restart to apply.")
		case "", "api":
			cfg.Backend = ""
			term.PrintSystem("Backend set to Anthropic API. Restart or use /api to switch live.")
		default:
			term.PrintError("Valid backends: cc, codex, api (use /gemma for cerebras)")
			return
		}

	case "cerebras_model", "cerebras-model":
		cfg.CerebrasModel = api.ResolveCerebrasModel(value)
		// Apply live if Cerebras is the active backend.
		if ag.Cerebras != nil {
			ag.Cerebras.Model = api.ResolveCerebrasModel(value)
		}
		term.PrintSystem(fmt.Sprintf("Cerebras model set to: %s", cfg.CerebrasModel))

	case "cerebras_reasoning_effort", "cerebras-reasoning-effort":
		if value == "" {
			cfg.CerebrasReasoningEffort = ""
		} else {
			if !api.ValidCerebrasReasoningEffort(value) {
				term.PrintError(fmt.Sprintf("Invalid value %q; allowed: none, low, medium, high", value))
				return
			}
			cfg.CerebrasReasoningEffort = api.NormalizeCerebrasReasoningEffort(value)
		}
		if ag.Cerebras != nil {
			ag.Cerebras.ReasoningEffort = cfg.CerebrasReasoningEffort
		}
		label := cfg.CerebrasReasoningEffort
		if label == "" {
			label = "none (off)"
		}
		term.PrintSystem(fmt.Sprintf("Cerebras reasoning_effort set to: %s", label))

	case "theme":
		valid := tui.ThemeNames()
		found := false
		for _, n := range valid {
			if n == strings.ToLower(value) {
				found = true
				break
			}
		}
		if !found {
			term.PrintError(fmt.Sprintf("Unknown theme %q. Available: %s", value, strings.Join(valid, ", ")))
			return
		}
		cfg.Theme = strings.ToLower(value)
		tui.ApplyTheme(tui.ThemeByName(cfg.Theme))
		term.PrintSystem(fmt.Sprintf("Theme set to: %s (takes full effect on restart)", cfg.Theme))

	case "anthropic-key", "anthropic_key":
		// Save Anthropic API key to OS keychain
		os.Setenv("ANTHROPIC_API_KEY", value)
		ag.Cfg.AnthropicKey = value
		if err := api.SaveAnthropicKey(value); err != nil {
			term.PrintSystem(fmt.Sprintf("Key set for this session (keychain: %s)", err))
		} else {
			term.PrintSystem("Anthropic API key saved to OS keychain.")
		}
		return // don't save to config.json — keychain handles it

	default:
		term.PrintError(fmt.Sprintf("Unknown config key: %s", key))
		term.PrintSystem("Keys: model, project, professional, autosave, cloud_sync, live_feed, output_verbose, budget, apikey, ollama, backend, cerebras_model, cerebras_reasoning_effort, theme")
		return
	}

	// Persist to disk
	if err := cfg.Save(); err != nil {
		term.PrintError(fmt.Sprintf("api.Config updated in memory but failed to save: %v", err))
	} else {
		term.PrintSystem("api.Config saved to ~/.qmax-code/config.json")
	}
}

func printContext(ctx *api.SessionContext, term *tui.Terminal) {
	term.PrintSystem(fmt.Sprintf("Project: #%d", ctx.ProjectID))
	if ctx.ProjectFile != "" {
		term.PrintSystem(fmt.Sprintf("Detected from: %s", ctx.ProjectFile))
	}
	if ctx.QMaxCfg.CloudURL != "" {
		term.PrintSystem(fmt.Sprintf("Cloud: %s", ctx.QMaxCfg.CloudURL))
	}
	if ctx.QMaxBin != "" {
		term.PrintSystem(fmt.Sprintf("qmax binary: %s", ctx.QMaxBin))
	}
	term.PrintSystem(fmt.Sprintf("Authenticated: %v", ctx.QMaxCfg.Token != ""))
	if gi := ctx.GitInfo; gi != nil {
		if gi.Branch != "" {
			term.PrintSystem(fmt.Sprintf("Git branch: %s", gi.Branch))
		}
		if gi.RemoteURL != "" {
			term.PrintSystem(fmt.Sprintf("Git remote: %s", gi.RemoteURL))
		}
		if len(gi.ChangedFiles) > 0 {
			term.PrintSystem(fmt.Sprintf("Changed files: %d", len(gi.ChangedFiles)))
		}
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func deactivateEmbeddedBackends(ag *agent.Agent) {
	ag.Mode = agent.OllamaModeOff
	ag.Cerebras = nil
}

// sdkCreditCutover is the date Anthropic moves `claude --print` /
// Agent SDK calls off the regular Pro/Max subscription pool onto a
// separate per-user monthly credit. Reference:
// https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan
var sdkCreditCutover = time.Date(2026, time.June, 15, 0, 0, 0, 0, time.Local)

// maybePrintSDKCreditBanner emits a one-line notice — at most once per
// local day — explaining that the cc backend's `claude --print` traffic
// is metered against the new Anthropic Agent SDK monthly credit. Before
// the cutover the message is a heads-up; after, it's a reminder that the
// session is drawing from that credit (and falls through to API rates
// once exhausted if extra-usage is enabled).
//
// Persistence lives in ~/.qmax-code/sdk_credit_banner_seen. The file holds
// the YYYY-MM-DD of the last shown banner; if it matches today we stay
// silent. Best-effort: any read/write failure simply re-shows.
func maybePrintSDKCreditBanner() {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return // can't track state; skip rather than nag every session
	}
	markerPath := filepath.Join(home, ".qmax-code", "sdk_credit_banner_seen")
	today := time.Now().Format("2006-01-02")

	if data, err := os.ReadFile(markerPath); err == nil {
		if strings.TrimSpace(string(data)) == today {
			return
		}
	}

	if time.Now().Before(sdkCreditCutover) {
		fmt.Printf("  %s↪ Heads-up: starting 2026-06-15, the cc backend will draw from your monthly Claude Agent SDK credit (Pro $20 / Max 5x $100 / Max 20x $200), then API rates if extra-usage is enabled.%s\n",
			tui.ColorDim, tui.ColorReset)
	} else {
		fmt.Printf("  %s↪ This session uses your Claude Agent SDK monthly credit (Pro $20 / Max 5x $100 / Max 20x $200), then API rates if extra-usage is enabled. /backend api to switch.%s\n",
			tui.ColorDim, tui.ColorReset)
	}

	// Best-effort marker write — silent failure means a re-show, which is
	// strictly better than a missed disclosure.
	_ = os.MkdirAll(filepath.Dir(markerPath), 0o755)
	_ = os.WriteFile(markerPath, []byte(today), 0o644)
}
