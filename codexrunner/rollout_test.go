package codexrunner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeRollout(t *testing.T, home, threadID string) string {
	t.Helper()
	directory := filepath.Join(home, "sessions", "2026", "08", "27")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal("could not create a session store")
	}
	path := filepath.Join(directory, "rollout-2026-08-27T10-15-00-"+threadID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal("could not write a rollout")
	}
	return path
}

func TestCodexHomeLocatorFindsRolloutForExactThread(t *testing.T) {
	home := t.TempDir()
	want := writeRollout(t, home, firstThreadID)
	locator := CodexHomeLocator{Home: home}

	if got := locator.LocateRollout(firstThreadID); got != want {
		t.Fatal("locator did not find the rollout for the exact thread")
	}
	if locator.LocateRollout(secondThreadID) != "" {
		t.Fatal("locator matched a thread with no rollout")
	}
	if locator.LocateRollout("--last") != "" {
		t.Fatal("locator accepted a non-canonical thread ID")
	}
	if (CodexHomeLocator{Home: filepath.Join(home, "absent")}).LocateRollout(firstThreadID) != "" {
		t.Fatal("locator invented a rollout for an absent store")
	}
}

func TestCodexHomeLocatorReadsCodexHomeEnvironment(t *testing.T) {
	home := t.TempDir()
	want := writeRollout(t, home, firstThreadID)
	t.Setenv("CODEX_HOME", home)

	if got := (CodexHomeLocator{}).LocateRollout(firstThreadID); got != want {
		t.Fatal("locator did not resolve $CODEX_HOME")
	}
}

func TestRunnerCheckpointCarriesRolloutPath(t *testing.T) {
	rollout := writeRollout(t, t.TempDir(), firstThreadID)
	executor := &scriptedExecutor{streams: []string{eventStream(t,
		map[string]any{"type": "thread.started", "thread_id": firstThreadID},
	)}}
	var checkpoints []Checkpoint
	runner := New(Options{Executor: executor, Rollouts: RolloutLocatorFunc(func(threadID string) string {
		if threadID != firstThreadID {
			t.Fatal("runner located a rollout for the wrong thread")
		}
		return rollout
	})})

	result, err := runner.Run(context.Background(), Turn{
		Prompt: generatedSensitiveValue(t),
		Hooks: Hooks{Checkpoints: CheckpointSinkFunc(func(_ context.Context, checkpoint Checkpoint) error {
			checkpoints = append(checkpoints, checkpoint)
			return nil
		})},
	})
	if err != nil {
		t.Fatal("turn failed")
	}
	if result.RolloutPath != rollout {
		t.Fatal("result did not carry the rollout path")
	}
	if len(checkpoints) != 1 || checkpoints[0].RolloutPath != rollout {
		t.Fatal("checkpoint sink did not receive the rollout path at thread.started")
	}
}

func TestRunnerCheckpointOmitsUnlocatableRollout(t *testing.T) {
	executor := &scriptedExecutor{streams: []string{eventStream(t,
		map[string]any{"type": "thread.started", "thread_id": firstThreadID},
	)}}
	runner := New(Options{Executor: executor, Rollouts: RolloutLocatorFunc(func(string) string { return "" })})

	result, err := runner.Run(context.Background(), Turn{Prompt: generatedSensitiveValue(t)})
	if err != nil {
		t.Fatal("a turn without a locatable rollout must still succeed")
	}
	if result.RolloutPath != "" {
		t.Fatal("runner invented a rollout path")
	}
}

func TestContinuityRetainsRolloutAcrossTurns(t *testing.T) {
	rollout := writeRollout(t, t.TempDir(), firstThreadID)
	executor := &scriptedExecutor{streams: []string{
		eventStream(t, map[string]any{"type": "thread.started", "thread_id": firstThreadID}),
		eventStream(t, map[string]any{"type": "thread.started", "thread_id": firstThreadID}),
	}}
	continuity := NewContinuity(New(Options{
		Executor: executor,
		Rollouts: RolloutLocatorFunc(func(string) string { return rollout }),
	}))

	if _, err := continuity.Run(context.Background(), generatedSensitiveValue(t), Hooks{}); err != nil {
		t.Fatal("initial turn failed")
	}
	if continuity.Checkpoint().RolloutPath != rollout {
		t.Fatal("continuity dropped the rollout on the initial turn")
	}
	if _, err := continuity.Run(context.Background(), generatedSensitiveValue(t), Hooks{}); err != nil {
		t.Fatal("resumed turn failed")
	}
	if continuity.Checkpoint().RolloutPath != rollout {
		t.Fatal("continuity dropped the rollout when adopting the resumed turn")
	}
}

func TestContinuityRestoreValidatesRollout(t *testing.T) {
	rollout := writeRollout(t, t.TempDir(), firstThreadID)
	continuity := NewContinuity(New(Options{Executor: &scriptedExecutor{}}))

	restored := Checkpoint{ThreadID: firstThreadID, Model: DefaultModel, RolloutPath: rollout}
	if err := continuity.Restore(restored); err != nil {
		t.Fatal("restore rejected a rollout present on this box")
	}
	if continuity.Checkpoint() != restored {
		t.Fatal("restore changed the durable checkpoint")
	}

	continuity.Reset()
	missing := filepath.Join(t.TempDir(), "rollout-2026-08-27T10-15-00-"+firstThreadID+".jsonl")
	err := continuity.Restore(Checkpoint{ThreadID: firstThreadID, RolloutPath: missing})
	if !errors.Is(err, ErrRolloutUnavailable) {
		t.Fatal("restore resumed into a rollout that does not exist on this box")
	}
	if continuity.Checkpoint() != (Checkpoint{}) {
		t.Fatal("a refused restore installed state anyway")
	}

	relative := filepath.Join("sessions", "rollout-2026-08-27T10-15-00-"+firstThreadID+".jsonl")
	if err := continuity.Restore(Checkpoint{ThreadID: firstThreadID, RolloutPath: relative}); !errors.Is(err, ErrInvalidRolloutPath) {
		t.Fatal("restore accepted a rollout path that depends on the working directory")
	}
}

func TestContinuityRestoreWithoutRolloutStartsFreshThread(t *testing.T) {
	executor := &scriptedExecutor{streams: []string{
		eventStream(t, map[string]any{"type": "thread.started", "thread_id": secondThreadID}),
	}}
	continuity := NewContinuity(New(Options{
		Executor: executor,
		Rollouts: RolloutLocatorFunc(func(string) string { return "" }),
	}))

	// The documented recovery from ErrRolloutUnavailable: restore the same
	// checkpoint with the rollout cleared, which starts a new thread instead of
	// resuming into one whose transcript this box does not have.
	if err := continuity.Restore(Checkpoint{Model: DefaultModel}); err != nil {
		t.Fatal("restore rejected a checkpoint with no thread and no rollout")
	}
	result, err := continuity.Run(context.Background(), generatedSensitiveValue(t), Hooks{})
	if err != nil || result.ThreadID != secondThreadID {
		t.Fatal("recovery did not start a fresh thread")
	}
}
