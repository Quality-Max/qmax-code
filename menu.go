package main

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// SlashMenuItem represents a selectable command.
type SlashMenuItem struct {
	Cmd  string
	Desc string
}

var slashMenuItems = []SlashMenuItem{
	{"/help", "Show help"},
	{"/status", "Auth + session info"},
	{"/cost", "Token usage + cost"},
	{"/config", "Show config"},
	{"/sessions", "List saved sessions"},
	{"/resume", "Resume a session"},
	{"/save", "Save current session"},
	{"/project", "Set active project"},
	{"/set", "Update config"},
	{"/clear", "Clear history"},
	{"/quit", "Exit"},
}

// RunSlashMenu shows an interactive arrow-key menu and returns the selected command.
// Returns empty string if cancelled (Escape/Ctrl+C).
func RunSlashMenu() string {
	// Switch to raw mode to capture arrow keys
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		// Fallback: just return empty
		return ""
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	selected := 0
	drawMenu(selected)

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return ""
		}

		if n == 1 {
			switch buf[0] {
			case 13, 10: // Enter
				clearMenu()
				return slashMenuItems[selected].Cmd
			case 27: // Escape
				clearMenu()
				return ""
			case 3: // Ctrl+C
				clearMenu()
				return ""
			case 'j', 'J': // vim down
				selected = (selected + 1) % len(slashMenuItems)
				drawMenu(selected)
			case 'k', 'K': // vim up
				selected = (selected - 1 + len(slashMenuItems)) % len(slashMenuItems)
				drawMenu(selected)
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == 91 {
			switch buf[2] {
			case 65: // Up arrow
				selected = (selected - 1 + len(slashMenuItems)) % len(slashMenuItems)
				drawMenu(selected)
			case 66: // Down arrow
				selected = (selected + 1) % len(slashMenuItems)
				drawMenu(selected)
			}
		}
	}
}

func drawMenu(selected int) {
	// Move cursor up to redraw (clear previous menu)
	for i := 0; i < len(slashMenuItems); i++ {
		fmt.Print("\033[A\033[2K") // move up, clear line
	}

	for i, item := range slashMenuItems {
		if i == selected {
			// Highlighted: cyan background
			fmt.Printf("\r  \033[7m\033[36m %-12s %s \033[0m\n", item.Cmd, item.Desc)
		} else {
			fmt.Printf("\r  \033[36m %-12s\033[0m \033[2m%s\033[0m\n", item.Cmd, item.Desc)
		}
	}
}

func clearMenu() {
	// Clear the menu lines
	for i := 0; i < len(slashMenuItems); i++ {
		fmt.Print("\033[A\033[2K")
	}
	fmt.Print("\r")
}
