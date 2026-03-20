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
	Version = "1.0.0"
	Name    = "qmax-code"
)

func main() {
	// Flags
	projectID := flag.Int("project-id", 0, "Default project ID for this session")
	model := flag.String("model", "auto", "Claude model: auto (haiku+sonnet), sonnet, opus, haiku, or full ID")
	apiKey := flag.String("api-key", "", "Anthropic API key (or set ANTHROPIC_API_KEY)")
	cloudURL := flag.String("cloud-url", "", "QualityMax cloud URL (or use qmax login)")
	oneShot := flag.String("p", "", "Run a single prompt and exit (non-interactive)")
	verbose := flag.Bool("verbose", false, "Show tool calls and raw responses")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s v%s\n", Name, Version)
		return
	}

	// Resolve model shorthand
	*model = resolveModel(*model)

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

	// Build session context
	ctx := &SessionContext{
		ProjectID: *projectID,
		QMaxCfg:   qmaxCfg,
		QMaxBin:   qmaxBin,
		QMaxInfo:  probeQMaxStatus(qmaxBin),
	}

	// Build agent with smart model routing
	autoRoute := *model == "auto"
	var baseModel, chatModel string
	if autoRoute {
		baseModel = ModelSonnet
		chatModel = ModelHaiku
	} else {
		baseModel = resolveModel(*model)
		chatModel = baseModel
	}

	agent := NewAgent(AgentConfig{
		AnthropicKey: anthropicKey,
		Model:        baseModel,
		ChatModel:    chatModel,
		Verbose:      *verbose,
		Context:      ctx,
		AutoRoute:    autoRoute,
	})

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

	// Interactive REPL
	runREPL(agent)
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

func runREPL(agent *Agent) {
	term := NewTerminal()
	defer term.Close()

	// Graceful interrupt handling
	var (
		sigMu       sync.Mutex
		lastSigTime time.Time
	)

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
					fmt.Println("\nGoodbye!")
					term.Close()
					os.Exit(0)
				}
				lastSigTime = now
			} else {
				// SIGTERM: exit
				sigMu.Unlock()
				fmt.Println("\nGoodbye!")
				term.Close()
				os.Exit(0)
			}
			sigMu.Unlock()
		}
	}()

	// Welcome
	term.PrintBanner(Version, agent.config.Context)

	for {
		input, err := term.ReadLine()
		if err != nil {
			break // EOF or error
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		// Built-in commands
		switch {
		case input == "/quit" || input == "/exit" || input == "/q":
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
		}

		// Run through the LLM agent with streaming
		result, err := agent.RunStreaming(input, term)
		if err != nil {
			term.PrintError(err.Error())
			continue
		}

		if result != "" {
			fmt.Println()
		}
	}
}

func printREPLHelp() {
	fmt.Println(`
Commands:
  /project <id>  Set the active project
  /context       Show current session context
  /status        Show qmax auth + session info
  /cost          Show session token usage and estimated cost
  /clear         Clear conversation history
  /help          Show this help
  /quit          Exit

Shortcuts:
  Ctrl+C         Cancel current operation (double-tap to exit)

Models (--model flag):
  auto            Smart routing: haiku for chat, sonnet for tools (default)
  sonnet          Claude Sonnet (all requests)
  opus            Claude Opus (most capable, all requests)
  haiku           Claude Haiku (cheapest, all requests)

Examples:
  "test the login flow"
  "what's our test coverage?"
  "crawl staging.myapp.com and generate tests"
  "run all tests and show failures"
  "import https://github.com/user/repo and review it"
  "create a PR with the generated tests"`)
}

func printContext(ctx *SessionContext, term *Terminal) {
	term.PrintSystem(fmt.Sprintf("Project: #%d", ctx.ProjectID))
	if ctx.QMaxCfg.CloudURL != "" {
		term.PrintSystem(fmt.Sprintf("Cloud: %s", ctx.QMaxCfg.CloudURL))
	}
	if ctx.QMaxBin != "" {
		term.PrintSystem(fmt.Sprintf("qmax binary: %s", ctx.QMaxBin))
	}
	term.PrintSystem(fmt.Sprintf("Authenticated: %v", ctx.QMaxCfg.Token != ""))
}
