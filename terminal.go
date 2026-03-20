package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/chzyer/readline"
)

// ANSI color codes
const (
	colorReset   = "\033[0m"
	colorBold    = "\033[1m"
	colorDim     = "\033[2m"
	colorItalic  = "\033[3m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
	colorWhite   = "\033[37m"
	colorBgBlue  = "\033[44m"
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
}

// Terminal handles all user-facing I/O with colors and personality.
type Terminal struct {
	rl *readline.Instance
}

// NewTerminal creates a new interactive terminal.
func NewTerminal() *Terminal {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          fmt.Sprintf("%s%sqmax%s %s🐾%s ", colorBold, colorCyan, colorReset, colorMagenta, colorReset),
		HistoryFile:     "/tmp/qmax-code-history",
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		// Fallback: no readline features
		rl, _ = readline.New("> ")
	}
	return &Terminal{rl: rl}
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
// Named after Max, the real cat who inspired QualityMax.
func (t *Terminal) PrintBanner(version string, projectID int) {
	banner := fmt.Sprintf(`
%s%s    ____  __  __    _    __  __     %s
%s%s   / __ \|  \/  |  / \   \ \/ /     %s  %s/\_/\%s
%s%s  | |  | | |\/| | / _ \   \  /      %s  %s( o.o )%s
%s%s  | |__| | |  | |/ ___ \  /  \      %s  %s > ^ <%s
%s%s   \___\_\_|  |_/_/   \_\/_/\_\     %s  %s/|   |\%s
%s%s                          code %s    %s  %s(_|   |_) meow.%s
`,
		colorBold, colorCyan, colorReset,
		colorBold, colorCyan, colorReset, colorYellow, colorReset,
		colorBold, colorCyan, colorReset, colorYellow, colorReset,
		colorBold, colorCyan, colorReset, colorYellow, colorReset,
		colorBold, colorCyan, colorReset, colorYellow, colorReset,
		colorBold, colorMagenta, version, colorReset, colorYellow, colorReset,
	)
	fmt.Print(banner)

	// Cat-themed geeky subtitles — Max is the QA cat
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

	// Pick a subtitle based on day-of-year for variety
	idx := time.Now().YearDay() % len(subtitles)
	fmt.Printf("  %s%s%s\n\n", colorDim, subtitles[idx], colorReset)

	if projectID > 0 {
		fmt.Printf("  %s▸ Project #%d active%s\n", colorGreen, projectID, colorReset)
	}
	fmt.Printf("  %sType /help for commands or just tell me what to test. 🐾%s\n\n", colorDim, colorReset)
}

// PrintAssistant prints the agent's text response.
func (t *Terminal) PrintAssistant(text string) {
	fmt.Print(text)
}

// PrintToolStart shows a tool invocation with a fun icon.
func (t *Terminal) PrintToolStart(name string, input interface{}) {
	icon := toolIcons[name]
	if icon == "" {
		icon = "🔧"
	}

	// Format tool name nicely
	displayName := strings.ReplaceAll(name, "_", " ")

	// Show compact input summary
	summary := formatToolInput(input)

	fmt.Printf("\n  %s %s%s%s", icon, colorYellow, displayName, colorReset)
	if summary != "" {
		fmt.Printf(" %s%s%s", colorDim, summary, colorReset)
	}
	fmt.Println()
}

// PrintToolResult shows tool output (abbreviated).
func (t *Terminal) PrintToolResult(name string, output string) {
	// Show a brief result summary
	lines := strings.Split(strings.TrimSpace(output), "\n")
	lineCount := len(lines)

	if strings.HasPrefix(output, "{\"error\"") {
		fmt.Printf("  %s✗ Error%s %s%s%s\n", colorRed, colorReset, colorDim, truncateStr(output, 120), colorReset)
		return
	}

	if lineCount <= 3 {
		for _, line := range lines {
			fmt.Printf("  %s%s%s\n", colorDim, truncateStr(line, 140), colorReset)
		}
	} else {
		fmt.Printf("  %s✓ %d lines of output%s\n", colorGreen, lineCount, colorReset)
	}
}

// PrintSystem prints a system message.
func (t *Terminal) PrintSystem(msg string) {
	fmt.Printf("  %s%s●%s %s\n", colorBold, colorBlue, colorReset, msg)
}

// PrintError prints an error message.
func (t *Terminal) PrintError(msg string) {
	fmt.Printf("  %s✗ %s%s\n", colorRed, msg, colorReset)
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

// truncateStr limits a string to maxLen characters.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
