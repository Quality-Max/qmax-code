package main

import (
	"errors"
	"os"
	"testing"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/session"
	"github.com/qualitymax/qmax-code/internal/tui"
)

func TestSaveSessionFlagOverridesAutoSaveWithoutChangingOtherConfig(t *testing.T) {
	cfg := api.DefaultConfig()
	cfg.AutoSave = false
	applySaveSessionFlag(cfg, true)
	if !cfg.AutoSave {
		t.Fatal("--save-session should enable auto-save for the current run")
	}
}

func TestSaveSessionFlagDoesNotChangeDisabledConfigWhenAbsent(t *testing.T) {
	cfg := api.DefaultConfig()
	cfg.AutoSave = false
	applySaveSessionFlag(cfg, false)
	if cfg.AutoSave {
		t.Fatal("auto-save should remain disabled when --save-session is absent")
	}
}

func TestShouldSaveOneShotSession(t *testing.T) {
	history := []api.Message{{Role: "user", Content: "hi"}}
	if shouldSaveOneShotSession(false, history) {
		t.Error("must not save when auto-save is off")
	}
	if shouldSaveOneShotSession(true, nil) {
		t.Error("must not save an empty history")
	}
	if !shouldSaveOneShotSession(true, history) {
		t.Error("must save when auto-save is on and history is non-empty")
	}
}

// TestOneShotSessionPersistsToDisk is the regression test for the gap where
// --save-session (or a persisted auto_save=true) only flipped a config field
// that the one-shot path (-p / positional-arg mode) never consulted, so
// `qmax-code -p "..."` never wrote a session file for /resume to find. This
// exercises the same shouldSaveOneShotSession + session.SaveSession pair that
// runOneShot's saveOneShotSession closure calls.
func TestOneShotSessionPersistsToDisk(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })
	os.Setenv("HOME", t.TempDir())

	sessionID := session.GenerateSessionID()
	history := []api.Message{
		{Role: "user", Content: "test the login flow"},
		{Role: "assistant", Content: "done"},
	}

	if !shouldSaveOneShotSession(true /* autoSave */, history) {
		t.Fatal("expected gate to allow saving")
	}
	if err := session.SaveSession(sessionID, history, 7, api.TokenUsage{}, "sonnet"); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	loaded, err := session.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("expected one-shot session to be persisted, LoadSession failed: %v", err)
	}
	if len(loaded.Messages) != len(history) {
		t.Errorf("loaded %d messages, want %d", len(loaded.Messages), len(history))
	}
	if loaded.ProjectID != 7 {
		t.Errorf("loaded ProjectID = %d, want 7", loaded.ProjectID)
	}
}

// fakeCLIAgent is a tiny CLIAgent implementation that records whether its
// Run method was called. Used to verify the QUA-576 dispatch fix without
// needing a real claude/codex binary.
type fakeCLIAgent struct {
	called bool
	prompt string
	result string
	runErr error
}

func (f *fakeCLIAgent) Run(userMsg string, _ *tui.Terminal) (string, error) {
	f.called = true
	f.prompt = userMsg
	return f.result, f.runErr
}
func (f *fakeCLIAgent) Cancel()               {}
func (f *fakeCLIAgent) Cleanup()              {}
func (f *fakeCLIAgent) SetOutputVerbose(bool) {}

// dispatchForTest mirrors the dispatch logic in main.go's `runOneShot`
// closure. Keeping it in sync with that closure is the regression contract
// of TestOneShotDispatch_* below.
//
// The real closure (main.go) also creates a tui.Terminal when cliAgent is
// non-nil; for unit testing we pass nil since fakeCLIAgent doesn't use it.
func dispatchForTest(prompt string, cliAgent agent.CLIAgent, apiCallback func(string) (string, error)) error {
	if cliAgent != nil {
		_, err := cliAgent.Run(prompt, nil)
		return err
	}
	_, err := apiCallback(prompt)
	return err
}

// TestOneShotDispatch_PrefersCLIAgent is the regression test for QUA-576.
// Before the fix, -p and positional-arg modes called ag.Run directly even
// when backend=cc was configured. This test asserts that when a CLIAgent
// is present, it is the agent that handles the one-shot prompt.
func TestOneShotDispatch_PrefersCLIAgent(t *testing.T) {
	fake := &fakeCLIAgent{result: "cli-result"}
	apiCalled := false
	apiCallback := func(string) (string, error) {
		apiCalled = true
		return "api-result", nil
	}

	if err := dispatchForTest("hello", fake, apiCallback); err != nil {
		t.Fatalf("dispatch returned err: %v", err)
	}
	if !fake.called {
		t.Error("expected fakeCLIAgent.Run to be called, but it was not")
	}
	if apiCalled {
		t.Error("expected API callback NOT to be called when cliAgent is set, but it was")
	}
	if fake.prompt != "hello" {
		t.Errorf("prompt passed to CLI agent: got %q, want %q", fake.prompt, "hello")
	}
}

// TestOneShotDispatch_FallsBackToAPI confirms the no-CLI-backend fallback.
func TestOneShotDispatch_FallsBackToAPI(t *testing.T) {
	apiCalled := false
	apiCallback := func(string) (string, error) {
		apiCalled = true
		return "api-result", nil
	}

	if err := dispatchForTest("hello", nil, apiCallback); err != nil {
		t.Fatalf("dispatch returned err: %v", err)
	}
	if !apiCalled {
		t.Error("expected API callback to be called when cliAgent is nil, but it was not")
	}
}

// TestOneShotDispatch_PropagatesCLIError ensures errors from the CLI agent
// surface to the caller (which exits non-zero in main.go).
func TestOneShotDispatch_PropagatesCLIError(t *testing.T) {
	want := errors.New("cc subprocess failed")
	fake := &fakeCLIAgent{runErr: want}
	apiCallback := func(string) (string, error) {
		t.Fatal("API callback must not be called when cliAgent is present")
		return "", nil
	}

	err := dispatchForTest("hello", fake, apiCallback)
	if err == nil || err.Error() != want.Error() {
		t.Errorf("dispatch error: got %v, want %v", err, want)
	}
}
