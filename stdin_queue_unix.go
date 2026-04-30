//go:build darwin || linux

package main

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// startQueueReader starts a background goroutine that reads lines from stdin
// (using OS canonical mode — full-line buffering with echo) while the agent
// is processing.  Each non-empty line is pushed onto pq.
//
// Returns a stop function that must be called before the next ReadInput so
// that no stdin data is consumed by two readers simultaneously.
func startQueueReader(pq *promptQueue, term *Terminal) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()

		fd := int(os.Stdin.Fd())
		var pending []byte // bytes read before the closing newline

		for {
			// Check whether we've been asked to stop.
			select {
			case <-done:
				return
			default:
			}

			// Poll stdin with a 50 ms timeout so we wake up frequently
			// enough to notice the done signal.
			var rfds unix.FdSet
			rfds.Set(fd)
			tv := unix.Timeval{Sec: 0, Usec: 50_000}
			n, err := unix.Select(fd+1, &rfds, nil, nil, &tv)
			if err != nil || n == 0 {
				continue
			}

			// In canonical mode the OS delivers a complete line at once.
			buf := make([]byte, 4096)
			nr, err := unix.Read(fd, buf)
			if err != nil || nr == 0 {
				continue
			}

			pending = append(pending, buf[:nr]...)

			// Extract complete lines.
			for {
				idx := strings.IndexByte(string(pending), '\n')
				if idx < 0 {
					break
				}
				line := strings.TrimSpace(string(pending[:idx]))
				pending = pending[idx+1:]

				if line == "" {
					continue
				}
				pq.push(line)
				pos := pq.len()
				// Print confirmation on its own line so it doesn't corrupt
				// mid-stream token output.
				fmt.Printf("\n  %s✓ queued [%d]:%s %s\n",
					colorDim, pos, colorReset, line)
			}
		}
	}()

	return func() {
		close(done)
		wg.Wait() // guaranteed no stdin reads after this returns
	}
}
