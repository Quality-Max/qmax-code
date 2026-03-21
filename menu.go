package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Slash Command Menu ---

type menuItem struct {
	cmd  string
	desc string
}

type menuModel struct {
	items    []menuItem
	cursor   int
	selected string
	filter   string
	quit     bool
}

func newMenuModel() menuModel {
	return menuModel{
		items: []menuItem{
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
		},
	}
}

func (m menuModel) Init() tea.Cmd { return nil }

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			filtered := m.filteredItems()
			if len(filtered) > 0 && m.cursor < len(filtered) {
				m.selected = filtered[m.cursor].cmd
			}
			return m, tea.Quit
		case "esc", "ctrl+c":
			m.quit = true
			return m, tea.Quit
		case "up", "k":
			filtered := m.filteredItems()
			if m.cursor > 0 {
				m.cursor--
			} else {
				m.cursor = len(filtered) - 1
			}
		case "down", "j":
			filtered := m.filteredItems()
			if m.cursor < len(filtered)-1 {
				m.cursor++
			} else {
				m.cursor = 0
			}
		case "backspace":
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
				m.cursor = 0
			}
		default:
			if len(msg.String()) == 1 {
				m.filter += msg.String()
				m.cursor = 0
			}
		}
	}
	return m, nil
}

func (m menuModel) filteredItems() []menuItem {
	if m.filter == "" {
		return m.items
	}
	var filtered []menuItem
	lower := strings.ToLower(m.filter)
	for _, item := range m.items {
		if strings.Contains(strings.ToLower(item.cmd), lower) ||
			strings.Contains(strings.ToLower(item.desc), lower) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

var (
	menuSelected = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("69")).
			Bold(true).
			PaddingLeft(1).
			PaddingRight(1)

	menuNormal = lipgloss.NewStyle().
			Foreground(lipgloss.Color("75")).
			PaddingLeft(1)

	menuDesc = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242"))

	menuDescSelected = lipgloss.NewStyle().
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("69"))

	menuFilter = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)

	menuHint = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

func (m menuModel) View() string {
	var b strings.Builder

	filtered := m.filteredItems()

	if m.filter != "" {
		b.WriteString(fmt.Sprintf("  %s %s\n", menuFilter.Render("filter:"), m.filter))
	}

	if len(filtered) == 0 {
		b.WriteString(menuHint.Render("  No matching commands\n"))
		return b.String()
	}

	for i, item := range filtered {
		if i == m.cursor {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				menuSelected.Render(fmt.Sprintf("%-11s", item.cmd)),
				menuDescSelected.Render(item.desc)))
		} else {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				menuNormal.Render(fmt.Sprintf("%-11s", item.cmd)),
				menuDesc.Render(item.desc)))
		}
	}

	b.WriteString(menuHint.Render("  ↑↓ navigate • enter select • esc cancel • type to filter"))

	return b.String()
}

// RunSlashMenu shows an interactive selector and returns the chosen command.
// Returns empty string if cancelled.
func RunSlashMenu() string {
	m := newMenuModel()
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return ""
	}
	final := result.(menuModel)
	if final.quit {
		return ""
	}
	return final.selected
}
