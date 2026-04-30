package main

import "github.com/charmbracelet/lipgloss"

// Theme defines the full color palette for the terminal UI.
type Theme struct {
	Name string

	// Semantic color roles — 256-color ANSI palette strings for lipgloss.
	Accent     string // tools, badges, star, filter — primary accent
	Brand      string // Claude icon, system msgs, selection highlight
	Success    string // pass/success states
	Error      string // error states

	// Text hierarchy
	TextBright string // selected/focused text
	TextNormal string // normal label text
	TextDim    string // secondary/dimmed text
	TextSubtle string // hints, footers, usage

	// Surfaces
	SurfaceDark   string // status bar background
	SurfaceSelect string // row selection background
	SurfaceBorder string // box border
	SurfaceSep    string // section dividers

	// Icon colors
	IconCodex string // Codex section icon
	IconAPI   string // Direct API icon
	MenuItem  string // slash menu item color

	// ANSI escape sequences for readline prompt and banner (not lipgloss).
	ANSIPromptName  string // "qmax" text in prompt
	ANSIPromptArrow string // ">" arrow in prompt
	ANSIBanner      string // ASCII art banner color
	ANSICatArt      string // Max cat art color
	ANSIStatus      string // ▸ status lines in banner
}

var allThemes = map[string]Theme{
	"historic": {
		Name:          "historic",
		Accent:        "214",
		Brand:         "69",
		Success:       "82",
		Error:         "196",
		TextBright:    "255",
		TextNormal:    "252",
		TextDim:       "242",
		TextSubtle:    "240",
		SurfaceDark:   "236",
		SurfaceSelect: "237",
		SurfaceBorder: "238",
		SurfaceSep:    "237",
		IconCodex:     "107",
		IconAPI:       "242",
		MenuItem:      "75",
		ANSIPromptName:  colorCyan,
		ANSIPromptArrow: colorMagenta,
		ANSIBanner:      colorCyan,
		ANSICatArt:      colorYellow,
		ANSIStatus:      colorGreen,
	},
	"ocean": {
		Name:          "ocean",
		Accent:        "51",
		Brand:         "33",
		Success:       "49",
		Error:         "203",
		TextBright:    "255",
		TextNormal:    "252",
		TextDim:       "242",
		TextSubtle:    "240",
		SurfaceDark:   "236",
		SurfaceSelect: "237",
		SurfaceBorder: "238",
		SurfaceSep:    "237",
		IconCodex:     "120",
		IconAPI:       "242",
		MenuItem:      "87",
		ANSIPromptName:  colorCyan,
		ANSIPromptArrow: colorBlue,
		ANSIBanner:      colorCyan,
		ANSICatArt:      colorCyan,
		ANSIStatus:      colorCyan,
	},
	"neon": {
		Name:          "neon",
		Accent:        "201",
		Brand:         "51",
		Success:       "46",
		Error:         "197",
		TextBright:    "255",
		TextNormal:    "252",
		TextDim:       "242",
		TextSubtle:    "240",
		SurfaceDark:   "235",
		SurfaceSelect: "236",
		SurfaceBorder: "237",
		SurfaceSep:    "235",
		IconCodex:     "135",
		IconAPI:       "242",
		MenuItem:      "159",
		ANSIPromptName:  colorMagenta,
		ANSIPromptArrow: colorCyan,
		ANSIBanner:      colorMagenta,
		ANSICatArt:      colorCyan,
		ANSIStatus:      colorMagenta,
	},
	"ember": {
		Name:          "ember",
		Accent:        "208",
		Brand:         "202",
		Success:       "220",
		Error:         "160",
		TextBright:    "255",
		TextNormal:    "252",
		TextDim:       "242",
		TextSubtle:    "240",
		SurfaceDark:   "236",
		SurfaceSelect: "237",
		SurfaceBorder: "238",
		SurfaceSep:    "237",
		IconCodex:     "214",
		IconAPI:       "242",
		MenuItem:      "215",
		ANSIPromptName:  colorRed,
		ANSIPromptArrow: colorYellow,
		ANSIBanner:      colorRed,
		ANSICatArt:      colorYellow,
		ANSIStatus:      colorYellow,
	},
	"aurora": {
		Name:          "aurora",
		Accent:        "49",
		Brand:         "141",
		Success:       "120",
		Error:         "204",
		TextBright:    "255",
		TextNormal:    "252",
		TextDim:       "242",
		TextSubtle:    "240",
		SurfaceDark:   "236",
		SurfaceSelect: "237",
		SurfaceBorder: "238",
		SurfaceSep:    "237",
		IconCodex:     "71",
		IconAPI:       "242",
		MenuItem:      "123",
		ANSIPromptName:  colorMagenta,
		ANSIPromptArrow: colorGreen,
		ANSIBanner:      colorGreen,
		ANSICatArt:      colorMagenta,
		ANSIStatus:      colorGreen,
	},
}

// ThemeNames returns available theme names in display order.
func ThemeNames() []string {
	return []string{"historic", "ocean", "neon", "ember", "aurora"}
}

// ThemeByName returns the named theme, defaulting to "historic".
func ThemeByName(name string) Theme {
	if t, ok := allThemes[name]; ok {
		return t
	}
	return allThemes["historic"]
}

// Package-level vars for theme-driven ANSI colors used in the prompt and banner.
// Initialized to historic defaults; ApplyTheme overwrites them.
var (
	themePromptName  = colorCyan
	themePromptArrow = colorMagenta
	themeBannerColor = colorCyan
	themeCatColor    = colorYellow
	themeStatusColor = colorGreen
)

// ApplyTheme rebuilds all lipgloss styles and ANSI prompt vars from t.
// Must be called before NewTerminal() and ShowModelPicker().
func ApplyTheme(t Theme) {
	// ANSI prompt/banner vars
	themePromptName = t.ANSIPromptName
	themePromptArrow = t.ANSIPromptArrow
	themeBannerColor = t.ANSIBanner
	themeCatColor = t.ANSICatArt
	themeStatusColor = t.ANSIStatus

	// terminal.go styles
	styleTool = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Bold(true)
	styleToolDim = lipgloss.NewStyle().Foreground(lipgloss.Color(t.TextDim))
	styleError = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Error)).Bold(true)
	styleSystem = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Brand)).Bold(true)
	styleSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Success))
	styleDim = lipgloss.NewStyle().Foreground(lipgloss.Color(t.TextDim))
	styleUsage = lipgloss.NewStyle().Foreground(lipgloss.Color(t.TextSubtle))

	// tui_backend.go styles
	pickerBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.SurfaceBorder)).
		Padding(0, 1)
	pickerSectionHeader = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.TextDim)).
		PaddingTop(1)
	pickerRowSelected = lipgloss.NewStyle().
		Background(lipgloss.Color(t.SurfaceSelect)).
		Bold(true)
	pickerRowNormal = lipgloss.NewStyle()
	pickerIcon = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Brand))
	pickerIconCodex = lipgloss.NewStyle().Foreground(lipgloss.Color(t.IconCodex))
	pickerIconAPI = lipgloss.NewStyle().Foreground(lipgloss.Color(t.IconAPI))
	pickerLabel = lipgloss.NewStyle().Foreground(lipgloss.Color(t.TextNormal))
	pickerLabelSel = lipgloss.NewStyle().Foreground(lipgloss.Color(t.TextBright)).Bold(true)
	pickerBadgeNew = lipgloss.NewStyle().
		Foreground(lipgloss.Color("0")).
		Background(lipgloss.Color(t.Accent)).
		Bold(true).
		PaddingLeft(1).PaddingRight(1)
	pickerBadgeCurrent = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Success)).Bold(true)
	pickerBadgeStar = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent))
	pickerBadgeExt = lipgloss.NewStyle().Foreground(lipgloss.Color(t.TextDim))
	pickerShortcut = lipgloss.NewStyle().Foreground(lipgloss.Color(t.TextSubtle))
	pickerDivider = lipgloss.NewStyle().Foreground(lipgloss.Color(t.SurfaceSep))
	effortLabelActive = lipgloss.NewStyle().
		Foreground(lipgloss.Color("0")).
		Background(lipgloss.Color(t.Brand)).
		Bold(true).
		PaddingLeft(2).PaddingRight(2)
	effortLabelInactive = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.TextDim)).
		PaddingLeft(2).PaddingRight(2)
	pickerFooter = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.TextSubtle)).
		PaddingTop(1)
	pickerStatusBar = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.TextNormal)).
		Background(lipgloss.Color(t.SurfaceDark)).
		PaddingLeft(1).PaddingRight(1)
	pickerStatusIcon = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Brand)).
		Background(lipgloss.Color(t.SurfaceDark)).
		Bold(true)
	pickerStatusIconCodex = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.IconCodex)).
		Background(lipgloss.Color(t.SurfaceDark)).
		Bold(true)
	pickerStatusEffort = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Accent)).
		Background(lipgloss.Color(t.SurfaceDark)).
		Bold(true)

	// input.go styles
	menuSelStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("0")).
		Background(lipgloss.Color(t.Brand)).
		Bold(true).
		PaddingLeft(1).PaddingRight(1)
	menuItemStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.MenuItem)).
		PaddingLeft(1)
	menuDescStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.TextDim))
	menuDescSelSty = lipgloss.NewStyle().
		Foreground(lipgloss.Color("0")).
		Background(lipgloss.Color(t.Brand))
	menuHintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.TextSubtle))
	filterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Bold(true)
}
