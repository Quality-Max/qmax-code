package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func pickerRows() []SettingsRow {
	return []SettingsRow{
		{Key: "autosave", Label: "Auto-save", Kind: SettingsToggle, Value: "false"},
		{Key: "theme", Label: "Theme", Kind: SettingsCycle, Value: "ocean", Options: []string{"ocean", "forest", "sunset"}},
		{Key: "budget", Label: "Budget", Kind: SettingsText, Value: "1000"},
	}
}

func updateSettings(t *testing.T, m settingsPickerModel, msg tea.KeyMsg) settingsPickerModel {
	t.Helper()
	updated, _ := m.Update(msg)
	next, ok := updated.(settingsPickerModel)
	if !ok {
		t.Fatalf("Update returned %T, want settingsPickerModel", updated)
	}
	return next
}

func TestSettingsPickerToggleFlipsValue(t *testing.T) {
	m := newSettingsPickerModel(pickerRows())
	m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.rows[0].Value != "true" {
		t.Fatalf("Enter on toggle: value = %q, want true", m.rows[0].Value)
	}
	m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.rows[0].Value != "false" {
		t.Fatalf("second Enter on toggle: value = %q, want false", m.rows[0].Value)
	}
}

func TestSettingsPickerCycleAdvancesAndWraps(t *testing.T) {
	m := newSettingsPickerModel(pickerRows())
	m.cursor = 1

	m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.rows[1].Value != "forest" {
		t.Fatalf("cycle 1: value = %q, want forest", m.rows[1].Value)
	}
	m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.rows[1].Value != "sunset" {
		t.Fatalf("cycle 2: value = %q, want sunset", m.rows[1].Value)
	}
	m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.rows[1].Value != "ocean" {
		t.Fatalf("cycle 3 should wrap: value = %q, want ocean", m.rows[1].Value)
	}
}

func TestSettingsPickerTextEditCommitsAndCancels(t *testing.T) {
	m := newSettingsPickerModel(pickerRows())
	m.cursor = 2

	m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.editing {
		t.Fatal("Enter on text row should open the inline editor")
	}
	// The editor pre-fills the current value ("1000") — clear it before typing.
	for range "1000" {
		m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	for _, r := range "5000" {
		m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.editing {
		t.Fatal("Enter in the editor should close it")
	}
	if m.rows[2].Value != "5000" {
		t.Fatalf("committed value = %q, want 5000", m.rows[2].Value)
	}

	// Esc during editing discards the buffer, keeping the row value.
	m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}})
	m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.editing {
		t.Fatal("Esc should close the editor")
	}
	if m.rows[2].Value != "5000" {
		t.Fatalf("Esc-cancelled edit leaked into value: %q", m.rows[2].Value)
	}
}

func TestSettingsPickerChangesOnlyReportsChangedRows(t *testing.T) {
	m := newSettingsPickerModel(pickerRows())
	m = updateSettings(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // flip autosave

	changes := m.changes()
	if len(changes) != 1 {
		t.Fatalf("changes = %v, want exactly the flipped autosave row", changes)
	}
	if changes["autosave"] != "true" {
		t.Fatalf("changes[autosave] = %q, want true", changes["autosave"])
	}
}

func TestSettingsPickerSaveAndDiscardKeys(t *testing.T) {
	m := newSettingsPickerModel(pickerRows())

	saved := updateSettings(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if !saved.done || !saved.saved {
		t.Fatal("'s' should save and exit")
	}

	discarded := updateSettings(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if !discarded.done || discarded.saved {
		t.Fatal("Esc should exit without saving")
	}
}

func TestNextCycleOptionFallsBackToFirst(t *testing.T) {
	got := nextCycleOption([]string{"a", "b"}, "not-in-list")
	if got != "a" {
		t.Fatalf("nextCycleOption fallback = %q, want a", got)
	}
	if got := nextCycleOption(nil, "x"); got != "x" {
		t.Fatalf("empty options should keep current, got %q", got)
	}
}

func TestSettingsPickerViewAndShow(t *testing.T) {
	rows := pickerRows()
	rows[2].Hint = "0 = unlimited"
	rows[1].Display = func(v string) string { return "[" + v + "]" }
	var model tea.Model = newSettingsPickerModel(rows)
	if model.Init() != nil {
		t.Fatal("settings Init should be nil")
	}
	steps := []tea.KeyMsg{
		{Type: tea.KeyUp}, {Type: tea.KeyDown}, {Type: tea.KeyDown}, {Type: tea.KeyDown},
		{Type: tea.KeyEnter}, {Type: tea.KeyBackspace}, {Type: tea.KeyRunes, Runes: []rune("5")}, {Type: tea.KeyEsc},
		{Type: tea.KeyEnter}, {Type: tea.KeyRunes, Runes: []rune("9")}, {Type: tea.KeyEnter},
		{Type: tea.KeyUp}, {Type: tea.KeyEnter}, {Type: tea.KeyRunes, Runes: []rune("x")},
	}
	for _, k := range steps {
		model, _ = model.Update(k)
		if model.View() == "" {
			t.Fatalf("empty view after %v", k)
		}
	}
	if _, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Fatal("q should quit")
	}
	if _, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd == nil {
		t.Fatal("esc should quit")
	}

	scriptedPrograms(t, "\x1b[B\r\x1b[B\r12\rs")
	res := ShowSettingsPicker(pickerRows())
	if !res.Confirmed || res.Changes["theme"] != "forest" || res.Changes["budget"] != "100012" {
		t.Fatalf("ShowSettingsPicker = %+v", res)
	}
	scriptedPrograms(t, "q")
	if res := ShowSettingsPicker(pickerRows()); res.Confirmed {
		t.Fatalf("q should not confirm: %+v", res)
	}
}
