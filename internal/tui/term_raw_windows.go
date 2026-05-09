//go:build windows

package tui

import "fmt"

type TermState struct{}

func EnableRawMode() (*TermState, error) {
	return nil, fmt.Errorf("raw mode not supported on Windows")
}

func RestoreTermMode(state *TermState) {}
