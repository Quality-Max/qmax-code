package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/qualitymax/qmax-code/internal/api"
)

// Theme defines the full color palette for the terminal UI.
type Theme struct {
	Name string
	Dark bool // true = optimized for dark terminal background

	// Semantic color roles — 256-color ANSI palette strings for lipgloss.
	Accent  string // tools, badges, star, filter — primary accent
	Brand   string // Claude icon, system msgs, selection highlight
	Success string // pass/success states
	Error   string // error states

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
		Name:            "historic",
		Dark:            true,
		Accent:          "214",
		Brand:           "69",
		Success:         "82",
		Error:           "196",
		TextBright:      "255",
		TextNormal:      "252",
		TextDim:         "242",
		TextSubtle:      "240",
		SurfaceDark:     "236",
		SurfaceSelect:   "237",
		SurfaceBorder:   "238",
		SurfaceSep:      "237",
		IconCodex:       "107",
		IconAPI:         "242",
		MenuItem:        "75",
		ANSIPromptName:  ColorCyan,
		ANSIPromptArrow: ColorMagenta,
		ANSIBanner:      ColorCyan,
		ANSICatArt:      ColorYellow,
		ANSIStatus:      ColorGreen,
	},
	"ocean": {
		Name:            "ocean",
		Dark:            true,
		Accent:          "51",
		Brand:           "33",
		Success:         "49",
		Error:           "203",
		TextBright:      "255",
		TextNormal:      "252",
		TextDim:         "242",
		TextSubtle:      "240",
		SurfaceDark:     "236",
		SurfaceSelect:   "237",
		SurfaceBorder:   "238",
		SurfaceSep:      "237",
		IconCodex:       "120",
		IconAPI:         "242",
		MenuItem:        "87",
		ANSIPromptName:  ColorCyan,
		ANSIPromptArrow: ColorBlue,
		ANSIBanner:      ColorCyan,
		ANSICatArt:      ColorCyan,
		ANSIStatus:      ColorCyan,
	},
	"neon": {
		Name:            "neon",
		Dark:            true,
		Accent:          "201",
		Brand:           "51",
		Success:         "46",
		Error:           "197",
		TextBright:      "255",
		TextNormal:      "252",
		TextDim:         "242",
		TextSubtle:      "240",
		SurfaceDark:     "235",
		SurfaceSelect:   "236",
		SurfaceBorder:   "237",
		SurfaceSep:      "235",
		IconCodex:       "135",
		IconAPI:         "242",
		MenuItem:        "159",
		ANSIPromptName:  ColorMagenta,
		ANSIPromptArrow: ColorCyan,
		ANSIBanner:      ColorMagenta,
		ANSICatArt:      ColorCyan,
		ANSIStatus:      ColorMagenta,
	},
	"ember": {
		Name:            "ember",
		Dark:            true,
		Accent:          "208",
		Brand:           "202",
		Success:         "220",
		Error:           "160",
		TextBright:      "255",
		TextNormal:      "252",
		TextDim:         "242",
		TextSubtle:      "240",
		SurfaceDark:     "236",
		SurfaceSelect:   "237",
		SurfaceBorder:   "238",
		SurfaceSep:      "237",
		IconCodex:       "214",
		IconAPI:         "242",
		MenuItem:        "215",
		ANSIPromptName:  ColorRed,
		ANSIPromptArrow: ColorYellow,
		ANSIBanner:      ColorRed,
		ANSICatArt:      ColorYellow,
		ANSIStatus:      ColorYellow,
	},
	"aurora": {
		Name:            "aurora",
		Dark:            true,
		Accent:          "49",
		Brand:           "141",
		Success:         "120",
		Error:           "204",
		TextBright:      "255",
		TextNormal:      "252",
		TextDim:         "242",
		TextSubtle:      "240",
		SurfaceDark:     "236",
		SurfaceSelect:   "237",
		SurfaceBorder:   "238",
		SurfaceSep:      "237",
		IconCodex:       "71",
		IconAPI:         "242",
		MenuItem:        "123",
		ANSIPromptName:  ColorMagenta,
		ANSIPromptArrow: ColorGreen,
		ANSIBanner:      ColorGreen,
		ANSICatArt:      ColorMagenta,
		ANSIStatus:      ColorGreen,
	},
	// Light-terminal themes — dark text on light surfaces.
	"paper": {
		Name:            "paper",
		Dark:            false,
		Accent:          "26",  // medium blue — badges, stars
		Brand:           "27",  // royal blue — icons, selection bg
		Success:         "28",  // forest green
		Error:           "160", // red
		TextBright:      "232", // near-black — selected label
		TextNormal:      "236", // dark gray — normal label
		TextDim:         "243", // medium gray — section headers
		TextSubtle:      "247", // lighter gray — hints/footer
		SurfaceDark:     "250", // light gray — status bar bg
		SurfaceSelect:   "229", // pale yellow — selected row bg
		SurfaceBorder:   "248", // medium-light border
		SurfaceSep:      "252", // light gray divider
		IconCodex:       "22",  // dark green
		IconAPI:         "244", // gray
		MenuItem:        "26",  // blue
		ANSIPromptName:  ColorBlue,
		ANSIPromptArrow: ColorRed,
		ANSIBanner:      ColorBlue,
		ANSICatArt:      ColorRed,
		ANSIStatus:      ColorGreen,
	},
	"sky": {
		Name:            "sky",
		Dark:            false,
		Accent:          "33",  // sky blue — badges, stars
		Brand:           "25",  // medium blue — icons, selection bg
		Success:         "34",  // green
		Error:           "160", // red
		TextBright:      "232", // near-black — selected label
		TextNormal:      "236", // dark gray — normal label
		TextDim:         "243", // medium gray — section headers
		TextSubtle:      "247", // lighter gray — hints/footer
		SurfaceDark:     "153", // light blue — status bar bg
		SurfaceSelect:   "195", // pale cyan — selected row bg
		SurfaceBorder:   "153", // light blue border
		SurfaceSep:      "254", // near-white divider
		IconCodex:       "30",  // teal
		IconAPI:         "244", // gray
		MenuItem:        "27",  // blue
		ANSIPromptName:  ColorBlue,
		ANSIPromptArrow: ColorCyan,
		ANSIBanner:      ColorCyan,
		ANSICatArt:      ColorBlue,
		ANSIStatus:      ColorGreen,
	},
	"sparkling": {
		Name:            "sparkling",
		Dark:            false,
		Accent:          "57",  // vivid purple — amethyst sparkle
		Brand:           "93",  // medium purple — icons, selection bg
		Success:         "34",  // green
		Error:           "197", // hot pink-red
		TextBright:      "232", // near-black — selected label
		TextNormal:      "236", // dark gray — normal label
		TextDim:         "243", // medium gray — section headers
		TextSubtle:      "247", // lighter gray — hints/footer
		SurfaceDark:     "253", // near-white — status bar bg
		SurfaceSelect:   "189", // pale lavender — selected row bg
		SurfaceBorder:   "189", // soft lavender border
		SurfaceSep:      "254", // near-white divider
		IconCodex:       "99",  // medium purple
		IconAPI:         "244", // gray
		MenuItem:        "57",  // vivid purple
		ANSIPromptName:  ColorMagenta,
		ANSIPromptArrow: ColorBlue,
		ANSIBanner:      ColorMagenta,
		ANSICatArt:      ColorBlue,
		ANSIStatus:      ColorGreen,
	},
	"radiance": {
		Name:            "radiance",
		Dark:            false,
		Accent:          "167", // salmon-rose — warm glow
		Brand:           "161", // deep rose — icons, selection bg
		Success:         "34",  // green
		Error:           "160", // red
		TextBright:      "232", // near-black — selected label
		TextNormal:      "236", // dark gray — normal label
		TextDim:         "243", // medium gray — section headers
		TextSubtle:      "247", // lighter gray — hints/footer
		SurfaceDark:     "224", // blush pink — status bar bg
		SurfaceSelect:   "225", // pale rose — selected row bg
		SurfaceBorder:   "218", // soft pink border
		SurfaceSep:      "254", // near-white divider
		IconCodex:       "125", // deep magenta
		IconAPI:         "244", // gray
		MenuItem:        "167", // salmon
		ANSIPromptName:  ColorMagenta,
		ANSIPromptArrow: ColorRed,
		ANSIBanner:      ColorRed,
		ANSICatArt:      ColorMagenta,
		ANSIStatus:      ColorGreen,
	},
	"goldenhour": {
		Name:            "goldenhour",
		Dark:            false,
		Accent:          "214", // amber-orange — golden light
		Brand:           "130", // warm amber — icons, selection bg
		Success:         "22",  // forest green
		Error:           "160", // red
		TextBright:      "232", // near-black — selected label
		TextNormal:      "236", // dark gray — normal label
		TextDim:         "243", // medium gray — section headers
		TextSubtle:      "247", // lighter gray — hints/footer
		SurfaceDark:     "229", // pale gold — status bar bg
		SurfaceSelect:   "223", // honey-gold — selected row bg
		SurfaceBorder:   "222", // warm gold border
		SurfaceSep:      "230", // very pale yellow divider
		IconCodex:       "136", // warm gold
		IconAPI:         "244", // gray
		MenuItem:        "130", // amber
		ANSIPromptName:  ColorYellow,
		ANSIPromptArrow: ColorRed,
		ANSIBanner:      ColorYellow,
		ANSICatArt:      ColorRed,
		ANSIStatus:      ColorGreen,
	},
}

// ThemeNames returns available theme names in display order.
func ThemeNames() []string {
	return []string{"historic", "ocean", "neon", "ember", "aurora", "paper", "sky", "sparkling", "radiance", "goldenhour"}
}

// SaveTheme validates and persists the selected theme name on the config.
// Empty value clears the preference (restoring the default). Lives here, not
// on (*api.Config), so config.go has no dep on theme.go's name list.
func SaveTheme(c *api.Config, theme string) error {
	if c == nil {
		return fmt.Errorf("config not loaded")
	}
	if theme != "" {
		valid := false
		for _, name := range ThemeNames() {
			if name == theme {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("invalid theme %q; available: %s", theme, strings.Join(ThemeNames(), ", "))
		}
	}

	previous := c.Theme
	c.Theme = theme
	if err := c.Save(); err != nil {
		c.Theme = previous
		return err
	}
	return nil
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
	themePromptName  = ColorCyan
	themePromptArrow = ColorMagenta
	themeBannerColor = ColorCyan
	themeCatColor    = ColorYellow
	themeStatusColor = ColorGreen
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
