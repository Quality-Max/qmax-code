package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

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

func TestTurnViewportInsertsSpaceKey(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m.text = []rune("hello")
	m.cursor = len(m.text)
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeySpace})
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("world")})

	if got, want := string(m.text), "hello world"; got != want {
		t.Fatalf("text = %q, want %q (space key was dropped)", got, want)
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

func TestTurnViewportDoneDoesNotOverwriteSubmittedInput(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m.text = []rune("submitted prompt")
	m.cursor = len(m.text)
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	// Simulate another input event winning the race before the backend's done
	// message is delivered. The submitted result must remain authoritative.
	m.text = nil
	m, _ = updateTurnViewport(t, m, turnDoneMsg{})

	if got, want := m.result.Text, "submitted prompt"; got != want {
		t.Fatalf("completed turn replaced submitted input: got %q, want %q", got, want)
	}
}

func TestTurnViewportOnlyShowsOutputThatFitsAbovePanel(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m.height = 12 // four output lines after reserving the persistent panel
	m.appendOutput("one\ntwo\nthree\nfour\nfive\nsix")

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
	m.appendOutput("abcdefghijklmnopqrstuvwxyz")

	visible := m.visibleOutput()
	if !strings.Contains(visible, "\n") || !strings.Contains(visible, "uvwxyz") {
		t.Fatalf("long stream line was not wrapped into visible output: %q", visible)
	}
}

func TestTurnViewportCachesWrappedOutputAcrossTicks(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m.appendOutput("a streaming line that needs wrapping")
	m.width = 12

	_ = m.visibleOutput()
	_ = m.visibleOutput()
	if got, want := m.cache.wraps, 1; got != want {
		t.Fatalf("unchanged output wrapped %d times, want %d", got, want)
	}

	m, _ = updateTurnViewport(t, m, turnTickMsg{})
	_ = m.visibleOutput()
	if got, want := m.cache.wraps, 1; got != want {
		t.Fatalf("timer tick re-wrapped unchanged output %d times, want %d", got, want)
	}

	m.appendOutput(" with more text")
	_ = m.visibleOutput()
	if got, want := m.cache.wraps, 2; got != want {
		t.Fatalf("changed output wrapped %d times, want %d", got, want)
	}
}

func TestTurnViewportBoundsStoredAndLiveOutput(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	// No newline forces the ANSI/UTF-8-safe fallback rather than the normal
	// line-boundary trim.
	huge := "\x1b[31m" + strings.Repeat("é", maxStoredTurnOutput/2+1024)
	m.appendOutput(huge)

	if m.output.Len() > maxStoredTurnOutput {
		t.Fatalf("stored output = %d bytes, want <= %d", m.output.Len(), maxStoredTurnOutput)
	}
	if m.live.Len() > maxLiveOutput {
		t.Fatalf("live output = %d bytes, want <= %d", m.live.Len(), maxLiveOutput)
	}
	if !strings.Contains(m.output.String(), turnOutputTruncatedNotice) {
		t.Fatal("bounded scrollback did not explain that earlier output was omitted")
	}
	if !utf8.ValidString(m.output.String()) || !utf8.ValidString(m.live.String()) {
		t.Fatal("output bounding split a UTF-8 rune")
	}
	if strings.Contains(m.live.String(), "\x1b[") {
		t.Fatal("long-line live suffix retained a partial or active ANSI sequence")
	}
}

func TestTurnViewportCollapsesCarriageReturnProgressRedraws(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	// A subprocess (git clone, npm install, pip, docker, curl -#, ...) piped
	// through a tool call redraws its own progress line with bare '\r'. In
	// this append-only buffer that byte doesn't erase anything — left as-is
	// it would make the real terminal overwrite part of a later line once
	// printed, which is exactly the garbled/overlapping text this collapses.
	m.appendOutput("Cloning into repo...\rReceiving objects:  40%\rReceiving objects: 100%, done.\n")
	m, _ = updateTurnViewport(t, m, turnOutputMsg("next line\r\n"))

	got := m.output.String()
	if strings.ContainsRune(got, '\r') {
		t.Fatalf("output retained a raw carriage return: %q", got)
	}
	if strings.Contains(got, "Cloning") || strings.Contains(got, "40%") {
		t.Fatalf("earlier overwritten progress frames should not survive: %q", got)
	}
	want := "Receiving objects: 100%, done.\nnext line\n"
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestTurnViewportReplaceTailSwapsRawForRendered(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m, _ = updateTurnViewport(t, m, turnOutputMsg("**bold**"))
	m, _ = updateTurnViewport(t, m, turnReplaceTailMsg{rawLen: len("**bold**"), rendered: "BOLD"})

	if got, want := m.output.String(), "BOLD"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if strings.Contains(m.live.String(), "**") {
		t.Fatalf("raw markdown survived the replace: %q", m.live.String())
	}
}

func TestTurnViewportReplaceTailClampsOversizedRawLen(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m, _ = updateTurnViewport(t, m, turnOutputMsg("hi"))

	// rawLen larger than the buffer must clamp instead of underflowing the slice.
	m, _ = updateTurnViewport(t, m, turnReplaceTailMsg{rawLen: 999, rendered: "X"})

	if got, want := m.output.String(), "X"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestTurnViewportScrollKeysMoveOffsetAndClamp(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m.height = 20

	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.scrollOffset != 1 {
		t.Fatalf("scrollOffset after Up = %d, want 1", m.scrollOffset)
	}
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.scrollOffset != 0 {
		t.Fatalf("scrollOffset after Down = %d, want 0", m.scrollOffset)
	}
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.scrollOffset != 0 {
		t.Fatalf("Down at the live tail should clamp at 0, got %d", m.scrollOffset)
	}
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if want := m.outputMaxLines(); m.scrollOffset != want {
		t.Fatalf("scrollOffset after PgUp = %d, want %d", m.scrollOffset, want)
	}
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.scrollOffset != 0 {
		t.Fatalf("scrollOffset after PgDown = %d, want 0", m.scrollOffset)
	}
}

func TestTurnViewportScrollOffsetWindowsVisibleOutput(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m.height = 12 // outputMaxLines = 4
	m.appendOutput("line0\nline1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9")

	m.scrollOffset = 2
	visible := m.visibleOutput()
	for _, want := range []string{"line4", "line5", "line6", "line7"} {
		if !strings.Contains(visible, want) {
			t.Fatalf("scrolled view missing %q: %q", want, visible)
		}
	}
	if strings.Contains(visible, "line8") || strings.Contains(visible, "line9") {
		t.Fatalf("scrolled view should not show the live tail: %q", visible)
	}
}

func TestTurnViewportAltArrowsJumpBetweenDiffMarks(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m.height = 30
	m.width = 80

	m, _ = updateTurnViewport(t, m, turnMarkMsg{})
	m, _ = updateTurnViewport(t, m, turnOutputMsg("+ file_a.go +1 -0\n+ hello\n"))
	m, _ = updateTurnViewport(t, m, turnOutputMsg("some commentary\nmore commentary\n"))
	m, _ = updateTurnViewport(t, m, turnMarkMsg{})
	m, _ = updateTurnViewport(t, m, turnOutputMsg("+ file_b.go +1 -0\n+ world\n"))
	m, _ = updateTurnViewport(t, m, turnOutputMsg("trailing chatter\n"))

	if len(m.diffMarks) != 2 {
		t.Fatalf("diffMarks = %v, want 2 marks", m.diffMarks)
	}

	// Alt+Up from the live tail lands on the most recent (second) mark first.
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	firstStop := m.scrollOffset
	if firstStop <= 0 {
		t.Fatalf("Alt+Up did not scroll back to a mark, offset = %d", firstStop)
	}

	// A second Alt+Up moves further back to the older, first mark.
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	secondStop := m.scrollOffset
	if secondStop <= firstStop {
		t.Fatalf("second Alt+Up should reach an older mark: first=%d second=%d", firstStop, secondStop)
	}

	// Alt+Down walks back toward the live tail, retracing the same marks.
	m, _ = updateTurnViewport(t, m, tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	if m.scrollOffset != firstStop {
		t.Fatalf("Alt+Down = %d, want back to %d", m.scrollOffset, firstStop)
	}
}

func TestTurnViewportPrunesDiffMarksWhenLiveOutputTrims(t *testing.T) {
	m := newTurnViewportModel("qmax > ", nil, nil)
	m, _ = updateTurnViewport(t, m, turnMarkMsg{})
	m, _ = updateTurnViewport(t, m, turnOutputMsg("old diff block\n"))

	// Push enough newline-delimited content through to exceed maxLiveOutput
	// and trigger the front-trim in appendOutput.
	const fillerLine = "filler line\n"
	filler := strings.Repeat(fillerLine, maxLiveOutput/len(fillerLine)+10)
	m, _ = updateTurnViewport(t, m, turnOutputMsg(filler))

	if m.live.Len() > maxLiveOutput {
		t.Fatalf("live output = %d bytes, want <= %d", m.live.Len(), maxLiveOutput)
	}
	liveLines := strings.Count(m.live.String(), "\n")
	for _, ln := range m.diffMarks {
		if ln < 0 || ln > liveLines {
			t.Fatalf("stale diff mark %d out of range after trim (live has %d lines)", ln, liveLines)
		}
	}
	// The mark predating the flood of filler no longer has a valid home in
	// the retained suffix and must be dropped rather than pointing at the
	// wrong line.
	if len(m.diffMarks) != 0 {
		t.Fatalf("expected the stale mark to be pruned by the trim, got %v", m.diffMarks)
	}
}
