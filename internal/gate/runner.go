package gate

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
)

const (
	maxCommandOutput = 12 * 1024
	truncationMarker = "\n… output truncated …"
)

var (
	ErrToolUnavailable = errors.New("required tool is unavailable")
	ErrTimedOut        = errors.New("command timed out")
)

// Runner is injectable so gate behavior can be tested without spawning tools.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// ExecRunner runs commands directly, never through a shell.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	output := &limitWriter{limit: maxCommandOutput}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return output.String(), ErrTimedOut
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return output.String(), context.Canceled
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return output.String(), fmtToolUnavailable(name, execErr)
	}
	return output.String(), err
}

func fmtToolUnavailable(name string, err error) error {
	return errors.Join(ErrToolUnavailable, errors.New(name+": "+err.Error()))
}

type limitWriter struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	truncated bool
}

// Write accepts all input so evidence truncation never changes a command's
// exit status, while retaining only the bounded prefix for display.
func (w *limitWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	originalLen := len(p)
	remaining := w.limit - len(w.buf)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
			w.truncated = true
		}
		w.buf = append(w.buf, p...)
	} else if originalLen > 0 {
		w.truncated = true
	}
	return originalLen, nil
}

func (w *limitWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.truncated {
		return string(w.buf)
	}
	return string(w.buf) + truncationMarker
}

var _ io.Writer = (*limitWriter)(nil)
