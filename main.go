package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/mcp"
	"github.com/qualitymax/qmax-code/internal/repl"
	"github.com/qualitymax/qmax-code/internal/session"
	"github.com/qualitymax/qmax-code/internal/setup"
	"github.com/qualitymax/qmax-code/internal/sysutil"
	"github.com/qualitymax/qmax-code/internal/tui"
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
			mcp.RunServer(Version)
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
			cfg, err = setup.LoginViaBrowser()
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
		setupAuth, setupProjectID := setup.RunInteractive()
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
	var cliAgent agent.CLIAgent
	cliBackend := appConfig.Backend // "cc" | "codex" | "" (API)

	if cliBackend == "cc" {
		claudeBin := agent.FindClaudeCode()
		if claudeBin == "" {
			fmt.Fprintln(os.Stderr, "\nError: backend=cc but 'claude' CLI was not found.")
			fmt.Fprintln(os.Stderr, "  Install Claude Code: https://claude.ai/download")
			fmt.Fprintln(os.Stderr, "  Or switch backend: qmax-code config set backend api")
			os.Exit(1)
		}
		consent := setup.PromptOrchConsent(appConfig, "cc")
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
		codexBin := agent.FindCodex()
		if codexBin == "" {
			fmt.Fprintln(os.Stderr, "\nError: backend=codex but 'codex' CLI was not found.")
			fmt.Fprintln(os.Stderr, "  Install Codex CLI: npm install -g @openai/codex")
			fmt.Fprintln(os.Stderr, "  Or switch backend: qmax-code config set backend api")
			os.Exit(1)
		}
		consent := setup.PromptOrchConsent(appConfig, "codex")
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
		key := setup.ReadSecret("  Paste your Anthropic key: ")
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

	ag := agent.NewAgent(agent.AgentConfig{
		AnthropicKey:  anthropicKey,
		Model:         baseModel,
		ChatModel:     chatModel,
		Verbose:       *verbose,
		OutputVerbose: appConfig.OutputVerbose,
		Context:       ctx,
		AutoRoute:     autoRoute,
		Professional:  appConfig.Professional,
	})
	ag.AppConfig = appConfig
	ag.Ollama = agent.NewOllamaClient(appConfig)
	if ag.Ollama != nil {
		ag.Mode = agent.OllamaModeFull // default to full when configured
	}

	// Build the CLI agent if a CLI backend was selected and consented to above.
	// Global MCP install (~/.claude/settings.json or ~/.codex/config.toml) is
	// performed only when the user opted into it during the consent prompt.
	switch cliBackend {
	case "cc":
		if appConfig.OrchGlobalInstall && !setup.IsOrchInstalled("cc") {
			if res, err := setup.InstallCC(); err == nil && !res.AlreadyHadMCP {
				fmt.Printf("  qmax MCP entry added to %s\n", res.MCPPath)
			}
		}
		cliAgent = agent.NewCCAgent(agent.FindClaudeCode(), appConfig.ModelOverride, appConfig.Effort, appConfig.OrchPermissionMode, appConfig.OutputVerbose, ctx)
	case "codex":
		if appConfig.OrchGlobalInstall && !setup.IsOrchInstalled("codex") {
			if res, err := setup.InstallCodex(); err == nil && !res.AlreadyHadMCP {
				fmt.Printf("  qmax MCP entry added to %s\n", res.MCPPath)
			}
		}
		ca := agent.NewCodexAgent(agent.FindCodex(), appConfig.ModelOverride, appConfig.Effort, appConfig.OrchPermissionMode, appConfig.OutputVerbose, ctx)
		if err := ca.WriteMCPConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write Codex MCP config: %v\n", err)
		}
		cliAgent = ca
	}

	// One-shot mode
	if *oneShot != "" {
		result, err := ag.Run(*oneShot)
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
		result, err := ag.Run(prompt)
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
		ag.History = sess.Messages
		ag.Usage = sess.Usage
		if sess.ProjectID > 0 {
			ag.Cfg.Context.ProjectID = sess.ProjectID
		}
		fmt.Printf("Resumed session %s (%d turns)\n", sess.ID, sess.Turns)
	}

	// Clean up old sessions (>7 days)
	if removed := session.CleanupOldSessions(); removed > 0 && *verbose {
		fmt.Printf("[cleanup] Removed %d old sessions\n", removed)
	}

	// Sweep orphaned MCP config files left by crashed previous instances.
	agent.CleanupStaleMCPConfigs()

	// Interactive REPL
	repl.Run(ag, cliAgent, *quiet, Version)
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

