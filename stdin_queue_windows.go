//go:build windows

package main

// startQueueReader is a no-op on Windows; the prompt queue still works via
// /queue but concurrent stdin reading while streaming is not supported.
func startQueueReader(pq *promptQueue, term *Terminal) func() {
	return func() {}
}
