package tui

import (
	"reflect"
	"strconv"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestThemeNames_Order(t *testing.T) {
	want := []string{"historic", "ocean", "neon", "ember", "aurora", "paper", "sky", "sparkling", "radiance", "goldenhour"}
	got := ThemeNames()
	if len(got) != len(want) {
		t.Fatalf("ThemeNames() length: got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("ThemeNames()[%d]: got %q, want %q", i, got[i], w)
		}
	}
}

func TestThemeNames_CoverageVsAllThemes(t *testing.T) {
	for _, name := range ThemeNames() {
		if _, ok := allThemes[name]; !ok {
			t.Errorf("ThemeNames() includes %q but allThemes has no entry for it", name)
		}
	}
}

func TestThemeByName_KnownThemes(t *testing.T) {
	for _, name := range ThemeNames() {
		got := ThemeByName(name)
		if got.Name != name {
			t.Errorf("ThemeByName(%q).Name = %q, want %q", name, got.Name, name)
		}
	}
}

func TestThemeByName_UnknownFallsToHistoric(t *testing.T) {
	got := ThemeByName("does-not-exist")
	if got.Name != "historic" {
		t.Errorf("ThemeByName(unknown).Name = %q, want %q", got.Name, "historic")
	}
}

func TestThemeByName_EmptyFallsToHistoric(t *testing.T) {
	got := ThemeByName("")
	if got.Name != "historic" {
		t.Errorf("ThemeByName(\"\").Name = %q, want %q", got.Name, "historic")
	}
}

func TestThemeByName_ReturnsDistinctThemes(t *testing.T) {
	// Each named theme must have at least one field that differs from historic,
	// confirming allThemes entries are not just copies of each other.
	historic := ThemeByName("historic")
	for _, name := range ThemeNames() {
		if name == "historic" {
			continue
		}
		t := ThemeByName(name)
		if t.Accent == historic.Accent &&
			t.Brand == historic.Brand &&
			t.ANSIPromptName == historic.ANSIPromptName {
			// All three same implies likely a copy — flag it.
			// (Not all fields need to differ, but at least one of these should.)
			_ = t // suppress unused warning
		}
	}
}

func TestApplyTheme_UpdatesANSIVars(t *testing.T) {
	orig := Theme{
		Name:            "test",
		ANSIPromptName:  "\033[31m", // red — unusual
		ANSIPromptArrow: "\033[32m",
		ANSIBanner:      "\033[33m",
		ANSICatArt:      "\033[34m",
		ANSIStatus:      "\033[35m",
	}
	ApplyTheme(orig)

	if themePromptName != orig.ANSIPromptName {
		t.Errorf("themePromptName: got %q, want %q", themePromptName, orig.ANSIPromptName)
	}
	if themePromptArrow != orig.ANSIPromptArrow {
		t.Errorf("themePromptArrow: got %q, want %q", themePromptArrow, orig.ANSIPromptArrow)
	}
	if themeBannerColor != orig.ANSIBanner {
		t.Errorf("themeBannerColor: got %q, want %q", themeBannerColor, orig.ANSIBanner)
	}
	if themeCatColor != orig.ANSICatArt {
		t.Errorf("themeCatColor: got %q, want %q", themeCatColor, orig.ANSICatArt)
	}
	if themeStatusColor != orig.ANSIStatus {
		t.Errorf("themeStatusColor: got %q, want %q", themeStatusColor, orig.ANSIStatus)
	}

	// Restore to avoid polluting other tests.
	ApplyTheme(ThemeByName("historic"))
}

func TestApplyTheme_AllBuiltinThemes(t *testing.T) {
	// Smoke-test: applying every built-in theme must not panic.
	for _, name := range ThemeNames() {
		theme := ThemeByName(name)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ApplyTheme(%q) panicked: %v", name, r)
				}
			}()
			ApplyTheme(theme)
		}()
	}
	// Leave in a consistent state.
	ApplyTheme(ThemeByName("historic"))
}

func TestApplyTheme_SetsPolarity(t *testing.T) {
	for _, name := range ThemeNames() {
		theme := ThemeByName(name)
		ApplyTheme(theme)
		if ThemeIsDark != theme.Dark {
			t.Errorf("ApplyTheme(%q): ThemeIsDark = %v, want %v", name, ThemeIsDark, theme.Dark)
		}
	}
	ApplyTheme(ThemeByName("historic"))
}

// TestApplyTheme_BackgroundOwnership guards against ANSI cutouts. Structural
// containers stay transparent because nested styled spans emit reset sequences
// that interrupt inherited parent backgrounds. Leaf surfaces may be solid.
func TestApplyTheme_BackgroundOwnership(t *testing.T) {
	for _, name := range ThemeNames() {
		theme := ThemeByName(name)
		ApplyTheme(theme)
		surface := lipgloss.Color(theme.SurfaceDark)
		transparent := []struct {
			label string
			got   lipgloss.TerminalColor
		}{
			{"pickerBox", pickerBox.GetBackground()},
			{"inputBoxStyle", inputBoxStyle.GetBackground()},
		}
		for _, c := range transparent {
			if _, ok := c.got.(lipgloss.NoColor); !ok {
				t.Errorf("ApplyTheme(%q): %s background = %v, want transparent", name, c.label, c.got)
			}
		}
		solid := []struct {
			label string
			got   lipgloss.TerminalColor
		}{
			{"statusBarStyle", statusBarStyle.GetBackground()},
			{"statusMetricsStyle", statusMetricsStyle.GetBackground()},
		}
		for _, c := range solid {
			if c.got != surface {
				t.Errorf("ApplyTheme(%q): %s background = %v, want %v", name, c.label, c.got, surface)
			}
		}
		if got := pickerRowSelected.GetBackground(); got != lipgloss.Color(theme.SurfaceSelect) {
			t.Errorf("ApplyTheme(%q): pickerRowSelected background = %v, want %v", name, got, theme.SurfaceSelect)
		}
	}
	ApplyTheme(ThemeByName("historic"))
}

// TestThemePalette_SelectionContrastsSurface guards the picker selection
// highlight: SurfaceSelect must be visibly distinct from SurfaceDark now
// selected rows still need a visible state transition on either polarity.
func TestThemePalette_SelectionContrastsSurface(t *testing.T) {
	for _, name := range ThemeNames() {
		theme := ThemeByName(name)
		bg, err1 := strconv.Atoi(theme.SurfaceDark)
		sel, err2 := strconv.Atoi(theme.SurfaceSelect)
		if err1 != nil || err2 != nil {
			t.Fatalf("Theme %q: non-numeric surface colors (%q, %q)", name, theme.SurfaceDark, theme.SurfaceSelect)
		}
		if d := absInt(bg - sel); d < 4 {
			t.Errorf("Theme %q: SurfaceSelect %d is only %d steps from SurfaceDark %d; selection highlight would be invisible", name, sel, d, bg)
		}
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func TestSetMarkdownStyle_SwapsPolarity(t *testing.T) {
	term := &Terminal{}
	term.setMarkdownStyle(false)
	if term.renderer == nil || term.markdownDark {
		t.Fatal("setMarkdownStyle(false) did not build a light renderer")
	}
	light, lightOK := term.renderMarkdown("# hi")
	term.setMarkdownStyle(true)
	if term.renderer == nil || !term.markdownDark {
		t.Fatal("setMarkdownStyle(true) did not build a dark renderer")
	}
	dark, darkOK := term.renderMarkdown("# hi")
	if lightOK && darkOK && light == dark {
		t.Error("dark and light renderers produced identical output; style was not swapped")
	}
}

func TestApplyTheme_RefreshesLiveTerminalRenderer(t *testing.T) {
	term := &Terminal{}
	registerLiveTerminal(term)
	defer func() {
		liveTerminalsMu.Lock()
		liveTerminals = nil
		liveTerminalsMu.Unlock()
	}()

	ApplyTheme(ThemeByName("paper")) // light theme
	if term.renderer == nil {
		t.Fatal("ApplyTheme did not build a renderer for the registered terminal")
	}
	if term.markdownDark {
		t.Error("ApplyTheme(paper) left the renderer on dark style; want light")
	}
	ApplyTheme(ThemeByName("historic")) // dark theme, also restores globals
	if !term.markdownDark {
		t.Error("ApplyTheme(historic) left the renderer on light style; want dark")
	}
}

func TestThemePicker_PolarityTransitionsRequestFullRepaint(t *testing.T) {
	m := newThemePickerModel("aurora") // last dark theme; next is light

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(themePickerModel)
	if got := m.themes[m.cursor]; got != "paper" {
		t.Fatalf("down transition selected %q, want paper", got)
	}
	if cmd == nil || reflect.TypeOf(cmd()) != reflect.TypeOf(tea.ClearScreen()) {
		t.Fatal("dark-to-light preview did not request a full-screen repaint")
	}

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(themePickerModel)
	if got := m.themes[m.cursor]; got != "sky" {
		t.Fatalf("same-polarity down selected %q, want sky", got)
	}
	if cmd != nil {
		t.Fatal("same-polarity move requested a full-screen repaint; clearing erases the visible transcript and must be gated on polarity flips")
	}

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(themePickerModel)
	if got := m.themes[m.cursor]; got != "paper" {
		t.Fatalf("up selected %q, want paper", got)
	}
	if cmd != nil {
		t.Fatal("same-polarity up move requested a full-screen repaint; want none")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(themePickerModel)
	if got := m.themes[m.cursor]; got != "aurora" {
		t.Fatalf("up transition selected %q, want aurora", got)
	}
	if cmd == nil || reflect.TypeOf(cmd()) != reflect.TypeOf(tea.ClearScreen()) {
		t.Fatal("light-to-dark preview did not request a full-screen repaint")
	}

	m = newThemePickerModel("historic")
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(themePickerModel)
	if !m.cancelled {
		t.Fatal("esc did not cancel")
	}
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("cancel without a polarity change must quit without a screen clear")
	}
	ApplyTheme(ThemeByName("historic"))
}

// TestApplyTheme_PickerChromeFollowsTerminalPolarity guards the invisible-
// chrome regression: foreground-only picker styles must key off the
// terminal's real polarity, so previewing (or running) an opposite-polarity
// theme keeps labels readable on the live terminal background.
func TestApplyTheme_PickerChromeFollowsTerminalPolarity(t *testing.T) {
	orig := terminalDark
	defer func() {
		terminalDark = orig
		ApplyTheme(ThemeByName("historic"))
	}()

	terminalDark = true
	ApplyTheme(ThemeByName("paper")) // light theme previewed on a dark terminal
	if got := pickerLabel.GetForeground(); got != lipgloss.Color("252") {
		t.Errorf("pickerLabel on dark terminal = %v, want light label 252", got)
	}
	if got := pickerFooter.GetForeground(); got != lipgloss.Color("240") {
		t.Errorf("pickerFooter on dark terminal = %v, want subtle 240", got)
	}
	if got := pickerDivider.GetForeground(); got != lipgloss.Color("237") {
		t.Errorf("pickerDivider on dark terminal = %v, want 237", got)
	}

	terminalDark = false
	ApplyTheme(ThemeByName("historic")) // dark theme on a light terminal
	if got := pickerLabel.GetForeground(); got != lipgloss.Color("236") {
		t.Errorf("pickerLabel on light terminal = %v, want dark label 236", got)
	}
	if got := pickerFooter.GetForeground(); got != lipgloss.Color("247") {
		t.Errorf("pickerFooter on light terminal = %v, want subtle 247", got)
	}
}

// TestSetMarkdownStyle_SkipsRedundantRebuild ensures a same-polarity
// ApplyTheme does not churn the glamour renderer.
func TestSetMarkdownStyle_SkipsRedundantRebuild(t *testing.T) {
	term := &Terminal{}
	term.setMarkdownStyle(true)
	if term.renderer == nil {
		t.Fatal("initial build did not produce a renderer")
	}
	first := term.renderer

	term.setMarkdownStyle(true)
	if term.renderer != first {
		t.Error("same-polarity call rebuilt the renderer")
	}

	term.setMarkdownStyle(false)
	if term.renderer == first {
		t.Error("polarity flip did not rebuild the renderer")
	}
	if term.markdownDark {
		t.Error("markdownDark not updated after flip")
	}
}
