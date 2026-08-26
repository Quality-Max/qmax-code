package codexrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	firstThreadID  = "abcdef12-3456-4abc-8def-1234567890ab"
	secondThreadID = "12345678-90ab-4cde-8fab-1234567890ab"
)

func TestRunnerInitialTurnCapturesExactThreadID(t *testing.T) {
	prompt := generatedSensitiveValue(t)
	response := strings.ToUpper(t.Name())
	executor := &scriptedExecutor{streams: []string{eventStream(t,
		map[string]any{"type": "thread.started", "thread_id": firstThreadID},
		map[string]any{"type": "turn.started"},
		map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": response}},
		map[string]any{"type": "turn.completed", "usage": map[string]any{"input_tokens": 7, "output_tokens": 3}},
	)}}

	result, err := New(Options{Executable: "codex-test", WorkingDirectory: t.TempDir(), Executor: executor}).Run(context.Background(), Turn{Prompt: prompt})
	if err != nil {
		t.Fatal("initial turn failed")
	}
	if result.ThreadID != firstThreadID {
		t.Fatal("runner did not return the exact thread ID")
	}
	if result.Response != response {
		t.Fatal("runner did not return the assistant response")
	}
	if result.Usage != (Usage{InputTokens: 7, OutputTokens: 3}) {
		t.Fatal("runner did not capture turn usage")
	}
	command := executor.command(t, 0)
	if command.executable != "codex-test" || !slices.Equal(command.args, []string{"exec", "--json", "-"}) {
		t.Fatal("initial command shape changed")
	}
	if command.stdin != prompt {
		t.Fatal("prompt was not sent exactly through stdin")
	}
	assertPromptAbsent(t, prompt, command.args, err)
}

func TestRunnerResumeUsesExactThreadID(t *testing.T) {
	prompt := generatedSensitiveValue(t)
	executor := &scriptedExecutor{streams: []string{eventStream(t,
		map[string]any{"type": "thread.started", "thread_id": firstThreadID},
	)}}

	result, err := New(Options{Executor: executor}).Run(context.Background(), Turn{Prompt: prompt, ThreadID: firstThreadID})
	if err != nil {
		t.Fatal("resume turn failed")
	}
	if result.ThreadID != firstThreadID {
		t.Fatal("resume changed the thread ID")
	}
	command := executor.command(t, 0)
	want := []string{"exec", "resume", "--json", firstThreadID, "-"}
	if !slices.Equal(command.args, want) {
		t.Fatal("resume command shape changed")
	}
	if slices.Contains(command.args, "--last") {
		t.Fatal("resume command used forbidden implicit continuity")
	}
	if command.stdin != prompt {
		t.Fatal("resume prompt was not sent exactly through stdin")
	}
	assertPromptAbsent(t, prompt, command.args, err)
}

func TestRunnerUsesExactModelOnInitialAndResumedTurns(t *testing.T) {
	executor := &scriptedExecutor{streams: []string{
		eventStream(t, map[string]any{"type": "thread.started", "thread_id": firstThreadID}),
		eventStream(t, map[string]any{"type": "thread.started", "thread_id": firstThreadID}),
	}}
	runner := New(Options{Executor: executor})
	var checkpoint Checkpoint

	first, err := runner.Run(context.Background(), Turn{
		Prompt: generatedSensitiveValue(t),
		Model:  DefaultModel,
		Hooks: Hooks{Checkpoints: CheckpointSinkFunc(func(_ context.Context, next Checkpoint) error {
			checkpoint = next
			return nil
		})},
	})
	if err != nil {
		t.Fatal("modeled initial turn failed")
	}
	if first.Model != DefaultModel || checkpoint != (Checkpoint{ThreadID: firstThreadID, Model: DefaultModel}) {
		t.Fatal("initial turn did not preserve its exact model")
	}
	if want := []string{"exec", "--model", DefaultModel, "--json", "-"}; !slices.Equal(executor.command(t, 0).args, want) {
		t.Fatal("initial turn did not pass the exact model")
	}

	second, err := runner.Run(context.Background(), Turn{
		Prompt:   generatedSensitiveValue(t),
		ThreadID: checkpoint.ThreadID,
		Model:    checkpoint.Model,
	})
	if err != nil {
		t.Fatal("modeled resume turn failed")
	}
	if second.Model != DefaultModel {
		t.Fatal("resumed turn changed its exact model")
	}
	if want := []string{"exec", "resume", "--model", DefaultModel, "--json", firstThreadID, "-"}; !slices.Equal(executor.command(t, 1).args, want) {
		t.Fatal("resumed turn did not pass the exact model")
	}
}

func TestRunnerRejectsUnsupportedModelBeforeProcessStart(t *testing.T) {
	for _, model := range []string{"", "auto", "--model", "GPT-5.6-TERRA", "gpt-unknown"} {
		if model == "" {
			continue
		}
		t.Run(model, func(t *testing.T) {
			executor := &scriptedExecutor{}
			_, err := New(Options{Executor: executor}).Run(context.Background(), Turn{Model: model})
			if !errors.Is(err, ErrInvalidModel) {
				t.Fatal("unsupported model was not rejected")
			}
			if executor.commandCount() != 0 {
				t.Fatal("unsupported model reached the process boundary")
			}
		})
	}
}

func TestRunnerRejectsImplicitOrMismatchedContinuity(t *testing.T) {
	t.Run("option-like ID", func(t *testing.T) {
		executor := &scriptedExecutor{}
		_, err := New(Options{Executor: executor}).Run(context.Background(), Turn{ThreadID: "--last"})
		if !errors.Is(err, ErrInvalidThreadID) {
			t.Fatal("option-like thread ID was not rejected")
		}
		if executor.commandCount() != 0 {
			t.Fatal("invalid thread ID reached the process boundary")
		}
	})

	t.Run("stream mismatch", func(t *testing.T) {
		executor := &scriptedExecutor{streams: []string{eventStream(t,
			map[string]any{"type": "thread.started", "thread_id": secondThreadID},
		)}}
		_, err := New(Options{Executor: executor}).Run(context.Background(), Turn{ThreadID: firstThreadID})
		if !errors.Is(err, ErrThreadMismatch) {
			t.Fatal("mismatched resumed thread was not rejected")
		}
	})
}

func TestRunnerFailsClosedOnMalformedStreams(t *testing.T) {
	prompt := generatedSensitiveValue(t)
	malformed := strings.Repeat("{", 3)
	executor := &scriptedExecutor{streams: []string{
		eventStream(t, map[string]any{"type": "thread.started", "thread_id": firstThreadID}) + malformed + "\n",
	}}

	result, err := New(Options{Executor: executor}).Run(context.Background(), Turn{Prompt: prompt})
	if !errors.Is(err, ErrMalformedStream) {
		t.Fatal("malformed JSONL was not rejected")
	}
	if result.ThreadID != firstThreadID {
		t.Fatal("validated checkpoint was not retained before stream failure")
	}
	assertPromptAbsent(t, prompt, executor.command(t, 0).args, err)
	if strings.Contains(err.Error(), malformed) {
		t.Fatal("malformed provider payload escaped through the error boundary")
	}
}

func TestRunnerSanitizesProcessFailures(t *testing.T) {
	prompt := generatedSensitiveValue(t)
	t.Run("start", func(t *testing.T) {
		executor := &scriptedExecutor{startErr: errors.New(prompt)}

		_, err := New(Options{Executor: executor}).Run(context.Background(), Turn{Prompt: prompt})
		if !errors.Is(err, ErrStart) {
			t.Fatal("start failure did not use the public sentinel")
		}
		assertPromptAbsent(t, prompt, executor.command(t, 0).args, err)
	})

	t.Run("wait", func(t *testing.T) {
		executor := &scriptedExecutor{
			streams:    []string{eventStream(t, map[string]any{"type": "thread.started", "thread_id": firstThreadID})},
			waitErrors: []error{errors.New(prompt)},
		}

		result, err := New(Options{Executor: executor}).Run(context.Background(), Turn{Prompt: prompt})
		if !errors.Is(err, ErrProcess) {
			t.Fatal("process failure did not use the public sentinel")
		}
		if result.ThreadID != firstThreadID {
			t.Fatal("process failure lost the validated checkpoint")
		}
		assertPromptAbsent(t, prompt, executor.command(t, 0).args, err)
	})
}

func TestRunnerRequiresAThreadCheckpoint(t *testing.T) {
	executor := &scriptedExecutor{streams: []string{eventStream(t,
		map[string]any{"type": "turn.started"},
		map[string]any{"type": "turn.completed"},
	)}}

	_, err := New(Options{Executor: executor}).Run(context.Background(), Turn{})
	if !errors.Is(err, ErrMissingThreadID) {
		t.Fatal("stream without a thread checkpoint was accepted")
	}
}

func TestRunnerCancellationReapsTheTurn(t *testing.T) {
	prompt := generatedSensitiveValue(t)
	executor := newCancelExecutor(t, firstThreadID)
	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := New(Options{Executor: executor}).Run(ctx, Turn{Prompt: prompt})
		done <- outcome{result: result, err: err}
	}()

	<-executor.started
	cancel()
	got := <-done
	if !errors.Is(got.err, context.Canceled) || !got.result.Canceled {
		t.Fatal("context cancellation was not returned as a canceled result")
	}
	if got.result.ThreadID != firstThreadID {
		t.Fatal("cancellation lost the checkpoint captured before interruption")
	}
	assertPromptAbsent(t, prompt, executor.command.args, got.err)
}

func TestRunnerAlreadyCanceledDoesNotStart(t *testing.T) {
	executor := &scriptedExecutor{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := New(Options{Executor: executor}).Run(ctx, Turn{Prompt: generatedSensitiveValue(t)})
	if !errors.Is(err, context.Canceled) || !result.Canceled {
		t.Fatal("already-canceled turn did not return cancellation")
	}
	if executor.commandCount() != 0 {
		t.Fatal("already-canceled turn crossed the process boundary")
	}
}

func TestRunnerHooksSeparateStructureFromPresentation(t *testing.T) {
	response := strings.ToUpper(t.Name())
	executor := &scriptedExecutor{streams: []string{eventStream(t,
		map[string]any{"type": "thread.started", "thread_id": firstThreadID},
		map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": response}},
	)}}
	var events []Event
	var presentations []Presentation
	result, err := New(Options{Executor: executor}).Run(context.Background(), Turn{Hooks: Hooks{
		Events: EventSinkFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		}),
		Presenter: PresenterFunc(func(_ context.Context, presentation Presentation) error {
			presentations = append(presentations, presentation)
			return nil
		}),
	}})
	if err != nil || result.Response != response {
		t.Fatal("hooked turn failed")
	}
	if len(events) != 2 || events[0].Kind != EventThreadStarted || events[1].Kind != EventAssistantMessage {
		t.Fatal("structural event sequence changed")
	}
	if len(presentations) != 1 || presentations[0] != (Presentation{Kind: PresentationText, Text: response}) {
		t.Fatal("response did not remain inside the presentation boundary")
	}
}

func TestRunnerSanitizesPlanLimitPresentation(t *testing.T) {
	providerMessage := generatedSensitiveValue(t) + " quota"
	executor := &scriptedExecutor{streams: []string{eventStream(t,
		map[string]any{"type": "thread.started", "thread_id": firstThreadID},
		map[string]any{"type": "error", "message": providerMessage},
	)}}
	var events []Event
	var presentations []Presentation

	result, err := New(Options{Executor: executor}).Run(context.Background(), Turn{Hooks: Hooks{
		Events: EventSinkFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		}),
		Presenter: PresenterFunc(func(_ context.Context, presentation Presentation) error {
			presentations = append(presentations, presentation)
			return nil
		}),
	}})
	if err != nil || !result.PlanLimit {
		t.Fatal("plan-limit event was not classified")
	}
	wantEvents := []EventKind{EventThreadStarted, EventProviderError, EventPlanLimit}
	if len(events) != len(wantEvents) {
		t.Fatal("plan-limit structural event count changed")
	}
	for index, want := range wantEvents {
		if events[index].Kind != want {
			t.Fatal("plan-limit structural event sequence changed")
		}
	}
	if len(presentations) != 1 || presentations[0] != (Presentation{Kind: PresentationPlanLimit}) {
		t.Fatal("provider payload crossed the sanitized plan-limit presentation boundary")
	}
	if strings.Contains(result.Response, providerMessage) {
		t.Fatal("provider payload crossed the result boundary")
	}
}

type recordedCommand struct {
	executable       string
	args             []string
	stdin            string
	workingDirectory string
}

type scriptedExecutor struct {
	mu         sync.Mutex
	streams    []string
	waitErrors []error
	startErr   error
	commands   []recordedCommand
}

func (executor *scriptedExecutor) Start(_ context.Context, command Command) (ToolProcess, error) {
	input, readErr := io.ReadAll(command.Stdin)
	if readErr != nil {
		return nil, readErr
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.commands = append(executor.commands, recordedCommand{
		executable:       command.Executable,
		args:             append([]string(nil), command.Args...),
		stdin:            string(input),
		workingDirectory: command.WorkingDirectory,
	})
	if executor.startErr != nil {
		return nil, executor.startErr
	}
	index := len(executor.commands) - 1
	if index >= len(executor.streams) {
		return nil, errors.New("test stream unavailable")
	}
	var waitErr error
	if index < len(executor.waitErrors) {
		waitErr = executor.waitErrors[index]
	}
	return staticProcess{stdout: strings.NewReader(executor.streams[index]), waitErr: waitErr}, nil
}

func (executor *scriptedExecutor) command(t *testing.T, index int) recordedCommand {
	t.Helper()
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if index >= len(executor.commands) {
		t.Fatal("expected command was not recorded")
	}
	return executor.commands[index]
}

func (executor *scriptedExecutor) commandCount() int {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return len(executor.commands)
}

type staticProcess struct {
	stdout  io.Reader
	waitErr error
}

func (process staticProcess) Stdout() io.Reader { return process.stdout }
func (process staticProcess) Wait() error       { return process.waitErr }

type cancelExecutor struct {
	testing *testing.T
	id      string
	started chan struct{}
	command recordedCommand
}

func newCancelExecutor(t *testing.T, id string) *cancelExecutor {
	return &cancelExecutor{testing: t, id: id, started: make(chan struct{})}
}

func (executor *cancelExecutor) Start(ctx context.Context, command Command) (ToolProcess, error) {
	input, err := io.ReadAll(command.Stdin)
	if err != nil {
		return nil, err
	}
	executor.command = recordedCommand{args: append([]string(nil), command.Args...), stdin: string(input)}
	reader, writer := io.Pipe()
	ready := make(chan struct{})
	go func() {
		defer writer.Close()
		_, _ = io.WriteString(writer, eventStream(executor.testing, map[string]any{"type": "thread.started", "thread_id": executor.id}))
		close(ready)
		<-ctx.Done()
	}()
	go func() {
		<-ready
		close(executor.started)
	}()
	return &cancelProcess{stdout: reader, ctx: ctx}, nil
}

type cancelProcess struct {
	stdout io.Reader
	ctx    context.Context
}

func (process *cancelProcess) Stdout() io.Reader { return process.stdout }
func (process *cancelProcess) Wait() error {
	<-process.ctx.Done()
	return process.ctx.Err()
}

func eventStream(t *testing.T, events ...map[string]any) string {
	t.Helper()
	var stream bytes.Buffer
	encoder := json.NewEncoder(&stream)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatal("could not construct synthetic event stream")
		}
	}
	return stream.String()
}

func generatedSensitiveValue(t *testing.T) string {
	t.Helper()
	return strings.Repeat(t.Name(), 2)
}

func assertPromptAbsent(t *testing.T, prompt string, args []string, err error) {
	t.Helper()
	for _, arg := range args {
		if strings.Contains(arg, prompt) {
			t.Fatal("prompt escaped into process arguments")
		}
	}
	if err != nil && strings.Contains(err.Error(), prompt) {
		t.Fatal("prompt escaped into a public error")
	}
}
