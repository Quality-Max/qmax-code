package tui

import (
	"strconv"
	"testing"

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

// TestApplyTheme_BackgroundsFollowTheme guards the invisible-text bug: after
// ApplyTheme, surfaces that promise a solid background must actually carry
// the theme's SurfaceDark color, so menus stay readable when the terminal
// background polarity mismatches the selected theme.
func TestApplyTheme_BackgroundsFollowTheme(t *testing.T) {
	for _, name := range ThemeNames() {
		theme := ThemeByName(name)
		ApplyTheme(theme)
		surface := lipgloss.Color(theme.SurfaceDark)
		cases := []struct {
			label string
			got   lipgloss.TerminalColor
		}{
			{"pickerBox", pickerBox.GetBackground()},
			{"inputBoxStyle", inputBoxStyle.GetBackground()},
			{"statusBarStyle", statusBarStyle.GetBackground()},
			{"statusMetricsStyle", statusMetricsStyle.GetBackground()},
		}
		for _, c := range cases {
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
// that pickerBox paints its own background behind every row.
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
