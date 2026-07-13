package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	xterm "golang.org/x/term"

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
var Version = "1.20.7"

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
	saveSession := flag.Bool("save-session", false, "Save this session on exit (overrides auto-save setting)")
	verbose := flag.Bool("verbose", false, "Show tool calls and raw responses")
	professional := flag.Bool("professional", false, "Disable cat personality, be direct and professional")
	quiet := flag.Bool("q", false, "Quiet mode — no banner, minimal output (for CI)")
	showVersion := flag.Bool("version", false, "Show version")
	backendFlag := flag.String("backend", "", "Orchestration backend: cc, codex, cerebras, opencode, or api (overrides saved config)")
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

	// Attach a fresh local Codex login to the authenticated QualityMax user.
	if len(os.Args) > 1 && os.Args[1] == "codex" {
		if err := handleCodexCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		return
	}

	// Attach a fresh local Claude Code login to the authenticated QualityMax user.
	if len(os.Args) > 1 && os.Args[1] == "cc" {
		if err := handleCCCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
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

	// QUA-580: refuse interactive startup when stdin is not a terminal.
	// This must happen before any setup, consent, or API-key prompt, because
	// those paths are interactive too and can block forever when stdin is a
	// pipe. One-shot prompts (-p or positional args) remain valid headless use.
	if *oneShot == "" && len(flag.Args()) == 0 && !xterm.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "qmax-code: stdin is not a terminal.")
		fmt.Fprintln(os.Stderr, "  For non-interactive use, pass a prompt:")
		fmt.Fprintln(os.Stderr, "      qmax-code -p \"<your prompt>\"")
		fmt.Fprintln(os.Stderr, "      qmax-code \"<your prompt>\"")
		os.Exit(2)
	}

	// Load persistent user config
	appConfig := api.LoadQMaxCodeConfig()
	// --save-session is an explicit per-run opt-in. It must override a
	// persisted auto_save=false setting without changing that setting on disk.
	applySaveSessionFlag(appConfig, *saveSession)

	// Apply color theme before constructing any UI components.
	tui.ApplyTheme(tui.ThemeByName(appConfig.Theme))

	// Apply --professional flag (CLI flag overrides saved config)
	if *professional {
		appConfig.Professional = true
	}

	// --backend flag overrides saved config for this session only.
	if *backendFlag != "" {
		switch *backendFlag {
		case "cc", "codex", "cerebras", "opencode", "api", "":
			if *backendFlag == "api" {
				appConfig.Backend = ""
			} else {
				appConfig.Backend = *backendFlag
			}
		default:
			fmt.Fprintf(os.Stderr, "Error: --backend must be cc, codex, cerebras, opencode, or api\n")
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
	if !isValidModelName(effectiveModel) {
		fmt.Fprintf(os.Stderr, "Error: --model %q is not recognized.\n", *model)
		fmt.Fprintf(os.Stderr, "  Valid: %s.\n", api.ValidClaudeModelsHelp())
		os.Exit(2)
	}

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
	// Neither a QM-held Anthropic API key nor an OpenAI key is required. In cc
	// mode, qmax-code uses the user's Claude Code login via `claude --print`;
	// starting 2026-06-15, that traffic draws from the user's monthly Claude
	// Agent SDK credit before any extra-usage billing.
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
	} else if cliBackend == "cerebras" {
		// Cerebras drives the native qmax agent loop (full tool set, native
		// function calling) via its OpenAI-compatible API. No Anthropic key,
		// no external CLI, no MCP subprocess — qmax owns the loop directly.
		if appConfig.CerebrasKey == "" {
			fmt.Println()
			fmt.Println("  Cerebras API key needed (powers fast, low-cost inference).")
			fmt.Println("  Get one at: https://cloud.cerebras.ai")
			fmt.Println()
			key := setup.ReadSecret("  Paste your Cerebras key: ")
			if key != "" {
				if looks, verr := api.ValidateCerebrasKey(key); verr != nil {
					fmt.Fprintf(os.Stderr, "  That doesn't look like an API key: %v\n", verr)
				} else {
					if !looks {
						fmt.Println("  Note: key doesn't start with \"csk-\" — saving anyway.")
					}
					appConfig.CerebrasKey = key
					if err := api.SaveCerebrasKey(key); err == nil {
						fmt.Println("  Saved to OS keychain.")
					}
				}
			}
		}
		if appConfig.CerebrasKey == "" {
			fmt.Fprintln(os.Stderr, "\nError: Cerebras API key required for backend=cerebras.")
			fmt.Fprintln(os.Stderr, "  export CEREBRAS_API_KEY=csk-...")
			fmt.Fprintln(os.Stderr, "  Or switch backend: qmax-code config set backend api")
			os.Exit(1)
		}
		anthropicKey = "__cerebras_mode__" // skip Anthropic key gate below
	} else if cliBackend == "opencode" {
		openCodeBin := agent.FindOpenCode()
		if openCodeBin == "" {
			fmt.Fprintln(os.Stderr, "\nError: backend=opencode but 'opencode' CLI was not found.")
			fmt.Fprintln(os.Stderr, "  Install opencode: https://opencode.ai  (curl -fsSL https://opencode.ai/install | bash)")
			fmt.Fprintln(os.Stderr, "  Or switch backend: qmax-code config set backend api")
			os.Exit(1)
		}
		consent := setup.PromptOrchConsent(appConfig, "opencode")
		if !consent.Proceed {
			fmt.Fprintln(os.Stderr, "  opencode backend not activated. Falling back to direct API.")
			cliBackend = ""
			appConfig.Backend = ""
		} else {
			appConfig.OrchPermissionMode = consent.PermissionMode
			_ = appConfig.Save()
			anthropicKey = "__opencode_mode__" // skip Anthropic key gate below
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
		fmt.Fprintln(os.Stderr, "    qmax-code config set backend cc      # Claude Code login / Agent SDK credit")
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
	// Cerebras backend takes precedence: when selected it owns every turn
	// (native function calling over the full tool set), so leave Ollama mode off.
	if appConfig.Backend == "cerebras" {
		ag.Cerebras = agent.NewCerebrasClient(appConfig)
	} else if ag.Ollama != nil {
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
		// Skills sync every launch (idempotent) so an upgraded catalog reaches
		// users who already have the MCP installed. Independent of IsOrchInstalled.
		if appConfig.OrchGlobalInstall {
			_, _ = setup.InstallSkills("cc")
		}
		cliAgent = agent.NewCCAgent(agent.FindClaudeCode(), appConfig.ModelOverride, appConfig.Effort, appConfig.OrchPermissionMode, appConfig.OutputVerbose, ctx)
	case "codex":
		if appConfig.OrchGlobalInstall && !setup.IsOrchInstalled("codex") {
			if res, err := setup.InstallCodex(); err == nil && !res.AlreadyHadMCP {
				fmt.Printf("  qmax MCP entry added to %s\n", res.MCPPath)
			}
		}
		// Skills sync every launch (idempotent), independent of IsOrchInstalled.
		if appConfig.OrchGlobalInstall {
			_, _ = setup.InstallSkills("codex")
		}
		ca := agent.NewCodexAgent(agent.FindCodex(), appConfig.ModelOverride, appConfig.Effort, appConfig.OrchPermissionMode, appConfig.OutputVerbose, ctx)
		if err := ca.WriteMCPConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write Codex MCP config: %v\n", err)
		}
		cliAgent = ca
	case "opencode":
		oc := agent.NewOpenCodeAgent(agent.FindOpenCode(), appConfig.ModelOverride, appConfig.Effort, appConfig.OrchPermissionMode, appConfig.OutputVerbose, appConfig, ctx)
		if _, err := agent.WriteOpenCodeConfig(appConfig, ctx, appConfig.OrchPermissionMode); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write opencode config: %v\n", err)
		}
		cliAgent = oc
	}

	// One-shot mode and positional-arg mode honour the configured CLI backend.
	// Prior to the QUA-576 fix these paths always called ag.Run (Anthropic API),
	// silently ignoring backend=cc and backend=codex — which meant every CI /
	// scripting / cloud-session invocation bypassed the cc backend and hit the
	// Anthropic API. Now we route through cliAgent when configured, falling
	// back to the API agent only when no CLI backend is active.
	// One-shot sessions only accumulate into ag.History via the Cerebras and
	// direct-API branches below (cliAgent runs cc/codex as a separate process
	// and manages its own native --resume state, so there's nothing here for
	// qmax-code's session store to persist). Generate the ID up front so a
	// crash mid-run still leaves a partial session file behind.
	oneShotSessionID := session.GenerateSessionID()
	saveOneShotSession := func() {
		if !shouldSaveOneShotSession(appConfig.AutoSave, ag.History) {
			return
		}
		if err := session.SaveSession(oneShotSessionID, ag.History, ag.Cfg.Context.ProjectID, ag.Usage, ag.Cfg.Model); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save session: %v\n", err)
		}
	}

	runOneShot := func(prompt string) error {
		if cliAgent != nil {
			// CLI backends stream their own output (tool icons, glamour-rendered
			// text) via the Terminal; FinishMarkdown handles final rendering.
			// Build a Terminal here since main never created one (repl.Run owns
			// the interactive Terminal instance).
			term := tui.NewTerminal()
			defer term.Close()
			_, err := cliAgent.Run(prompt, term)
			return err
		}
		if ag.Cerebras != nil {
			// Cerebras runs the full native tool loop, streaming UI to a Terminal.
			// repl.Run owns the Logger in interactive mode; create one here so the
			// one-shot tool loop (which logs each tool call) doesn't nil-panic.
			if ag.Logger == nil {
				ag.Logger = sysutil.NewLogger("oneshot")
				defer ag.Logger.Close()
			}
			term := tui.NewTerminal()
			defer term.Close()
			_, err := ag.RunStreaming(prompt, term)
			saveOneShotSession()
			return err
		}
		// Direct-API path: non-streaming. Print the returned text ourselves.
		result, err := ag.Run(prompt)
		saveOneShotSession()
		if err != nil {
			return err
		}
		fmt.Println(result)
		return nil
	}

	if *oneShot != "" {
		if err := runOneShot(*oneShot); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Also handle positional args as a prompt: qmax-code "test the login flow"
	if remaining := flag.Args(); len(remaining) > 0 {
		if err := runOneShot(strings.Join(remaining, " ")); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
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
	return api.ResolveClaudeModel(m)
}

// applySaveSessionFlag applies the explicit per-run save-session override.
// Keeping this separate makes the flag behavior testable without starting the
// interactive terminal or loading credentials.
func applySaveSessionFlag(cfg *api.Config, enabled bool) {
	if enabled {
		cfg.AutoSave = true
	}
}

// shouldSaveOneShotSession gates the one-shot session write: only persist
// when auto-save is on (--save-session forces this via applySaveSessionFlag)
// and there's actually a conversation to save.
func shouldSaveOneShotSession(autoSave bool, history []api.Message) bool {
	return autoSave && len(history) > 0
}

// isValidModelName reports whether m is a recognized model identifier.
// QUA-579: pre-fix, any string was forwarded to the API and produced a
// confusing 401/400. Now we fail fast with a list of valid choices.
func isValidModelName(m string) bool {
	return api.IsValidClaudeModelName(m)
}
