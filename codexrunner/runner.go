package codexrunner

// qmax:allow=os/exec, exec.Command

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
)

const maxEventBytes = 4 << 20

var (
	// ErrInvalidThreadID means a caller or stream supplied a non-canonical UUID.
	ErrInvalidThreadID = errors.New("codex runner: invalid thread ID")
	// ErrInvalidRolloutPath means a checkpoint carried a rollout reference that
	// is not an absolute path, so it cannot be resolved on another box.
	ErrInvalidRolloutPath = errors.New("codex runner: invalid rollout path")
	// ErrRolloutUnavailable means a checkpoint named a rollout that is not
	// present on this box, which is the expected result of restoring a
	// checkpoint into a replacement sandbox. Callers must surface a recovery
	// reason and start a new thread rather than resume into an empty one.
	ErrRolloutUnavailable = errors.New("codex runner: checkpoint rollout unavailable")
	// ErrThreadMismatch means a resumed stream identified a different thread.
	ErrThreadMismatch = errors.New("codex runner: resumed thread does not match checkpoint")
	// ErrMissingThreadID means the stream ended without a thread.started event.
	ErrMissingThreadID = errors.New("codex runner: thread ID missing from stream")
	// ErrMalformedStream means stdout was not a valid Codex JSONL event stream.
	ErrMalformedStream = errors.New("codex runner: malformed event stream")
	// ErrStart means the Codex process could not be started.
	ErrStart = errors.New("codex runner: process start failed")
	// ErrProcess means Codex exited unsuccessfully.
	ErrProcess = errors.New("codex runner: process failed")
	// ErrEventSink means a structural event consumer rejected an event.
	ErrEventSink = errors.New("codex runner: event sink failed")
	// ErrCheckpointSink means a checkpoint consumer rejected a checkpoint.
	ErrCheckpointSink = errors.New("codex runner: checkpoint sink failed")
	// ErrPresenter means the response presenter rejected an update.
	ErrPresenter = errors.New("codex runner: presenter failed")
	// ErrRunnerUnavailable means a Continuity was created without a Runner.
	ErrRunnerUnavailable = errors.New("codex runner: runner unavailable")
)

// EventKind identifies a safe structural lifecycle event. Events intentionally
// contain no prompt, response, source, tool arguments, provider message, or raw
// JSON payload.
type EventKind uint8

const (
	EventThreadStarted EventKind = iota + 1
	EventTurnStarted
	EventAssistantMessage
	EventTurnCompleted
	EventProviderError
	EventPlanLimit
)

// Usage is aggregate token accounting reported by Codex for one turn.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Event is safe for lifecycle observation and metrics. It never contains raw
// provider content.
type Event struct {
	Kind  EventKind
	Usage Usage
}

// EventSink consumes structural runner events.
type EventSink interface {
	OnEvent(context.Context, Event) error
}

// EventSinkFunc adapts a function to EventSink.
type EventSinkFunc func(context.Context, Event) error

// OnEvent implements EventSink.
func (f EventSinkFunc) OnEvent(ctx context.Context, event Event) error {
	return f(ctx, event)
}

// Checkpoint is the minimum durable state required to continue a Codex thread
// on the same exact model.
//
// ThreadID alone is only sufficient on the box that produced it: "codex exec
// resume" resolves a thread from the local Codex session store, so a thread ID
// restored into a fresh sandbox names a rollout that no longer exists there.
// RolloutPath is what makes the thread portable.
type Checkpoint struct {
	ThreadID string
	Model    string
	// RolloutPath is the local Codex rollout for ThreadID, or "" when the
	// executor cannot locate one. Callers upload it and record the result as
	// checkpoint.codex.rollout_ref; this package never reads or ships the
	// file itself.
	RolloutPath string
}

// CheckpointSink persists a validated checkpoint as soon as thread.started is
// observed, including when the turn later fails or is canceled.
type CheckpointSink interface {
	OnCheckpoint(context.Context, Checkpoint) error
}

// CheckpointSinkFunc adapts a function to CheckpointSink.
type CheckpointSinkFunc func(context.Context, Checkpoint) error

// OnCheckpoint implements CheckpointSink.
func (f CheckpointSinkFunc) OnCheckpoint(ctx context.Context, checkpoint Checkpoint) error {
	return f(ctx, checkpoint)
}

// PresentationKind distinguishes response text from a sanitized provider
// notice. PresentationText contains model output and must be rendered directly,
// not logged or copied into metrics.
type PresentationKind uint8

const (
	PresentationText PresentationKind = iota + 1
	PresentationPlanLimit
)

// Presentation is the only callback value that may contain response text.
type Presentation struct {
	Kind PresentationKind
	Text string
}

// Presenter is the terminal-neutral rendering boundary. Cloud callers may omit
// it and consume Result.Response through their protected artifact boundary.
type Presenter interface {
	Present(context.Context, Presentation) error
}

// PresenterFunc adapts a function to Presenter.
type PresenterFunc func(context.Context, Presentation) error

// Present implements Presenter.
func (f PresenterFunc) Present(ctx context.Context, presentation Presentation) error {
	return f(ctx, presentation)
}

// Cancellation is the runner's cancellation boundary. A standard
// context.Context satisfies it, preserving deadlines and process teardown
// without coupling callers to a terminal implementation.
type Cancellation interface {
	context.Context
}

// Command describes one direct process invocation. Stdin carries the prompt;
// Args is always one of the fixed, allowlisted command shapes documented by
// this package. Stderr is deliberately absent so raw diagnostics cannot be
// logged.
type Command struct {
	Executable       string
	Args             []string
	Stdin            io.Reader
	WorkingDirectory string
}

// ToolProcess is the minimal process boundary required by Runner.
type ToolProcess interface {
	Stdout() io.Reader
	Wait() error
}

// ToolExecutor starts Codex without coupling the runner to a terminal or a
// particular process host.
type ToolExecutor interface {
	Start(context.Context, Command) (ToolProcess, error)
}

// Options configures an immutable Runner.
type Options struct {
	Executable       string
	WorkingDirectory string
	Executor         ToolExecutor
	// Rollouts resolves the local rollout backing a thread. A nil locator uses
	// CodexHomeLocator.
	Rollouts RolloutLocator
}

// Hooks are optional per-turn observation and presentation boundaries.
type Hooks struct {
	Events      EventSink
	Checkpoints CheckpointSink
	Presenter   Presenter
}

// Turn contains the sensitive prompt, an optional exact continuity ID, and an
// optional exact model. When Model is present, it must be allowlisted and is
// passed on both initial and resumed commands. Callers must not log Prompt.
type Turn struct {
	Prompt   string
	ThreadID string
	Model    string
	Hooks    Hooks
}

// Result contains the exact validated Codex thread ID, selected model, and
// response. Response is transcript data and must not be logged or placed in
// workflow history.
type Result struct {
	ThreadID  string
	Model     string
	Response  string
	Usage     Usage
	Canceled  bool
	PlanLimit bool
	// RolloutPath mirrors Checkpoint.RolloutPath for the thread this turn ran
	// on, so a caller adopting a Result as durable state does not silently
	// drop the rollout reference the checkpoint sink already received.
	RolloutPath string
}

// Runner executes terminal-neutral Codex turns.
type Runner struct {
	executable       string
	workingDirectory string
	executor         ToolExecutor
	rollouts         RolloutLocator
}

// New returns a runner. An empty executable uses "codex", a nil executor uses
// the local OS process implementation, and a nil rollout locator uses
// CodexHomeLocator.
func New(options Options) *Runner {
	executable := options.Executable
	if executable == "" {
		executable = "codex"
	}
	executor := options.Executor
	if executor == nil {
		executor = OSExecutor{}
	}
	rollouts := options.Rollouts
	if rollouts == nil {
		rollouts = CodexHomeLocator{}
	}
	return &Runner{
		executable:       executable,
		workingDirectory: options.WorkingDirectory,
		executor:         executor,
		rollouts:         rollouts,
	}
}

// Run executes one turn. Cancellation is controlled exclusively by ctx.
func (r *Runner) Run(ctx Cancellation, turn Turn) (Result, error) {
	var result Result
	if r == nil || r.executor == nil {
		return result, ErrRunnerUnavailable
	}
	if err := ctx.Err(); err != nil {
		result.Canceled = true
		return result, err
	}
	if turn.ThreadID != "" && !validThreadID(turn.ThreadID) {
		return result, ErrInvalidThreadID
	}
	if turn.Model != "" {
		if err := ValidateModel(turn.Model); err != nil {
			return result, err
		}
		result.Model = turn.Model
	}

	args := []string{"exec", "--json", "-"}
	if turn.Model != "" {
		args = []string{"exec", "--model", turn.Model, "--json", "-"}
	}
	if turn.ThreadID != "" {
		args = []string{"exec", "resume", "--json", turn.ThreadID, "-"}
		if turn.Model != "" {
			args = []string{"exec", "resume", "--model", turn.Model, "--json", turn.ThreadID, "-"}
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	process, err := r.executor.Start(runCtx, Command{
		Executable:       r.executable,
		Args:             append([]string(nil), args...),
		Stdin:            strings.NewReader(turn.Prompt),
		WorkingDirectory: r.workingDirectory,
	})
	if err != nil {
		if ctx.Err() != nil {
			result.Canceled = true
			return result, ctx.Err()
		}
		return result, ErrStart
	}

	var response strings.Builder
	var streamErr error
	scanner := bufio.NewScanner(process.Stdout())
	scanner.Buffer(make([]byte, 64<<10), maxEventBytes)
	for scanner.Scan() {
		var event wireEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			streamErr = ErrMalformedStream
			break
		}
		if err := r.handleEvent(runCtx, turn, event, &result, &response); err != nil {
			streamErr = err
			break
		}
	}
	if streamErr == nil && scanner.Err() != nil {
		streamErr = ErrMalformedStream
	}
	if streamErr != nil {
		cancel()
	}
	waitErr := process.Wait()

	result.Response = strings.TrimSpace(response.String())
	if ctx.Err() != nil {
		result.Canceled = true
		return result, ctx.Err()
	}
	if streamErr != nil {
		return result, streamErr
	}
	if waitErr != nil {
		return result, ErrProcess
	}
	if result.ThreadID == "" {
		return result, ErrMissingThreadID
	}
	return result, nil
}

func (r *Runner) handleEvent(ctx context.Context, turn Turn, event wireEvent, result *Result, response *strings.Builder) error {
	switch event.Type {
	case "thread.started":
		if !validThreadID(event.ThreadID) {
			return ErrInvalidThreadID
		}
		if turn.ThreadID != "" && event.ThreadID != turn.ThreadID {
			return ErrThreadMismatch
		}
		if result.ThreadID != "" && event.ThreadID != result.ThreadID {
			return ErrThreadMismatch
		}
		if result.ThreadID == "" {
			result.ThreadID = event.ThreadID
			result.RolloutPath = r.locateRollout(event.ThreadID)
			if turn.Hooks.Checkpoints != nil {
				checkpoint := Checkpoint{
					ThreadID:    event.ThreadID,
					Model:       turn.Model,
					RolloutPath: result.RolloutPath,
				}
				if err := turn.Hooks.Checkpoints.OnCheckpoint(ctx, checkpoint); err != nil {
					return ErrCheckpointSink
				}
			}
		}
		return emitEvent(ctx, turn.Hooks.Events, Event{Kind: EventThreadStarted})
	case "turn.started":
		return emitEvent(ctx, turn.Hooks.Events, Event{Kind: EventTurnStarted})
	case "item.completed":
		if event.Item.Type != "agent_message" {
			return nil
		}
		if turn.Hooks.Presenter != nil {
			if err := turn.Hooks.Presenter.Present(ctx, Presentation{Kind: PresentationText, Text: event.Item.Text}); err != nil {
				return ErrPresenter
			}
		}
		response.WriteString(event.Item.Text)
		response.WriteByte('\n')
		return emitEvent(ctx, turn.Hooks.Events, Event{Kind: EventAssistantMessage})
	case "turn.completed":
		result.Usage = event.tokenUsage()
		return emitEvent(ctx, turn.Hooks.Events, Event{Kind: EventTurnCompleted, Usage: result.Usage})
	default:
		if !event.isError() {
			return nil
		}
		if err := emitEvent(ctx, turn.Hooks.Events, Event{Kind: EventProviderError}); err != nil {
			return err
		}
		if !event.isPlanLimit() || result.PlanLimit {
			return nil
		}
		result.PlanLimit = true
		if err := emitEvent(ctx, turn.Hooks.Events, Event{Kind: EventPlanLimit}); err != nil {
			return err
		}
		if turn.Hooks.Presenter != nil {
			if err := turn.Hooks.Presenter.Present(ctx, Presentation{Kind: PresentationPlanLimit}); err != nil {
				return ErrPresenter
			}
		}
		return nil
	}
}

// locateRollout resolves the rollout for a validated thread ID. A locator that
// finds nothing is not an error: the turn is still valid, the checkpoint is
// just not portable to another sandbox.
func (r *Runner) locateRollout(threadID string) string {
	if r == nil || r.rollouts == nil {
		return ""
	}
	return r.rollouts.LocateRollout(threadID)
}

func emitEvent(ctx context.Context, sink EventSink, event Event) error {
	if sink == nil {
		return nil
	}
	if err := sink.OnEvent(ctx, event); err != nil {
		return ErrEventSink
	}
	return nil
}

type wireEvent struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Message  string          `json:"message"`
	Error    json.RawMessage `json:"error"`
	Item     struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	Usage        *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		Input        int `json:"input"`
		Output       int `json:"output"`
	} `json:"usage"`
}

func (event wireEvent) tokenUsage() Usage {
	usage := Usage{InputTokens: event.InputTokens, OutputTokens: event.OutputTokens}
	if event.Usage == nil {
		return usage
	}
	if event.Usage.InputTokens > 0 || event.Usage.OutputTokens > 0 {
		return Usage{InputTokens: event.Usage.InputTokens, OutputTokens: event.Usage.OutputTokens}
	}
	return Usage{InputTokens: event.Usage.Input, OutputTokens: event.Usage.Output}
}

func (event wireEvent) isError() bool {
	return strings.Contains(strings.ToLower(event.Type), "error") || len(event.Error) > 0
}

func (event wireEvent) isPlanLimit() bool {
	message := event.Message
	if len(event.Error) > 0 {
		var text string
		if json.Unmarshal(event.Error, &text) == nil {
			message += " " + text
		} else {
			var detail struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			}
			if json.Unmarshal(event.Error, &detail) == nil {
				message += " " + detail.Message + " " + detail.Code
			}
		}
	}
	message = strings.ToLower(message)
	for _, marker := range []string{"plan limit", "usage limit", "rate limit", "quota", "too many requests"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func validThreadID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	for index := range id {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		character := id[index]
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

// OSExecutor starts the local Codex executable directly, without a shell.
// Stderr is discarded because provider diagnostics may contain sensitive data.
type OSExecutor struct{}

// Start implements ToolExecutor.
func (OSExecutor) Start(ctx context.Context, command Command) (ToolProcess, error) {
	cmd := exec.CommandContext(ctx, command.Executable, command.Args...)
	cmd.Dir = command.WorkingDirectory
	cmd.Stdin = command.Stdin
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &osProcess{cmd: cmd, stdout: stdout}, nil
}

type osProcess struct {
	cmd    *exec.Cmd
	stdout io.Reader
}

func (process *osProcess) Stdout() io.Reader { return process.stdout }
func (process *osProcess) Wait() error       { return process.cmd.Wait() }
