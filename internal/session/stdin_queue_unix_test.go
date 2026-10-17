//go:build darwin || linux

package session

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/tui"
)

// withStdinPipeWriter swaps os.Stdin for a fresh pipe and returns the write
// end so tests can feed the queue reader incrementally. Cleanup restores the
// original stdin and closes both ends. Tests using it must not run parallel.
func withStdinPipeWriter(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		r.Close()
		w.Close()
	})
	return w
}

// queuedWithin polls the queue until an item arrives, failing the test after
// the deadline instead of hanging.
func queuedWithin(t *testing.T, q *PromptQueue) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if s, ok := q.Pop(); ok {
			return s
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for a queued prompt")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// stopQueueWithTimeout invokes the stop function under a deadline so a wedged
// reader goroutine fails the test instead of hanging it.
func stopQueueWithTimeout(t *testing.T, stop func() string) string {
	t.Helper()
	done := make(chan string, 1)
	go func() { done <- stop() }()
	select {
	case s := <-done:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("StartQueueReader stop function did not return within 2s")
		return ""
	}
}

func startTestQueueReader(t *testing.T, q *PromptQueue, cancel func()) func() string {
	t.Helper()
	stop := sync.OnceValue(StartQueueReader(q, &tui.Terminal{}, cancel))
	// Stop before the stdin fixture is restored, including on assertion failure.
	t.Cleanup(func() { stopQueueWithTimeout(t, stop) })
	return stop
}

func TestStartQueueReaderQueuesLineAndCancelsAgent(t *testing.T) {
	w := withStdinPipeWriter(t)

	q := &PromptQueue{}
	cancelled := make(chan struct{}, 1)
	stop := startTestQueueReader(t, q, func() { cancelled <- struct{}{} })

	if _, err := w.WriteString("interrupt the agent\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}

	if got := queuedWithin(t, q); got != "interrupt the agent" {
		t.Fatalf("queued prompt = %q, want the typed line", got)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelFn was not invoked when the prompt was queued")
	}

	if partial := stopQueueWithTimeout(t, stop); partial != "" {
		t.Fatalf("partial = %q, want empty after Enter", partial)
	}
}

func TestStartQueueReaderStopReturnsPartialLine(t *testing.T) {
	w := withStdinPipeWriter(t)

	q := &PromptQueue{}
	stop := startTestQueueReader(t, q, nil)

	// Unterminated input stays buffered in the reader; stopping must return
	// it rather than silently dropping it (QUA-577).
	if _, err := w.WriteString("unfinishe"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	time.Sleep(300 * time.Millisecond) // let the 50ms select loop consume it

	if partial := stopQueueWithTimeout(t, stop); partial != "unfinishe" {
		t.Fatalf("partial = %q, want unfinishe", partial)
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("unterminated input must never be pushed onto the queue")
	}
}

func TestStartQueueReaderStopBeforeAnyInput(t *testing.T) {
	withStdinPipeWriter(t)

	q := &PromptQueue{}
	stop := startTestQueueReader(t, q, nil)

	if partial := stopQueueWithTimeout(t, stop); partial != "" {
		t.Fatalf("partial = %q, want empty", partial)
	}
	if q.Len() != 0 {
		t.Fatalf("queue len = %d, want 0", q.Len())
	}
}

func TestStartQueueReaderHandlesBackspace(t *testing.T) {
	w := withStdinPipeWriter(t)

	q := &PromptQueue{}
	stop := startTestQueueReader(t, q, nil)

	// Both DEL (127) and BS (8) erase the previous character.
	if _, err := w.WriteString("abcd\x7f\x08\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if got := queuedWithin(t, q); got != "ab" {
		t.Fatalf("queued prompt = %q, want backspaced ab", got)
	}
	stopQueueWithTimeout(t, stop)
}

func TestStartQueueReaderCtrlCResetsLine(t *testing.T) {
	w := withStdinPipeWriter(t)

	q := &PromptQueue{}
	stop := startTestQueueReader(t, q, nil)

	if _, err := w.WriteString("discard me\x03fresh\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if got := queuedWithin(t, q); got != "fresh" {
		t.Fatalf("queued prompt = %q, want fresh (Ctrl+C wipes the draft)", got)
	}
	stopQueueWithTimeout(t, stop)
}

func TestStartQueueReaderIgnoresBlankLines(t *testing.T) {
	w := withStdinPipeWriter(t)

	q := &PromptQueue{}
	stop := startTestQueueReader(t, q, nil)

	// Whitespace-only lines queue nothing; the first real line still lands.
	if _, err := w.WriteString("\n   \nreal prompt\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if got := queuedWithin(t, q); got != "real prompt" {
		t.Fatalf("queued prompt = %q, want real prompt", got)
	}
	stopQueueWithTimeout(t, stop)
}

func TestStartQueueReaderSwallowsEscapeSequences(t *testing.T) {
	w := withStdinPipeWriter(t)

	q := &PromptQueue{}
	stop := startTestQueueReader(t, q, nil)

	// ESC+b (Option+word-jump) and full CSI sequences (arrow keys) must be
	// consumed as units: the b in ESC+b and every CSI byte are dropped rather
	// than inserted into the typed line.
	if _, err := w.WriteString("a\x1bbc\x1b[Dd\x1b[?1049h\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if got := queuedWithin(t, q); got != "acd" {
		t.Fatalf("queued prompt = %q, want acd without escape noise", got)
	}
	stopQueueWithTimeout(t, stop)
}

func TestStartQueueReaderDecodesUTF8AndDropsBadBytes(t *testing.T) {
	w := withStdinPipeWriter(t)

	q := &PromptQueue{}
	stop := startTestQueueReader(t, q, nil)

	// Multi-byte é must decode as one rune; the invalid 0xff byte is dropped.
	if _, err := w.WriteString("héllo\xff!\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if got := queuedWithin(t, q); got != "héllo!" {
		t.Fatalf("queued prompt = %q, want héllo!", got)
	}
	stopQueueWithTimeout(t, stop)
}
