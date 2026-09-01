package gate

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestLimitWriterConsumesOverflowAndBoundsEvidence(t *testing.T) {
	w := &limitWriter{limit: 4}
	input := "abcdefgh"
	n, err := w.Write([]byte(input))
	if err != nil || n != len(input) {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if got := w.String(); !strings.HasPrefix(got, "abcd") || !strings.Contains(got, "truncated") {
		t.Fatalf("String() = %q", got)
	}
}

func TestFmtToolUnavailablePreservesClassificationAndContext(t *testing.T) {
	cause := errors.New("missing executable")
	err := fmtToolUnavailable("git", cause)

	if !errors.Is(err, ErrToolUnavailable) {
		t.Fatalf("fmtToolUnavailable() error = %v, want ErrToolUnavailable", err)
	}
	if !strings.Contains(err.Error(), "git: missing executable") {
		t.Fatalf("fmtToolUnavailable() error = %q, want tool and cause", err)
	}
}

func TestLimitWriterDoesNotTruncateExactLimit(t *testing.T) {
	w := &limitWriter{limit: 4}
	if n, err := w.Write([]byte("abcd")); err != nil || n != 4 {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if got := w.String(); got != "abcd" {
		t.Fatalf("String() = %q, want exact untruncated output", got)
	}
}

func TestLimitWriterSupportsConcurrentWrites(t *testing.T) {
	w := &limitWriter{limit: 128}
	var group sync.WaitGroup
	for range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _ = w.Write([]byte("abcdefgh"))
		}()
	}
	group.Wait()

	got := w.String()
	if !strings.Contains(got, truncationMarker) {
		t.Fatalf("String() = %q, want truncation marker", got)
	}
	if prefix := strings.TrimSuffix(got, truncationMarker); len(prefix) != 128 {
		t.Fatalf("captured bytes = %d, want 128", len(prefix))
	}
}

func TestExecRunnerPropagatesCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (ExecRunner{}).Run(ctx, ".", os.Args[0], "-test.run=TestExecRunnerPropagatesCanceledContext")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}
