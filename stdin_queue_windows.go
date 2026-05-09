//go:build windows

package main

import "github.com/qualitymax/qmax-code/internal/tui"

// startQueueReader is a no-op on Windows; the prompt queue still works via
// /queue but concurrent stdin reading while streaming is not supported.
func startQueueReader(pq *promptQueue, term *tui.Terminal) func() {
	return func() {}
}
