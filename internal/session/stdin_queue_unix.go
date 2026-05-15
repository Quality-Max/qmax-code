//go:build darwin || linux

package session

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/qualitymax/qmax-code/internal/tui"
)

// StartQueueReader starts a background goroutine that reads chars from stdin
// one at a time while the agent is processing. Each non-empty line (Enter-terminated)
// is pushed onto pq. If cancelFn is non-nil it is called when the user presses Enter,
// allowing the running agent to be interrupted so the queued prompt is processed next.
//
// The goroutine switches stdin to "half-raw" mode: ICANON and ECHO are disabled so
// we can manage echo ourselves (preventing the spinner from overwriting typed text),
// while OPOST is left enabled so the agent's concurrent stdout output still gets
// proper newline translation. Returns a stop function that must be called before the
// next tui.ReadInput to ensure stdin is never shared between two readers.
//
// The stop function returns any partial line the user had typed but not yet
// Enter-terminated when the agent finished. If the agent's response races the
// user's typing (turn ends before Enter is pressed), the caller can recover the
// partial text rather than silently dropping it (QUA-577).
func StartQueueReader(pq *PromptQueue, term *tui.Terminal, cancelFn func()) func() string {
	done := make(chan struct{})
	partialCh := make(chan string, 1)
	var wg sync.WaitGroup
	wg.Add(1)

	// lineRunes is only mutated inside the goroutine. The stop function
	// reads its final value via partialCh after wg.Wait, so there is no
	// concurrent access window.
	var lineRunes []rune

	go func() {
		defer wg.Done()
		defer term.SetUserTyping(false)
		// Send whatever partial line was buffered when the goroutine exits.
		// Runs before wg.Done (LIFO), so wg.Wait observes the send. Channel
		// is buffered (cap 1), so this never blocks even if no one reads.
		defer func() { partialCh <- string(lineRunes) }()

		fd := int(os.Stdin.Fd())

		// Switch to half-raw mode: disable canonical mode and echo only.
		// Keeping OPOST ensures the agent's fmt.Print output (running concurrently)
		// still gets \n → \r\n translation and doesn't look broken.
		oldState, stateErr := unix.IoctlGetTermios(fd, ioctlGetTermios)
		if stateErr == nil {
			newState := *oldState
			newState.Lflag &^= unix.ICANON | unix.ECHO
			newState.Cc[unix.VMIN] = 1
			newState.Cc[unix.VTIME] = 0
			if setErr := unix.IoctlSetTermios(fd, ioctlSetTermios, &newState); setErr == nil {
				defer func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, oldState) }()
			}
		}

		var rawBuf []byte
		// Pre-allocated read buffer — reused every iteration.
		readBuf := make([]byte, 32)
		typing := false

		// Escape-sequence state machine: absorbs ESC sequences so that
		// Option+Arrow (which sends ESC+b/f or ESC+[+...+D/C) doesn't
		// insert stray characters into the typed line.
		escSeq := false    // true after ESC, consuming the sequence
		escBracket := false // true after ESC+[ (CSI), consuming until final byte

		for {
			select {
			case <-done:
				if typing {
					fmt.Print("\r\033[K") // erase the typing line before exiting
				}
				return
			default:
			}

			var rfds unix.FdSet
			rfds.Set(fd)
			tv := unix.Timeval{Sec: 0, Usec: 50_000}
			n, err := unix.Select(fd+1, &rfds, nil, nil, &tv)
			if err != nil || n == 0 {
				continue
			}

			nr, err := unix.Read(fd, readBuf)
			if err != nil || nr == 0 {
				continue
			}
			rawBuf = append(rawBuf, readBuf[:nr]...)

			for len(rawBuf) > 0 {
				r, size := utf8.DecodeRune(rawBuf)
				if size == 0 {
					break // incomplete sequence — wait for more bytes
				}
				rawBuf = rawBuf[size:]
				if r == utf8.RuneError && size == 1 {
					continue // bad byte; discard
				}

				// On the first keystroke: tell the spinner to pause (it checks this
				// flag before each frame write), then open a dedicated typing line.
				if !typing && !escSeq && r != 0x1b {
					typing = true
					term.SetUserTyping(true)
					// \r moves to column 0; \033[K clears to EOL (erases any spinner
					// text); \n moves to a fresh line where we show the input prompt.
					fmt.Print("\r\033[K\n  ⌨  ")
				}

				switch r {
				case '\r', '\n': // Enter
					text := strings.TrimSpace(string(lineRunes))
					lineRunes = lineRunes[:0]
					fmt.Println()
					typing = false
					term.SetUserTyping(false)
					if text == "" {
						break
					}
					// Cancel the running agent so this prompt is processed immediately.
					if cancelFn != nil {
						cancelFn()
					}
					pq.Push(text)
					pos := pq.Len()
					fmt.Printf("  %s✓ queued [%d]:%s %s\n",
						tui.ColorDim, pos, tui.ColorReset, text)

				case 127, 8: // Backspace / Delete
					if len(lineRunes) > 0 {
						lineRunes = lineRunes[:len(lineRunes)-1]
						fmt.Print("\b \b")
					}

				case 3: // Ctrl+C — owned by the signal handler in repl.go; don't eat it
					typing = false
					term.SetUserTyping(false)
					lineRunes = lineRunes[:0]
					fmt.Println()

				default:
					if r == 0x1b { // ESC — start of an escape sequence
						escSeq = true
						escBracket = false
					} else if escSeq {
						if escBracket {
							// Inside CSI: consume until final byte (0x40–0x7E)
							if r >= 0x40 && r <= 0x7E {
								escSeq = false
								escBracket = false
							}
						} else if r == '[' {
							escBracket = true // ESC+[ introduces a CSI sequence
						} else {
							// Simple ESC+char (e.g., Option+b/f on macOS Terminal.app)
							escSeq = false
						}
						// In all escape-sequence branches: do not append to lineRunes
					} else if r >= 32 { // printable character
						lineRunes = append(lineRunes, r)
						fmt.Printf("%c", r)
					}
				}
			}
		}
	}()

	return func() string {
		close(done)
		wg.Wait()
		// Channel is buffered and always written in a defer before wg.Done,
		// so by the time we read, the value is there. Use a non-blocking
		// receive with a default as belt-and-suspenders.
		select {
		case s := <-partialCh:
			return s
		default:
			return ""
		}
	}
}
