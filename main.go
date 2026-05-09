package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/session"
	"github.com/qualitymax/qmax-code/internal/sysutil"
	"github.com/qualitymax/qmax-code/internal/tui"
	"github.com/qualitymax/qmax-code/internal/vnc"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z"
var Version = "1.16.4"

const Name = "qmax-code"

func main() {
	// Flags
	projectID := flag.Int("project-id", 0, "Default project ID for this session")
	model := flag.String("model", "", "Claude model: auto (haiku+sonnet), sonnet, opus, haiku, or full ID")
	anthropicAPIKey := flag.String("anthropic-api-key", "", "Anthropic API key (or set ANTHROPIC_API_KEY)")
	cloudURL := flag.String("cloud-url", "", "QualityMax cloud URL (or use qmax login)")
	oneShot := flag.String("p", "", "Run a single prompt and exit (non-interactive)")
	resumeID := flag.String("resume", "", "Resume a previous session by ID (or 'last')")
	listSessions := flag.Bool("list-sessions", false, "List recent sessions and exit")
	verbose := flag.Bool("verbose", false, "Show tool calls and raw responses")
	professional := flag.Bool("professional", false, "Disable cat personality, be direct and professional")
	quiet := flag.Bool("q", false, "Quiet mode — no banner, minimal output (for CI)")
	showVersion := flag.Bool("version", false, "Show version")
	backendFlag := flag.String("backend", "", "Orchestration backend: cc, codex, or api (overrides saved config)")
	flag.Parse()
	_ = quiet // reserved for future CI mode

	if *showVersion {
		fmt.Printf("%s v%s\n", Name, Version)
		return
	}

	// Initialize error reporting only when explicitly enabled.
	sysutil.InitErrorReporting(Version)
	defer sysutil.FlushErrorReporting()
	defer sysutil.RecoverPanic()

	// Handle "serve --mcp" subcommand: start MCP server for Claude Code integration.
	// CC spawns this automatically when qmax-code is listed as an MCP server.
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if len(os.Args) > 2 && os.Args[2] == "--mcp" {
			RunMCPServer()
			return
		}
		fmt.Fprintln(os.Stderr, "Usage: qmax-code serve --mcp")
		os.Exit(2)
	}

	// Handle "config" subcommand — lets users change persisted settings
	// without hand-editing ~/.qmax-code/config.json.
	//
	//   qmax-code config                              → show current config
	//   qmax-code config set default_framework rust_cargo
	//   qmax-code config unset default_framework
	if len(os.Args) > 1 && os.Args[1] == "config" {
		handleConfigCommand(os.Args[2:])
		return
	}

	// Handle "login" subcommand before flag parsing
	if len(os.Args) > 1 && os.Args[1] == "login" {
		loginFlags := flag.NewFlagSet("login", flag.ExitOnError)
		qualityMaxAPIKey := loginFlags.String("api-key", "", "QualityMax API key")
		_ = loginFlags.Parse(os.Args[2:])

		var cfg *api.AuthConfig
		var err error
		args := loginFlags.Args()
		if *qualityMaxAPIKey != "" {
			cfg, err = api.LoginWithAPIKey(*qualityMaxAPIKey)
		} else if len(args) > 0 && strings.HasPrefix(args[0], "qm-") {
			cfg, err = api.LoginWithAPIKey(args[0])
		} else {
			// Browser-based login (Railway-style)
			tui.AnimateMax(tui.MoodWaving, "Let's get you logged in!")
			cfg, err = LoginViaBrowser()
		}
		if err != nil {
			tui.AnimateMax(tui.MoodSad, "Login failed: "+err.Error())
			fmt.Fprintf(os.Stderr, "\n  Try: qmax-code login --api-key qm-YOUR-API-KEY\n")
			os.Exit(1)
		}
		tui.AnimateMax(tui.MoodHappy, fmt.Sprintf("Logged in as %s", cfg.Email))
		return
	}

	if *listSessions {
		sessions, err := session.ListSessions(20)
		if err != nil || len(sessions) == 0 {
			fmt.Println("No sessions found.")
			os.Exit(0)
		}
		fmt.Printf("%-10s  %-18s  %-6s  %-8s  %s\n", "ID", "Updated", "Turns", "Tokens", "Project")
		for _, s := range sessions {
			fmt.Printf("%-10s  %-18s  %-6d  %-8d  #%d\n",
				s.ID, s.UpdatedAt.Format("2006-01-02 15:04"), s.Turns, s.Tokens, s.ProjectID)
		}
		return
	}

	// Load persistent user config
	appConfig := api.LoadQMaxCodeConfig()

	// Apply color theme before constructing any UI components.
	tui.ApplyTheme(tui.ThemeByName(appConfig.Theme))

	// Apply --professional flag (CLI flag overrides saved config)
	if *professional {
		appConfig.Professional = true
	}

	// --backend flag overrides saved config for this session only.
	if *backendFlag != "" {
		switch *backendFlag {
		case "cc", "codex", "api", "":
			if *backendFlag == "api" {
				appConfig.Backend = ""
			} else {
				appConfig.Backend = *backendFlag
			}
		default:
			fmt.Fprintf(os.Stderr, "Error: --backend must be cc, codex, or api\n")
			os.Exit(2)
		}
	}

	// Resolve model: CLI flag > saved config > "auto"
	effectiveModel := *model
	if effectiveModel == "" {
		effectiveModel = appConfig.DefaultModel
	}
	if effectiveModel == "" {
		effectiveModel = "auto"
	}
	effectiveModel = resolveModel(effectiveModel)

	// Resolve Anthropic API key: flag > env > keychain
	anthropicKey := *anthropicAPIKey
	if anthropicKey == "" {
		anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if anthropicKey == "" && appConfig.AnthropicKey != "" {
		anthropicKey = appConfig.AnthropicKey
	}

	// Load auth (new standalone mode)
	auth := api.LoadAuth()

	// Load qmax config for cloud URL and auth token (legacy)
	qmaxCfg := api.LoadQMaxConfig()
	if *cloudURL != "" {
		qmaxCfg.CloudURL = *cloudURL
	}

	// Discover qmax binary (optional in standalone mode)
	qmaxBin := api.DiscoverQMaxBinary()

	// Initialize API client if authenticated (standalone mode)
	var apiClient *api.APIClient
	if auth != nil && auth.IsAuthenticated() {
		apiClient = api.NewAPIClient(auth)
	}

	// If no qmax CLI and no API client, run full interactive setup
	if qmaxBin == "" && apiClient == nil {
		setupAuth, setupProjectID := RunInteractiveSetup()
		auth = setupAuth
		apiClient = api.NewAPIClient(auth)
		appConfig.DefaultProject = setupProjectID
		if anthropicKey == "" {
			anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}

	// CLI backend mode: route all LLM inference through a local CLI subprocess.
	// Neither an Anthropic API key nor an OpenAI key is required — the user's
	// subscription (CC or OpenAI/Codex) covers inference.
	// qmax tools are served to the CLI via the embedded MCP server.
	var cliAgent CLIAgent
	cliBackend := appConfig.Backend // "cc" | "codex" | "" (API)

	if cliBackend == "cc" {
		claudeBin := FindClaudeCode()
		if claudeBin == "" {
			fmt.Fprintln(os.Stderr, "\nError: backend=cc but 'claude' CLI was not found.")
			fmt.Fprintln(os.Stderr, "  Install Claude Code: https://claude.ai/download")
			fmt.Fprintln(os.Stderr, "  Or switch backend: qmax-code config set backend api")
			os.Exit(1)
		}
		consent := promptOrchConsent(appConfig, "cc")
		if !consent.Proceed {
			fmt.Fprintln(os.Stderr, "  CC backend not activated. Falling back to direct API.")
			cliBackend = ""
			appConfig.Backend = ""
		} else {
			appConfig.OrchPermissionMode = consent.PermissionMode
			appConfig.OrchGlobalInstall = consent.GlobalInstall
			_ = appConfig.Save()
			anthropicKey = "__cc_mode__" // skip Anthropic key gate below
		}
	} else if cliBackend == "codex" {
		codexBin := FindCodex()
		if codexBin == "" {
			fmt.Fprintln(os.Stderr, "\nError: backend=codex but 'codex' CLI was not found.")
			fmt.Fprintln(os.Stderr, "  Install Codex CLI: npm install -g @openai/codex")
			fmt.Fprintln(os.Stderr, "  Or switch backend: qmax-code config set backend api")
			os.Exit(1)
		}
		consent := promptOrchConsent(appConfig, "codex")
		if !consent.Proceed {
			fmt.Fprintln(os.Stderr, "  Codex backend not activated. Falling back to direct API.")
			cliBackend = ""
			appConfig.Backend = ""
		} else {
			appConfig.OrchPermissionMode = consent.PermissionMode
			appConfig.OrchGlobalInstall = consent.GlobalInstall
			_ = appConfig.Save()
			anthropicKey = "__codex_mode__" // skip Anthropic key gate below
		}
	}

	// If connected but missing Anthropic key, prompt for it (skipped in CLI backend modes).
	if cliBackend == "" && anthropicKey == "" {
		fmt.Println()
		fmt.Println("  Anthropic API key needed (this powers the AI).")
		fmt.Println("  Get one at: https://console.anthropic.com/settings/keys")
		fmt.Println()
		key := readSecret("  Paste your Anthropic key: ")
		if key != "" {
			anthropicKey = key
			os.Setenv("ANTHROPIC_API_KEY", key)
			if err := api.SaveAnthropicKey(key); err == nil {
				fmt.Println("  Saved to OS keychain.")
			}
		}
	}

	if cliBackend == "" && anthropicKey == "" {
		fmt.Fprintln(os.Stderr, "\nError: Anthropic API key required.")
		fmt.Fprintln(os.Stderr, "  export ANTHROPIC_API_KEY=sk-ant-...")
		fmt.Fprintln(os.Stderr, "  Or use a CLI backend (no API key needed):")
		fmt.Fprintln(os.Stderr, "    qmax-code config set backend cc      # Claude Code subscription")
		fmt.Fprintln(os.Stderr, "    qmax-code config set backend codex   # OpenAI/Codex subscription")
		os.Exit(1)
	}

	// Detect project from cwd if not set via flag; fall back to saved config
	detectedProjectID := *projectID
	var projectFile string
	if detectedProjectID == 0 {
		detectedProjectID, projectFile = api.DetectProjectFromCwd()
	}
	if detectedProjectID == 0 && appConfig.DefaultProject > 0 {
		detectedProjectID = appConfig.DefaultProject
	}

	// Build session context
	ctx := &api.SessionContext{
		ProjectID:   detectedProjectID,
		QMaxCfg:     qmaxCfg,
		QMaxBin:     qmaxBin,
		QMaxInfo:    api.ProbeQMaxStatus(qmaxBin),
		GitInfo:     api.DetectGitInfo(),
		ProjectFile: projectFile,
		API:         apiClient,
		Auth:        auth,
		Backend:     appConfig.Backend,
		LiveFeed:    appConfig.LiveFeed,
	}

	// Build agent with smart model routing
	autoRoute := effectiveModel == "auto"
	var baseModel, chatModel string
	if autoRoute {
		baseModel = api.ModelSonnet
		chatModel = api.ModelHaiku
	} else {
		baseModel = resolveModel(effectiveModel)
		chatModel = baseModel
	}

	agent := NewAgent(AgentConfig{
		AnthropicKey:  anthropicKey,
		Model:         baseModel,
		ChatModel:     chatModel,
		Verbose:       *verbose,
		OutputVerbose: appConfig.OutputVerbose,
		Context:       ctx,
		AutoRoute:     autoRoute,
		Professional:  appConfig.Professional,
	})
	agent.appConfig = appConfig
	agent.ollama = NewOllamaClient(appConfig)
	if agent.ollama != nil {
		agent.ollamaMode = OllamaModeFull // default to full when configured
	}

	// Build the CLI agent if a CLI backend was selected and consented to above.
	// Global MCP install (~/.claude/settings.json or ~/.codex/config.toml) is
	// performed only when the user opted into it during the consent prompt.
	switch cliBackend {
	case "cc":
		if appConfig.OrchGlobalInstall && !IsOrchSetupDone("cc") {
			if res, err := SetupCCIntegration(); err == nil && !res.AlreadyHadMCP {
				fmt.Printf("  qmax MCP entry added to %s\n", res.MCPPath)
			}
		}
		cliAgent = NewCCAgent(FindClaudeCode(), appConfig.ModelOverride, appConfig.Effort, appConfig.OrchPermissionMode, appConfig.OutputVerbose, ctx)
	case "codex":
		if appConfig.OrchGlobalInstall && !IsOrchSetupDone("codex") {
			if res, err := SetupCodexIntegration(); err == nil && !res.AlreadyHadMCP {
				fmt.Printf("  qmax MCP entry added to %s\n", res.MCPPath)
			}
		}
		ca := NewCodexAgent(FindCodex(), appConfig.ModelOverride, appConfig.Effort, appConfig.OrchPermissionMode, appConfig.OutputVerbose, ctx)
		if err := ca.writeMCPConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write Codex MCP config: %v\n", err)
		}
		cliAgent = ca
	}

	// One-shot mode
	if *oneShot != "" {
		result, err := agent.Run(*oneShot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)
		return
	}

	// Also handle positional args as a prompt: qmax-code "test the login flow"
	if remaining := flag.Args(); len(remaining) > 0 {
		prompt := strings.Join(remaining, " ")
		result, err := agent.Run(prompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)
		return
	}

	// Handle --resume flag
	if *resumeID != "" {
		var sess *session.Session
		var loadErr error
		if *resumeID == "last" {
			sess, loadErr = session.LoadLastSession()
		} else {
			sess, loadErr = session.LoadSession(*resumeID)
		}
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "Cannot resume session %q: %v\n", *resumeID, loadErr)
			os.Exit(1)
		}
		agent.history = sess.Messages
		agent.usage = sess.Usage
		if sess.ProjectID > 0 {
			agent.config.Context.ProjectID = sess.ProjectID
		}
		fmt.Printf("Resumed session %s (%d turns)\n", sess.ID, sess.Turns)
	}

	// Clean up old sessions (>7 days)
	if removed := session.CleanupOldSessions(); removed > 0 && *verbose {
		fmt.Printf("[cleanup] Removed %d old sessions\n", removed)
	}

	// Sweep orphaned MCP config files left by crashed previous instances.
	cleanupStaleMCPConfigs()

	// Interactive REPL
	runREPL(agent, cliAgent, *quiet)
}

// resolveModel expands shorthand model names to full model IDs.
func resolveModel(m string) string {
	switch strings.ToLower(m) {
	case "sonnet":
		return api.ModelSonnet
	case "opus":
		return api.ModelOpus
	case "haiku":
		return api.ModelHaiku
	default:
		return m
	}
}

func runREPL(agent *Agent, cliAgent CLIAgent, quietMode bool) {
	term := tui.NewTerminal()
	defer term.Close()
	if cliAgent != nil {
		defer cliAgent.Cleanup()
	}

	// Prompt queue — collects prompts typed while the agent is running.
	pq := &session.PromptQueue{}

	// session.Session ID for this conversation
	sessionID := session.GenerateSessionID()

	// Initialize structured logger
	agent.logger = sysutil.NewLogger(sessionID)
	defer agent.logger.Close()

	// Cloud session tracking — created once when projectID is known.
	var tracker session.CloudSessionTracker
	startCloudSession := func() {
		api := agent.config.Context.API
		projectID := agent.config.Context.ProjectID
		if api == nil || projectID == 0 {
			return
		}
		cfg := agent.appConfig
		// First eligible session: ask the user once and persist their choice.
		if cfg != nil && cfg.CloudSync == nil {
			session.PromptCloudSyncConsent(cfg, term.ReadConsent)
		}
		if cfg == nil || cfg.CloudSync == nil || !*cfg.CloudSync {
			return
		}
		tracker.Start(api, projectID, agent.config.Model)
	}
	completeCloudSession := func() {
		cfg := agent.appConfig
		if cfg == nil || cfg.CloudSync == nil || !*cfg.CloudSync {
			return
		}
		tracker.Complete(agent.config.Context.API, agent.usage.TotalTokens(), session.SummaryFor(agent.history), agent.history)
	}

	// Graceful interrupt handling
	var (
		sigMu       sync.Mutex
		lastSigTime time.Time
	)

	autoSave := func() {
		if len(agent.history) > 0 && (agent.appConfig == nil || agent.appConfig.AutoSave) {
			_ = session.SaveSession(sessionID, agent.history, agent.config.Context.ProjectID, agent.usage, agent.config.Model)
		}
	}

	saveAndExit := func() {
		_ = session.SaveSession(sessionID, agent.history, agent.config.Context.ProjectID, agent.usage, agent.config.Model)
		completeCloudSession()
		fmt.Fprintf(os.Stderr, "session.Session %s saved.\n", sessionID)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for sig := range sigCh {
			sigMu.Lock()
			now := time.Now()
			if sig == syscall.SIGINT {
				// If agent is streaming or executing tools, cancel it
				agent.CancelCurrent()

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
		term.PrintBanner(Version, agent.config.Context)
		fmt.Printf("  %sSession: %s%s\n", tui.ColorDim, sessionID, tui.ColorReset)

		// Hint about recent session if one exists
		if recent, err := session.ListSessions(1); err == nil && len(recent) > 0 {
			age := time.Since(recent[0].UpdatedAt)
			if age < 24*time.Hour {
				fmt.Printf("  %sRecent session: %s (%d turns, %s ago) — type /resume to continue%s\n",
					tui.ColorDim, recent[0].ID, recent[0].Turns, formatDuration(age), tui.ColorReset)
			}
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
			result := tui.ReadInput(term.Prompt(), inputHistory, agent.config.OutputVerbose)

			if result.OutputToggle {
				toggleOutputVerbose(agent, cliAgent, term)
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
			printREPLHelp()
			continue
		case input == "/clear":
			agent.ClearHistory()
			term.PrintSystem("Conversation cleared.")
			continue
		case strings.HasPrefix(input, "/project "):
			id := strings.TrimPrefix(input, "/project ")
			var pid int
			if _, err := fmt.Sscanf(id, "%d", &pid); err == nil {
				agent.config.Context.ProjectID = pid
				term.PrintSystem(fmt.Sprintf("Project set to #%d", pid))
			} else {
				term.PrintError("Invalid project ID")
			}
			continue
		case input == "/context":
			printContext(agent.config.Context, term)
			continue
		case input == "/connect":
			handleConnect(agent, term)
			continue
		case input == "/disconnect":
			handleDisconnect(agent, term)
			continue
		case input == "/reconnect":
			reconnectMCPTransport(cliAgent, term)
			continue
		case input == "/status":
			term.PrintStatusInfo(agent.config.Context, agent.usage, agent.config.Model)
			continue
		case input == "/cost":
			term.PrintCostSummary(agent.usage, agent.config.Model)
			continue
		case input == "/resume" || strings.HasPrefix(input, "/resume "):
			resumeTarget := strings.TrimPrefix(input, "/resume ")
			resumeTarget = strings.TrimSpace(resumeTarget)
			var sess *session.Session
			var loadErr error
			if resumeTarget == "" || resumeTarget == "/resume" || resumeTarget == "last" {
				sess, loadErr = session.LoadLastSession()
			} else {
				sess, loadErr = session.LoadSession(resumeTarget)
			}
			if loadErr != nil {
				term.PrintError(fmt.Sprintf("Cannot resume: %v", loadErr))
				term.PrintSystem("Use /sessions to see available sessions")
			} else {
				session.SanitizeSessionMessages(sess.Messages)
				agent.history = sess.Messages
				agent.usage = sess.Usage
				sessionID = sess.ID
				if sess.ProjectID > 0 {
					agent.config.Context.ProjectID = sess.ProjectID
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
			sess, loadErr := session.LoadSession(chosenID)
			if loadErr != nil {
				term.PrintError(fmt.Sprintf("Cannot resume: %v", loadErr))
			} else {
				session.SanitizeSessionMessages(sess.Messages)
				agent.history = sess.Messages
				agent.usage = sess.Usage
				sessionID = sess.ID
				if sess.ProjectID > 0 {
					agent.config.Context.ProjectID = sess.ProjectID
				}
				term.SetSessionPrompt(sessionID)
				term.PrintSystem(fmt.Sprintf("Resumed session %s (%d turns, project #%d)",
					sess.ID, sess.Turns, sess.ProjectID))
			}
			continue
		case input == "/save":
			if err := session.SaveSession(sessionID, agent.history, agent.config.Context.ProjectID, agent.usage, agent.config.Model); err != nil {
				term.PrintError(fmt.Sprintf("Failed to save: %v", err))
			} else {
				term.PrintSystem(fmt.Sprintf("Session %s saved.", sessionID))
			}
			continue
		case input == "/config":
			printConfigInfo(agent.appConfig, term)
			continue
		case input == "/set":
			handleSetCommand(input, agent, term)
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

		case strings.HasPrefix(input, "/queue "):
			queued := strings.TrimSpace(strings.TrimPrefix(input, "/queue "))
			if queued != "" {
				pq.Push(queued)
				term.PrintSystem(fmt.Sprintf("queued [%d]: %s", pq.Len(), queued))
			}
			continue
		case input == "/orch":
			// Show the unified model + effort TUI picker and apply the selection instantly.
			cfg := agent.appConfig
			if cfg == nil {
				term.PrintError("api.Config not loaded.")
				continue
			}

			// Determine the picker's "currentBackend" — include ollama as a backend value.
			currentBackend := cfg.Backend
			if agent.ollamaMode == OllamaModeFull {
				currentBackend = "ollama"
			}
			result := tui.ShowModelPicker(tui.ModelPickerOpts{
				CurrentBackend: currentBackend,
				CurrentModelID: cfg.ModelOverride,
				Effort:         cfg.Effort,
				OllamaURL:      cfg.OllamaURL,
				OllamaModel:    cfg.OllamaModel,
				CCInstalled:    FindClaudeCode() != "",
				CodexInstalled: FindCodex() != "",
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
				if agent.ollama == nil {
					agent.ollama = NewOllamaClient(cfg)
				}
				if cliAgent != nil {
					cliAgent.Cleanup()
					cliAgent = nil
				}
				agent.ollamaMode = OllamaModeFull
				cfg.Backend = ""
				agent.config.Context.Backend = ""
				_ = cfg.Save()
				term.PrintSystem(fmt.Sprintf("Backend: Ollama  model: %s  endpoint: %s", cfg.OllamaModel, sysutil.MaskURL(cfg.OllamaURL)))
				continue
			}

			// ── Validate the chosen CLI is actually installed ─────────────────
			switch result.Backend {
			case "cc":
				if FindClaudeCode() == "" {
					term.PrintError("Claude Code ('claude') not found. Install it first.")
					term.PrintSystem("  https://claude.ai/download")
					continue
				}
			case "codex":
				if FindCodex() == "" {
					term.PrintError("Codex CLI ('codex') not found.")
					term.PrintSystem("  npm install -g @openai/codex")
					continue
				}
			}

			// Consent gate: required before activating CC/Codex (autonomous shell + edits).
			if result.Backend != "" {
				consent := promptOrchConsent(cfg, result.Backend)
				if !consent.Proceed {
					term.PrintSystem("Backend not changed.")
					continue
				}
				cfg.OrchPermissionMode = consent.PermissionMode
				cfg.OrchGlobalInstall = consent.GlobalInstall
				if consent.GlobalInstall && !IsOrchSetupDone(result.Backend) {
					RunOrchSetup(result.Backend, term)
				}
			}

			// Tear down current CLI agent and disable Ollama if switching away from it.
			if cliAgent != nil {
				cliAgent.Cleanup()
				cliAgent = nil
			}
			agent.ollamaMode = OllamaModeOff

			// Spin up the new agent with selected model + effort.
			switch result.Backend {
			case "cc":
				cliAgent = NewCCAgent(FindClaudeCode(), result.ModelID, result.Effort, cfg.OrchPermissionMode, cfg.OutputVerbose, agent.config.Context)
				term.PrintSystem(fmt.Sprintf("Backend: Claude Code  model: %s  effort: %s", result.ModelID, result.Effort))
			case "codex":
				ca := NewCodexAgent(FindCodex(), result.ModelID, result.Effort, cfg.OrchPermissionMode, cfg.OutputVerbose, agent.config.Context)
				if err := ca.writeMCPConfig(); err != nil {
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
			agent.config.Context.Backend = result.Backend
			_ = cfg.Save()
			continue

		case input == "/theme":
			cfg := agent.appConfig
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
			cfg := agent.appConfig
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
			cfg := agent.appConfig
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

			switch wantBackend {
			case "cc":
				bin := FindClaudeCode()
				if bin == "" {
					term.PrintError("'claude' CLI not found. Install Claude Code first.")
					term.PrintSystem("  https://claude.ai/download")
					continue
				}
				consent := promptOrchConsent(cfg, "cc")
				if !consent.Proceed {
					term.PrintSystem("Backend not changed.")
					continue
				}
				cfg.OrchPermissionMode = consent.PermissionMode
				cfg.OrchGlobalInstall = consent.GlobalInstall
				if consent.GlobalInstall && !IsOrchSetupDone("cc") {
					RunOrchSetup("cc", term)
				}
				cliAgent = NewCCAgent(bin, cfg.ModelOverride, cfg.Effort, cfg.OrchPermissionMode, cfg.OutputVerbose, agent.config.Context)
				cfg.Backend = "cc"
				_ = cfg.Save()
				term.PrintSystem(fmt.Sprintf("Backend → Claude Code (%s) · %s mode", bin, cfg.OrchPermissionMode))

			case "codex":
				bin := FindCodex()
				if bin == "" {
					term.PrintError("'codex' CLI not found.")
					term.PrintSystem("  npm install -g @openai/codex")
					continue
				}
				consent := promptOrchConsent(cfg, "codex")
				if !consent.Proceed {
					term.PrintSystem("Backend not changed.")
					continue
				}
				cfg.OrchPermissionMode = consent.PermissionMode
				cfg.OrchGlobalInstall = consent.GlobalInstall
				if consent.GlobalInstall && !IsOrchSetupDone("codex") {
					RunOrchSetup("codex", term)
				}
				ca := NewCodexAgent(bin, cfg.ModelOverride, cfg.Effort, cfg.OrchPermissionMode, cfg.OutputVerbose, agent.config.Context)
				if err := ca.writeMCPConfig(); err != nil {
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
			agent.config.Context.Backend = cfg.Backend
			continue

		case input == "/ollama":
			// Cycle through modes: off → chat → full → off
			cfg := agent.appConfig
			if cfg == nil || cfg.OllamaURL == "" {
				term.PrintError("Ollama not configured. Set it first:")
				term.PrintSystem("  qmax-code config set ollama_url https://user:pass@llm.example.com")
				term.PrintSystem("  qmax-code config set ollama_model gemma3:4b-it-q4_K_M")
				continue
			}
			if agent.ollama == nil {
				agent.ollama = NewOllamaClient(cfg)
			}
			switch agent.ollamaMode {
			case OllamaModeOff:
				agent.ollamaMode = OllamaModeChat
				term.PrintSystem(fmt.Sprintf("Ollama: CHAT mode (%s) — chat via local model, tools via Claude", agent.ollama.model))
			case OllamaModeChat:
				agent.ollamaMode = OllamaModeFull
				term.PrintSystem(fmt.Sprintf("Ollama: FULL mode (%s) — everything via local model (no Claude)", agent.ollama.agentModel))
			case OllamaModeFull:
				agent.ollamaMode = OllamaModeOff
				term.PrintSystem("Ollama: OFF — all calls via Claude")
			}
			continue
		case strings.HasPrefix(input, "/set "):
			handleSetCommand(input, agent, term)
			startCloudSession()
			continue
		case input == "/keys":
			handleKeys(agent, term)
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
			if err := ShowBrowserFeed(arg, mode); err != nil {
				term.PrintError(fmt.Sprintf("browserfeed: %v", err))
			}
			continue
		case input == "/live" || strings.HasPrefix(input, "/live "):
			arg := strings.TrimSpace(strings.TrimPrefix(input, "/live"))
			cfg := agent.appConfig
			if cfg == nil {
				term.PrintError("api.Config not loaded.")
				continue
			}
			applyLiveFeed := func(next bool) {
				cfg.LiveFeed = next
				agent.config.Context.LiveFeed = next
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
			url := agent.config.Context.LastLiveURL
			if url == "" {
				term.PrintSystem("No live feed URL captured yet.")
				if !agent.config.Context.LiveFeed {
					term.PrintSystem("  Enable with: /live on  (then run a test or crawl)")
				} else {
					term.PrintSystem("  Run a test or crawl, then try /feed again.")
				}
				continue
			}
			if err := ShowBrowserFeed(url, blockModeQuarter); err != nil {
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
			llmResult, err := agent.RunStreamingWithImages("Analyze this screenshot.", []tui.ImageAttachment{*img}, term)
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
				llmResult, err := agent.RunStreamingWithImages("Analyze this pasted image.", []tui.ImageAttachment{*img}, term)
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
		if c := agent.config.Context; c != nil {
			c.LiveURLLogged = false
			c.SandboxModeLogged = false
			c.SandboxFallbackSeen = false
		}

		// Run through the LLM agent with streaming.
		// Start the queue reader so the user can type the next prompt while
		// the agent is working.  It is stopped (and fully drained) before
		// the next tui.ReadInput call so stdin is never shared between readers.
		stopQueueReader := session.StartQueueReader(pq, term)

		// CC mode: delegate entirely to Claude Code subprocess (uses CC subscription).
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
				// Mirror the turn into agent.history so autoSave records it.
				// CCAgent/CodexAgent manage their own subprocess state; qmax's
				// history would otherwise stay empty and autoSave would no-op.
				agent.history = append(agent.history,
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
			llmResult, err = agent.RunStreamingWithImages(cleanInput, images, term)
		} else {
			llmResult, err = agent.RunStreaming(input, term)
		}

		// Stop queue reader and wait for the goroutine to exit before we
		// touch stdin again (either via the queue loop or tui.ReadInput).
		stopQueueReader()

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
				backendTag = agent.config.Context.Backend
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
				agent.config.Context.LastLiveURL = preConn.url
				agent.config.Context.SandboxModeLogged = true
			} else if url := sysutil.DrainLiveURLFromChild(); url != "" {
				agent.config.Context.LastLiveURL = url
				agent.config.Context.SandboxModeLogged = true
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
		maybeLaunchLiveFeed(agent.config.Context, term, preConn.stream, pendingExecID)
	}
}

// maybeLaunchLiveFeed opens /browserfeed using a pre-established VNCStream
// (from the mid-turn pre-connect goroutine) or by dialling fresh from the
// captured URL. When neither a URL nor a pre-stream is available but a
// pendingExecID is set, it polls CheckTestStatus directly (bypassing the LLM)
// until the live_browser_url appears — eliminating the 60–90s REPL freeze
// caused by E2B cold-start blocking the MCP subprocess.
//
// Idempotent: no-op when LiveFeed is off. Clears LastLiveURL on success so
// a stale URL from a previous run doesn't auto-launch on the next turn.
func maybeLaunchLiveFeed(sctx *api.SessionContext, term *tui.Terminal, preStream *vnc.VNCStream, pendingExecID string) {
	if sctx == nil || !sctx.LiveFeed {
		if preStream != nil {
			preStream.Close()
		}
		return
	}

	url := sctx.LastLiveURL

	// Fast-return path: run_test wrote an execution_id and returned immediately.
	// Poll the API directly here (post-LLM-turn) so the user sees the feed as
	// soon as the sandbox is ready rather than waiting inside the MCP tool call.
	if url == "" && pendingExecID != "" && sctx.API != nil {
		term.PrintSystem(fmt.Sprintf("Test started — waiting for live browser feed (exec: %s)...", pendingExecID))
		url = waitForLiveFeedURL(sctx.API, pendingExecID, 5*time.Minute)
	}

	if url == "" {
		if preStream != nil {
			preStream.Close()
		}
		// Only diagnose if the agent actually ran a tool this turn that
		// reported sandbox mode (i.e. a run/crawl actually happened).
		// Otherwise the user just chatted and we shouldn't nag.
		if !sctx.SandboxModeLogged && pendingExecID == "" {
			return
		}
		if pendingExecID != "" {
			term.PrintSystem("Live feed: sandbox did not expose a live_browser_url within 5 minutes.")
		} else {
			term.PrintSystem("Live feed was on, but no live_browser_url came back this turn.")
			if sctx.SandboxFallbackSeen {
				term.PrintSystem("  Server reported is_e2b=false. Most common reason:")
				term.PrintSystem("   • Script has agent_id set → server silently rejects the use_e2b combo")
			} else {
				term.PrintSystem("  Possible causes:")
				term.PrintSystem("   • Server's E2B_API_KEY env var isn't configured (check /api/playwright-execution/health)")
				term.PrintSystem("   • VNC stack failed to start in the sandbox (server logs: 'VNC setup FAILED')")
				term.PrintSystem("   • Script has agent_id set → server silently rejects the use_e2b combo")
			}
		}
		return
	}

	sctx.LastLiveURL = ""
	term.PrintSystem("Opening live browser feed... (Ctrl+] to return)")

	// Determine which stream to use. When we have a pendingExecID, dial a
	// fresh stream so we can monitor test status and auto-close it the moment
	// the test finishes — avoids the "black screen until sandbox teardown" hang.
	stream := preStream
	if stream == nil {
		var dialErr error
		stream, dialErr = vnc.DialVNC(context.Background(), url, 10)
		if dialErr != nil {
			term.PrintError(fmt.Sprintf("browserfeed: connect: %v", dialErr))
			sctx.LastLiveURL = url
			return
		}
	}

	// When tracking a specific execution, close the stream as soon as the test
	// reaches a terminal status — this triggers streamClosedMsg → tea.Quit so
	// the feed exits automatically instead of showing a black screen.
	if pendingExecID != "" && sctx.API != nil {
		api := sctx.API
		execID := pendingExecID
		go func() {
			for {
				time.Sleep(2 * time.Second)
				raw := api.CheckTestStatus(context.Background(), execID)
				var sm map[string]interface{}
				if json.Unmarshal([]byte(raw), &sm) != nil {
					continue
				}
				if st, _ := sm["status"].(string); st == "passed" || st == "failed" || st == "completed" {
					stream.Close()
					return
				}
			}
		}()
	}

	feedErr := ShowBrowserFeedFromStream(stream, blockModeQuarter,
		fmt.Sprintf("connected to %s — Ctrl+] to quit", url))
	if feedErr != nil {
		term.PrintError(fmt.Sprintf("browserfeed: %v", feedErr))
		sctx.LastLiveURL = url
	}
}

// waitForLiveFeedURL polls CheckTestStatus until live_browser_url appears or
// the test ends without one. Returns the URL on success, "" on timeout/failure.
func waitForLiveFeedURL(api *api.APIClient, execID string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		statusRaw := api.CheckTestStatus(context.Background(), execID)
		var m map[string]interface{}
		if json.Unmarshal([]byte(statusRaw), &m) != nil {
			continue
		}
		if urlVal, _ := m["live_browser_url"].(string); urlVal != "" {
			return urlVal
		}
		// Test already finished (no sandbox / fast failure) — stop waiting.
		if st, _ := m["status"].(string); st == "passed" || st == "failed" || st == "completed" {
			return ""
		}
	}
	return ""
}

// handleConnect runs the browser-based auth flow from within the REPL.
func handleConnect(agent *Agent, term *tui.Terminal) {
	ctx := agent.config.Context

	// Already connected?
	if ctx.Auth != nil && ctx.Auth.IsAuthenticated() {
		term.PrintSystem(fmt.Sprintf("Already connected as %s", ctx.Auth.Email))
		term.PrintSystem("Run /disconnect first to switch accounts.")
		return
	}

	tui.AnimateMax(tui.MoodWaving, "Let's connect you to QualityMax!")
	fmt.Println()

	auth, err := LoginViaBrowser()
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
func handleDisconnect(agent *Agent, term *tui.Terminal) {
	ctx := agent.config.Context

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

func toggleOutputVerbose(agent *Agent, cliAgent CLIAgent, term *tui.Terminal) {
	if agent == nil {
		return
	}
	cfg := agent.appConfig
	if cfg == nil {
		cfg = api.DefaultConfig()
		agent.appConfig = cfg
	}
	cfg.OutputVerbose = !cfg.OutputVerbose
	agent.config.OutputVerbose = cfg.OutputVerbose
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
func handleKeys(agent *Agent, term *tui.Terminal) {
	fmt.Println()

	// Show current key status
	anthropicKey := agent.config.AnthropicKey
	if anthropicKey == "" {
		anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	qmaxConnected := agent.config.Context.Auth != nil && agent.config.Context.Auth.IsAuthenticated()

	fmt.Printf("  %s API Keys %s\n\n", "\033[1m", "\033[0m")

	if anthropicKey != "" {
		masked := anthropicKey[:7] + "..." + anthropicKey[len(anthropicKey)-4:]
		fmt.Printf("  Anthropic:   %s● Set%s (%s)\n", "\033[32m", "\033[0m", masked)
	} else {
		fmt.Printf("  Anthropic:   %s● Not set%s\n", "\033[33m", "\033[0m")
	}

	if qmaxConnected {
		fmt.Printf("  QualityMax:  %s● Connected%s (%s)\n", "\033[32m", "\033[0m", agent.config.Context.Auth.Email)
	} else {
		fmt.Printf("  QualityMax:  %s● Not connected%s\n", "\033[33m", "\033[0m")
	}
	fmt.Println()

	choice := promptChoice("  What would you like to do?", []string{
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
		key := readSecret("  Paste your Anthropic key: ")
		if key == "" {
			term.PrintSystem("Cancelled.")
			return
		}
		os.Setenv("ANTHROPIC_API_KEY", key)
		agent.config.AnthropicKey = key
		if err := api.SaveAnthropicKey(key); err != nil {
			term.PrintSystem(fmt.Sprintf("Key set for this session (keychain unavailable: %s)", err))
		} else {
			tui.AnimateMax(tui.MoodHappy, "Key saved to OS keychain!")
			fmt.Println()
		}
	case 1: // QualityMax connect
		handleConnect(agent, term)
	case 2: // Disconnect
		handleDisconnect(agent, term)
	case 3: // Cancel
		return
	}
}

func printREPLHelp() {
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
  /cc            Switch to Claude Code backend (CC subscription, no API tokens)
  /codex         Switch to Codex CLI backend (OpenAI subscription, no API tokens)
  /api           Switch back to direct Anthropic API
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
  /set backend cc           Use Claude Code subscription (no API key needed)
  /set backend codex        Use OpenAI Codex subscription (no API key needed)
  /set backend api          Use Anthropic API directly (default)
  /set theme ocean          Switch color theme (historic, ocean, neon, ember, aurora · paper, sky, sparkling, radiance, goldenhour)

Queue:
  /queue                    Show pending queue
  /queue <prompt>           Add a prompt to the queue immediately
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

func reconnectMCPTransport(cliAgent CLIAgent, term *tui.Terminal) {
	switch a := cliAgent.(type) {
	case *CCAgent:
		if err := a.writeMCPConfig(); err != nil {
			term.PrintError(fmt.Sprintf("Could not restore Claude Code MCP transport: %v", err))
			return
		}
		term.PrintSystem("QMax MCP transport restored for Claude Code.")
	case *CodexAgent:
		if err := a.writeMCPConfig(); err != nil {
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

func handleSetCommand(input string, agent *Agent, term *tui.Terminal) {
	parts := strings.Fields(input)
	if len(parts) < 3 {
		term.PrintError("Usage: /set <key> <value>")
		term.PrintSystem("Keys: model, project, professional, autosave, cloud_sync, live_feed, output_verbose, budget, apikey, ollama, backend, theme")
		return
	}
	key := strings.ToLower(parts[1])
	value := parts[2]
	cfg := agent.appConfig
	if cfg == nil {
		cfg = api.DefaultConfig()
		agent.appConfig = cfg
	}

	switch key {
	case "model":
		validModels := map[string]bool{"auto": true, "sonnet": true, "opus": true, "haiku": true}
		if !validModels[strings.ToLower(value)] {
			term.PrintError("Valid models: auto, sonnet, opus, haiku")
			return
		}
		cfg.DefaultModel = strings.ToLower(value)
		term.PrintSystem(fmt.Sprintf("Default model set to: %s", cfg.DefaultModel))

	case "project":
		var pid int
		if _, err := fmt.Sscanf(value, "%d", &pid); err != nil || pid < 0 {
			term.PrintError("Project ID must be a non-negative integer.")
			return
		}
		cfg.DefaultProject = pid
		agent.config.Context.ProjectID = pid
		term.PrintSystem(fmt.Sprintf("Default project set to: #%d", pid))

	case "professional":
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on":
			cfg.Professional = true
			agent.config.Professional = true
			term.PrintSystem("Professional mode enabled. Cat personality disabled.")
		case "false", "0", "no", "off":
			cfg.Professional = false
			agent.config.Professional = false
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
			agent.config.OutputVerbose = true
			term.PrintSystem("Output mode set to verbose.")
		case "false", "0", "no", "off", "compact":
			cfg.OutputVerbose = false
			agent.config.OutputVerbose = false
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
			agent.config.Context.LiveFeed = true
			term.PrintSystem("Live feed enabled — test runs and AI crawls will stream in QM Cloud Sandbox.")
		case "false", "0", "no", "off":
			cfg.LiveFeed = false
			agent.config.Context.LiveFeed = false
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
		agent.config.Context.Auth = auth
		agent.config.Context.API = api.NewAPIClient(auth)
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
			agent.ollama = NewOllamaClient(cfg)
			term.PrintSystem(fmt.Sprintf("Ollama enabled: %s (%s)", sysutil.MaskURL(cfg.OllamaURL), cfg.OllamaModel))
		case "false", "0", "no", "off", "disabled":
			agent.ollama = nil
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
			if bin := FindClaudeCode(); bin == "" {
				term.PrintError("'claude' CLI not found. Install Claude Code first.")
				term.PrintSystem("  https://claude.ai/download")
				return
			}
			cfg.Backend = "cc"
			term.PrintSystem("Backend set to CC. Use /cc to switch live, or restart to apply.")
		case "codex":
			if bin := FindCodex(); bin == "" {
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
			term.PrintError("Valid backends: cc, codex, api")
			return
		}

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
		agent.config.AnthropicKey = value
		if err := api.SaveAnthropicKey(value); err != nil {
			term.PrintSystem(fmt.Sprintf("Key set for this session (keychain: %s)", err))
		} else {
			term.PrintSystem("Anthropic API key saved to OS keychain.")
		}
		return // don't save to config.json — keychain handles it

	default:
		term.PrintError(fmt.Sprintf("Unknown config key: %s", key))
		term.PrintSystem("Keys: model, project, professional, autosave, cloud_sync, live_feed, output_verbose, budget, apikey, ollama, backend, theme")
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
