//go:build windows

package main

import "fmt"

type termState struct{}

func enableRawMode() (*termState, error) {
	return nil, fmt.Errorf("raw mode not supported on Windows")
}

func restoreTermMode(state *termState) {}
