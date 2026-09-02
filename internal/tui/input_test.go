package tui

import (
	"strings"
	"testing"
	"time"

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

func TestInputCtrlArrowsStopOnPunctuation(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m.text = "src/foo/bar.go"
	m.cursor = len([]rune(m.text))

	want := []int{
		len([]rune("src/foo/bar.")),
		len([]rune("src/foo/")),
		len([]rune("src/")),
		0,
	}
	for i, w := range want {
		m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlLeft})
		if m.cursor != w {
			t.Fatalf("ctrl+left step %d: cursor = %d, want %d", i+1, m.cursor, w)
		}
	}
}

func TestInputCtrlWDeletesPathSegment(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m.text = "src/foo/bar.go"
	m.cursor = len([]rune(m.text))

	m = applyInputKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlW})
	if m.text != "src/foo/bar." {
		t.Fatalf("ctrl+w on path: text = %q, want %q", m.text, "src/foo/bar.")
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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	next, ok := updated.(inputModel)
	if !ok {
		t.Fatalf("Update returned %T, want inputModel", updated)
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

func TestInputCtrlOTogglesFromMenuMode(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m.mode = modeMenu

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	next, ok := updated.(inputModel)
	if !ok {
		t.Fatalf("Update returned %T, want inputModel", updated)
	}
	if cmd == nil {
		t.Fatal("ctrl+o from menu mode should quit input")
	}
	if !next.done || !next.outputToggle {
		t.Fatalf("ctrl+o from menu mode done=%v outputToggle=%v, want both true", next.done, next.outputToggle)
	}
}

// TestSlashMenuFilterRanksCmdMatchAheadOfDescMatch pins the /update → /set bug:
// /set's description ("Update config") description-matches the filter "update",
// so when /update is also in the table the command-name match must rank first.
func TestSlashMenuFilterRanksCmdMatchAheadOfDescMatch(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m.mode = modeMenu
	m.filter = "update"

	filtered := m.filteredMenuItems()
	if len(filtered) < 2 {
		t.Fatalf("filter 'update' should match /update by Cmd and /set by Desc, got %d items", len(filtered))
	}
	if filtered[0].Cmd != "/update" {
		t.Fatalf("first filtered item = %q, want /update (Cmd match outranks Desc match)", filtered[0].Cmd)
	}
	foundSet := false
	for _, item := range filtered[1:] {
		if item.Cmd == "/set" {
			foundSet = true
		}
	}
	if !foundSet {
		t.Errorf("/set (Desc 'Update config') should still be reachable, ranked after Cmd matches")
	}
}

// TestSlashMenuEnterSubmitsExactTypedCommand pins the hijack itself: with the
// filter exactly naming a command, Enter must submit that command verbatim —
// not whichever description-matched row the selection happens to sit on.
func TestSlashMenuEnterSubmitsExactTypedCommand(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m.mode = modeMenu
	m.filter = "update"

	filtered := m.filteredMenuItems()
	setIdx := -1
	for i, item := range filtered {
		if item.Cmd == "/set" {
			setIdx = i
		}
	}
	if setIdx < 0 {
		t.Fatal("precondition: /set should be in the filtered list via its description")
	}
	// Point the selection straight at the /set row — the exact-match rule must
	// still submit /update.
	m.menu = setIdx

	updated, _ := m.updateMenu(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := updated.(inputModel)
	if !ok {
		t.Fatalf("updateMenu returned %T, want inputModel", updated)
	}
	if !next.done || next.result != "/update" {
		t.Fatalf("Enter with filter 'update' selected /set: result = %q done=%v, want /update", next.result, next.done)
	}
}

// TestSlashMenuCoversCriticalCommands guards the other half of the bug: a
// command handled by the REPL but missing from the menu can never be typed
// exactly — the menu intercepts "/" and Enter submits a filtered row instead.
func TestSlashMenuCoversCriticalCommands(t *testing.T) {
	have := map[string]bool{}
	for _, item := range slashMenuItems {
		have[item.Cmd] = true
	}
	critical := []string{
		"/update", "/context", "/gemma", "/plan", // were missing
		"/orch", "/help", "/set", "/settings", "/clear", "/quit", "/gate",
	}
	for _, cmd := range critical {
		if !have[cmd] {
			t.Errorf("%s handled by the REPL but missing from the slash menu", cmd)
		}
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

	for _, want := range []string{"Ctrl+O output: verbose", "Ctrl+X×3 clear", "Opt+←/→ words"} {
		if !strings.Contains(view, want) {
			t.Fatalf("input footer missing %q in %q", want, view)
		}
	}
}

func TestSubmittedInputKeepsPanelBoundaryForStream(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m.width = 80
	m.done = true
	m.result = "check projects"

	view := m.View()
	for _, want := range []string{"╭", "╯", "qmax > check projects"} {
		if !strings.Contains(view, want) {
			t.Fatalf("submitted input should retain panel boundary %q in %q", want, view)
		}
	}
}

func TestInputSeparatesPromptAndRendersSessionStatus(t *testing.T) {
	m := newInputModelWithOutputMode("qmax > ", nil, false)
	m.width = 100
	m.status = &StatusInfo{
		Backend:        "cc",
		Model:          "claude-sonnet-5",
		Effort:         "high",
		PermissionMode: "standard",
		Task:           "fix the spacing around the input panel",
		TokensIn:       12_300,
		TokensOut:      4_500,
		ContextUsed:    16_800,
		ContextWindow:  200_000,
		LastTurnDur:    42 * time.Second,
		SessionStarted: time.Now().Add(-2*time.Minute - 10*time.Second),
	}

	view := m.View()
	for _, want := range []string{
		"╭", "╮", "╰", "╯", // a distinct bordered input field
		"ctx 16.8k/200.0k (8%)",
		"tokens 12.3k in / 4.5k out",
		"last turn 42s",
		"session 2m10s",
		"-- STANDARD --",
		"cc · claude-sonnet-5 · high effort",
		"task: fix the spacing around the input panel",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("input status missing %q in %q", want, view)
		}
	}
}

func TestInputRendersStatusInSlashMenu(t *testing.T) {
	m := newInputModel("qmax > ", nil)
	m.width = 100
	m.mode = modeMenu
	m.status = &StatusInfo{Backend: "cc", PermissionMode: "standard", Task: "review the diff"}

	view := m.View()
	for _, want := range []string{"↑↓ navigate", "-- STANDARD --", "task: review the diff"} {
		if !strings.Contains(view, want) {
			t.Fatalf("slash menu status missing %q in %q", want, view)
		}
	}
}

func TestStatusUsesLiveSessionStart(t *testing.T) {
	s := &StatusInfo{SessionStarted: time.Now().Add(-2 * time.Minute)}
	if got := time.Since(s.SessionStarted); got < 2*time.Minute || got > 2*time.Minute+time.Second {
		t.Fatalf("live session duration = %s, want about 2m", got)
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
