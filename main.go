package main

import (
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
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z"
var Version = "1.12.0"

const Name = "qmax-code"

func main() {
	// Flags
	projectID := flag.Int("project-id", 0, "Default project ID for this session")
	model := flag.String("model", "", "Claude model: auto (haiku+sonnet), sonnet, opus, haiku, or full ID")
	apiKey := flag.String("api-key", "", "Anthropic API key (or set ANTHROPIC_API_KEY)")
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

	// Initialize error reporting (Bugsink)
	InitErrorReporting()
	defer FlushErrorReporting()
	defer RecoverPanic()

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
		var cfg *AuthConfig
		var err error
		if *apiKey != "" {
			cfg, err = LoginWithAPIKey(*apiKey)
		} else if len(os.Args) > 2 && strings.HasPrefix(os.Args[2], "qm-") {
			cfg, err = LoginWithAPIKey(os.Args[2])
		} else {
			// Browser-based login (Railway-style)
			AnimateMax(MoodWaving, "Let's get you logged in!")
			cfg, err = LoginViaBrowser()
		}
		if err != nil {
			AnimateMax(MoodSad, "Login failed: "+err.Error())
			fmt.Fprintf(os.Stderr, "\n  Try: qmax-code login qm-YOUR-API-KEY\n")
			os.Exit(1)
		}
		AnimateMax(MoodHappy, fmt.Sprintf("Logged in as %s", cfg.Email))
		return
	}

	if *listSessions {
		sessions, err := ListSessions(20)
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
	appConfig := LoadQMaxCodeConfig()

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
	anthropicKey := *apiKey
	if anthropicKey == "" {
		anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if anthropicKey == "" && appConfig.AnthropicKey != "" {
		anthropicKey = appConfig.AnthropicKey
	}

	// Load auth (new standalone mode)
	auth := LoadAuth()

	// Load qmax config for cloud URL and auth token (legacy)
	qmaxCfg := loadQMaxConfig()
	if *cloudURL != "" {
		qmaxCfg.CloudURL = *cloudURL
	}

	// Discover qmax binary (optional in standalone mode)
	qmaxBin := discoverQMaxBinary()

	// Initialize API client if authenticated (standalone mode)
	var apiClient *APIClient
	if auth != nil && auth.IsAuthenticated() {
		apiClient = NewAPIClient(auth)
	}

	// If no qmax CLI and no API client, run full interactive setup
	if qmaxBin == "" && apiClient == nil {
		setupAuth, setupProjectID := RunInteractiveSetup()
		auth = setupAuth
		apiClient = NewAPIClient(auth)
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
		anthropicKey = "__cc_mode__" // skip Anthropic key gate below
	} else if cliBackend == "codex" {
		codexBin := FindCodex()
		if codexBin == "" {
			fmt.Fprintln(os.Stderr, "\nError: backend=codex but 'codex' CLI was not found.")
			fmt.Fprintln(os.Stderr, "  Install Codex CLI: npm install -g @openai/codex")
			fmt.Fprintln(os.Stderr, "  Or switch backend: qmax-code config set backend api")
			os.Exit(1)
		}
		anthropicKey = "__codex_mode__" // skip Anthropic key gate below
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
			if err := SaveAnthropicKey(key); err == nil {
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
		detectedProjectID, projectFile = detectProjectFromCwd()
	}
	if detectedProjectID == 0 && appConfig.DefaultProject > 0 {
		detectedProjectID = appConfig.DefaultProject
	}

	// Build session context
	ctx := &SessionContext{
		ProjectID:   detectedProjectID,
		QMaxCfg:     qmaxCfg,
		QMaxBin:     qmaxBin,
		QMaxInfo:    probeQMaxStatus(qmaxBin),
		GitInfo:     detectGitInfo(),
		ProjectFile: projectFile,
		API:         apiClient,
		Auth:        auth,
		Backend:     appConfig.Backend,
	}

	// Build agent with smart model routing
	autoRoute := effectiveModel == "auto"
	var baseModel, chatModel string
	if autoRoute {
		baseModel = ModelSonnet
		chatModel = ModelHaiku
	} else {
		baseModel = resolveModel(effectiveModel)
		chatModel = baseModel
	}

	agent := NewAgent(AgentConfig{
		AnthropicKey: anthropicKey,
		Model:        baseModel,
		ChatModel:    chatModel,
		Verbose:      *verbose,
		Context:      ctx,
		AutoRoute:    autoRoute,
		Professional: appConfig.Professional,
	})
	agent.appConfig = appConfig
	agent.ollama = NewOllamaClient(appConfig)
	if agent.ollama != nil {
		agent.ollamaMode = OllamaModeFull // default to full when configured
	}

	// Initialize the CLI agent and run one-time global MCP setup if needed.
	switch cliBackend {
	case "cc":
		if !IsOrchSetupDone("cc") {
			if res, err := SetupCCIntegration(); err == nil && !res.AlreadyHadMCP {
				fmt.Printf("  qmax MCP entry added to %s\n", res.MCPPath)
				fmt.Println("  Claude Code now has qmax tools in every session.")
			}
		}
		cliAgent = NewCCAgent(FindClaudeCode(), appConfig.ModelOverride, appConfig.Effort, ctx)
	case "codex":
		if !IsOrchSetupDone("codex") {
			if res, err := SetupCodexIntegration(); err == nil && !res.AlreadyHadMCP {
				fmt.Printf("  qmax MCP entry added to %s\n", res.MCPPath)
			}
		}
		ca := NewCodexAgent(FindCodex(), appConfig.ModelOverride, appConfig.Effort, ctx)
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
		var session *Session
		var loadErr error
		if *resumeID == "last" {
			session, loadErr = LoadLastSession()
		} else {
			session, loadErr = LoadSession(*resumeID)
		}
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "Cannot resume session %q: %v\n", *resumeID, loadErr)
			os.Exit(1)
		}
		agent.history = session.Messages
		agent.usage = session.Usage
		if session.ProjectID > 0 {
			agent.config.Context.ProjectID = session.ProjectID
		}
		fmt.Printf("Resumed session %s (%d turns)\n", session.ID, session.Turns)
	}

	// Clean up old sessions (>7 days)
	if removed := CleanupOldSessions(); removed > 0 && *verbose {
		fmt.Printf("[cleanup] Removed %d old sessions\n", removed)
	}

	// Interactive REPL
	runREPL(agent, cliAgent, *quiet)
}

// resolveModel expands shorthand model names to full model IDs.
func resolveModel(m string) string {
	switch strings.ToLower(m) {
	case "sonnet":
		return "claude-sonnet-4-20250514"
	case "opus":
		return "claude-opus-4-20250514"
	case "haiku":
		return "claude-haiku-4-5-20251001"
	default:
		return m
	}
}

func runREPL(agent *Agent, cliAgent CLIAgent, quietMode bool) {
	term := NewTerminal()
	defer term.Close()
	if cliAgent != nil {
		defer cliAgent.Cleanup()
	}

	// Session ID for this conversation
	sessionID := generateSessionID()

	// Initialize structured logger
	agent.logger = NewLogger(sessionID)
	defer agent.logger.Close()

	// Graceful interrupt handling
	var (
		sigMu       sync.Mutex
		lastSigTime time.Time
	)

	autoSave := func() {
		if len(agent.history) > 0 && (agent.appConfig == nil || agent.appConfig.AutoSave) {
			_ = SaveSession(sessionID, agent.history, agent.config.Context.ProjectID, agent.usage, agent.config.Model)
		}
	}

	saveAndExit := func() {
		_ = SaveSession(sessionID, agent.history, agent.config.Context.ProjectID, agent.usage, agent.config.Model)
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
		fmt.Printf("  %sSession: %s%s\n", colorDim, sessionID, colorReset)

		// Hint about recent session if one exists
		if recent, err := ListSessions(1); err == nil && len(recent) > 0 {
			age := time.Since(recent[0].UpdatedAt)
			if age < 24*time.Hour {
				fmt.Printf("  %sRecent session: %s (%d turns, %s ago) — type /resume to continue%s\n",
					colorDim, recent[0].ID, recent[0].Turns, formatDuration(age), colorReset)
			}
		}
		fmt.Println()
	}
	term.SetSessionPrompt(sessionID)

	var inputHistory []string
	var lastCtrlC time.Time

	for {
		result := ReadInput(term.currentPrompt, inputHistory)

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

		input := strings.TrimSpace(result.Text)
		if input == "" {
			continue
		}
		inputHistory = append(inputHistory, input)

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
		case input == "/status":
			term.PrintStatusInfo(agent.config.Context, agent.usage, agent.config.Model)
			continue
		case input == "/cost":
			term.PrintCostSummary(agent.usage, agent.config.Model)
			continue
		case input == "/resume" || strings.HasPrefix(input, "/resume "):
			resumeTarget := strings.TrimPrefix(input, "/resume ")
			resumeTarget = strings.TrimSpace(resumeTarget)
			var session *Session
			var loadErr error
			if resumeTarget == "" || resumeTarget == "/resume" || resumeTarget == "last" {
				session, loadErr = LoadLastSession()
			} else {
				session, loadErr = LoadSession(resumeTarget)
			}
			if loadErr != nil {
				term.PrintError(fmt.Sprintf("Cannot resume: %v", loadErr))
				term.PrintSystem("Use /sessions to see available sessions")
			} else {
				sanitizeSessionMessages(session.Messages)
				agent.history = session.Messages
				agent.usage = session.Usage
				sessionID = session.ID
				if session.ProjectID > 0 {
					agent.config.Context.ProjectID = session.ProjectID
				}
				term.SetSessionPrompt(sessionID)
				term.PrintSystem(fmt.Sprintf("Resumed session %s (%d turns, project #%d)",
					session.ID, session.Turns, session.ProjectID))
			}
			continue
		case input == "/sessions":
			sessions, err := ListSessions(10)
			if err != nil || len(sessions) == 0 {
				term.PrintSystem("No saved sessions.")
			} else {
				fmt.Println()
				fmt.Printf("  %-10s  %-18s  %-6s  %-8s  %s\n", "ID", "Updated", "Turns", "Tokens", "Project")
				fmt.Printf("  %-10s  %-18s  %-6s  %-8s  %s\n", "----------", "------------------", "------", "--------", "-------")
				for _, s := range sessions {
					marker := " "
					if s.ID == sessionID {
						marker = "*"
					}
					fmt.Printf(" %s%-10s  %-18s  %-6d  %-8d  #%d\n",
						marker, s.ID, s.UpdatedAt.Format("2006-01-02 15:04"), s.Turns, s.Tokens, s.ProjectID)
				}
				fmt.Println()
				term.PrintSystem("Resume with: /resume <id>")
			}
			continue
		case input == "/save":
			if err := SaveSession(sessionID, agent.history, agent.config.Context.ProjectID, agent.usage, agent.config.Model); err != nil {
				term.PrintError(fmt.Sprintf("Failed to save: %v", err))
			} else {
				term.PrintSystem(fmt.Sprintf("Session %s saved.", sessionID))
			}
			continue
		case input == "/config":
			printConfigInfo(agent.appConfig, term)
			continue
		case input == "/set":
			term.PrintError("Usage: /set <key> <value>")
			term.PrintSystem("Keys: model, project, professional, autosave, budget, ollama, backend")
			continue
		case input == "/orch":
			// Show the unified model + effort TUI picker and apply the selection instantly.
			cfg := agent.appConfig
			if cfg == nil {
				term.PrintError("Config not loaded.")
				continue
			}

			result := ShowModelPicker(cfg.Backend, cfg.ModelOverride, cfg.Effort)
			if !result.Confirmed {
				continue
			}

			// Validate the chosen CLI is actually installed.
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

			// Tear down current CLI agent.
			if cliAgent != nil {
				cliAgent.Cleanup()
				cliAgent = nil
			}

			// One-time autosetup: write qmax into the CLI's global MCP config.
			if result.Backend != "" && !IsOrchSetupDone(result.Backend) {
				RunOrchSetup(result.Backend, term)
			}

			// Spin up the new agent with selected model + effort.
			switch result.Backend {
			case "cc":
				cliAgent = NewCCAgent(FindClaudeCode(), result.ModelID, result.Effort, agent.config.Context)
				term.PrintSystem(fmt.Sprintf("Backend: Claude Code  model: %s  effort: %s", result.ModelID, result.Effort))
			case "codex":
				ca := NewCodexAgent(FindCodex(), result.ModelID, result.Effort, agent.config.Context)
				if err := ca.writeMCPConfig(); err != nil {
					term.PrintSystem(fmt.Sprintf("Warning: Codex MCP config: %v", err))
				}
				cliAgent = ca
				term.PrintSystem(fmt.Sprintf("Backend: Codex  model: %s  effort: %s", result.ModelID, result.Effort))
			default:
				term.PrintSystem("Backend: Anthropic API (direct)")
			}

			cfg.Backend = result.Backend
			cfg.ModelOverride = result.ModelID
			cfg.Effort = result.Effort
			agent.config.Context.Backend = result.Backend
			_ = cfg.Save()
			continue

		case input == "/cc", input == "/codex", input == "/api":
			// Instant backend switching — no restart needed.
			cfg := agent.appConfig
			if cfg == nil {
				term.PrintError("Config not loaded.")
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
				if !IsOrchSetupDone("cc") {
					RunOrchSetup("cc", term)
				}
				cliAgent = NewCCAgent(bin, cfg.ModelOverride, cfg.Effort, agent.config.Context)
				cfg.Backend = "cc"
				_ = cfg.Save()
				term.PrintSystem(fmt.Sprintf("Backend → Claude Code (%s) — qmax tools globally registered", bin))

			case "codex":
				bin := FindCodex()
				if bin == "" {
					term.PrintError("'codex' CLI not found.")
					term.PrintSystem("  npm install -g @openai/codex")
					continue
				}
				if !IsOrchSetupDone("codex") {
					RunOrchSetup("codex", term)
				}
				ca := NewCodexAgent(bin, cfg.ModelOverride, cfg.Effort, agent.config.Context)
				if err := ca.writeMCPConfig(); err != nil {
					term.PrintSystem(fmt.Sprintf("Warning: MCP config: %v", err))
				}
				cliAgent = ca
				cfg.Backend = "codex"
				_ = cfg.Save()
				term.PrintSystem(fmt.Sprintf("Backend → Codex (%s) — qmax tools globally registered", bin))

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
				term.PrintSystem(fmt.Sprintf("Ollama: CHAT mode (%s) — chat via Gemma, tools via Claude", agent.ollama.model))
			case OllamaModeChat:
				agent.ollamaMode = OllamaModeFull
				term.PrintSystem(fmt.Sprintf("Ollama: FULL mode (%s) — everything via Gemma (no Claude)", agent.ollama.agentModel))
			case OllamaModeFull:
				agent.ollamaMode = OllamaModeOff
				term.PrintSystem("Ollama: OFF — all calls via Claude")
			}
			continue
		case strings.HasPrefix(input, "/set "):
			handleSetCommand(input, agent, term)
			continue
		case input == "/keys":
			handleKeys(agent, term)
			continue
		case input == "/screenshot":
			img, err := CaptureScreenshot()
			if err != nil {
				term.PrintError(err.Error())
				continue
			}
			term.PrintSystem(fmt.Sprintf("Screenshot captured (%s)", img.FileName))
			llmResult, err := agent.RunStreamingWithImages("Analyze this screenshot.", []ImageAttachment{*img}, term)
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
			img, imgErr := PasteImageFromClipboard()
			if imgErr == nil {
				term.PrintSystem(fmt.Sprintf("Pasted image from clipboard (%s)", img.FileName))
				llmResult, err := agent.RunStreamingWithImages("Analyze this pasted image.", []ImageAttachment{*img}, term)
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
			text, textErr := PasteTextFromClipboard()
			if textErr != nil || text == "" {
				term.PrintError("Nothing in clipboard")
				continue
			}
			term.PrintSystem(fmt.Sprintf("Pasted %d chars from clipboard", len(text)))
			input = text // fall through to normal processing
		}

		// Detect image file paths dragged/pasted into input
		cleanInput, images := DetectAndLoadImages(input)

		// Run through the LLM agent with streaming.
		// CC mode: delegate entirely to Claude Code subprocess (uses CC subscription).
		// Normal mode: use direct Anthropic API.
		var llmResult string
		var err error
		if cliAgent != nil {
			if len(images) > 0 {
				term.PrintSystem("Note: image attachments are not supported in CLI backend mode.")
			}
			llmResult, err = cliAgent.Run(cleanInput, term)
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
		if err != nil {
			term.PrintError(err.Error())
			CaptureError(err, map[string]interface{}{"input": truncateStr(input, 100)})
			autoSave() // save even on error — preserves context
			continue
		}

		if llmResult != "" {
			fmt.Println()
		}

		// Auto-save after every exchange for crash safety
		autoSave()
	}
}

// handleConnect runs the browser-based auth flow from within the REPL.
func handleConnect(agent *Agent, term *Terminal) {
	ctx := agent.config.Context

	// Already connected?
	if ctx.Auth != nil && ctx.Auth.IsAuthenticated() {
		term.PrintSystem(fmt.Sprintf("Already connected as %s", ctx.Auth.Email))
		term.PrintSystem("Run /disconnect first to switch accounts.")
		return
	}

	AnimateMax(MoodWaving, "Let's connect you to QualityMax!")
	fmt.Println()

	auth, err := LoginViaBrowser()
	if err != nil {
		AnimateMax(MoodSad, "Connection failed: "+err.Error())
		fmt.Println()
		term.PrintSystem("You can also paste an API key:")
		term.PrintSystem("  /set apikey qm-YOUR-API-KEY")
		return
	}

	// Update session context in-place
	ctx.Auth = auth
	ctx.API = NewAPIClient(auth)

	AnimateMax(MoodHappy, fmt.Sprintf("Connected as %s", auth.Email))
	fmt.Println()
}

// handleDisconnect removes auth and API client from the session.
func handleDisconnect(agent *Agent, term *Terminal) {
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
		_ = os.Remove(filepath.Join(home, qmaxCodeConfigDir, "auth.json"))
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

	AnimateMax(MoodNeutral, fmt.Sprintf("Disconnected from %s", email))
	fmt.Println()
	term.PrintSystem("Run /connect to log in again.")
}

// handleKeys provides an interactive TUI for managing API keys.
func handleKeys(agent *Agent, term *Terminal) {
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
		if err := SaveAnthropicKey(key); err != nil {
			term.PrintSystem(fmt.Sprintf("Key set for this session (keychain unavailable: %s)", err))
		} else {
			AnimateMax(MoodHappy, "Key saved to OS keychain!")
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
  /status        Connection status + session info
  /project <id>  Set the active project
  /context       Show current session context
  /cost          Show session token usage and estimated cost
  /orch          Cycle orchestration backend: off → CC → Codex → off
  /cc            Switch to Claude Code backend (CC subscription, no API tokens)
  /codex         Switch to Codex CLI backend (OpenAI subscription, no API tokens)
  /api           Switch back to direct Anthropic API
  /ollama        Toggle Ollama on/off (self-hosted LLM for chat)
  /config        Show current config settings
  /keys          Set API keys (interactive menu)
  /screenshot    Capture a screenshot and analyze it
  /paste         Paste from clipboard (image or text)
  /set <k> <v>   Update config (model, project, professional, autosave, budget, ollama)
  /save          Save current session
  /sessions      List recent sessions
  /resume [id]   Resume a session (default: last)
  /clear         Clear conversation history
  /help          Show this help
  /quit          Exit

Config examples:
  /set model sonnet         Change default model
  /set project 42           Change default project
  /set professional true    Disable cat personality
  /set autosave false       Disable auto-save on exit
  /set budget 100000        Set max token budget warning
  /set ollama on            Enable self-hosted LLM for chat (saves API costs)
  /set ollama off           Disable Ollama, use Claude for all calls
  /set backend cc           Use Claude Code subscription (no API key needed)
  /set backend codex        Use OpenAI Codex subscription (no API key needed)
  /set backend api          Use Anthropic API directly (default)

Shortcuts:
  Ctrl+C         Cancel current operation (double-tap to exit)

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

func printConfigInfo(cfg *Config, term *Terminal) {
	if cfg == nil {
		term.PrintSystem("No config loaded (using defaults).")
		return
	}
	fmt.Println()
	fmt.Printf("  %s\n", "qmax-code Config (~/.qmax-code/config.json)")
	fmt.Printf("  %-20s %s\n", "Default model:", cfg.DefaultModel)
	fmt.Printf("  %-20s %d\n", "Default project:", cfg.DefaultProject)
	fmt.Printf("  %-20s %v\n", "Professional:", cfg.Professional)
	fmt.Printf("  %-20s %v\n", "Auto-save:", cfg.AutoSave)
	fmt.Printf("  %-20s %d\n", "Token budget:", cfg.MaxTokenBudget)
	if cfg.OllamaURL != "" {
		fmt.Printf("  %-20s %s\n", "Ollama URL:", maskURL(cfg.OllamaURL))
		fmt.Printf("  %-20s %s\n", "Ollama model:", cfg.OllamaModel)
	} else {
		fmt.Printf("  %-20s %s\n", "Ollama:", "(not configured)")
	}
	fmt.Println()
}

func handleSetCommand(input string, agent *Agent, term *Terminal) {
	parts := strings.Fields(input)
	if len(parts) < 3 {
		term.PrintError("Usage: /set <key> <value>")
		term.PrintSystem("Keys: model, project, professional, autosave, budget")
		return
	}
	key := strings.ToLower(parts[1])
	value := parts[2]
	cfg := agent.appConfig
	if cfg == nil {
		cfg = defaultConfig()
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
		auth, err := LoginWithAPIKey(value)
		if err != nil {
			term.PrintError(fmt.Sprintf("Invalid API key: %v", err))
			return
		}
		agent.config.Context.Auth = auth
		agent.config.Context.API = NewAPIClient(auth)
		AnimateMax(MoodHappy, fmt.Sprintf("Connected as %s", auth.Email))
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
			term.PrintSystem(fmt.Sprintf("Ollama enabled: %s (%s)", maskURL(cfg.OllamaURL), cfg.OllamaModel))
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

	case "anthropic-key", "anthropic_key":
		// Save Anthropic API key to OS keychain
		os.Setenv("ANTHROPIC_API_KEY", value)
		agent.config.AnthropicKey = value
		if err := SaveAnthropicKey(value); err != nil {
			term.PrintSystem(fmt.Sprintf("Key set for this session (keychain: %s)", err))
		} else {
			term.PrintSystem("Anthropic API key saved to OS keychain.")
		}
		return // don't save to config.json — keychain handles it

	default:
		term.PrintError(fmt.Sprintf("Unknown config key: %s", key))
		term.PrintSystem("Keys: model, project, professional, autosave, budget, apikey, backend")
		return
	}

	// Persist to disk
	if err := cfg.Save(); err != nil {
		term.PrintError(fmt.Sprintf("Config updated in memory but failed to save: %v", err))
	} else {
		term.PrintSystem("Config saved to ~/.qmax-code/config.json")
	}
}

func printContext(ctx *SessionContext, term *Terminal) {
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
