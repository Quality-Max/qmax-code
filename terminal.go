package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/chzyer/readline"
)

// ANSI color codes (kept for prompt and readline which don't use lipgloss)
const (
	colorReset   = "\033[0m"
	colorBold    = "\033[1m"
	colorDim     = "\033[2m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
)

// Lipgloss styles
var (
	styleTool = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)

	styleToolDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242"))

	styleError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	styleSystem = lipgloss.NewStyle().
			Foreground(lipgloss.Color("69")).
			Bold(true)

	styleSuccess = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82"))

	styleDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242"))

	styleUsage = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

// Tool emojis — cats hunt bugs, so these are Max-approved
var toolIcons = map[string]string{
	"list_projects":      "📋",
	"list_test_cases":    "🧪",
	"list_scripts":       "📜",
	"generate_test_code": "⚡",
	"run_test":           "🐾",
	"run_tests_batch":    "🐾",
	"check_test_status":  "👁️",
	"start_crawl":        "🐈",
	"crawl_status":       "🔍",
	"crawl_results":      "🎯",
	"list_crawl_jobs":    "📋",
	"list_repos":         "📦",
	"review_repo":        "🔬",
	"repo_coverage":      "📊",
	"repo_quality":       "✨",
	"import_repo":        "📥",
	"import_document":    "📄",
	"create_pr":          "🔀",
	"read_file":          "👀",
	"run_command":        "💻",
	"write_file":         "📝",
	"get_script":         "📖",
	"update_script":      "🔧",
	"rollback_script":    "⏪",
}

// Terminal handles all user-facing I/O with colors, glamour, and personality.
type Terminal struct {
	rl       *readline.Instance
	renderer *glamour.TermRenderer
	streaming bool // true when we're in the middle of streaming text
}

// NewTerminal creates a new interactive terminal with markdown rendering.
func NewTerminal() *Terminal {
	// Build completer for readline
	completer := readline.NewPrefixCompleter(
		readline.PcItem("/help"),
		readline.PcItem("/quit"),
		readline.PcItem("/clear"),
		readline.PcItem("/status"),
		readline.PcItem("/cost"),
		readline.PcItem("/config"),
		readline.PcItem("/sessions"),
		readline.PcItem("/resume"),
		readline.PcItem("/save"),
		readline.PcItem("/project"),
		readline.PcItem("/set",
			readline.PcItem("model",
				readline.PcItem("auto"),
				readline.PcItem("sonnet"),
				readline.PcItem("opus"),
				readline.PcItem("haiku"),
			),
			readline.PcItem("project"),
			readline.PcItem("professional",
				readline.PcItem("true"),
				readline.PcItem("false"),
			),
			readline.PcItem("autosave",
				readline.PcItem("true"),
				readline.PcItem("false"),
			),
			readline.PcItem("budget"),
		),
	)

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          fmt.Sprintf("%s%sqmax%s %s>%s ", colorBold, colorCyan, colorReset, colorMagenta, colorReset),
		HistoryFile:     "/tmp/qmax-code-history",
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
		AutoComplete:    completer,
	})
	if err != nil {
		rl, _ = readline.New("> ")
	}

	// Create glamour renderer for markdown
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
	)
	if err != nil {
		// Fallback: no markdown rendering
		renderer = nil
	}

	return &Terminal{
		rl:       rl,
		renderer: renderer,
	}
}

// PrintSlashMenu shows available commands in a compact vertical list.
func (t *Terminal) PrintSlashMenu() {
	commands := []struct {
		cmd  string
		desc string
	}{
		{"/help", "Show help"},
		{"/status", "Auth + session info"},
		{"/cost", "Token usage + cost"},
		{"/config", "Show config"},
		{"/sessions", "List saved sessions"},
		{"/resume", "Resume a session"},
		{"/save", "Save current session"},
		{"/project", "Set active project"},
		{"/set", "Update config"},
		{"/clear", "Clear history"},
		{"/quit", "Exit"},
	}
	fmt.Println()
	for _, c := range commands {
		fmt.Printf("  %s%-12s%s %s%s%s\n",
			colorCyan, c.cmd, colorReset,
			colorDim, c.desc, colorReset)
	}
	fmt.Println()
}

// SetSessionPrompt updates the prompt to include the session ID.
func (t *Terminal) SetSessionPrompt(sessionID string) {
	t.rl.SetPrompt(fmt.Sprintf("%s%sqmax%s %s[%s]%s %s>%s ",
		colorBold, colorCyan, colorReset,
		colorDim, sessionID, colorReset,
		colorMagenta, colorReset))
}

// Close cleans up the terminal.
func (t *Terminal) Close() {
	if t.rl != nil {
		t.rl.Close()
	}
}

// ReadLine reads a line of user input.
func (t *Terminal) ReadLine() (string, error) {
	return t.rl.Readline()
}

// PrintBanner shows the startup banner — fun, geeky, and cat-themed.
func (t *Terminal) PrintBanner(version string, ctx *SessionContext) {
	banner := fmt.Sprintf(`
  %s%s ██████╗ ███╗   ███╗ █████╗ ██╗  ██╗%s
  %s%s██╔═══██╗████╗ ████║██╔══██╗╚██╗██╔╝%s    %s /\_/\%s
  %s%s██║   ██║██╔████╔██║███████║ ╚███╔╝ %s    %s( o.o )%s
  %s%s██║▄▄ ██║██║╚██╔╝██║██╔══██║ ██╔██╗ %s    %s > ^ <%s
  %s%s╚██████╔╝██║ ╚═╝ ██║██║  ██║██╔╝ ██╗%s    %s/|   |\%s
  %s%s ╚══▀▀═╝ ╚═╝     ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝%s   %s(_|   |_) meow.%s
  %s%s                code %s%s
`,
		colorBold, colorCyan, colorReset,
		colorBold, colorCyan, colorReset, colorYellow, colorReset,
		colorBold, colorCyan, colorReset, colorYellow, colorReset,
		colorBold, colorCyan, colorReset, colorYellow, colorReset,
		colorBold, colorCyan, colorReset, colorYellow, colorReset,
		colorBold, colorCyan, colorReset, colorYellow, colorReset,
		colorBold, colorMagenta, version, colorReset,
	)
	fmt.Print(banner)

	// Cat-themed geeky subtitles
	subtitles := []string{
		"*knocks bugs off the table* — Max, QA cat 🐾",
		"Curiosity caught the bug. Max fixed it.",
		"I haz tests. They pass. You may pet me now.",
		"If it fits, I sits. If it breaks, I test it.",
		"Purrfect coverage or I will sit on your keyboard.",
		"*pushes failing test off desk* Works on my machine.",
		"sudo make tests-pass && treat --tuna",
		"I've seen things you people wouldn't believe... like tests that pass on the first try.",
		"Schrodinger's test: both passing and failing until you observe it.",
		"Named after a real cat. Powered by a real AI. Both knock things over.",
		"Napping is just background processing. 💤",
		"Nine lives, zero regressions.",
	}
	idx := time.Now().YearDay() % len(subtitles)
	fmt.Printf("  %s%s%s\n\n", colorDim, subtitles[idx], colorReset)

	// Show context info
	if ctx.ProjectID > 0 {
		fmt.Printf("  %s▸ Project #%d active%s\n", colorGreen, ctx.ProjectID, colorReset)
	}

	if ctx.QMaxBin != "" {
		fmt.Printf("  %s▸ qmax CLI: %s%s\n", colorGreen, ctx.QMaxBin, colorReset)
		if ctx.QMaxCfg.Email != "" {
			fmt.Printf("  %s▸ Logged in as: %s%s\n", colorGreen, ctx.QMaxCfg.Email, colorReset)
		}
		if ctx.QMaxCfg.CloudURL != "" {
			fmt.Printf("  %s▸ API: %s%s\n", colorDim, ctx.QMaxCfg.CloudURL, colorReset)
		}
	} else {
		fmt.Printf("  %s▸ qmax CLI not found%s — tools that need it will show install instructions\n", colorYellow, colorReset)
	}

	fmt.Printf("  %sType /help for commands or just tell me what to test. 🐾%s\n\n", colorDim, colorReset)
}

// StreamText prints text as it arrives from the SSE stream (token-by-token).
func (t *Terminal) StreamText(text string) {
	if !t.streaming {
		t.streaming = true
		fmt.Println() // newline before assistant response
	}
	fmt.Print(text)
}

// FinishMarkdown is called when a text block is complete.
// We already streamed the raw text, so now we just mark streaming as done.
// Glamour rendering happens for the full response, not mid-stream.
func (t *Terminal) FinishMarkdown(fullText string) {
	if t.streaming {
		t.streaming = false
		// We already printed the raw text via StreamText.
		// For a clean look, add a trailing newline if needed.
		if !strings.HasSuffix(fullText, "\n") {
			fmt.Println()
		}
	}
}

// PrintAssistant prints the agent's text response with markdown rendering.
// Used in non-streaming mode.
func (t *Terminal) PrintAssistant(text string) {
	if t.renderer != nil {
		rendered, err := t.renderer.Render(text)
		if err == nil {
			fmt.Print(rendered)
			return
		}
	}
	fmt.Println(text)
}

// PrintToolIcon shows a tool icon when a tool_use block starts streaming.
func (t *Terminal) PrintToolIcon(name string) {
	if t.streaming {
		t.streaming = false
		fmt.Println()
	}
	icon := toolIcons[name]
	if icon == "" {
		icon = "🔧"
	}
	displayName := strings.ReplaceAll(name, "_", " ")
	fmt.Printf("\n  %s %s", icon, styleTool.Render(displayName))
}

// PrintToolStart shows a tool invocation with its input summary.
func (t *Terminal) PrintToolStart(name string, input interface{}) {
	summary := formatToolInput(input)
	if summary != "" {
		fmt.Printf(" %s", styleToolDim.Render(summary))
	}
	fmt.Println()
}

// PrintToolResult shows tool output (abbreviated).
func (t *Terminal) PrintToolResult(name string, output string) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	lineCount := len(lines)

	if strings.HasPrefix(output, "{\"error\"") {
		fmt.Printf("  %s %s\n", styleError.Render("✗ Error"), styleDim.Render(truncateStr(output, 120)))
		return
	}

	if lineCount <= 3 {
		for _, line := range lines {
			fmt.Printf("  %s\n", styleDim.Render(truncateStr(line, 140)))
		}
	} else {
		fmt.Printf("  %s\n", styleSuccess.Render(fmt.Sprintf("✓ %d lines of output", lineCount)))
	}
}

// PrintSystem prints a system message.
func (t *Terminal) PrintSystem(msg string) {
	fmt.Printf("  %s %s\n", styleSystem.Render("●"), msg)
}

// PrintError prints an error message.
func (t *Terminal) PrintError(msg string) {
	fmt.Printf("  %s\n", styleError.Render("✗ "+msg))
}

// PrintTokenUsage shows token usage in dim text after a response.
func (t *Terminal) PrintTokenUsage(usage TokenUsage) {
	fmt.Printf("\n%s\n", styleUsage.Render(
		fmt.Sprintf("  tokens: %d in / %d out (session: %d total, %d requests)",
			usage.InputTokens, usage.OutputTokens, usage.TotalTokens(), usage.Requests)))
}

// PrintCostSummary shows detailed cost info for /cost command.
func (t *Terminal) PrintCostSummary(usage TokenUsage, model string) {
	cost := usage.EstimatedCost(model)
	fmt.Printf("\n")
	fmt.Printf("  %s\n", styleSystem.Render("Session Token Usage"))
	fmt.Printf("  %-20s %d\n", "Input tokens:", usage.InputTokens)
	fmt.Printf("  %-20s %d\n", "Output tokens:", usage.OutputTokens)
	fmt.Printf("  %-20s %d\n", "Total tokens:", usage.TotalTokens())
	fmt.Printf("  %-20s %d\n", "API requests:", usage.Requests)
	fmt.Printf("  %-20s $%.4f\n", "Estimated cost:", cost)
	fmt.Printf("  %-20s %s\n", "Model:", model)
	fmt.Println()
}

// PrintStatusInfo shows qmax status and session info for /status command.
func (t *Terminal) PrintStatusInfo(ctx *SessionContext, usage TokenUsage, model string) {
	fmt.Println()
	fmt.Printf("  %s\n", styleSystem.Render("qmax-code Status"))

	if ctx.QMaxBin != "" {
		fmt.Printf("  %-20s %s\n", "qmax binary:", ctx.QMaxBin)
	} else {
		fmt.Printf("  %-20s %s\n", "qmax binary:", styleError.Render("not found"))
	}

	if ctx.QMaxCfg.Email != "" {
		fmt.Printf("  %-20s %s\n", "Logged in as:", ctx.QMaxCfg.Email)
	} else {
		fmt.Printf("  %-20s %s\n", "Auth:", styleDim.Render("not authenticated"))
	}

	if ctx.QMaxCfg.CloudURL != "" {
		fmt.Printf("  %-20s %s\n", "API URL:", ctx.QMaxCfg.CloudURL)
	}

	fmt.Printf("  %-20s #%d\n", "Active project:", ctx.ProjectID)
	fmt.Printf("  %-20s %s\n", "Model:", model)
	fmt.Printf("  %-20s %d in / %d out\n", "Session tokens:", usage.InputTokens, usage.OutputTokens)
	fmt.Printf("  %-20s $%.4f\n", "Est. cost:", usage.EstimatedCost(model))
	fmt.Println()
}

// formatToolInput creates a compact summary of tool input.
func formatToolInput(input interface{}) string {
	m, ok := input.(map[string]interface{})
	if !ok || len(m) == 0 {
		return ""
	}

	var parts []string
	for k, v := range m {
		s := fmt.Sprintf("%v", v)
		if len(s) > 40 {
			s = s[:37] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, s))
	}
	return strings.Join(parts, " ")
}

// TestResult represents a single test result for visualization.
type TestResult struct {
	Name     string
	Passed   bool
	Duration float64
	Error    string
}

// PrintTestResults shows a colored pass/fail table of test results.
func (t *Terminal) PrintTestResults(results []TestResult) {
	if len(results) == 0 {
		return
	}

	passed, failed := 0, 0
	var totalDuration float64
	for _, r := range results {
		if r.Passed {
			passed++
		} else {
			failed++
		}
		totalDuration += r.Duration
	}

	// Summary line
	status := styleSuccess.Render("✅ ALL PASSED")
	if failed > 0 {
		status = styleError.Render(fmt.Sprintf("❌ %d FAILED", failed))
	}
	fmt.Printf("\n  %s  %d/%d passed (%.1fs)\n\n", status, passed, len(results), totalDuration)

	// Individual results
	for _, r := range results {
		icon := styleSuccess.Render("✓")
		if !r.Passed {
			icon = styleError.Render("✗")
		}
		dur := styleDim.Render(fmt.Sprintf("%.1fs", r.Duration))
		fmt.Printf("  %s %s %s\n", icon, r.Name, dur)
		if !r.Passed && r.Error != "" {
			errLine := r.Error
			if len(errLine) > 100 {
				errLine = errLine[:97] + "..."
			}
			fmt.Printf("    %s\n", styleError.Render(errLine))
		}
	}
	fmt.Println()
}

// truncateStr limits a string to maxLen characters.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
