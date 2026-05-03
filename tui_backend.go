package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── Styles ───────────────────────────────────────────────────────────────────

var (
	pickerBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)

	pickerSectionHeader = lipgloss.NewStyle().
				Foreground(lipgloss.Color("242")).
				PaddingTop(1)

	pickerRowSelected = lipgloss.NewStyle().
				Background(lipgloss.Color("237")).
				Bold(true)

	pickerRowNormal = lipgloss.NewStyle()

	pickerIcon = lipgloss.NewStyle().
			Foreground(lipgloss.Color("69"))   // blue — Claude ✦

	pickerIconCodex = lipgloss.NewStyle().
			Foreground(lipgloss.Color("107"))  // green — Codex ⊗

	pickerIconAPI = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242"))  // grey — Direct ○

	pickerLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	pickerLabelSel = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Bold(true)

	pickerBadgeNew = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("214")).
			Bold(true).
			PaddingLeft(1).PaddingRight(1)

	pickerBadgeCurrent = lipgloss.NewStyle().
				Foreground(lipgloss.Color("82")).
				Bold(true)

	pickerBadgeStar = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214"))

	pickerBadgeExt = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242"))

	pickerShortcut = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	pickerDivider = lipgloss.NewStyle().
			Foreground(lipgloss.Color("237"))

	effortLabelActive = lipgloss.NewStyle().
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("69")).
				Bold(true).
				PaddingLeft(2).PaddingRight(2)

	effortLabelInactive = lipgloss.NewStyle().
				Foreground(lipgloss.Color("242")).
				PaddingLeft(2).PaddingRight(2)

	pickerFooter = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			PaddingTop(1)

	pickerStatusBar = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("236")).
			PaddingLeft(1).PaddingRight(1)

	pickerStatusIcon = lipgloss.NewStyle().
				Foreground(lipgloss.Color("69")).
				Background(lipgloss.Color("236")).
				Bold(true)

	pickerStatusEffort = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214")).
				Background(lipgloss.Color("236")).
				Bold(true)

	pickerStatusIconCodex = lipgloss.NewStyle().
				Foreground(lipgloss.Color("107")).
				Background(lipgloss.Color("236")).
				Bold(true)
)

// ─── Model catalogue ──────────────────────────────────────────────────────────

type pickerEntry struct {
	backend  string // "cc" | "codex" | ""
	modelID  string // passed via --model to the CLI
	label    string // display name
	subLabel string // e.g. "1M ctx"
	isNew    bool
	isFav    bool    // ⭐ default model for this backend
	external bool    // ↗ opens to external provider
	shortcut byte    // '1'..'9','0'  (0 = no shortcut)
}

var ccModels = []pickerEntry{
	{backend: "cc", modelID: "claude-opus-4-7",        label: "Opus 4.7",    subLabel: "1M ctx", isNew: true, shortcut: '1'},
	{backend: "cc", modelID: "claude-opus-4-7",        label: "Opus 4.7",                        isNew: true, shortcut: '2'},
	{backend: "cc", modelID: "claude-opus-4-6",        label: "Opus 4.6",    subLabel: "1M ctx",              shortcut: '3'},
	{backend: "cc", modelID: "claude-sonnet-4-6",      label: "Sonnet 4.6",                       isFav: true, shortcut: '4'},
	{backend: "cc", modelID: "claude-haiku-4-5-20251001", label: "Haiku 4.5",                     shortcut: '5'},
}

var codexModels = []pickerEntry{
	{backend: "codex", modelID: "gpt-5.5",             label: "GPT-5.5",     isNew: true,  external: true, shortcut: '6'},
	{backend: "codex", modelID: "o4-mini",             label: "o4-mini",                   external: true, isFav: true, shortcut: '7'},
	{backend: "codex", modelID: "o3",                  label: "o3",                        external: true, shortcut: '8'},
	{backend: "codex", modelID: "o3-mini",             label: "o3-mini",                   external: true, shortcut: '9'},
	{backend: "codex", modelID: "gpt-4o",              label: "GPT-4o",                    external: true, shortcut: '0'},
}

var apiModels = []pickerEntry{
	{backend: "", modelID: "auto",              label: "auto",     subLabel: "haiku→sonnet routing", isFav: true},
	{backend: "", modelID: ModelSonnet,         label: "Sonnet 4.6"},
	{backend: "", modelID: ModelOpus,           label: "Opus 4.6"},
	{backend: "", modelID: ModelHaiku,          label: "Haiku 4.5"},
}

var effortLevels = []string{"low", "medium", "high"}

// ─── Bubbletea model ──────────────────────────────────────────────────────────

// ModelPickerResult is returned after the TUI closes.
type ModelPickerResult struct {
	Backend   string // "cc" | "codex" | ""
	ModelID   string // specific model, or "" for default
	Effort    string // "low" | "medium" | "high"
	Confirmed bool
}

type modelPickerModel struct {
	// All rows in order: cc entries, codex entries, api entries.
	allEntries []pickerEntry
	cursor     int    // index into allEntries
	effort     string // "low" | "medium" | "high"
	effortFocus bool  // Tab switches focus between list and effort bar

	// Current selection (what was active when the picker opened)
	currentBackend string
	currentModelID string

	cancelled bool
	chosen    *pickerEntry
}

func newModelPickerModel(currentBackend, currentModelID, effort string) modelPickerModel {
	entries := make([]pickerEntry, 0, len(ccModels)+len(codexModels)+len(apiModels))
	entries = append(entries, ccModels...)
	entries = append(entries, codexModels...)
	entries = append(entries, apiModels...)

	// Start cursor on the active entry.
	cursor := 0
	for i, e := range entries {
		if e.backend == currentBackend && (e.modelID == currentModelID || currentModelID == "") && e.isFav {
			cursor = i
		}
		if e.backend == currentBackend && e.modelID == currentModelID {
			cursor = i
		}
	}

	if effort == "" {
		effort = "high"
	}

	return modelPickerModel{
		allEntries:     entries,
		cursor:         cursor,
		effort:         effort,
		currentBackend: currentBackend,
		currentModelID: currentModelID,
	}
}

func (m modelPickerModel) Init() tea.Cmd { return nil }

func (m modelPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c", msg.String() == "esc", msg.String() == "q":
			m.cancelled = true
			return m, tea.Quit

		case msg.String() == "tab":
			m.effortFocus = !m.effortFocus

		case !m.effortFocus && (msg.String() == "up" || msg.String() == "k"):
			if m.cursor > 0 {
				m.cursor--
			}

		case !m.effortFocus && (msg.String() == "down" || msg.String() == "j"):
			if m.cursor < len(m.allEntries)-1 {
				m.cursor++
			}

		case m.effortFocus && (msg.String() == "left" || msg.String() == "h"):
			for i, e := range effortLevels {
				if e == m.effort && i > 0 {
					m.effort = effortLevels[i-1]
					break
				}
			}

		case m.effortFocus && (msg.String() == "right" || msg.String() == "l"):
			for i, e := range effortLevels {
				if e == m.effort && i < len(effortLevels)-1 {
					m.effort = effortLevels[i+1]
					break
				}
			}

		case msg.String() == "enter", msg.String() == " ":
			if !m.effortFocus {
				e := m.allEntries[m.cursor]
				m.chosen = &e
			}
			return m, tea.Quit

		default:
			// Number shortcuts 1-9, 0
			k := msg.String()
			if len(k) == 1 {
				ch := k[0]
				for i, e := range m.allEntries {
					if e.shortcut == ch {
						m.cursor = i
						ee := m.allEntries[i]
						m.chosen = &ee
						return m, tea.Quit
					}
				}
			}
		}
	}
	return m, nil
}

func (m modelPickerModel) View() string {
	ccInstalled := FindClaudeCode() != ""
	codexInstalled := FindCodex() != ""

	var b strings.Builder

	// ── Claude Code section ──────────────────────────────────────────
	sectionIcon := pickerIcon.Render("✦")
	sectionLabel := "Claude Code"
	if !ccInstalled {
		sectionLabel += pickerBadgeExt.Render("  not installed")
	}
	b.WriteString(pickerSectionHeader.Render(fmt.Sprintf("%s  %s", sectionIcon, sectionLabel)))
	b.WriteByte('\n')

	for i, e := range m.allEntries {
		if e.backend != "cc" {
			continue
		}
		b.WriteString(m.renderRow(i, e, "cc"))
		b.WriteByte('\n')
	}

	// ── Divider ─────────────────────────────────────────────────────
	b.WriteString(pickerDivider.Render(strings.Repeat("─", 52)))
	b.WriteByte('\n')

	// ── Codex section ────────────────────────────────────────────────
	sectionIcon2 := pickerIconCodex.Render("⊗")
	sectionLabel2 := "Codex"
	if !codexInstalled {
		sectionLabel2 += pickerBadgeExt.Render("  not installed")
	}
	b.WriteString(pickerSectionHeader.Render(fmt.Sprintf("%s  %s", sectionIcon2, sectionLabel2)))
	b.WriteByte('\n')

	for i, e := range m.allEntries {
		if e.backend != "codex" {
			continue
		}
		b.WriteString(m.renderRow(i, e, "codex"))
		b.WriteByte('\n')
	}

	// ── Divider ─────────────────────────────────────────────────────
	b.WriteString(pickerDivider.Render(strings.Repeat("─", 52)))
	b.WriteByte('\n')

	// ── Direct API section ───────────────────────────────────────────
	sectionIcon3 := pickerIconAPI.Render("○")
	b.WriteString(pickerSectionHeader.Render(fmt.Sprintf("%s  Direct API", sectionIcon3)))
	b.WriteByte('\n')

	for i, e := range m.allEntries {
		if e.backend != "" {
			continue
		}
		b.WriteString(m.renderRow(i, e, ""))
		b.WriteByte('\n')
	}

	// ── Effort bar ───────────────────────────────────────────────────
	b.WriteString(pickerDivider.Render(strings.Repeat("─", 52)))
	b.WriteByte('\n')
	b.WriteString(m.renderEffortBar())
	b.WriteByte('\n')

	// ── Footer ───────────────────────────────────────────────────────
	hint := "↑↓ navigate  ·  1-9 jump  ·  Tab effort  ·  Enter confirm  ·  Esc"
	b.WriteString(pickerFooter.Render(hint))
	b.WriteByte('\n')

	// ── Status bar ───────────────────────────────────────────────────
	b.WriteString(m.renderStatusBar())

	return pickerBox.Render(b.String())
}

func (m modelPickerModel) renderRow(idx int, e pickerEntry, backend string) string {
	isCursor := idx == m.cursor
	isCurrent := e.backend == m.currentBackend && (e.modelID == m.currentModelID || (m.currentModelID == "" && e.isFav))

	// Cursor indicator
	cursor := "  "
	if isCursor {
		cursor = pickerBadgeStar.Render("▶ ")
	}

	// Provider icon
	var icon string
	switch backend {
	case "cc":
		icon = pickerIcon.Render("✦")
	case "codex":
		icon = pickerIconCodex.Render("⊗")
	default:
		icon = pickerIconAPI.Render("○")
	}

	// Label
	label := e.label
	if e.subLabel != "" {
		label += "  " + pickerBadgeExt.Render(e.subLabel)
	}
	if isCursor {
		label = pickerLabelSel.Render(e.label)
		if e.subLabel != "" {
			label += "  " + pickerBadgeExt.Render(e.subLabel)
		}
	} else {
		label = pickerLabel.Render(label)
	}

	// Right-side badges
	var badges []string
	if e.isNew {
		badges = append(badges, pickerBadgeNew.Render("NEW"))
	}
	if isCurrent {
		badges = append(badges, pickerBadgeCurrent.Render("✓"))
	}
	if e.isFav && !isCurrent {
		badges = append(badges, pickerBadgeStar.Render("⭐"))
	}
	if e.external {
		badges = append(badges, pickerBadgeExt.Render("↗"))
	}

	// Shortcut
	sc := "  "
	if e.shortcut != 0 {
		sc = pickerShortcut.Render(fmt.Sprintf("%c ", e.shortcut))
	}

	right := strings.Join(badges, " ")
	row := fmt.Sprintf("%s%s  %-20s  %-16s %s", cursor, icon, label, right, sc)

	if isCursor {
		return pickerRowSelected.Render(row)
	}
	return pickerRowNormal.Render(row)
}

func (m modelPickerModel) renderEffortBar() string {
	focusMark := ""
	if m.effortFocus {
		focusMark = pickerBadgeStar.Render("▶ ")
	} else {
		focusMark = "  "
	}

	var parts []string
	for _, lvl := range effortLevels {
		label := strings.Title(lvl) //nolint:staticcheck
		if lvl == m.effort {
			parts = append(parts, effortLabelActive.Render(label))
		} else {
			parts = append(parts, effortLabelInactive.Render(label))
		}
	}

	hint := ""
	if m.effortFocus {
		hint = pickerFooter.Render("  ←→ adjust")
	}
	return fmt.Sprintf("%sEffort  %s%s", focusMark, strings.Join(parts, " "), hint)
}

func (m modelPickerModel) renderStatusBar() string {
	cur := m.allEntries[m.cursor]
	var icon string
	switch cur.backend {
	case "cc":
		icon = pickerStatusIcon.Render("✦")
	case "codex":
		icon = pickerStatusIconCodex.Render("⊗")
	default:
		icon = pickerStatusIcon.Render("○")
	}
	effort := pickerStatusEffort.Render(strings.Title(m.effort)) //nolint:staticcheck
	label := pickerStatusBar.Render(fmt.Sprintf("  %s  %s  ▌ %s effort  ", icon, cur.label, effort))
	return label
}

// ─── Theme Picker ─────────────────────────────────────────────────────────────

type themePickerModel struct {
	themes        []string
	cursor        int
	originalTheme string
	confirmed     bool
	cancelled     bool
}

func newThemePickerModel(currentTheme string) themePickerModel {
	names := ThemeNames()
	cursor := 0
	for i, n := range names {
		if n == currentTheme {
			cursor = i
			break
		}
	}
	return themePickerModel{
		themes:        names,
		cursor:        cursor,
		originalTheme: currentTheme,
	}
}

func (m themePickerModel) Init() tea.Cmd { return nil }

func (m themePickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.cancelled = true
			ApplyTheme(ThemeByName(m.originalTheme))
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				ApplyTheme(ThemeByName(m.themes[m.cursor]))
			}

		case "down", "j":
			if m.cursor < len(m.themes)-1 {
				m.cursor++
				ApplyTheme(ThemeByName(m.themes[m.cursor]))
			}

		case "enter", " ":
			m.confirmed = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m themePickerModel) View() string {
	var b strings.Builder

	b.WriteString(pickerSectionHeader.Render("Color Themes"))
	b.WriteByte('\n')

	for i, name := range m.themes {
		t := allThemes[name]
		isCursor := i == m.cursor
		isOriginal := name == m.originalTheme

		arrow := "  "
		if isCursor {
			arrow = pickerBadgeStar.Render("▶ ")
		}

		var label string
		if isCursor {
			label = pickerLabelSel.Render(fmt.Sprintf("%-10s", name))
		} else {
			label = pickerLabel.Render(fmt.Sprintf("%-10s", name))
		}

		swatches := fmt.Sprintf("%s %s %s %s",
			lipgloss.NewStyle().Background(lipgloss.Color(t.Accent)).Render("  "),
			lipgloss.NewStyle().Background(lipgloss.Color(t.Brand)).Render("  "),
			lipgloss.NewStyle().Background(lipgloss.Color(t.Success)).Render("  "),
			lipgloss.NewStyle().Background(lipgloss.Color(t.Error)).Render("  "),
		)

		check := ""
		if isOriginal {
			check = "  " + pickerBadgeCurrent.Render("✓")
		}

		row := fmt.Sprintf("%s%s  %s%s", arrow, label, swatches, check)
		if isCursor {
			b.WriteString(pickerRowSelected.Render(row))
		} else {
			b.WriteString(pickerRowNormal.Render(row))
		}
		b.WriteByte('\n')
	}

	b.WriteString(pickerDivider.Render(strings.Repeat("─", 38)))
	b.WriteByte('\n')
	b.WriteString(pickerFooter.Render("↑↓ preview  ·  Enter confirm  ·  Esc cancel"))
	b.WriteByte('\n')

	return pickerBox.Render(b.String())
}

// ShowThemePicker opens the live-preview theme picker TUI.
// Returns the chosen theme name and whether it was confirmed.
// On cancel the original theme is automatically restored by the picker.
func ShowThemePicker(currentTheme string) (string, bool) {
	if currentTheme == "" {
		currentTheme = "historic"
	}
	m := newThemePickerModel(currentTheme)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return currentTheme, false
	}
	final := result.(themePickerModel)
	if final.cancelled || !final.confirmed {
		return currentTheme, false
	}
	return final.themes[final.cursor], true
}

// ─── Public entry point ───────────────────────────────────────────────────────

// ShowModelPicker opens the unified model + effort TUI.
// Returns the result; Confirmed=false means the user cancelled.
func ShowModelPicker(currentBackend, currentModelID, effort string) ModelPickerResult {
	m := newModelPickerModel(currentBackend, currentModelID, effort)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return ModelPickerResult{Confirmed: false}
	}
	final := result.(modelPickerModel)
	if final.cancelled || final.chosen == nil {
		return ModelPickerResult{Confirmed: false}
	}
	return ModelPickerResult{
		Backend:   final.chosen.backend,
		ModelID:   final.chosen.modelID,
		Effort:    final.effort,
		Confirmed: true,
	}
}

// ─── Session picker ───────────────────────────────────────────────────────────

type sessionPickerModel struct {
	sessions    []SessionSummary
	cursor      int
	activeID    string
	confirmed   bool
	cancelled   bool
}

func newSessionPickerModel(sessions []SessionSummary, activeID string) sessionPickerModel {
	cursor := 0
	for i, s := range sessions {
		if s.ID == activeID {
			cursor = i
			break
		}
	}
	return sessionPickerModel{sessions: sessions, cursor: cursor, activeID: activeID}
}

func (m sessionPickerModel) Init() tea.Cmd { return nil }

func (m sessionPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
			}
		case "enter", " ":
			m.confirmed = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m sessionPickerModel) View() string {
	var b strings.Builder

	b.WriteString(pickerSectionHeader.Render("Saved Sessions"))
	b.WriteByte('\n')

	now := time.Now()
	for i, s := range m.sessions {
		isCursor := i == m.cursor
		isActive := s.ID == m.activeID

		arrow := "  "
		if isCursor {
			arrow = pickerBadgeStar.Render("▶ ")
		}

		ago := formatAgo(now.Sub(s.UpdatedAt))
		meta := fmt.Sprintf("%s  %2d turns  %s  %s", ago, s.Turns, formatTokens(s.Tokens), formatModelShort(s.Model))
		if s.ProjectID > 0 {
			meta += fmt.Sprintf("  #%d", s.ProjectID)
		}

		var idLabel, metaLabel string
		if isCursor {
			idLabel = pickerLabelSel.Render(fmt.Sprintf("%-10s", s.ID))
			metaLabel = menuDescSelSty.Render(meta)
		} else {
			idLabel = pickerLabel.Render(fmt.Sprintf("%-10s", s.ID))
			metaLabel = pickerFooter.Render(meta)
		}

		badge := ""
		if isActive {
			badge = "  " + pickerBadgeCurrent.Render("active")
		}

		row := fmt.Sprintf("%s%s  %s%s", arrow, idLabel, metaLabel, badge)
		if isCursor {
			b.WriteString(pickerRowSelected.Render(row))
		} else {
			b.WriteString(pickerRowNormal.Render(row))
		}
		b.WriteByte('\n')
	}

	b.WriteString(pickerDivider.Render(strings.Repeat("─", 52)))
	b.WriteByte('\n')
	b.WriteString(pickerFooter.Render("↑↓ navigate  ·  Enter resume  ·  Esc cancel"))
	b.WriteByte('\n')

	return pickerBox.Render(b.String())
}

func formatAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now   "
	case d < time.Hour:
		return fmt.Sprintf("%2dm ago    ", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%2dh ago    ", int(d.Hours()))
	default:
		return fmt.Sprintf("%2dd ago    ", int(d.Hours()/24))
	}
}

// formatModelShort condenses a model ID like "claude-sonnet-4-6" → "sonnet-4.6".
// Returns "cc" when empty (CC chose the model automatically).
func formatModelShort(model string) string {
	if model == "" {
		return "cc"
	}
	s := strings.TrimPrefix(model, "claude-")
	// Strip trailing 8-digit date suffix (e.g. "-20251022")
	if idx := strings.LastIndex(s, "-"); idx > 0 {
		if suffix := s[idx+1:]; len(suffix) == 8 {
			allDigits := true
			for _, c := range suffix {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				s = s[:idx]
			}
		}
	}
	// Replace remaining dashes in version segment with dots: sonnet-4-6 → sonnet-4.6
	// Only the last two segments (major.minor) get dotted.
	parts := strings.Split(s, "-")
	if len(parts) >= 3 {
		s = strings.Join(parts[:len(parts)-2], "-") + "-" + parts[len(parts)-2] + "." + parts[len(parts)-1]
	}
	return s
}

func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM tok", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk tok", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d tok", n)
	}
}

// ShowSessionPicker opens the interactive session picker TUI.
// Returns the selected session ID and whether the user confirmed.
func ShowSessionPicker(sessions []SessionSummary, activeID string) (string, bool) {
	if len(sessions) == 0 {
		return "", false
	}
	m := newSessionPickerModel(sessions, activeID)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return "", false
	}
	final := result.(sessionPickerModel)
	if final.cancelled || !final.confirmed {
		return "", false
	}
	return final.sessions[final.cursor].ID, true
}
