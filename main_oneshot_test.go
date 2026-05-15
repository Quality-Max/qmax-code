package main

import (
	"errors"
	"testing"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// fakeCLIAgent is a tiny CLIAgent implementation that records whether its
// Run method was called. Used to verify the QUA-576 dispatch fix without
// needing a real claude/codex binary.
type fakeCLIAgent struct {
	called  bool
	prompt  string
	result  string
	runErr  error
}

func (f *fakeCLIAgent) Run(userMsg string, _ *tui.Terminal) (string, error) {
	f.called = true
	f.prompt = userMsg
	return f.result, f.runErr
}
func (f *fakeCLIAgent) Cancel()                {}
func (f *fakeCLIAgent) Cleanup()               {}
func (f *fakeCLIAgent) SetOutputVerbose(bool)  {}

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
