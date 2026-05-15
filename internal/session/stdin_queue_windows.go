//go:build windows

package session

import "github.com/qualitymax/qmax-code/internal/tui"

// StartQueueReader is a no-op on Windows; the prompt queue still works via
// /queue but concurrent stdin reading while streaming is not supported.
// The returned stop function always reports no partial input.
func StartQueueReader(pq *PromptQueue, term *tui.Terminal, cancelFn func()) func() string {
	return func() string { return "" }
}
