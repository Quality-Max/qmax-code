package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPickerIncludesCerebrasEntries(t *testing.T) {
	m := newModelPickerModel("", "", "high", "", "", true, true, false, false, nil)
	var got []string
	for _, e := range m.allEntries {
		if e.backend == "cerebras" {
			got = append(got, e.modelID)
		}
	}
	if len(got) != len(cerebrasModels) {
		t.Fatalf("expected %d cerebras entries, got %d (%v)", len(cerebrasModels), len(got), got)
	}
	if got[0] != "gpt-oss-120b" {
		t.Errorf("first cerebras model = %q, want gpt-oss-120b", got[0])
	}
}

func TestPickerCerebrasSectionRenders(t *testing.T) {
	// No key configured → status should advertise the inline prompt.
	m := newModelPickerModel("", "", "high", "", "", true, true, false, false, nil)
	view := m.View()
	if !strings.Contains(view, "Cerebras") {
		t.Error("picker view missing Cerebras section header")
	}
	if !strings.Contains(view, "no key") {
		t.Error("picker should show 'no key' status when CerebrasKeySet is false")
	}

	// Key configured → status should say so.
	m2 := newModelPickerModel("", "", "high", "", "", true, true, true, false, nil)
	if !strings.Contains(m2.View(), "key set") {
		t.Error("picker should show 'key set' status when CerebrasKeySet is true")
	}
}

func TestPickerCerebrasCursorOnCurrent(t *testing.T) {
	// When cerebras is the active backend, the cursor should land on the
	// matching model entry.
	m := newModelPickerModel("cerebras", "zai-glm-4.7", "high", "", "", true, true, true, false, nil)
	cur := m.allEntries[m.cursor]
	if cur.backend != "cerebras" || cur.modelID != "zai-glm-4.7" {
		t.Errorf("cursor on %s/%s, want cerebras/zai-glm-4.7", cur.backend, cur.modelID)
	}
}

func TestPickerEnterConfirmsWhileEffortFocused(t *testing.T) {
	m := newModelPickerModel("cerebras", "gemma-4-31b", "high", "", "", true, true, true, false, nil)
	m.effortFocus = true
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := updated.(modelPickerModel)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if cmd == nil {
		t.Fatal("Enter should quit the picker")
	}
	if next.chosen == nil {
		t.Fatal("Enter with effort focused discarded the selection")
	}
	if next.chosen.backend != "cerebras" || next.chosen.modelID != "gemma-4-31b" {
		t.Errorf("chose %s/%s, want cerebras/gemma-4-31b", next.chosen.backend, next.chosen.modelID)
	}
	if next.effort != "high" {
		t.Errorf("effort = %q, want high", next.effort)
	}
}

func TestPickerOpenCodeSetupRowsAreEnableActions(t *testing.T) {
	models := []OpenCodeModelEntry{
		{ProviderID: "zai-coding-plan", ProviderName: "Enter to enable", ModelID: "enable:zai-coding-plan", Label: "Z.AI Coding Plan"},
	}
	m := newModelPickerModel("", "", "high", "", "", true, true, false, true, models)
	view := m.View()
	if !strings.Contains(view, "opencode") {
		t.Fatal("picker missing opencode section for setup rows")
	}
	if !strings.Contains(view, "Z.AI Coding Plan") {
		t.Fatal("picker missing Z.AI setup row")
	}
	if !strings.Contains(view, "not enabled") {
		t.Fatal("picker should say the provider is not enabled yet")
	}
	var enable []pickerEntry
	for _, e := range m.allEntries {
		if e.backend == "opencode-enable" {
			enable = append(enable, e)
		}
	}
	if len(enable) != 1 || enable[0].modelID != "zai-coding-plan" {
		t.Fatalf("setup rows = %#v, want one opencode-enable/zai-coding-plan", enable)
	}
}

func TestProviderPickerEnterEnablesHighlighted(t *testing.T) {
	m := newProviderPickerModel([]ProviderPickerItem{
		{ID: "zai-coding-plan", DisplayName: "Z.AI Coding Plan", Status: "available", Allowed: true},
		{ID: "groq", DisplayName: "Groq", Status: "available", Allowed: true},
		{ID: "openrouter", DisplayName: "OpenRouter", Status: "available", Allowed: true},
	})
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want first available provider", m.cursor)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next := updated.(providerPickerModel)
	if next.cursor != 1 {
		t.Fatalf("after ↓ cursor = %d, want 1 (groq)", next.cursor)
	}
	updated, cmd := next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(providerPickerModel)
	if cmd == nil {
		t.Fatal("Enter should quit the picker")
	}
	if !next.confirmed || next.cancelled {
		t.Fatal("Enter should confirm the highlighted provider")
	}
	if next.items[next.cursor].ID != "groq" {
		t.Errorf("confirmed %q, want groq", next.items[next.cursor].ID)
	}
}

func TestProviderPickerEnterSkipsLocked(t *testing.T) {
	m := newProviderPickerModel([]ProviderPickerItem{
		{ID: "groq", DisplayName: "Groq", Status: "not entitled", Allowed: false},
	})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(providerPickerModel)
	if cmd != nil {
		t.Fatal("Enter on a locked provider should stay in the picker")
	}
	if next.confirmed {
		t.Fatal("locked provider must not confirm")
	}
}

func TestProviderPickerViewShowsRows(t *testing.T) {
	m := newProviderPickerModel([]ProviderPickerItem{
		{ID: "zai-coding-plan", DisplayName: "Z.AI Coding Plan", Status: "available", Allowed: true},
		{ID: "groq", DisplayName: "Groq", Status: "enabled", Enabled: true, Allowed: true},
	})
	view := m.View()
	if !strings.Contains(view, "Z.AI Coding Plan") || !strings.Contains(view, "Groq") {
		t.Fatal("picker missing provider names")
	}
	if !strings.Contains(view, "Enter enable") {
		t.Fatal("picker missing Enter enable hint")
	}
}
