package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SlashMenuItem represents a selectable command.
type SlashMenuItem struct {
	Cmd  string
	Desc string
}

var slashMenuItems = []SlashMenuItem{
	{"/help", "Show help"},
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

// inputModel is a bubbletea model that handles text input + slash menu
type inputModel struct {
	mode     inputMode
	text     string
	menu     int // selected menu index
	filter   string
	result   string // final submitted text
	done     bool
	ctrlC    bool   // true if exited via Ctrl+C
	prompt   string
	history  []string
	histIdx  int
}

func newInputModel(prompt string, history []string) inputModel {
	return inputModel{
		prompt:  prompt,
		history: history,
		histIdx: -1,
	}
}


func (m inputModel) Init() tea.Cmd { return nil }

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.mode == modeMenu {
			return m.updateMenu(msg)
		}
		return m.updateTyping(msg)
	}
	return m, nil
}

func (m inputModel) updateTyping(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		if len(m.text) > 0 {
			m.text = m.text[:len(m.text)-1]
		}
	case tea.KeyUp:
		if len(m.history) > 0 {
			if m.histIdx < len(m.history)-1 {
				m.histIdx++
			}
			m.text = m.history[len(m.history)-1-m.histIdx]
		}
	case tea.KeyDown:
		if m.histIdx > 0 {
			m.histIdx--
			m.text = m.history[len(m.history)-1-m.histIdx]
		} else {
			m.histIdx = -1
			m.text = ""
		}
	case tea.KeyRunes:
		ch := string(msg.Runes)
		// If typing / on empty line, switch to menu mode
		if ch == "/" && m.text == "" {
			m.mode = modeMenu
			m.menu = 0
			m.filter = ""
			return m, nil
		}
		m.text += ch
	case tea.KeySpace:
		m.text += " "
	case tea.KeyTab:
		// ignore
	}
	return m, nil
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
			m.filter = m.filter[:len(m.filter)-1]
			m.menu = 0
		} else {
			// Back to typing mode
			m.mode = modeTyping
			m.text = ""
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
	// When done, show only the final input — no menu residue
	if m.done {
		if m.result == "" {
			return ""
		}
		return m.prompt + m.result + "\n"
	}

	var b strings.Builder

	if m.mode == modeTyping {
		b.WriteString(m.prompt)
		b.WriteString(m.text)
		b.WriteString("█")
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
	Text  string
	CtrlC bool
}

// ReadInput runs the bubbletea input widget and returns the submitted text.
func ReadInput(prompt string, history []string) InputResult {
	m := newInputModel(prompt, history)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return InputResult{}
	}
	final := result.(inputModel)
	return InputResult{
		Text:  final.result,
		CtrlC: final.ctrlC,
	}
}
