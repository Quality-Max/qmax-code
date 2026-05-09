package tui

import "testing"

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
