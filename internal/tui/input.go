package tui

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// StatusInfo carries session metrics rendered around the input box: token
// counts, context-window fill, timing, and the mode/task bottom bar. Zero
// values are omitted from the display, so backends that can't report a given
// metric simply don't show it.
type StatusInfo struct {
	Backend        string // "cc" | "codex" | "opencode" | "cerebras" | "ollama" | "" (direct API)
	Model          string // active model ID; "" = backend default
	Effort         string // "low" | "medium" | "high" (CLI backends)
	PermissionMode string // "standard" | "unattended" (CLI backends)
	OutputVerbose  bool
	Task           string        // what the agent is working on (latest user prompt)
	TokensIn       int           // session input tokens
	TokensOut      int           // session output tokens
	ContextUsed    int           // tokens occupying the model context after the last turn
	ContextWindow  int           // assumed context window of the active model
	LastTurnDur    time.Duration // wall-clock duration of the previous agent turn
	SessionStarted time.Time     // used to keep the session timer live while typing
}

// SlashMenuItem represents a selectable command.
type SlashMenuItem struct {
	Cmd  string
	Desc string
}

var slashMenuItems = []SlashMenuItem{
	{"/help", "Show help"},
	{"/orch", "Model + effort picker (CC / Codex / API)"},
	{"/theme", "Live-preview color scheme picker"},
	{"/cloudsync", "Toggle cloud session sync (enabled/disabled)"},
	{"/live", "Toggle live browser feed for test runs / AI crawls (off by default)"},
	{"/feed", "Open the most recent live browser feed (after a test/crawl with /live on)"},
	{"/cc", "Switch to Claude Code backend"},
	{"/codex", "Switch to Codex CLI backend"},
	{"/opencode", "Switch to opencode backend (Z.AI / Groq / OpenRouter)"},
	{"/api", "Switch to direct Anthropic API"},
	{"/providers", "Enable/disable opencode providers (per-user opt-in)"},
	{"/connect", "Log in to QualityMax (browser)"},
	{"/disconnect", "Log out"},
	{"/reconnect", "Restore active MCP transport"},
	{"/status", "Auth + session info"},
	{"/cost", "Token usage + cost"},
	{"/config", "Show config"},
	{"/skills", "List qmax QA skills + install status"},
	{"/sessions", "List saved sessions"},
	{"/resume", "Resume a session"},
	{"/save", "Save current session"},
	{"/project", "Set active project"},
	{"/keys", "Set API keys (interactive)"},
	{"/screenshot", "Capture & analyze a screenshot"},
	{"/browserfeed", "Live ASCII browser feed from a QM Cloud Sandbox noVNC URL"},
	{"/paste", "Paste from clipboard (image or text)"},
	{"/queue", "Show or add to prompt queue"},
	{"/set", "Update config"},
	{"/ollama", "Toggle Ollama on/off"},
	{"/clear", "Clear history"},
	{"/quit", "Exit"},
}

// inputMode tracks what the input widget is doing
type inputMode int

const (
	modeTyping inputMode = iota
	modeMenu
)

// inputModel is a bubbletea model that handles text input + slash menu.
type inputModel struct {
	mode          inputMode
	text          string
	cursor        int // cursor position in runes
	width         int // terminal width; 0 = not yet known
	menu          int // selected menu index
	filter        string
	result        string // final submitted text
	done          bool
	ctrlC         bool // true if exited via Ctrl+C
	pasted        bool
	outputToggle  bool
	outputVerbose bool
	clearPresses  int
	prompt        string
	history       []string
	histIdx       int
	status        *StatusInfo // optional metrics/mode/task display; nil hides it
}

func newInputModel(prompt string, history []string) inputModel {
	return newInputModelWithOutputMode(prompt, history, false)
}

func newInputModelWithOutputMode(prompt string, history []string, outputVerbose bool) inputModel {
	w := 80 // sensible fallback
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && width > 0 {
		w = width
	}
	return inputModel{
		prompt:        prompt,
		history:       history,
		histIdx:       -1,
		width:         w,
		outputVerbose: outputVerbose,
	}
}

type statusTickMsg time.Time

func (m inputModel) Init() tea.Cmd {
	if m.status == nil || m.status.SessionStarted.IsZero() {
		return nil
	}
	return nextStatusTick()
}

func nextStatusTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return statusTickMsg(t) })
}

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case statusTickMsg:
		return m, nextStatusTick()
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlO {
			m.result = ""
			m.done = true
			m.outputToggle = true
			return m, tea.Quit
		}
		if m.mode == modeMenu {
			return m.updateMenu(msg)
		}
		return m.updateTyping(msg)
	}
	return m, nil
}

func (m inputModel) updateTyping(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	runes := []rune(m.text)
	resetClearPresses := true

	switch msg.Type {
	case tea.KeyEnter:
		m.result = m.text
		m.done = true
		return m, tea.Quit

	case tea.KeyCtrlC:
		m.result = ""
		m.done = true
		m.ctrlC = true
		return m, tea.Quit

	case tea.KeyBackspace:
		if m.cursor > 0 {
			runes = append(runes[:m.cursor-1], runes[m.cursor:]...)
			m.text = string(runes)
			m.cursor--
		}

	case tea.KeyDelete:
		if m.cursor < len(runes) {
			runes = append(runes[:m.cursor], runes[m.cursor+1:]...)
			m.text = string(runes)
		}

	case tea.KeyLeft:
		// Alt+Left (xterm \x1b[1;3D) = word-left, plain Left = char-left.
		if msg.Alt {
			m.cursor = previousWordBoundary(runes, m.cursor)
		} else if m.cursor > 0 {
			m.cursor--
		}

	case tea.KeyRight:
		// Alt+Right (xterm \x1b[1;3C) = word-right, plain Right = char-right.
		if msg.Alt {
			m.cursor = nextWordBoundary(runes, m.cursor)
		} else if m.cursor < len(runes) {
			m.cursor++
		}

	case tea.KeyCtrlLeft:
		m.cursor = previousWordBoundary(runes, m.cursor)

	case tea.KeyCtrlRight:
		m.cursor = nextWordBoundary(runes, m.cursor)

	case tea.KeyCtrlA:
		m.cursor = 0

	case tea.KeyCtrlE:
		m.cursor = len(runes)

	case tea.KeyCtrlW:
		end := m.cursor
		m.cursor = previousWordBoundary(runes, m.cursor)
		runes = append(runes[:m.cursor], runes[end:]...)
		m.text = string(runes)

	case tea.KeyCtrlU:
		m.text = ""
		m.cursor = 0

	case tea.KeyCtrlK:
		m.text = string(runes[:m.cursor])

	case tea.KeyCtrlX:
		resetClearPresses = false
		if len(runes) > 0 {
			m.clearPresses++
			if m.clearPresses >= 3 {
				m.text = ""
				m.cursor = 0
				m.clearPresses = 0
			}
		}

	case tea.KeyUp:
		if len(m.history) > 0 {
			if m.histIdx < len(m.history)-1 {
				m.histIdx++
			}
			m.text = m.history[len(m.history)-1-m.histIdx]
			m.cursor = len([]rune(m.text))
		}

	case tea.KeyDown:
		if m.histIdx > 0 {
			m.histIdx--
			m.text = m.history[len(m.history)-1-m.histIdx]
			m.cursor = len([]rune(m.text))
		} else {
			m.histIdx = -1
			m.text = ""
			m.cursor = 0
		}

	case tea.KeyRunes:
		if msg.Paste {
			m.pasted = true
		}
		// macOS Option+Left sends ESC+'b', Option+Right sends ESC+'f'.
		// Bubbletea delivers these as KeyRunes with Alt=true.
		if msg.Alt && len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'b', 'B':
				m.cursor = previousWordBoundary(runes, m.cursor)
				return m, nil
			case 'f', 'F':
				m.cursor = nextWordBoundary(runes, m.cursor)
				return m, nil
			}
		}
		ch := string(msg.Runes)
		// Typing "/" on an empty line opens the slash menu.
		if ch == "/" && m.text == "" {
			m.mode = modeMenu
			m.menu = 0
			m.filter = ""
			return m, nil
		}
		// Insert runes at cursor.
		newRunes := make([]rune, 0, len(runes)+len(msg.Runes))
		newRunes = append(newRunes, runes[:m.cursor]...)
		newRunes = append(newRunes, msg.Runes...)
		newRunes = append(newRunes, runes[m.cursor:]...)
		m.text = string(newRunes)
		m.cursor += len(msg.Runes)

	case tea.KeySpace:
		newRunes := make([]rune, 0, len(runes)+1)
		newRunes = append(newRunes, runes[:m.cursor]...)
		newRunes = append(newRunes, ' ')
		newRunes = append(newRunes, runes[m.cursor:]...)
		m.text = string(newRunes)
		m.cursor++

	case tea.KeyTab:
		// ignored
	}

	// Any keystroke while navigating history snaps back to "live" index.
	if msg.Type != tea.KeyUp && msg.Type != tea.KeyDown {
		m.histIdx = -1
	}
	if resetClearPresses {
		m.clearPresses = 0
	}

	return m, nil
}

// isWordChar matches readline's word definition: letters, digits, underscore.
// Punctuation, slashes, dots, etc. are treated as separators so word motion
// stops at each path segment / identifier boundary, matching bash/zsh muscle memory.
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func previousWordBoundary(runes []rune, cursor int) int {
	if cursor > len(runes) {
		cursor = len(runes)
	}
	for cursor > 0 && !isWordChar(runes[cursor-1]) {
		cursor--
	}
	for cursor > 0 && isWordChar(runes[cursor-1]) {
		cursor--
	}
	return cursor
}

func nextWordBoundary(runes []rune, cursor int) int {
	if cursor < 0 {
		cursor = 0
	}
	for cursor < len(runes) && isWordChar(runes[cursor]) {
		cursor++
	}
	for cursor < len(runes) && !isWordChar(runes[cursor]) {
		cursor++
	}
	return cursor
}

func (m inputModel) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtered := m.filteredMenuItems()

	switch msg.Type {
	case tea.KeyEnter:
		if len(filtered) > 0 && m.menu < len(filtered) {
			m.result = filtered[m.menu].Cmd
			m.done = true
			return m, tea.Quit
		}
	case tea.KeyEscape, tea.KeyCtrlC:
		m.mode = modeTyping
		m.text = ""
		m.cursor = 0
		return m, nil
	case tea.KeyUp:
		if m.menu > 0 {
			m.menu--
		} else {
			m.menu = len(filtered) - 1
		}
	case tea.KeyDown:
		if m.menu < len(filtered)-1 {
			m.menu++
		} else {
			m.menu = 0
		}
	case tea.KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = string([]rune(m.filter)[:len([]rune(m.filter))-1])
			m.menu = 0
		} else {
			m.mode = modeTyping
			m.text = ""
			m.cursor = 0
		}
	case tea.KeyRunes:
		m.filter += string(msg.Runes)
		m.menu = 0
	}
	return m, nil
}

func (m inputModel) filteredMenuItems() []SlashMenuItem {
	if m.filter == "" {
		return slashMenuItems
	}
	var filtered []SlashMenuItem
	lower := strings.ToLower(m.filter)
	for _, item := range slashMenuItems {
		if strings.Contains(strings.ToLower(item.Cmd), lower) ||
			strings.Contains(strings.ToLower(item.Desc), lower) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

var (
	menuSelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("69")).Bold(true).PaddingLeft(1).PaddingRight(1)
	menuItemStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).PaddingLeft(1)
	menuDescStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	menuDescSelSty = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("69"))
	menuHintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	filterStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)

	// Input box + status bar styles. The bordered box visually separates the
	// prompt from the agent stream above it; the bottom bar mirrors the
	// pickerStatusBar look (light text on a raised background).
	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)

	statusMetricsStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	statusBarStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236"))
	statusBarModeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Background(lipgloss.Color("236")).Bold(true)
	statusBarDimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Background(lipgloss.Color("236"))
)

func (m inputModel) View() string {
	// Keep the submitted input panel in the terminal scrollback. Streaming starts
	// immediately after Bubble Tea exits, so returning a bare prompt here used to
	// make the agent stream visually collapse into the user's last input.
	if m.done {
		if m.result == "" {
			return ""
		}
		w := m.width
		if w <= 0 {
			w = 80
		}
		return m.renderInputBox(m.result, w) + "\n"
	}

	var b strings.Builder

	w := m.width
	if w <= 0 {
		w = 80
	}

	if m.mode == modeTyping {
		runes := []rune(m.text)

		// Build the text with a block cursor inserted at the cursor position.
		var display []rune
		display = append(display, runes[:m.cursor]...)
		display = append(display, '█')
		display = append(display, runes[m.cursor:]...)

		b.WriteString(m.renderInputBox(string(display), w))

		mode := "compact"
		if m.outputVerbose {
			mode = "verbose"
		}
		b.WriteString("\n")
		b.WriteString(menuHintStyle.Render(fmt.Sprintf("  Ctrl+O output: %s • ↑↓ history • Ctrl+X×3 clear • Opt+←/→ words • Ctrl+C cancel • / commands", mode)))
		if status := m.renderStatus(w); status != "" {
			b.WriteString("\n")
			b.WriteString(status)
		}
	} else {
		// Menu mode
		filterLine := "/"
		if m.filter != "" {
			filterLine += filterStyle.Render(m.filter)
		}
		filterLine += "█"
		b.WriteString(m.renderInputBox(filterLine, w))
		b.WriteString("\n")

		filtered := m.filteredMenuItems()
		if len(filtered) == 0 {
			b.WriteString(menuHintStyle.Render("  No matching commands\n"))
		} else {
			for i, item := range filtered {
				if i == m.menu {
					b.WriteString(fmt.Sprintf("  %s %s\n",
						menuSelStyle.Render(fmt.Sprintf("%-11s", item.Cmd)),
						menuDescSelSty.Render(item.Desc)))
				} else {
					b.WriteString(fmt.Sprintf("  %s %s\n",
						menuItemStyle.Render(fmt.Sprintf("%-11s", item.Cmd)),
						menuDescStyle.Render(item.Desc)))
				}
			}
		}
		b.WriteString(menuHintStyle.Render("  ↑↓ navigate • enter select • esc cancel • type to filter"))
		if status := m.renderStatus(w); status != "" {
			b.WriteString("\n")
			b.WriteString(status)
		}
	}

	return b.String()
}

// renderInputBox draws the prompt + content inside a full-width rounded
// border so the input field reads as a distinct panel below the agent stream.
func (m inputModel) renderInputBox(content string, w int) string {
	// Inner width: total minus 2 border columns minus 2 padding columns.
	innerW := w - 4
	if innerW < 20 {
		innerW = 20
	}

	promptW := lipgloss.Width(m.prompt)
	availW := innerW - promptW
	if availW < 10 {
		availW = 10
	}

	var text strings.Builder
	text.WriteString(m.prompt)
	runes := []rune(content)
	if len(runes) <= availW {
		text.WriteString(content)
	} else {
		// Wrap at availW; indent continuation lines to align with text start.
		indent := strings.Repeat(" ", promptW)
		col := 0
		for _, r := range runes {
			if col >= availW {
				text.WriteString("\n")
				text.WriteString(indent)
				col = 0
			}
			text.WriteRune(r)
			col++
		}
	}

	return inputBoxStyle.Width(w - 2).Render(text.String())
}

// renderStatus produces the metrics line (context window, tokens, timings)
// and the bottom bar (mode + task). Returns "" when no status was provided.
func (m inputModel) renderStatus(w int) string {
	s := m.status
	if s == nil {
		return ""
	}

	var lines []string

	// ── Metrics line: token window · tokens · time ──────────────────────
	var metrics []string
	if s.ContextUsed > 0 && s.ContextWindow > 0 {
		pct := s.ContextUsed * 100 / s.ContextWindow
		metrics = append(metrics, fmt.Sprintf("ctx %s/%s (%d%%)",
			compactTokens(s.ContextUsed), compactTokens(s.ContextWindow), pct))
	}
	if s.TokensIn+s.TokensOut > 0 {
		metrics = append(metrics, fmt.Sprintf("tokens %s in / %s out",
			compactTokens(s.TokensIn), compactTokens(s.TokensOut)))
	}
	if s.LastTurnDur > 0 {
		metrics = append(metrics, "last turn "+compactDuration(s.LastTurnDur))
	}
	if !s.SessionStarted.IsZero() {
		if sessionDur := time.Since(s.SessionStarted); sessionDur > 0 {
			metrics = append(metrics, "session "+compactDuration(sessionDur))
		}
	}
	if len(metrics) > 0 {
		lines = append(lines, statusMetricsStyle.Render("  "+strings.Join(metrics, " · ")))
	}

	// ── Bottom bar: mode (left) + task (right) ──────────────────────────
	mode := s.PermissionMode
	if mode == "" {
		mode = "api"
	}
	left := " " + statusBarModeStyle.Render("-- "+strings.ToUpper(mode)+" --") + statusBarStyle.Render("  "+m.describeBackend())

	task := s.Task
	if task == "" {
		task = "idle"
	}
	leftW := lipgloss.Width(left)
	// " task: " + text + trailing space must fit in what's left of the row.
	taskAvail := w - leftW - 9
	if taskAvail < 8 {
		taskAvail = 8
	}
	taskRunes := []rune(strings.ReplaceAll(task, "\n", " "))
	if len(taskRunes) > taskAvail {
		task = string(taskRunes[:taskAvail-1]) + "…"
	}
	right := statusBarDimStyle.Render("task: ") + statusBarStyle.Render(task+" ")

	gap := w - leftW - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	bar := left + statusBarStyle.Render(strings.Repeat(" ", gap)) + right
	lines = append(lines, bar)

	return strings.Join(lines, "\n")
}

// describeBackend renders "backend · model · effort" from whichever parts are set.
func (m inputModel) describeBackend() string {
	s := m.status
	backend := s.Backend
	if backend == "" {
		backend = "api"
	}
	parts := []string{backend}
	if s.Model != "" {
		parts = append(parts, s.Model)
	}
	if s.Effort != "" {
		parts = append(parts, s.Effort+" effort")
	}
	if s.OutputVerbose {
		parts = append(parts, "verbose")
	}
	return strings.Join(parts, " · ")
}

// compactTokens formats a token count as "950", "12.3k", or "1.2M".
func compactTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// compactDuration formats a duration as "42s", "2m10s", or "1h03m".
func compactDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// InputResult holds the result of a ReadInput call.
type InputResult struct {
	Text         string
	CtrlC        bool
	Pasted       bool
	OutputToggle bool
}

// ReadInput runs the bubbletea input widget and returns the submitted text.
// status is optional session metrics rendered below the input box (nil hides them).
func ReadInput(prompt string, history []string, outputVerbose bool, status *StatusInfo) InputResult {
	m := newInputModelWithOutputMode(prompt, history, outputVerbose)
	m.status = status
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return InputResult{}
	}
	final := result.(inputModel)
	return InputResult{
		Text:         final.result,
		CtrlC:        final.ctrlC,
		Pasted:       final.pasted,
		OutputToggle: final.outputToggle,
	}
}
