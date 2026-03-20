package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const (
	Version = "0.1.0"
	Name    = "qmax-code"
)

func main() {
	// Flags
	projectID := flag.Int("project-id", 0, "Default project ID for this session")
	model := flag.String("model", "claude-sonnet-4-20250514", "Claude model to use")
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

	// Build session context
	ctx := &SessionContext{
		ProjectID: *projectID,
		QMaxCfg:   qmaxCfg,
	}

	// Build agent
	agent := NewAgent(AgentConfig{
		AnthropicKey: anthropicKey,
		Model:        *model,
		Verbose:      *verbose,
		Context:      ctx,
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

func runREPL(agent *Agent) {
	term := NewTerminal()
	defer term.Close()

	// Handle Ctrl+C gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nGoodbye!")
		term.Close()
		os.Exit(0)
	}()

	// Welcome
	term.PrintBanner(Version, agent.config.Context.ProjectID)

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
		}

		// Run through the LLM agent
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
  /clear         Clear conversation history
  /help          Show this help
  /quit          Exit

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
	term.PrintSystem(fmt.Sprintf("Authenticated: %v", ctx.QMaxCfg.Token != ""))
}
