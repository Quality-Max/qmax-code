package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func km(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func alt(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t, Alt: true} }

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestInputEditingKeys(t *testing.T) {
	m := newInputModel("qmax > ", []string{"first", "second"})
	m.status = &StatusInfo{Backend: "cc", Model: "opus", Effort: "high", PermissionMode: "standard", TokensIn: 1200, TokensOut: 300,
		ContextUsed: 50_000, ContextWindow: 200_000, LastTurnDur: 3 * time.Second, SessionStarted: time.Now().Add(-time.Minute),
		Task: "a long running task description that keeps going", PlanActive: true, PlanRemaining: time.Hour, PlanTurns: 3, OutputVerbose: true}
	if m.Init() == nil {
		t.Fatal("status with a start time should tick")
	}
	steps := []tea.KeyMsg{
		runes("hello"), km(tea.KeySpace), runes("world"),
		km(tea.KeyLeft), km(tea.KeyLeft), km(tea.KeyRight), km(tea.KeyBackspace), km(tea.KeyDelete),
		km(tea.KeyCtrlA), km(tea.KeyCtrlE), km(tea.KeyCtrlW), km(tea.KeyCtrlK), km(tea.KeyCtrlU),
		runes("abc def"), alt(tea.KeyRunes), {Type: tea.KeyRunes, Runes: []rune("b"), Alt: true}, {Type: tea.KeyRunes, Runes: []rune("f"), Alt: true},
		km(tea.KeyHome), km(tea.KeyEnd), km(tea.KeyCtrlX), km(tea.KeyCtrlX), km(tea.KeyCtrlX),
		km(tea.KeyUp), km(tea.KeyUp), km(tea.KeyUp), km(tea.KeyDown), km(tea.KeyDown), km(tea.KeyDown),
		{Type: tea.KeyRunes, Runes: []rune("pasted\ntext"), Paste: true},
	}
	var model tea.Model = m
	for _, k := range steps {
		model, _ = model.Update(k)
		if model.View() == "" {
			t.Fatalf("empty view after %v", k)
		}
	}
	model, _ = model.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	model, cmd := model.Update(statusTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("status tick should reschedule")
	}
	_ = model.View()
	model, _ = model.Update("unknown msg")
	if _, cmd := model.Update(km(tea.KeyEnter)); cmd == nil {
		t.Fatal("enter should submit")
	}
	if next, _ := model.Update(km(tea.KeyCtrlO)); !next.(inputModel).outputToggle {
		t.Fatal("ctrl+o should request an output toggle")
	}
	empty := newInputModel("> ", nil)
	if next, cmd := empty.Update(km(tea.KeyCtrlC)); cmd == nil || !next.(inputModel).ctrlC {
		t.Fatal("ctrl+c should quit")
	}
}

func TestInputSlashMenu(t *testing.T) {
	var model tea.Model = newInputModel("> ", nil)
	model, _ = model.Update(runes("/"))
	if model.(inputModel).mode != modeMenu {
		t.Fatal("slash did not open the menu")
	}
	for _, k := range []tea.KeyMsg{km(tea.KeyUp), km(tea.KeyDown), km(tea.KeyDown), runes("he"), km(tea.KeyBackspace), km(tea.KeyBackspace), km(tea.KeyBackspace)} {
		model, _ = model.Update(k)
		_ = model.View()
	}
	if model.(inputModel).mode != modeTyping {
		t.Fatal("backspace past the filter should close the menu")
	}
	model, _ = model.Update(runes("/"))
	model, _ = model.Update(km(tea.KeyEsc))
	if model.(inputModel).mode != modeTyping {
		t.Fatal("esc should close the menu")
	}
	model, _ = model.Update(runes("/"))
	model, _ = model.Update(runes("update"))
	next, cmd := model.Update(km(tea.KeyEnter))
	if cmd == nil || next.(inputModel).result != "/update" {
		t.Fatalf("exact command = %q", next.(inputModel).result)
	}
	model, _ = newInputModel("> ", nil).Update(runes("/"))
	model, _ = model.Update(runes("cos"))
	if next, _ := model.Update(km(tea.KeyEnter)); next.(inputModel).result != "/cost" {
		t.Fatalf("filtered selection = %q", next.(inputModel).result)
	}
	model, _ = newInputModel("> ", nil).Update(runes("/"))
	model, _ = model.Update(runes("zzzz"))
	if _, cmd := model.Update(km(tea.KeyEnter)); cmd != nil {
		t.Fatal("enter with no matches should not submit")
	}
}

func TestReadInputScripted(t *testing.T) {
	scriptedPrograms(t, "hi there\r")
	if got := ReadInput("> ", nil, false, nil); got.Text != "hi there" {
		t.Fatalf("ReadInput = %+v", got)
	}
	scriptedPrograms(t, "\x0f")
	if got := ReadInput("> ", nil, true, &StatusInfo{}); !got.OutputToggle {
		t.Fatalf("ctrl+o = %+v", got)
	}
}

func TestTurnViewportKeysAndMarks(t *testing.T) {
	canceled := 0
	m := newTurnViewportModel("> ", &StatusInfo{Backend: "api", Model: "sonnet"}, func() { canceled++ })
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	for i := 0; i < 40; i++ {
		model, _ = model.Update(turnOutputMsg("line " + strings.Repeat("x", i%7) + "\n"))
		if i%10 == 0 {
			model, _ = model.Update(turnMarkMsg{})
		}
	}
	model, _ = model.Update(turnOutputMsg("progress 10%\rprogress 90%\n"))
	model, _ = model.Update(turnThinkingMsg(true))
	model, _ = model.Update(turnActivityMsg("Running test... 50%"))
	model, _ = model.Update(turnReplaceTailMsg{rawLen: 3, rendered: "RENDERED\n"})
	keys := []tea.KeyMsg{
		km(tea.KeyUp), km(tea.KeyUp), km(tea.KeyDown), km(tea.KeyPgUp), km(tea.KeyPgDown), km(tea.KeyPgDown),
		alt(tea.KeyUp), alt(tea.KeyUp), alt(tea.KeyUp), alt(tea.KeyUp), alt(tea.KeyDown), alt(tea.KeyDown), alt(tea.KeyDown), alt(tea.KeyDown),
		runes("next prompt"), km(tea.KeySpace), runes("x"), km(tea.KeyLeft), alt(tea.KeyLeft), km(tea.KeyRight), alt(tea.KeyRight),
		km(tea.KeyCtrlLeft), km(tea.KeyCtrlRight), km(tea.KeyCtrlA), km(tea.KeyCtrlE), km(tea.KeyHome), km(tea.KeyEnd),
		{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true}, {Type: tea.KeyRunes, Runes: []rune("f"), Alt: true},
		km(tea.KeyBackspace), km(tea.KeyDelete), km(tea.KeyCtrlW), km(tea.KeyCtrlK), km(tea.KeyCtrlU), runes("queued"),
	}
	for _, k := range keys {
		model, _ = model.Update(k)
		if model.View() == "" {
			t.Fatalf("empty view after %v", k)
		}
	}
	model, _ = model.Update(turnThinkingMsg(false))
	next, cmd := model.Update(km(tea.KeyEnter))
	if cmd == nil || next.(turnViewportModel).result.Text != "queued" {
		t.Fatalf("enter result = %+v", next.(turnViewportModel).result)
	}
	if _, cmd := newTurnViewportModel("> ", nil, nil).Update(km(tea.KeyCtrlC)); cmd == nil {
		t.Fatal("ctrl+c should cancel the turn")
	}
	if _, cmd := newTurnViewportModel("> ", nil, nil).Update(km(tea.KeyEnter)); cmd != nil {
		t.Fatal("enter with empty input should do nothing")
	}
}

func TestRunTurnViewportScripted(t *testing.T) {
	scriptedPrograms(t, "")
	term := &Terminal{}
	out := captureTermOutput(t, func() {
		res := RunTurnViewport(term, "> ", &StatusInfo{}, nil, func() {
			term.StartThinking()
			term.PrintSystem("working")
			term.StreamText("**done**")
			term.FinishMarkdown("**done**")
			term.PrintFileDiff("f.go", "a\n", "b\n")
			if !term.SetTurnActivity("busy") {
				t.Error("activity should route to the live viewport")
			}
			term.StopThinking()
		})
		if res.Canceled {
			t.Errorf("unexpected cancel: %+v", res)
		}
	})
	if !strings.Contains(out, "working") {
		t.Fatalf("turn output was not re-emitted: %q", out)
	}
}
