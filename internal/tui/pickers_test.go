package tui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// keyMsg builds a Bubble Tea key event from a readable name.
func keyMsg(name string) tea.KeyMsg {
	switch name {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// drive sends keys to a model, rendering after every step, and returns the
// final model plus whether any step requested a command.
func drive(t *testing.T, m tea.Model, keys ...string) (tea.Model, bool) {
	t.Helper()
	_ = m.View()
	quit := false
	for _, k := range keys {
		var cmd tea.Cmd
		m, cmd = m.Update(keyMsg(k))
		if cmd != nil {
			quit = true
		}
		if v := m.View(); v == "" {
			t.Fatalf("empty view after %q", k)
		}
	}
	return m, quit
}

// scriptedPrograms makes every picker program read keys from input and render
// nowhere, for the duration of the test.
func scriptedPrograms(t *testing.T, input string) {
	t.Helper()
	orig := programOptions
	programOptions = []tea.ProgramOption{tea.WithInput(strings.NewReader(input)), tea.WithOutput(io.Discard)}
	t.Cleanup(func() { programOptions = orig })
}

func repeat(k string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = k
	}
	return out
}

func TestModelPickerWalksEveryRow(t *testing.T) {
	for _, installed := range []bool{true, false} {
		oc := []OpenCodeModelEntry{
			{ProviderID: "groq", ProviderName: "Groq", ModelID: "groq/llama", Label: "llama"},
			{ProviderID: "openrouter", ProviderName: "Enter to enable", ModelID: "enable:openrouter", Label: "OpenRouter"},
		}
		m := newModelPickerModel("cerebras", "gemma-4-31b", "", "http://u:p@ollama.local", "llama3.2", installed, installed, installed, installed, installed, oc)
		if m.effort != "high" {
			t.Fatalf("default effort = %q", m.effort)
		}
		next, _ := m.Update(ollamaProbeMsg{reachable: installed})
		m = next.(modelPickerModel)
		keys := append(repeat("up", len(m.allEntries)+2), repeat("down", len(m.allEntries)+2)...)
		keys = append(keys, "tab", "left", "left", "left", "right", "right", "right", "h", "l", "tab", "k", "j")
		final, _ := drive(t, m, keys...)
		fm := final.(modelPickerModel)
		if fm.cursor != len(fm.allEntries)-1 {
			t.Fatalf("cursor = %d, want last row", fm.cursor)
		}
		final, quit := drive(t, fm, "enter")
		if !quit || final.(modelPickerModel).chosen == nil || final.(modelPickerModel).chosen.backend != "ollama" {
			t.Fatalf("enter did not choose the last (ollama) row")
		}
	}

	m := newModelPickerModel("cc", "", "low", "", "", true, true, false, false, false, nil)
	final, quit := drive(t, m, "3")
	if !quit || final.(modelPickerModel).chosen == nil {
		t.Fatal("shortcut 3 did not choose a model")
	}
	final, quit = drive(t, m, "q")
	if !quit || !final.(modelPickerModel).cancelled {
		t.Fatal("q did not cancel")
	}
	if _, quit := drive(t, m, "z"); quit {
		t.Fatal("unbound key quit the picker")
	}
	empty := modelPickerModel{}
	if next, _ := empty.Update(keyMsg("enter")); !next.(modelPickerModel).cancelled {
		t.Fatal("enter on an empty picker should cancel")
	}
	if m.Init() != nil {
		t.Fatal("no ollama URL should mean no probe")
	}
}

func TestModelPickerProbesOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("probe path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()
	m := newModelPickerModel("ollama", "m", "", srv.URL, "m", false, false, false, false, false, nil)
	msg := m.Init()()
	if probe, ok := msg.(ollamaProbeMsg); !ok || !probe.reachable {
		t.Fatalf("probe = %#v", msg)
	}
	if probeOllamaReachable("") || probeOllamaReachable("http://127.0.0.1:1") || probeOllamaReachable("://bad") {
		t.Fatal("unreachable endpoints reported reachable")
	}
}

func TestShowModelPickerScripted(t *testing.T) {
	scriptedPrograms(t, "\x1b[B\r")
	res := ShowModelPicker(ModelPickerOpts{CurrentBackend: "cc", CCInstalled: true, Effort: "medium"})
	if !res.Confirmed || res.Effort != "medium" {
		t.Fatalf("result = %+v", res)
	}
	scriptedPrograms(t, "q")
	if res := ShowModelPicker(ModelPickerOpts{}); res.Confirmed {
		t.Fatalf("q should cancel: %+v", res)
	}
}

func TestThemePickerPreviewAndRestore(t *testing.T) {
	t.Cleanup(func() { ApplyTheme(ThemeByName("")) })
	names := ThemeNames()
	m := newThemePickerModel(names[len(names)-1])
	keys := append(repeat("up", len(names)+1), repeat("down", len(names)+1)...)
	final, _ := drive(t, m, keys...)
	if _, quit := drive(t, final, "esc"); !quit {
		t.Fatal("esc did not quit")
	}
	if final, quit := drive(t, final, "enter"); !quit || !final.(themePickerModel).confirmed {
		t.Fatal("enter did not confirm")
	}
	if m.Init() != nil {
		t.Fatal("theme picker Init should be nil")
	}

	scriptedPrograms(t, "\r")
	if got, ok := ShowThemePicker(""); !ok || got == "" {
		t.Fatalf("ShowThemePicker = %q %v", got, ok)
	}
	scriptedPrograms(t, "\x1b[B\x1b[Bq")
	if got, ok := ShowThemePicker(names[0]); ok || got != names[0] {
		t.Fatalf("cancelled ShowThemePicker = %q %v", got, ok)
	}
}

func TestSessionPicker(t *testing.T) {
	now := time.Now()
	sessions := []SessionPickerItem{
		{ID: "a", UpdatedAt: now.Add(-10 * time.Second), Turns: 2, Tokens: 900, Model: ""},
		{ID: "b", UpdatedAt: now.Add(-5 * time.Minute), Turns: 4, Tokens: 12_000, ProjectID: 7, Model: "claude-sonnet-4-6"},
		{ID: "c", UpdatedAt: now.Add(-3 * time.Hour), Turns: 9, Tokens: 2_500_000, Model: "claude-haiku-4-5-20251001"},
		{ID: "d", UpdatedAt: now.Add(-72 * time.Hour), Turns: 1, Tokens: 5, Model: "gpt"},
	}
	m := newSessionPickerModel(sessions, "c")
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want the active session", m.cursor)
	}
	final, _ := drive(t, m, "up", "up", "up", "down", "down", "down", "down", "j", "k")
	if final.(sessionPickerModel).cursor != 2 {
		t.Fatalf("cursor after walk = %d", final.(sessionPickerModel).cursor)
	}
	if _, quit := drive(t, m, "esc"); !quit {
		t.Fatal("esc did not quit")
	}
	if formatModelShort("claude-haiku-4-5-20251001") != "haiku-4.5" || formatModelShort("") != "cc" || formatModelShort("gpt") != "gpt" {
		t.Fatal("formatModelShort")
	}
	if m.Init() != nil {
		t.Fatal("session Init")
	}

	scriptedPrograms(t, "\x1b[B\r")
	if id, ok := ShowSessionPicker(sessions, "a"); !ok || id != "b" {
		t.Fatalf("ShowSessionPicker = %q %v", id, ok)
	}
	scriptedPrograms(t, "q")
	if _, ok := ShowSessionPicker(sessions, "a"); ok {
		t.Fatal("q should cancel")
	}
}

func TestProviderPicker(t *testing.T) {
	items := []ProviderPickerItem{
		{ID: "groq", DisplayName: "Groq", Allowed: true, Enabled: true, Status: "enabled"},
		{ID: "blocked", DisplayName: "Blocked", Allowed: false, Status: "not entitled"},
		{ID: "openrouter", DisplayName: "OpenRouter", Allowed: true, Status: "available"},
	}
	m := newProviderPickerModel(items)
	final, _ := drive(t, m, "down", "down", "down", "up", "up", "up", "2")
	if final.(providerPickerModel).cursor != 1 {
		t.Fatalf("number shortcut cursor = %d", final.(providerPickerModel).cursor)
	}
	if _, quit := drive(t, final, "enter"); quit {
		t.Fatal("enter on a non-entitled provider must not confirm")
	}
	if _, quit := drive(t, final, "9", "ctrl+c"); !quit {
		t.Fatal("ctrl+c did not quit")
	}
	empty := newProviderPickerModel(nil)
	if next, _ := empty.Update(keyMsg("enter")); !next.(providerPickerModel).cancelled {
		t.Fatal("enter with no providers should cancel")
	}
	_ = empty.View()
	if m.Init() != nil {
		t.Fatal("provider Init")
	}

	scriptedPrograms(t, "3\r")
	if res := ShowProviderPicker(items); !res.Confirmed || res.ID != "openrouter" {
		t.Fatalf("ShowProviderPicker = %+v", res)
	}
	scriptedPrograms(t, "q")
	if res := ShowProviderPicker(items); res.Confirmed {
		t.Fatal("q should cancel")
	}
}

func TestCloudSyncAndLiveFeedPickers(t *testing.T) {
	on, off := true, false
	for _, current := range []*bool{nil, &on, &off} {
		m := newCloudSyncPickerModel(current)
		final, _ := drive(t, m, "up", "down", "down", "up", "k", "j")
		if _, quit := drive(t, final, "enter"); !quit {
			t.Fatal("cloud sync enter did not quit")
		}
		if _, quit := drive(t, m, "q"); !quit {
			t.Fatal("cloud sync q did not quit")
		}
		_ = m.Init()
	}
	for _, current := range []bool{true, false} {
		m := newLiveFeedPickerModel(current)
		final, _ := drive(t, m, "up", "down", "down", "up")
		if _, quit := drive(t, final, "space"); !quit {
			t.Fatal("live feed space did not quit")
		}
		if _, quit := drive(t, m, "esc"); !quit {
			t.Fatal("live feed esc did not quit")
		}
		_ = m.Init()
	}

	scriptedPrograms(t, "\x1b[B\r")
	if v, ok := ShowCloudSyncPicker(nil); !ok || v {
		t.Fatalf("ShowCloudSyncPicker = %v %v", v, ok)
	}
	scriptedPrograms(t, "q")
	if _, ok := ShowCloudSyncPicker(&on); ok {
		t.Fatal("cloud sync q should cancel")
	}
	scriptedPrograms(t, "\r")
	if v, ok := ShowLiveFeedPicker(true); !ok || !v {
		t.Fatalf("ShowLiveFeedPicker = %v %v", v, ok)
	}
	scriptedPrograms(t, "q")
	if _, ok := ShowLiveFeedPicker(false); ok {
		t.Fatal("live feed q should cancel")
	}
}
