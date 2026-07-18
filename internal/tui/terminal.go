package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/chzyer/readline"

	"github.com/qualitymax/qmax-code/internal/api"
)

// ANSI color codes (kept for prompt and readline which don't use lipgloss)
const (
	ColorReset   = "\033[0m"
	ColorBold    = "\033[1m"
	ColorDim     = "\033[2m"
	ColorRed     = "\033[31m"
	ColorGreen   = "\033[32m"
	ColorYellow  = "\033[33m"
	ColorBlue    = "\033[34m"
	ColorMagenta = "\033[35m"
	ColorCyan    = "\033[36m"
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
	"update_plan":        "📋",
}

// spinnerFrames is the braille animation cycle.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerMessages are shown next to the spinner while the agent is working.
var spinnerMessages = []string{
	"thinking",
	"checking context",
	"planning next step",
	"reading tool results",
	"preparing request",
	"waiting for model",
	"reviewing response",
	"running tool loop",
	"checking test status",
	"validating output",
}

// spinner is a running thinking animation.
type spinner struct {
	done sync.Once
	stop chan struct{}
	wg   sync.WaitGroup
	term *Terminal // checked each tick to pause while user is typing
}

func newSpinner(t *Terminal) *spinner {
	s := &spinner{stop: make(chan struct{}), term: t}
	if p := t.activeTurnProgram(); p != nil {
		p.Send(turnThinkingMsg(true))
		return s
	}
	msg := spinnerMessages[rand.Intn(len(spinnerMessages))]
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Print("\r\033[K") // erase spinner line
				return
			case <-ticker.C:
				// Pause while the user is typing so our \r doesn't overwrite their input.
				if s.term != nil && s.term.userTyping.Load() {
					continue
				}
				frame := spinnerFrames[i%len(spinnerFrames)]
				i++
				fmt.Printf("\r  %s%s %s...%s",
					ColorDim, frame, msg, ColorReset)
			}
		}
	}()
	return s
}

func (s *spinner) Stop() {
	s.done.Do(func() {
		close(s.stop)
		if s.term != nil {
			if p := s.term.activeTurnProgram(); p != nil {
				p.Send(turnThinkingMsg(false))
				return
			}
		}
		s.wg.Wait()
	})
}

// Terminal handles all user-facing I/O with colors, glamour, and personality.
type Terminal struct {
	rl            *readline.Instance
	renderer      *glamour.TermRenderer
	streaming     bool            // true when we're in the middle of streaming text
	streamBuf     strings.Builder // buffers streamed text for post-render
	currentPrompt string          // track prompt for readline recreation
	thinking      *spinner        // active thinking spinner, if any
	userTyping    atomic.Bool     // true while StartQueueReader has the user typing
	toolStreak    bool            // last printed line was a tool call; keeps consecutive tools tight
	turnMu        sync.RWMutex
	turnProgram   *tea.Program // non-nil while a persistent turn viewport owns rendering
}

func (t *Terminal) attachTurnProgram(p *tea.Program) {
	t.turnMu.Lock()
	t.turnProgram = p
	t.turnMu.Unlock()
}

func (t *Terminal) detachTurnProgram(p *tea.Program) {
	t.turnMu.Lock()
	if t.turnProgram == p {
		t.turnProgram = nil
	}
	t.turnMu.Unlock()
}

func (t *Terminal) activeTurnProgram() *tea.Program {
	t.turnMu.RLock()
	defer t.turnMu.RUnlock()
	return t.turnProgram
}

func (t *Terminal) emit(text string) {
	if p := t.activeTurnProgram(); p != nil {
		p.Send(turnOutputMsg(text))
		return
	}
	fmt.Print(text)
}

func (t *Terminal) printf(format string, args ...interface{}) {
	t.emit(fmt.Sprintf(format, args...))
}

// Write makes Terminal an io.Writer so subprocess diagnostics can be routed
// through the renderer that currently owns the terminal.
func (t *Terminal) Write(p []byte) (int, error) {
	t.emit(string(p))
	return len(p), nil
}

// EndLine completes an output line through whichever renderer currently owns
// the terminal. CLI backends use it instead of writing directly to stdout.
func (t *Terminal) EndLine() { t.emit("\n") }

// SetTurnActivity updates an ephemeral activity line above the persistent
// input panel. It returns false when no turn viewport is active so callers can
// fall back to their legacy terminal animation.
func (t *Terminal) SetTurnActivity(text string) bool {
	if p := t.activeTurnProgram(); p != nil {
		p.Send(turnActivityMsg(text))
		return true
	}
	return false
}

// SetUserTyping tells the active spinner to pause (true) or resume (false) its
// line rewrites so typed characters are not overwritten.
func (t *Terminal) SetUserTyping(v bool) {
	t.userTyping.Store(v)
}

// Stderr returns the writer used for diagnostic / debug log lines that the
// readline instance steers around the prompt. Callers in other packages need
// this to emit verbose messages without corrupting the readline buffer.
func (t *Terminal) Stderr() io.Writer {
	if t == nil {
		return os.Stderr
	}
	if t.activeTurnProgram() != nil {
		return t
	}
	if t.rl == nil {
		return os.Stderr
	}
	return t.rl.Stderr()
}

// Prompt returns the current rendered prompt string. Callers (notably the
// REPL using tui.ReadInput) need this to redraw the prompt after the
// bubbletea input session takes over the terminal.
func (t *Terminal) Prompt() string {
	if t == nil {
		return ""
	}
	return t.currentPrompt
}

// NewTerminal creates a new interactive terminal with markdown rendering.
func NewTerminal() *Terminal {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          fmt.Sprintf("%s%sqmax%s %s>%s ", ColorBold, themePromptName, ColorReset, themePromptArrow, ColorReset),
		HistoryFile:     "/tmp/qmax-code-history",
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
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

	prompt := fmt.Sprintf("%s%sqmax%s %s>%s ", ColorBold, themePromptName, ColorReset, themePromptArrow, ColorReset)
	return &Terminal{
		rl:            rl,
		renderer:      renderer,
		currentPrompt: prompt,
	}
}

// SetSessionPrompt updates the prompt to include the session ID.
func (t *Terminal) SetSessionPrompt(sessionID string) {
	t.currentPrompt = fmt.Sprintf("%s%sqmax%s %s[%s]%s %s>%s ",
		ColorBold, themePromptName, ColorReset,
		ColorDim, sessionID, ColorReset,
		themePromptArrow, ColorReset)
	t.rl.SetPrompt(t.currentPrompt)
}

// Close cleans up the terminal.
func (t *Terminal) Close() {
	t.StopThinking()
	if t.rl != nil {
		t.rl.Close()
	}
}

// StartThinking shows an animated spinner with a funny cat-themed message.
// Safe to call even if a spinner is already running (replaces it).
func (t *Terminal) StartThinking() {
	t.StopThinking()
	t.thinking = newSpinner(t)
}

// StopThinking stops the spinner and erases it from the terminal.
// Idempotent — safe to call when no spinner is active.
func (t *Terminal) StopThinking() {
	if t.thinking != nil {
		t.thinking.Stop()
		t.thinking = nil
	}
}

// ReadLine reads a line of user input via readline (fallback, used by non-REPL code).
func (t *Terminal) ReadLine() (string, error) {
	return t.rl.Readline()
}

// ReadConsent temporarily clears the readline prompt and reads one line.
// Used for consent questions that print their own prompt text via fmt, avoiding
// the raw-mode conflict that arises when using bufio.NewReader(os.Stdin) while
// readline already owns the terminal.
func (t *Terminal) ReadConsent() (string, error) {
	saved := t.currentPrompt
	t.rl.SetPrompt("")
	line, err := t.rl.Readline()
	t.rl.SetPrompt(saved)
	return line, err
}

// PrintBanner shows the startup banner — fun, geeky, and cat-themed.
func (t *Terminal) PrintBanner(version string, ctx *api.SessionContext) {
	// Pick Max's mood based on context
	mood := MoodNeutral
	if ctx.API != nil || ctx.QMaxBin != "" {
		mood = MoodHappy
	}

	// Get mood-specific cat art
	frame := maxFrames[mood]
	catLines := strings.Split(frame.Art, "\n")
	// Pad to 5 lines
	for len(catLines) < 5 {
		catLines = append(catLines, "")
	}
	// Pad each line to consistent width
	for i := range catLines {
		for len(catLines[i]) < 14 {
			catLines[i] += " "
		}
	}

	banner := fmt.Sprintf(`
  %s%s ██████╗ ███╗   ███╗ █████╗ ██╗  ██╗%s
  %s%s██╔═══██╗████╗ ████║██╔══██╗╚██╗██╔╝%s    %s%s%s
  %s%s██║   ██║██╔████╔██║███████║ ╚███╔╝ %s    %s%s%s
  %s%s██║▄▄ ██║██║╚██╔╝██║██╔══██║ ██╔██╗ %s    %s%s%s
  %s%s╚██████╔╝██║ ╚═╝ ██║██║  ██║██╔╝ ██╗%s    %s%s%s
  %s%s ╚══▀▀═╝ ╚═╝     ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝%s   %s%s%s
  %s%s                code %s%s
`,
		ColorBold, themeBannerColor, ColorReset,
		ColorBold, themeBannerColor, ColorReset, themeCatColor, catLines[0], ColorReset,
		ColorBold, themeBannerColor, ColorReset, themeCatColor, catLines[1], ColorReset,
		ColorBold, themeBannerColor, ColorReset, themeCatColor, catLines[2], ColorReset,
		ColorBold, themeBannerColor, ColorReset, themeCatColor, catLines[3], ColorReset,
		ColorBold, themeBannerColor, ColorReset, themeCatColor, catLines[4], ColorReset,
		ColorBold, themePromptArrow, version, ColorReset,
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
	fmt.Printf("  %s%s%s\n\n", ColorDim, subtitles[idx], ColorReset)

	// Show context info
	if ctx.ProjectID > 0 {
		fmt.Printf("  %s▸ Project #%d active%s\n", themeStatusColor, ctx.ProjectID, ColorReset)
	}

	switch ctx.Backend {
	case "cc":
		fmt.Printf("  %s▸ Backend: Claude Code (no QM API key; Agent SDK credit for --print)%s\n", themeStatusColor, ColorReset)
		if ctx.Auth != nil && ctx.Auth.Email != "" {
			fmt.Printf("  %s▸ QualityMax: %s%s\n", themeStatusColor, ctx.Auth.Email, ColorReset)
		}
	case "codex":
		fmt.Printf("  %s▸ Backend: Codex CLI (OpenAI subscription — no API tokens)%s\n", themeStatusColor, ColorReset)
		if ctx.Auth != nil && ctx.Auth.Email != "" {
			fmt.Printf("  %s▸ QualityMax: %s%s\n", themeStatusColor, ctx.Auth.Email, ColorReset)
		}
	default:
		// API mode — show direct API or qmax CLI connection status.
		if ctx.API != nil {
			fmt.Printf("  %s▸ Mode: standalone (direct API)%s\n", themeStatusColor, ColorReset)
			if ctx.Auth != nil && ctx.Auth.Email != "" {
				fmt.Printf("  %s▸ Logged in as: %s%s\n", themeStatusColor, ctx.Auth.Email, ColorReset)
			}
			fmt.Printf("  %s▸ API: %s%s\n", ColorDim, ctx.Auth.GetCloudURL(), ColorReset)
		} else if ctx.QMaxBin != "" {
			fmt.Printf("  %s▸ qmax CLI: %s%s\n", themeStatusColor, ctx.QMaxBin, ColorReset)
			if ctx.QMaxCfg.Email != "" {
				fmt.Printf("  %s▸ Logged in as: %s%s\n", themeStatusColor, ctx.QMaxCfg.Email, ColorReset)
			}
			if ctx.QMaxCfg.CloudURL != "" {
				fmt.Printf("  %s▸ API: %s%s\n", ColorDim, ctx.QMaxCfg.CloudURL, ColorReset)
			}
		} else {
			fmt.Printf("  %s▸ Not connected%s — type %s/connect%s to log in\n", ColorYellow, ColorReset, ColorBold, ColorReset)
		}
	}

	fmt.Printf("  %sType /help for commands or just tell me what to test. 🐾%s\n\n", ColorDim, ColorReset)
}

// StreamText prints text as it arrives from the SSE stream (token-by-token).
func (t *Terminal) StreamText(text string) {
	if !t.streaming {
		t.StopThinking() // erase spinner before first token
		t.streaming = true
		t.toolStreak = false
		t.streamBuf.Reset()
		// Hide readline prompt during streaming to prevent input overlap
		if t.activeTurnProgram() == nil && t.rl != nil {
			t.rl.SetPrompt("")
			t.rl.Refresh()
		}
		t.emit("\n") // newline before assistant response
	}
	t.emit(text)
	t.streamBuf.WriteString(text)
}

// FinishMarkdown is called when a text block is complete.
// Re-renders the streamed text with glamour for syntax highlighting.
func (t *Terminal) FinishMarkdown(fullText string) {
	t.toolStreak = false
	if t.streaming {
		t.streaming = false
		if t.activeTurnProgram() != nil {
			t.streamBuf.Reset()
			if !strings.HasSuffix(fullText, "\n") {
				t.emit("\n")
			}
			return
		}

		// If the text contains code blocks, re-render with glamour for highlighting
		if t.renderer != nil && strings.Contains(fullText, "```") {
			// Move cursor up to overwrite raw output, then print rendered version
			rawLines := strings.Count(t.streamBuf.String(), "\n") + 1
			// Clear the raw streamed output
			for i := 0; i < rawLines; i++ {
				fmt.Print("\033[1A\033[2K") // move up + clear line
			}
			rendered, err := t.renderer.Render(fullText)
			if err == nil {
				fmt.Print(rendered)
				t.streamBuf.Reset()
				return
			}
		}

		t.streamBuf.Reset()
		if !strings.HasSuffix(fullText, "\n") {
			fmt.Println()
		}
		// Restore readline prompt after streaming completes
		if t.rl != nil && t.currentPrompt != "" {
			t.rl.SetPrompt(t.currentPrompt)
		}
	}
}

// PrintAssistant prints the agent's text response with markdown rendering.
// Used in non-streaming mode.
func (t *Terminal) PrintAssistant(text string) {
	t.toolStreak = false
	if t.renderer != nil {
		rendered, err := t.renderer.Render(text)
		if err == nil {
			t.emit(rendered)
			return
		}
	}
	t.emit(text + "\n")
}

// PrintToolIcon shows a tool icon when a tool_use block starts streaming.
// Consecutive tool calls render as a tight group: only the first tool after
// text/system output gets a separating blank line.
func (t *Terminal) PrintToolIcon(name string) {
	t.StopThinking() // erase spinner before tool display
	if t.streaming {
		t.streaming = false
		t.emit("\n")
	}
	icon := toolIcons[name]
	if icon == "" {
		icon = "🔧"
	}
	displayName := strings.ReplaceAll(name, "_", " ")
	if !t.toolStreak {
		t.emit("\n")
	}
	t.toolStreak = true
	t.emit(fmt.Sprintf("  %s %s", icon, styleTool.Render(displayName)))
}

// PrintToolStart shows a tool invocation with its input summary.
func (t *Terminal) PrintToolStart(name string, input interface{}) {
	summary := formatToolInput(input)
	if summary != "" {
		t.emit(fmt.Sprintf(" %s", styleToolDim.Render(summary)))
	}
	t.emit("\n")
}

// PrintToolResult shows tool output with smart formatting for known result types.
// Restarts the thinking spinner on exit so it runs while the agent processes the result.
func (t *Terminal) PrintToolResult(name string, output string) {
	defer t.StartThinking()
	if strings.HasPrefix(output, "{\"error\"") {
		t.printf("  %s %s\n", styleError.Render("✗ Error"), styleDim.Render(TruncateStr(output, 120)))
		return
	}

	// Smart display for execution status results
	if (name == "check_test_status" || name == "run_test") && strings.Contains(output, "execution_id") {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(output), &data); err == nil {
			execID, _ := data["execution_id"].(string)
			status, _ := data["status"].(string)
			t.printf("  Execution: %s\n", styleDim.Render(execID))
			switch status {
			case "passed":
				t.printf("    Status: %s\n", styleSuccess.Render("✓ PASSED"))
			case "failed":
				t.printf("    Status: %s\n", styleError.Render("✗ FAILED"))
			default:
				t.printf("    Status: %s\n", styleSystem.Render(status))
			}
			if msg, ok := data["message"].(string); ok && msg != "" {
				t.printf("    Message: %s\n", styleDim.Render(TruncateStr(msg, 120)))
			}
			if errs, ok := data["test_errors"].(string); ok && errs != "" {
				t.printf("    Errors: %s\n", styleError.Render(TruncateStr(errs, 300)))
			}
			// Extract errors from console_logs if test_errors is empty
			if logs, ok := data["console_logs"].([]interface{}); ok && len(logs) > 0 {
				var errorLines []string
				for _, l := range logs {
					if logEntry, ok := l.(map[string]interface{}); ok {
						text, _ := logEntry["text"].(string)
						if strings.Contains(text, "Error") || strings.Contains(text, "failed") || strings.Contains(text, "✗") {
							errorLines = append(errorLines, text)
						}
					}
				}
				if len(errorLines) > 0 {
					t.emit("    Console errors:\n")
					for _, line := range errorLines {
						t.printf("      %s\n", styleError.Render(TruncateStr(line, 120)))
					}
				}
			}
			if dur, ok := data["execution_time"].(float64); ok && dur > 0 {
				t.printf("    Duration: %.1fs\n", dur)
			}
			if screenshots, ok := data["screenshot_paths"].([]interface{}); ok && len(screenshots) > 0 {
				t.printf("    Screenshots: %d captured\n", len(screenshots))
				for i, s := range screenshots {
					if url, ok := s.(string); ok && url != "" {
						t.emit(FormatScreenshotCompact(fmt.Sprintf("Screenshot %d", i+1), url))
					}
				}
			}
			if video, ok := data["video_path"].(string); ok && video != "" {
				t.printf("    Video: %s\n", styleDim.Render(video))
			}
			return
		}
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	lineCount := len(lines)

	if lineCount <= 3 {
		for _, line := range lines {
			t.printf("  %s\n", styleDim.Render(TruncateStr(line, 140)))
		}
	} else {
		t.printf("  %s\n", styleSuccess.Render(fmt.Sprintf("✓ %d lines of output", lineCount)))
	}
}

// PlanStep is one entry of an agent task plan, in the shape PrintPlan renders.
// Kept here (not imported from agent) because agent imports tui, not vice versa.
type PlanStep struct {
	Title  string
	Status string // pending | in_progress | done
}

// PrintPlan renders the agent's task plan (from update_plan) as a checklist with
// a done/total progress line. Marks: ✓ done, ▸ in_progress, ◦ pending.
// Restarts the thinking spinner on exit, mirroring PrintToolResult.
func (t *Terminal) PrintPlan(steps []PlanStep) {
	t.StopThinking() // erase spinner before plan display
	if t.streaming {
		t.streaming = false
		t.emit("\n")
	}
	t.toolStreak = false
	defer t.StartThinking()

	done := 0
	for _, s := range steps {
		if s.Status == "done" {
			done++
		}
	}

	icon := toolIcons["update_plan"]
	if icon == "" {
		icon = "📋"
	}
	t.printf("\n  %s %s %s\n",
		icon,
		styleTool.Render("plan"),
		styleDim.Render(fmt.Sprintf("(%d/%d done)", done, len(steps))))

	for _, s := range steps {
		var mark, title string
		switch s.Status {
		case "done":
			mark = styleSuccess.Render("✓")
			title = styleDim.Render(s.Title)
		case "in_progress":
			mark = styleSystem.Render("▸")
			title = styleTool.Render(s.Title)
		default:
			mark = styleDim.Render("◦")
			title = s.Title
		}
		t.printf("    %s %s\n", mark, title)
	}
}

// PrintSystem prints a system message.
func (t *Terminal) PrintSystem(msg string) {
	t.toolStreak = false
	t.emit(fmt.Sprintf("  %s %s\n", styleSystem.Render("●"), msg))
}

// PrintError prints an error message.
func (t *Terminal) PrintError(msg string) {
	t.toolStreak = false
	t.emit(fmt.Sprintf("  %s\n", styleError.Render("✗ "+msg)))
}

// PrintTokenUsage shows token usage in dim text after a response.
func (t *Terminal) PrintTokenUsage(usage api.TokenUsage) {
	t.emit(fmt.Sprintf("\n%s\n", styleUsage.Render(
		fmt.Sprintf("  tokens: %d in / %d out (session: %d total, %d requests)",
			usage.InputTokens, usage.OutputTokens, usage.TotalTokens(), usage.Requests))))
}

// PrintCostSummary shows detailed cost info for /cost command.
func (t *Terminal) PrintCostSummary(usage api.TokenUsage, model string) {
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
func (t *Terminal) PrintStatusInfo(ctx *api.SessionContext, usage api.TokenUsage, model string) {
	fmt.Println()
	fmt.Printf("  %s\n", styleSystem.Render("qmax-code Status"))

	// Connection status (primary)
	if ctx.API != nil && ctx.Auth != nil && ctx.Auth.IsAuthenticated() {
		fmt.Printf("  %-20s %s%s Connected%s\n", "QualityMax:", themeStatusColor, "●", ColorReset)
		fmt.Printf("  %-20s %s\n", "Logged in as:", ctx.Auth.Email)
		fmt.Printf("  %-20s %s\n", "API:", ctx.Auth.GetCloudURL())
		fmt.Printf("  %-20s standalone (direct API)\n", "Mode:")
	} else if ctx.QMaxBin != "" {
		fmt.Printf("  %-20s %s%s Connected (via CLI)%s\n", "QualityMax:", themeStatusColor, "●", ColorReset)
		if ctx.QMaxCfg.Email != "" {
			fmt.Printf("  %-20s %s\n", "Logged in as:", ctx.QMaxCfg.Email)
		}
		fmt.Printf("  %-20s %s\n", "CLI:", ctx.QMaxBin)
	} else {
		fmt.Printf("  %-20s %s%s Not connected%s\n", "QualityMax:", ColorYellow, "●", ColorReset)
		fmt.Printf("  %-20s run %s/connect%s to log in\n", "", ColorBold, ColorReset)
	}

	fmt.Println()
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

// TruncateStr limits a string to maxLen characters.
func TruncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
