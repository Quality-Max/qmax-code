//go:build windows

package session

import "github.com/qualitymax/qmax-code/internal/tui"

// StartQueueReader is a no-op on Windows; the prompt queue still works via
// /queue but concurrent stdin reading while streaming is not supported.
func StartQueueReader(pq *PromptQueue, term *tui.Terminal) func() {
	return func() {}
}
