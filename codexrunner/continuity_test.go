package codexrunner

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestContinuityStartsResumesAndResetsExactThread(t *testing.T) {
	executor := &scriptedExecutor{streams: []string{
		eventStream(t, map[string]any{"type": "thread.started", "thread_id": firstThreadID}),
		eventStream(t, map[string]any{"type": "thread.started", "thread_id": firstThreadID}),
		eventStream(t, map[string]any{"type": "thread.started", "thread_id": secondThreadID}),
	}}
	continuity := NewContinuity(New(Options{Executor: executor}))

	first, err := continuity.Run(context.Background(), generatedSensitiveValue(t), Hooks{})
	if err != nil || first.ThreadID != firstThreadID {
		t.Fatal("continuity did not capture the initial thread")
	}
	second, err := continuity.Run(context.Background(), generatedSensitiveValue(t), Hooks{})
	if err != nil || second.ThreadID != firstThreadID {
		t.Fatal("continuity did not retain the exact thread")
	}
	if !slices.Equal(executor.command(t, 0).args, []string{"exec", "--json", "-"}) {
		t.Fatal("continuity did not start with the initial command")
	}
	if !slices.Equal(executor.command(t, 1).args, []string{"exec", "resume", "--json", firstThreadID, "-"}) {
		t.Fatal("continuity did not use the exact resume command")
	}

	continuity.Reset()
	third, err := continuity.Run(context.Background(), generatedSensitiveValue(t), Hooks{})
	if err != nil || third.ThreadID != secondThreadID {
		t.Fatal("reset did not start a fresh thread")
	}
	if !slices.Equal(executor.command(t, 2).args, []string{"exec", "--json", "-"}) {
		t.Fatal("reset reused prior continuity")
	}
}

func TestContinuityCheckpointRestoreIsValidated(t *testing.T) {
	continuity := NewContinuity(New(Options{Executor: &scriptedExecutor{}}))
	if err := continuity.Restore(Checkpoint{ThreadID: "--last"}); !errors.Is(err, ErrInvalidThreadID) {
		t.Fatal("restore accepted implicit continuity")
	}
	if err := continuity.Restore(Checkpoint{ThreadID: firstThreadID, Model: DefaultModel}); err != nil {
		t.Fatal("restore rejected a canonical checkpoint")
	}
	if continuity.Checkpoint() != (Checkpoint{ThreadID: firstThreadID, Model: DefaultModel}) {
		t.Fatal("restore changed the exact thread or model")
	}
	if err := continuity.Restore(Checkpoint{ThreadID: firstThreadID, Model: "auto"}); !errors.Is(err, ErrInvalidModel) {
		t.Fatal("restore accepted a non-exact model")
	}
}
