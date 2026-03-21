package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	Version = "1.1.5"
	Name    = "qmax-code"
)

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
	flag.Parse()
	_ = quiet // reserved for future CI mode

	if *showVersion {
		fmt.Printf("%s v%s\n", Name, Version)
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

	// Resolve model: CLI flag > saved config > "auto"
	effectiveModel := *model
	if effectiveModel == "" {
		effectiveModel = appConfig.DefaultModel
	}
	if effectiveModel == "" {
		effectiveModel = "auto"
	}
	effectiveModel = resolveModel(effectiveModel)

	// Resolve Anthropic API key
	anthropicKey := *apiKey
	if anthropicKey == "" {
		anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if anthropicKey == "" {
		fmt.Fprintln(os.Stderr, "Error: Anthropic API key required.")
		fmt.Fprintln(os.Stderr, "Set ANTHROPIC_API_KEY or use --api-key")
		os.Exit(1)
	}

	// Load qmax config for cloud URL and auth token
	qmaxCfg := loadQMaxConfig()
	if *cloudURL != "" {
		qmaxCfg.CloudURL = *cloudURL
	}

	// Discover qmax binary
	qmaxBin := discoverQMaxBinary()

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
	runREPL(agent, *quiet)
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

func runREPL(agent *Agent, quietMode bool) {
	term := NewTerminal()
	defer term.Close()

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
		autoSave()
		if len(agent.history) > 0 {
			fmt.Printf("Session %s saved.\n", sessionID)
		}
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

	for {
		input, err := term.ReadLine()
		if err != nil {
			saveAndExit()
			break // EOF or error
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		// Interactive slash menu
		if input == "/" {
			selected := RunSlashMenu()
			if selected != "" {
				input = selected
			} else {
				continue
			}
		}

		// Built-in commands
		switch {
		case input == "/quit" || input == "/exit" || input == "/q":
			saveAndExit()
			fmt.Println("Goodbye!")
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
		case strings.HasPrefix(input, "/set "):
			handleSetCommand(input, agent, term)
			continue
		}

		// Run through the LLM agent with streaming
		result, err := agent.RunStreaming(input, term)
		if err != nil {
			term.PrintError(err.Error())
			autoSave() // save even on error — preserves context
			continue
		}

		if result != "" {
			fmt.Println()
		}

		// Auto-save after every exchange for crash safety
		autoSave()
	}
}

func printREPLHelp() {
	fmt.Println(`
Commands:
  /project <id>  Set the active project
  /context       Show current session context
  /status        Show qmax auth + session info
  /cost          Show session token usage and estimated cost
  /config        Show current config settings
  /set <k> <v>   Update config (model, project, professional, autosave, budget)
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

	default:
		term.PrintError(fmt.Sprintf("Unknown config key: %s", key))
		term.PrintSystem("Keys: model, project, professional, autosave, budget")
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
