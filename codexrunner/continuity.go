package codexrunner

import (
	"context"
	"sync"
)

// Continuity serializes turns and remembers only the exact validated thread
// checkpoint. It stores no prompts, responses, source, or provider payloads.
// Durable callers should also provide Hooks.Checkpoints on every turn.
type Continuity struct {
	runner *Runner
	runMu  sync.Mutex
	mu     sync.RWMutex
	state  Checkpoint
}

// NewContinuity creates an empty continuity adapter for runner.
func NewContinuity(runner *Runner) *Continuity {
	return &Continuity{runner: runner}
}

// Run executes a turn using the current checkpoint and atomically adopts the
// validated checkpoint emitted by the runner, even if the turn later fails.
func (continuity *Continuity) Run(ctx Cancellation, prompt string, hooks Hooks) (Result, error) {
	continuity.runMu.Lock()
	defer continuity.runMu.Unlock()
	if continuity.runner == nil {
		return Result{}, ErrRunnerUnavailable
	}

	checkpoint := continuity.Checkpoint()
	externalSink := hooks.Checkpoints
	hooks.Checkpoints = CheckpointSinkFunc(func(ctx context.Context, next Checkpoint) error {
		continuity.mu.Lock()
		continuity.state = next
		continuity.mu.Unlock()
		if externalSink != nil {
			return externalSink.OnCheckpoint(ctx, next)
		}
		return nil
	})

	result, err := continuity.runner.Run(ctx, Turn{
		Prompt:   prompt,
		ThreadID: checkpoint.ThreadID,
		Hooks:    hooks,
	})
	if result.ThreadID != "" {
		continuity.mu.Lock()
		continuity.state = Checkpoint{ThreadID: result.ThreadID}
		continuity.mu.Unlock()
	}
	return result, err
}

// Checkpoint returns the current exact continuity state.
func (continuity *Continuity) Checkpoint() Checkpoint {
	continuity.mu.RLock()
	defer continuity.mu.RUnlock()
	return continuity.state
}

// Restore validates and installs a durable checkpoint. An empty checkpoint is
// equivalent to Reset.
func (continuity *Continuity) Restore(checkpoint Checkpoint) error {
	continuity.runMu.Lock()
	defer continuity.runMu.Unlock()
	if checkpoint.ThreadID != "" && !validThreadID(checkpoint.ThreadID) {
		return ErrInvalidThreadID
	}
	continuity.mu.Lock()
	continuity.state = checkpoint
	continuity.mu.Unlock()
	return nil
}

// Reset forgets the current thread so the next Run starts a new one.
func (continuity *Continuity) Reset() {
	continuity.runMu.Lock()
	defer continuity.runMu.Unlock()
	continuity.mu.Lock()
	continuity.state = Checkpoint{}
	continuity.mu.Unlock()
}
