package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func updateTurnViewport(t *testing.T, m turnViewportModel, msg tea.Msg) (turnViewportModel, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	next, ok := updated.(turnViewportModel)
	if !ok {
		t.Fatalf("Update returned %T, want turnViewportModel", updated)
	}
	return next, cmd
}

func TestTurnViewportKeepsOutputAboveInputBoundary(t *testing.T) {
	m := newTurnViewportModel("qmax > ", &StatusInfo{Backend: "cc", Task: "keep the input visible"}, nil)
	m.width = 90
	m.height = 24
	m, _ = updateTurnViewport(t, m, turnOutputMsg("streamed response"))

	view := m.View()
	for _, want := range []string{
		"streamed response",
		"╭", "╮", "╰", "╯",
		"qmax > █",
		"Enter queue + interrupt",
		"task: keep the input visible",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("active viewport missing %q in %q", want, view)
		}
	}
}

func TestTurnViewportAcceptsMultipleOutputMessages(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m, _ = updateTurnViewport(t, m, turnOutputMsg("first "))
	m, _ = updateTurnViewport(t, m, turnOutputMsg("second"))

	if got, want := m.output.String(), "first second"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestTurnViewportActivityReplacesSpinnerWithoutTouchingInput(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m, _ = updateTurnViewport(t, m, turnThinkingMsg(true))
	m, _ = updateTurnViewport(t, m, turnActivityMsg("Running test... 40%"))

	view := m.View()
	if !strings.Contains(view, "Running test... 40%") {
		t.Fatalf("activity missing from viewport: %q", view)
	}
	if strings.Contains(view, "agent working") {
		t.Fatalf("spinner should yield to specific activity: %q", view)
	}
	if !strings.Contains(view, "qmax > █") {
		t.Fatalf("activity update displaced the input panel: %q", view)
	}
}

func TestTurnViewportSubmitReturnsQueuedInput(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("next prompt"), Paste: true})
	m, cmd := updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if cmd == nil {
		t.Fatal("Enter should interrupt and quit the active viewport")
	}
	if got, want := m.result.Text, "next prompt"; got != want {
		t.Fatalf("queued text = %q, want %q", got, want)
	}
	if !m.result.Canceled || !m.result.Pasted {
		t.Fatalf("result = %#v, want canceled pasted input", m.result)
	}
}

func TestTurnViewportEditingMatchesMainInput(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m.text = []rune("alpha beta")
	m.cursor = len(m.text)
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyCtrlLeft})
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyDelete})

	if got, want := string(m.text), "alpha eta"; got != want {
		t.Fatalf("edited text = %q, want %q", got, want)
	}
}

func TestTurnViewportPreservesDraftWhenTurnFinishes(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("follow up")})
	m, cmd := updateTurnViewport(t, m, turnDoneMsg{})

	if cmd == nil {
		t.Fatal("turn completion should quit the active viewport")
	}
	if got, want := m.result.Text, "follow up"; got != want {
		t.Fatalf("recovered draft = %q, want %q", got, want)
	}
}

func TestTurnViewportOnlyShowsOutputThatFitsAbovePanel(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m.height = 12 // four output lines after reserving the persistent panel
	m.output.WriteString("one\ntwo\nthree\nfour\nfive\nsix")

	visible := m.visibleOutput()
	if strings.Contains(visible, "one") || strings.Contains(visible, "two") {
		t.Fatalf("old output should scroll out of the viewport: %q", visible)
	}
	for _, want := range []string{"three", "four", "five", "six"} {
		if !strings.Contains(visible, want) {
			t.Fatalf("visible output missing %q in %q", want, visible)
		}
	}
}

func TestTurnViewportWrapsLongStreamingLines(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m.width = 20
	m.height = 20
	m.output.WriteString("abcdefghijklmnopqrstuvwxyz")

	visible := m.visibleOutput()
	if !strings.Contains(visible, "\n") || !strings.Contains(visible, "uvwxyz") {
		t.Fatalf("long stream line was not wrapped into visible output: %q", visible)
	}
}
