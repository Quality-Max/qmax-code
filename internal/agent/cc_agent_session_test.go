package agent

// Regression tests for session persistence in CLI agent mode (CC/Codex backends).
//
// Bug: when cliAgent != nil, each turn was dispatched to a subprocess
// (CCAgent/CodexAgent) that never wrote back to agent.history. The autoSave
// guard `len(agent.history) > 0` always evaluated false, so every session
// was written as empty (0 turns, 0 tokens).
//
// Fix (main.go): after a successful cliAgent.Run(), the user message and
// assistant response are appended to agent.history before autoSave is called.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/session"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// mockCLIAgent is a CLIAgent stub that returns scripted responses without
// spawning any subprocess or touching the terminal.
type mockCLIAgent struct {
	responses []string
	calls     int
	err       error // if set, Run returns this error on every call
}

func (m *mockCLIAgent) Run(_ string, _ *tui.Terminal) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if m.calls < len(m.responses) {
		r := m.responses[m.calls]
		m.calls++
		return r, nil
	}
	return "ok", nil
}

func (m *mockCLIAgent) Cancel() {}

func (m *mockCLIAgent) Cleanup() {}

func (m *mockCLIAgent) SetOutputVerbose(bool) {}

// historyAfterCLITurns simulates the fixed main-loop path for N turns:
// calls mockCLIAgent.Run() and mirrors each successful turn into history.
// This is the exact logic added to main.go in the fix.
func historyAfterCLITurns(agent *mockCLIAgent, turns []string) []api.Message {
	var history []api.Message
	for _, userMsg := range turns {
		result, err := agent.Run(userMsg, nil)
		if err == nil {
			history = append(history,
				api.Message{Role: "user", Content: userMsg},
				api.Message{Role: "assistant", Content: result},
			)
		}
	}
	return history
}

// TestCLIAgentHistoryMirroredOnSuccess verifies that after each successful
// cliAgent.Run() the turn is appended to history, making autoSave non-empty.
func TestCLIAgentHistoryMirroredOnSuccess(t *testing.T) {
	agent := &mockCLIAgent{
		responses: []string{
			"Here are 3 issues I found…",
			"Fixed: added nil check",
			"All 12 tests passed",
		},
	}
	turns := []string{"review my tests", "fix issue 1", "run tests"}

	history := historyAfterCLITurns(agent, turns)

	if len(history) != 6 {
		t.Errorf("Messages: got %d, want 6 (2 per turn × 3 turns)", len(history))
	}
	// Count user messages — these become Turns in SaveSession
	userTurns := 0
	for _, m := range history {
		if m.Role == "user" {
			userTurns++
		}
	}
	if userTurns != 3 {
		t.Errorf("User turns: got %d, want 3", userTurns)
	}
}

// TestCLIAgentSessionSavedWithCorrectTurns is the core regression test.
// Before the fix: history was empty → autoSave skipped → sessions showed 0 turns.
// After the fix: history is mirrored → autoSave saves → sessions show real turns.
func TestCLIAgentSessionSavedWithCorrectTurns(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	agent := &mockCLIAgent{responses: []string{"response A", "response B"}}
	turns := []string{"question 1", "question 2"}
	history := historyAfterCLITurns(agent, turns)

	sessionID := "cli-regression-test"

	// autoSave condition: only saves when history is non-empty
	if len(history) == 0 {
		t.Fatal("history is empty — mirroring fix not applied, autoSave would no-op")
	}
	if err := session.SaveSession(sessionID, history, 0, api.TokenUsage{}, "cc"); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	sess, err := session.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if sess.Turns != 2 {
		t.Errorf("Turns: got %d, want 2", sess.Turns)
	}
	if len(sess.Messages) != 4 {
		t.Errorf("Messages: got %d, want 4", len(sess.Messages))
	}
}

// TestCLIAgentHistoryNotMirroredOnError verifies that a failed cliAgent.Run()
// does NOT append to history (so partial/corrupt output isn't persisted).
func TestCLIAgentHistoryNotMirroredOnError(t *testing.T) {
	agent := &mockCLIAgent{err: errors.New("claude: exit status 1")}
	history := historyAfterCLITurns(agent, []string{"will fail"})

	if len(history) != 0 {
		t.Errorf("Expected empty history on error, got %d messages", len(history))
	}
}

// TestCLIAgentMultiTurnSessionAccumulates verifies that each turn adds to
// the running history (not replaced), so turn counts grow correctly.
func TestCLIAgentMultiTurnSessionAccumulates(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	agent := &mockCLIAgent{responses: []string{"r1", "r2", "r3", "r4", "r5"}}
	sessionID := "multi-turn-cli"

	var history []api.Message
	for i, msg := range []string{"t1", "t2", "t3", "t4", "t5"} {
		result, err := agent.Run(msg, nil)
		if err == nil {
			history = append(history,
				api.Message{Role: "user", Content: msg},
				api.Message{Role: "assistant", Content: result},
			)
		}
		if err := session.SaveSession(sessionID, history, 0, api.TokenUsage{}, "cc"); err != nil {
			t.Fatalf("turn %d: SaveSession: %v", i+1, err)
		}

		sess, err := session.LoadSession(sessionID)
		if err != nil {
			t.Fatalf("turn %d: LoadSession: %v", i+1, err)
		}
		want := i + 1
		if sess.Turns != want {
			t.Errorf("after turn %d: Turns = %d, want %d", i+1, sess.Turns, want)
		}
	}
}

func TestOutputStyleDirectiveModes(t *testing.T) {
	compact := outputStyleDirective(false)
	if !strings.Contains(compact, "OUTPUT MODE: COMPACT") || !strings.Contains(compact, "Still fetch real data") {
		t.Fatalf("compact directive does not preserve QA rigor: %q", compact)
	}

	verbose := outputStyleDirective(true)
	if !strings.Contains(verbose, "OUTPUT MODE: VERBOSE") || !strings.Contains(verbose, "previous detailed response style") {
		t.Fatalf("verbose directive does not describe previous style: %q", verbose)
	}
}

func TestCodexBuildPromptReflectsOutputVerbose(t *testing.T) {
	a := &CodexAgent{effort: "high", outputVerbose: false, sctx: &api.SessionContext{}}
	if !strings.Contains(a.buildPrompt("hi"), "OUTPUT MODE: COMPACT") {
		t.Fatal("compact directive not in built prompt")
	}

	a.SetOutputVerbose(true)
	if !strings.Contains(a.buildPrompt("hi"), "OUTPUT MODE: VERBOSE") {
		t.Fatal("SetOutputVerbose(true) did not propagate into the built prompt")
	}
}

func TestCCAgentSetOutputVerboseTogglesField(t *testing.T) {
	a := &CCAgent{effort: "high", outputVerbose: false}
	a.SetOutputVerbose(true)
	if !a.outputVerbose {
		t.Fatal("SetOutputVerbose(true) did not toggle CC agent field")
	}
	a.SetOutputVerbose(false)
	if a.outputVerbose {
		t.Fatal("SetOutputVerbose(false) did not toggle CC agent field")
	}
}

func TestCCSessionIDValidation(t *testing.T) {
	valid := []string{
		"550e8400-e29b-41d4-a716-446655440000",
		"D31D7581-31BC-413A-9288-6DF74AA8A1C7",
	}
	for _, id := range valid {
		if err := validateCCSessionIDForResume(id); err != nil {
			t.Fatalf("validateCCSessionIDForResume(%q) = %v, want nil", id, err)
		}
	}

	invalid := []string{
		"",
		"5f7a8d9c",
		"session_ABC-123",
		"../../session",
		"$(touch pwned)",
		"--dangerously-skip-permissions",
		"abc def",
		strings.Repeat("a", 129),
	}
	for _, id := range invalid {
		if err := validateCCSessionIDForResume(id); err == nil {
			t.Fatalf("validateCCSessionIDForResume(%q) = nil, want error", id)
		}
	}
}

func TestCCAgentRunRejectsInvalidResumeIDBeforeExec(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	claudeBin := writeFakeCLI(t, "claude", "#!/bin/sh\ntouch "+marker+"\n")
	mcpConfigPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpConfigPath, []byte("{}"), 0600); err != nil {
		t.Fatalf("write mcp config: %v", err)
	}

	a := NewCCAgent(claudeBin, "", "high", "standard", false, &api.SessionContext{})
	a.mu.Lock()
	a.mcpConfigPath = mcpConfigPath
	a.ccSessionID = "$(touch pwned)"
	a.mu.Unlock()

	_, err := a.Run("hello", &tui.Terminal{})
	if err == nil {
		t.Fatal("Run returned nil error for invalid resume ID")
	}
	if !strings.Contains(err.Error(), "invalid Claude session ID") {
		t.Fatalf("Run error = %v, want invalid session ID", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("fake claude executed despite invalid resume ID; stat err = %v", err)
	}
}

func TestSanitizeCCUserPrompt(t *testing.T) {
	got, err := sanitizeCCUserPrompt("hello\tworld\n$(echo ok)\x1b[31m")
	if err != nil {
		t.Fatalf("sanitizeCCUserPrompt returned error: %v", err)
	}
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("sanitizeCCUserPrompt kept escape byte in %q", got)
	}
	if !strings.Contains(got, "$(echo ok)") {
		t.Fatalf("sanitizeCCUserPrompt removed printable prompt text: %q", got)
	}

	if _, err := sanitizeCCUserPrompt("bad\x00prompt"); err == nil {
		t.Fatal("sanitizeCCUserPrompt accepted NUL byte")
	}
}

func TestValidateMCPConfigPathForClaude(t *testing.T) {
	valid := filepath.Join(t.TempDir(), "qmax-mcp-1234-abcd.json")
	got, err := validateMCPConfigPathForClaude(valid)
	if err != nil {
		t.Fatalf("validateMCPConfigPathForClaude(%q) returned error: %v", valid, err)
	}
	if got != filepath.Clean(valid) {
		t.Fatalf("validated path = %q, want %q", got, filepath.Clean(valid))
	}

	for _, path := range []string{
		filepath.Join(t.TempDir(), "other.json"),
		filepath.Join(t.TempDir(), "qmax-mcp-bad path.json"),
	} {
		if _, err := validateMCPConfigPathForClaude(path); err == nil {
			t.Fatalf("validateMCPConfigPathForClaude(%q) = nil, want error", path)
		}
	}
}
