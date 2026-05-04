package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func applyInputKey(t *testing.T, m inputModel, msg tea.KeyMsg) inputModel {
	t.Helper()
	updated, _ := m.updateTyping(msg)
	next, ok := updated.(inputModel)
	if !ok {
		t.Fatalf("updateTyping returned %T, want inputModel", updated)
	}
	return next
}

func TestInputCtrlArrowsMoveByWord(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m.text = "alpha beta  gamma"
	m.cursor = len([]rune(m.text))

	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlLeft})
	if got, want := m.cursor, len([]rune("alpha beta  ")); got != want {
		t.Fatalf("ctrl+left cursor = %d, want %d", got, want)
	}

	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlLeft})
	if got, want := m.cursor, len([]rune("alpha ")); got != want {
		t.Fatalf("second ctrl+left cursor = %d, want %d", got, want)
	}

	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlRight})
	if got, want := m.cursor, len([]rune("alpha beta  ")); got != want {
		t.Fatalf("ctrl+right cursor = %d, want %d", got, want)
	}
}

func TestInputCtrlXTripleClearsLine(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m.text = "clear this input"
	m.cursor = len([]rune(m.text))

	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlX})
	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if m.text == "" {
		t.Fatal("line cleared before third ctrl+x")
	}

	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if m.text != "" || m.cursor != 0 {
		t.Fatalf("triple ctrl+x should clear line, got text=%q cursor=%d", m.text, m.cursor)
	}
}

func TestInputCtrlOTogglesOutputMode(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m.text = "keep this draft"
	m.cursor = len([]rune(m.text))

	updated, cmd := m.updateTyping(tea.KeyMsg{Type: tea.KeyCtrlO})
	next, ok := updated.(inputModel)
	if !ok {
		t.Fatalf("updateTyping returned %T, want inputModel", updated)
	}
	if cmd == nil {
		t.Fatal("ctrl+o should quit input so the REPL can toggle output mode")
	}
	if !next.done || !next.outputToggle {
		t.Fatalf("ctrl+o done=%v outputToggle=%v, want both true", next.done, next.outputToggle)
	}
	if next.result != "" || next.ctrlC {
		t.Fatalf("ctrl+o should not submit text or mark ctrl-c, result=%q ctrlC=%v", next.result, next.ctrlC)
	}
}

func TestInputCtrlXClearStreakResets(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m.text = "keep text"
	m.cursor = len([]rune(m.text))

	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlX})
	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlX})
	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if m.text == "" {
		t.Fatal("non-ctrl+x key did not reset clear streak")
	}
}

func TestInputFooterShowsOutputModeAndHotkeys(t *testing.T) {
	m := newInputModelWithOutputMode("qmax > ", nil, true)
	view := m.View()

	for _, want := range []string{"Ctrl+O output: verbose", "Ctrl+X×3 clear", "Ctrl+←/→ words"} {
		if !strings.Contains(view, want) {
			t.Fatalf("input footer missing %q in %q", want, view)
		}
	}
}

func TestInputMarksBracketedPaste(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("large pasted body"), Paste: true})

	if !m.pasted {
		t.Fatal("paste flag was not recorded")
	}
	if !strings.Contains(m.text, "large pasted body") {
		t.Fatalf("pasted text not inserted, got %q", m.text)
	}
}
