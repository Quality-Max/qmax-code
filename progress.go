package main

import (
	"fmt"
	"strings"
	"time"
)

// ProgressBar renders an animated progress bar in the terminal.
type ProgressBar struct {
	label    string
	total    int
	current  int
	width    int
	start    time.Time
	done     bool
	frames   []string
	frameIdx int
}

var browserFrames = []string{
	"  🌐 ┌──────────────────────────────────┐\n" +
		"     │  ◉ ◉ ◉  ░░░░░░░░░░░░░░░░░░░░  │\n" +
		"     │  ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓  │\n" +
		"     │  ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓  │\n" +
		"     └──────────────────────────────────┘",

	"  🌐 ┌──────────────────────────────────┐\n" +
		"     │  ◉ ◉ ◉  ▓░░░░░░░░░░░░░░░░░░░  │\n" +
		"     │  ▓▓▓▓▓▓▓▓░░░░░░▓▓▓▓▓▓▓▓▓▓▓▓▓▓  │\n" +
		"     │  ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓░░░░░░▓▓▓▓  │\n" +
		"     └──────────────────────────────────┘",

	"  🌐 ┌──────────────────────────────────┐\n" +
		"     │  ◉ ◉ ◉  ▓▓▓░░░░░░░░░░░░░░░░░  │\n" +
		"     │  ▓▓▓▓▓▓▓▓▓▓░░░░▓▓▓▓▓▓▓▓▓▓▓▓▓▓  │\n" +
		"     │  ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓░░░░▓▓▓▓  │\n" +
		"     └──────────────────────────────────┘",

	"  🌐 ┌──────────────────────────────────┐\n" +
		"     │  ◉ ◉ ◉  ▓▓▓▓▓░░░░░░░░░░░░░░░  │\n" +
		"     │  ▓▓▓▓▓▓▓▓▓▓▓▓░░▓▓▓▓▓▓▓▓▓▓▓▓▓▓  │\n" +
		"     │  ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓░░▓▓▓▓  │\n" +
		"     └──────────────────────────────────┘",
}

var spinnerChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NewProgressBar creates a new progress bar.
func NewProgressBar(label string, width int) *ProgressBar {
	if width <= 0 {
		width = 30
	}
	return &ProgressBar{
		label: label,
		width: width,
		start: time.Now(),
	}
}

// Update redraws the progress bar with new progress (0-100).
func (p *ProgressBar) Update(percent int, message string) {
	if p.done {
		return
	}
	if percent > 100 {
		percent = 100
	}
	p.current = percent

	filled := p.width * percent / 100
	empty := p.width - filled
	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)
	elapsed := time.Since(p.start).Round(time.Second)

	spinner := spinnerChars[p.frameIdx%len(spinnerChars)]
	p.frameIdx++

	status := message
	if status == "" {
		status = p.label
	}

	// Overwrite current line
	fmt.Printf("\r  %s %s %s %3d%% %s", spinner, bar, styleDim.Render(fmt.Sprintf("[%s]", elapsed)), percent, truncateStr(status, 30))
}

// Finish marks the progress bar as complete.
func (p *ProgressBar) Finish(success bool, message string) {
	p.done = true
	elapsed := time.Since(p.start).Round(time.Second)

	icon := styleSuccess.Render("✓")
	if !success {
		icon = styleError.Render("✗")
	}

	bar := strings.Repeat("█", p.width)
	fmt.Printf("\r  %s %s %s 100%% %s\n", icon, bar, styleDim.Render(fmt.Sprintf("[%s]", elapsed)), message)
}

// ShowBrowserAnimation displays an ASCII browser frame (for test execution).
func ShowBrowserAnimation(frame int) {
	idx := frame % len(browserFrames)
	// Clear previous frame (5 lines)
	if frame > 0 {
		for i := 0; i < 5; i++ {
			fmt.Print("\033[1A\033[2K")
		}
	}
	fmt.Println(browserFrames[idx])
}

// ClearBrowserAnimation clears the browser animation from terminal.
func ClearBrowserAnimation() {
	for i := 0; i < 5; i++ {
		fmt.Print("\033[1A\033[2K")
	}
}
