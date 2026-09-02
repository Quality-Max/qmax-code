package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// SettingsPicker is a keyboard-driven editor for the qmax-code config that
// /set exposes as raw "key value" text. Rows are one of:
//
//	SettingsToggle — boolean, Enter flips it
//	SettingsCycle  — fixed option list, Enter advances to the next option
//	SettingsText   — free text, Enter opens an inline editor, Enter commits
//
// `s` saves every changed row and exits; Esc (or q) discards and exits. The
// picker itself never touches config — the caller receives the per-key
// changes and applies them through the same code path as /set.

type SettingsRowKind int

const (
	SettingsToggle SettingsRowKind = iota
	SettingsCycle
	SettingsText
)

// SettingsRow describes one editable setting. Value is the CURRENT value:
// "true"/"false" for toggles, one of Options for cycles, raw text otherwise.
// Display may override how Value renders (e.g. "" → "none") without changing
// what is committed.
type SettingsRow struct {
	Key     string
	Label   string
	Kind    SettingsRowKind
	Value   string
	Options []string
	Hint    string
	Display func(value string) string
}

// SettingsPickerResult reports what the user changed. Changes holds only rows
// whose value differs from the initial one, keyed by SettingsRow.Key.
type SettingsPickerResult struct {
	Confirmed bool
	Changes   map[string]string
}

type settingsPickerModel struct {
	rows    []SettingsRow
	initial map[string]string
	cursor  int
	editing bool
	editBuf string
	done    bool
	saved   bool
}

func newSettingsPickerModel(rows []SettingsRow) settingsPickerModel {
	initial := make(map[string]string, len(rows))
	for _, r := range rows {
		initial[r.Key] = r.Value
	}
	return settingsPickerModel{rows: rows, initial: initial}
}

func (m settingsPickerModel) Init() tea.Cmd { return nil }

func (m settingsPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.editing {
			return m.updateEditing(msg)
		}
		return m.updateBrowsing(msg)
	}
	return m, nil
}

func (m settingsPickerModel) updateBrowsing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case tea.KeyEnter:
		if m.cursor >= 0 && m.cursor < len(m.rows) {
			row := &m.rows[m.cursor]
			switch row.Kind {
			case SettingsToggle:
				if row.Value == "true" {
					row.Value = "false"
				} else {
					row.Value = "true"
				}
			case SettingsCycle:
				row.Value = nextCycleOption(row.Options, row.Value)
			case SettingsText:
				m.editing = true
				m.editBuf = row.Value
			}
		}
	case tea.KeyEsc, tea.KeyCtrlC:
		m.done, m.saved = true, false
		return m, tea.Quit
	default:
		// Single-letter shortcuts outside edit mode: s = save, q = quit.
		if msg.Type == tea.KeyRunes {
			switch strings.ToLower(string(msg.Runes)) {
			case "s":
				m.done, m.saved = true, true
				return m, tea.Quit
			case "q":
				m.done, m.saved = true, false
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m settingsPickerModel) updateEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	row := &m.rows[m.cursor]
	switch msg.Type {
	case tea.KeyEnter:
		row.Value = m.editBuf
		m.editing = false
	case tea.KeyEsc, tea.KeyCtrlC:
		m.editing = false
	case tea.KeyBackspace:
		if n := len(m.editBuf); n > 0 {
			runes := []rune(m.editBuf)
			m.editBuf = string(runes[:len(runes)-1])
		}
	case tea.KeyRunes:
		m.editBuf += string(msg.Runes)
	}
	return m, nil
}

func nextCycleOption(options []string, current string) string {
	if len(options) == 0 {
		return current
	}
	for i, o := range options {
		if o == current {
			return options[(i+1)%len(options)]
		}
	}
	return options[0]
}

func (m settingsPickerModel) changes() map[string]string {
	changes := map[string]string{}
	for _, r := range m.rows {
		if r.Value != m.initial[r.Key] {
			changes[r.Key] = r.Value
		}
	}
	return changes
}

func (m settingsPickerModel) rowDisplay(r SettingsRow) string {
	if r.Display != nil {
		return r.Display(r.Value)
	}
	return r.Value
}

func (m settingsPickerModel) View() string {
	var b strings.Builder
	b.WriteString(pickerLabel.Render("  qmax-code settings"))
	b.WriteByte('\n')
	b.WriteString(pickerFooter.Render("  Backend & model: /orch   ·   API keys: /keys"))
	b.WriteByte('\n')
	b.WriteString(pickerDivider.Render(strings.Repeat("─", 52)))
	b.WriteByte('\n')

	for i, r := range m.rows {
		isCursor := i == m.cursor
		arrow := "  "
		if isCursor {
			arrow = pickerBadgeStar.Render("▶ ")
		}

		label := r.Label
		if isCursor {
			label = pickerLabelSel.Render(label)
		} else {
			label = pickerLabel.Render(label)
		}

		value := m.rowDisplay(r)
		if isCursor && m.editing {
			value = pickerBadgeCurrent.Render(m.editBuf + "▌")
		} else if r.Value != m.initial[r.Key] {
			value = pickerBadgeCurrent.Render(value + " *")
		} else {
			value = pickerFooter.Render(value)
		}
		if r.Hint != "" {
			value += pickerFooter.Render("  " + r.Hint)
		}

		row := fmt.Sprintf("%s%-24s %s", arrow, label, value)
		if isCursor {
			b.WriteString(pickerRowSelected.Render(row))
		} else {
			b.WriteString(pickerRowNormal.Render(row))
		}
		b.WriteByte('\n')
	}

	b.WriteString(pickerDivider.Render(strings.Repeat("─", 52)))
	b.WriteByte('\n')
	if m.editing {
		b.WriteString(pickerFooter.Render("  Enter commit  ·  Esc cancel edit"))
	} else {
		b.WriteString(pickerFooter.Render("  ↑↓ navigate  ·  Enter change  ·  s save  ·  Esc/q discard"))
	}
	b.WriteByte('\n')
	return pickerBox.Render(b.String())
}

// ShowSettingsPicker opens the settings editor. It never fails: a program
// error is reported as a plain cancellation.
func ShowSettingsPicker(rows []SettingsRow) SettingsPickerResult {
	m := newSettingsPickerModel(rows)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return SettingsPickerResult{}
	}
	final, ok := result.(settingsPickerModel)
	if !ok || !final.done || !final.saved {
		return SettingsPickerResult{}
	}
	return SettingsPickerResult{Confirmed: true, Changes: final.changes()}
}
