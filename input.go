package main

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// SlashMenuItem represents a selectable command.
type SlashMenuItem struct {
	Cmd  string
	Desc string
}

var slashMenuItems = []SlashMenuItem{
	{"/help", "Show help"},
	{"/orch", "Model + effort picker (CC / Codex / API)"},
	{"/theme", "Live-preview color scheme picker"},
	{"/cc", "Switch to Claude Code backend"},
	{"/codex", "Switch to Codex CLI backend"},
	{"/api", "Switch to direct Anthropic API"},
	{"/connect", "Log in to QualityMax (browser)"},
	{"/disconnect", "Log out"},
	{"/status", "Auth + session info"},
	{"/cost", "Token usage + cost"},
	{"/config", "Show config"},
	{"/sessions", "List saved sessions"},
	{"/resume", "Resume a session"},
	{"/save", "Save current session"},
	{"/project", "Set active project"},
	{"/keys", "Set API keys (interactive)"},
	{"/screenshot", "Capture & analyze a screenshot"},
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

func (m inputModel) Init() tea.Cmd { return nil }

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
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
		if m.cursor > 0 {
			m.cursor--
		}

	case tea.KeyRight:
		if m.cursor < len(runes) {
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
)

func (m inputModel) View() string {
	// When done, show only the final input — no menu residue.
	if m.done {
		if m.result == "" {
			return ""
		}
		return m.prompt + m.result + "\n"
	}

	var b strings.Builder

	if m.mode == modeTyping {
		runes := []rune(m.text)

		// Build the text with a block cursor inserted at the cursor position.
		var display []rune
		display = append(display, runes[:m.cursor]...)
		display = append(display, '█')
		display = append(display, runes[m.cursor:]...)

		b.WriteString(m.prompt)

		w := m.width
		if w <= 0 {
			w = 80
		}
		promptW := lipgloss.Width(m.prompt)
		availW := w - promptW
		if availW < 10 {
			availW = 10
		}

		if len(display) <= availW {
			b.WriteString(string(display))
		} else {
			// Wrap at availW; indent continuation lines to align with text start.
			indent := strings.Repeat(" ", promptW)
			col := 0
			for _, r := range display {
				if col >= availW {
					b.WriteString("\n")
					b.WriteString(indent)
					col = 0
				}
				b.WriteRune(r)
				col++
			}
		}

		mode := "compact"
		if m.outputVerbose {
			mode = "verbose"
		}
		b.WriteString("\n")
		b.WriteString(menuHintStyle.Render(fmt.Sprintf("  Ctrl+O output: %s • Ctrl+X×3 clear • Ctrl+←/→ words • Ctrl+C cancel • / commands", mode)))
	} else {
		// Menu mode
		b.WriteString(m.prompt)
		b.WriteString("/")
		if m.filter != "" {
			b.WriteString(filterStyle.Render(m.filter))
		}
		b.WriteString("█")
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
	}

	return b.String()
}

// InputResult holds the result of a ReadInput call.
type InputResult struct {
	Text         string
	CtrlC        bool
	Pasted       bool
	OutputToggle bool
}

// ReadInput runs the bubbletea input widget and returns the submitted text.
func ReadInput(prompt string, history []string, outputVerbose bool) InputResult {
	m := newInputModelWithOutputMode(prompt, history, outputVerbose)
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
