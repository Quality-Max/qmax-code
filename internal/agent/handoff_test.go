package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/session"
	"github.com/qualitymax/qmax-code/internal/tui"
)

func TestBugFixHandoffKeepsRejectedApproachVisibleAfterCompactionAndRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &Agent{}
	t.Cleanup(a.CleanupConversation)
	first := &conversationSpy{fail: true}
	if _, err := a.RunCLI(first, "fix reconnect without changing the public API", nil); err == nil {
		t.Fatal("expected interrupted attempt")
	}
	if a.Conversation.Handoff != nil {
		t.Fatal("a failed turn was automatically classified as a rejected approach")
	}
	const approach = "Increase the reconnect timeout"
	const evidence = "TestReconnect fails at both 5s and 60s; the connection is never reopened"
	if err := a.RejectApproach(approach, evidence); err != nil {
		t.Fatal(err)
	}
	// Bury the decision deeply enough that its ordinary transcript entry loses
	// the evidence in the inline handoff and the built-in working summary.
	for i := 0; i < 50; i++ {
		a.AppendHistory(api.Message{Role: "assistant", Content: strings.Repeat("later investigation ", 300)})
	}
	a.compressHistory()
	if len(a.History) >= len(a.Conversation.Transcript) {
		t.Fatal("test did not compact the working history")
	}
	if err := session.SaveSession("bug-handoff", a.History, 0, a.Usage, "cc", a.Conversation); err != nil {
		t.Fatal(err)
	}
	saved, err := session.LoadSession("bug-handoff")
	if err != nil {
		t.Fatal(err)
	}
	restored := &Agent{Cfg: AgentConfig{Context: &api.SessionContext{LocalOnly: true}}}
	t.Cleanup(restored.CleanupConversation)
	restored.RestoreConversation(saved.Messages, saved.Conversation)
	next := &conversationSpy{}
	if _, err := restored.RunCLI(next, "continue the fix on another backend", nil); err != nil {
		t.Fatal(err)
	}
	checkpoint, _, found := strings.Cut(next.prompt, "The complete retained conversation")
	if !found || !strings.Contains(checkpoint, approach) || !strings.Contains(checkpoint, evidence) {
		t.Fatal("the destination must receive the full rejected approach and evidence before abbreviated history")
	}
	if len(next.prompt) > maxInlineHandoffBytes {
		t.Fatal("checkpoint broke the inline handoff budget")
	}
	if !strings.Contains(restored.buildSystemPrompt(), evidence) {
		t.Fatal("switching to a built-in provider lost the rejection evidence")
	}
	if !strings.Contains(next.prompt, "explain what changed before retrying") {
		t.Fatal("destination was not instructed to justify revisiting a rejected approach")
	}
}

func TestNativeResumeReceivesCheckpointOnlyWhenNeeded(t *testing.T) {
	home := withTempHome(t)
	promptPath := filepath.Join(home, "prompt.txt")
	t.Setenv("QMAX_TEST_PROMPT_PATH", promptPath)
	bin := writeFakeCLI(t, "handoff-native", `#!/bin/sh
cat > "$QMAX_TEST_PROMPT_PATH"
printf '%s\n' '{"type":"thread.started","thread_id":"abcdef12-3456-4abc-8def-1234567890ab"}' '{"type":"item.completed","item":{"type":"agent_message","text":"continue investigation"}}'
`)
	a := &Agent{}
	run := func(cli CLIAgent, request string) string {
		t.Helper()
		if _, err := a.RunCLI(cli, request, nil); err != nil {
			t.Fatal(err)
		}
		if _, ok := cli.(*conversationSpy); ok {
			return cli.(*conversationSpy).prompt
		}
		prompt, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(prompt)
	}
	codex := func() CLIAgent {
		return NewCodexAgent(bin, "gpt-6-astra", "high", false, "", &api.SessionContext{})
	}
	if err := a.RejectApproach("Increase the timeout", "60s still fails in TestReconnect"); err != nil {
		t.Fatal(err)
	}
	if got := run(codex(), "investigate"); !strings.Contains(got, "60s still fails in TestReconnect") {
		t.Fatal("a new native session must receive the checkpoint")
	}
	if got := run(codex(), "continue"); strings.Contains(got, "Handoff checkpoint") || strings.Contains(got, "conversation_history") {
		t.Fatal("an unchanged checkpoint was repeated into the native session's history")
	}
	if err := a.RejectApproach("Retry the dial", "the socket is closed before the retry"); err != nil {
		t.Fatal(err)
	}
	if got := run(codex(), "continue"); !strings.Contains(got, "60s still fails") || !strings.Contains(got, "socket is closed") {
		t.Fatal("a changed checkpoint must be resent in full")
	}
	// Switching away and back transfers missed entries, so the checkpoint
	// travels with them even though this native session saw it before.
	if got := run(&conversationSpy{}, "try elsewhere"); !strings.Contains(got, "socket is closed") {
		t.Fatal("the other backend did not receive the checkpoint")
	}
	if got := run(codex(), "back again"); !strings.Contains(got, "socket is closed") {
		t.Fatal("returning to a backend must resend the checkpoint with the transferred entries")
	}
	if a.Conversation.Native["codex"].Handoff != a.handoffDigest() {
		t.Fatal("the delivered checkpoint was not recorded for restart")
	}
}

func TestHandoffSwitchHintSuggestsWithoutRecording(t *testing.T) {
	recorded := func(content any) api.Message {
		var transcript TurnTranscript
		transcript.record("assistant", content)
		return transcript.take()[0]
	}
	codexItem := func(item string) api.Message { return recorded(item) }
	for _, tc := range []struct {
		name    string
		history []api.Message
		want    string
	}{
		{"no work", nil, ""},
		{"successful work", []api.Message{{Role: "assistant", Content: "done"}}, "checkpoint. If"},
		{"interrupted turn", []api.Message{{Role: "assistant", Content: interruptedTurnMarker}}, "(1 interrupted turn)"},
		{"codex exit code", []api.Message{codexItem(`{"type":"command_execution","exit_code":1,"status":"failed"}`)}, "(1 failed tool call)"},
		{"codex success", []api.Message{codexItem(`{"type":"command_execution","exit_code":0,"status":"completed"}`)}, "checkpoint. If"},
		{"opencode error", []api.Message{recorded(map[string]string{"type": "tool", "state": "error"})}, "(1 failed tool call)"},
		{"antigravity error", []api.Message{recorded(map[string]any{"name": "run", "error": map[string]string{"type": "exit"}})}, "(1 failed tool call)"},
		{"built-in error", []api.Message{
			{Role: "assistant", Content: []api.ContentBlock{{Type: "tool_use", ID: "1", Name: "run_command"}}},
			{Role: "user", Content: []api.ContentBlock{{Type: "tool_result", ToolUseID: "1", Content: `{"error": "exit status 1"}`}}},
			{Role: "assistant", Content: interruptedTurnMarker},
		}, "(1 interrupted turn, 1 failed tool call)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{}
			a.AppendHistory(tc.history...)
			got := a.HandoffSwitchHint()
			if tc.want == "" {
				if got != "" {
					t.Fatalf("unexpected hint %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) || !strings.Contains(got, "/handoff reject") {
				t.Fatalf("hint %q does not contain %q", got, tc.want)
			}
			if a.Conversation.Handoff != nil {
				t.Fatal("the hint recorded a rejection without the user")
			}
		})
	}
}

func TestHandoffSwitchHintCountsClaudeToolErrorsSinceLastCheckpoint(t *testing.T) {
	cli := &CCAgent{}
	cli.parseStream(strings.NewReader(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"FAIL TestReconnect","is_error":true}]}}`+"\n"), &tui.Terminal{})
	a := &Agent{}
	a.AppendHistory(cli.take()...)
	if got := a.HandoffSwitchHint(); !strings.Contains(got, "1 failed tool call") {
		t.Fatalf("Claude Code tool error was not counted: %q", got)
	}
	if err := a.RejectApproach("Increase the timeout", "FAIL TestReconnect"); err != nil {
		t.Fatal(err)
	}
	if got := a.HandoffSwitchHint(); got != "" {
		t.Fatalf("work already covered by the checkpoint produced a hint: %q", got)
	}
}

func TestHandoffValidationDoesNotChangeState(t *testing.T) {
	for _, tc := range []struct {
		name, approach, evidence string
	}{
		{"empty approach", " ", "test still fails"},
		{"empty evidence", "increase timeout", " "},
		{"too large", "increase timeout", strings.Repeat("e", maxHandoffNotesBytes)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{}
			if err := a.RejectApproach(tc.approach, tc.evidence); err == nil {
				t.Fatal("invalid note accepted")
			}
			if a.Conversation.Handoff != nil || len(a.History) != 0 {
				t.Fatal("invalid note mutated the session")
			}
		})
	}
	a := &Agent{}
	if err := a.RejectApproach("increase timeout", "test still fails"); err != nil {
		t.Fatal(err)
	}
	if err := a.RejectApproach(" increase timeout ", "test still fails "); err == nil {
		t.Fatal("duplicate note accepted")
	}
	if err := a.RejectApproach("another approach", strings.Repeat("e", maxHandoffNotesBytes)); err == nil {
		t.Fatal("combined notes exceeded budget")
	}
	if len(a.Conversation.Handoff.RejectedApproaches) != 1 || len(a.History) != 1 {
		t.Fatal("rejected addition modified existing notes or history")
	}
}

func TestForgetHandoffSupersedesOldNotesAndClearResetsCheckpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &Agent{}
	if err := a.RejectApproach("increase timeout", "test still fails"); err != nil {
		t.Fatal(err)
	}
	for _, number := range []int{-1, 0, 2} {
		if err := a.ForgetRejectedApproach(number); err == nil {
			t.Fatal("invalid entry removed a rejection")
		}
	}
	if err := a.ForgetRejectedApproach(1); err != nil {
		t.Fatal(err)
	}
	if err := session.SaveSession("forgotten", a.History, 0, a.Usage, "", a.Conversation); err != nil {
		t.Fatal(err)
	}
	saved, err := session.LoadSession("forgotten")
	if err != nil {
		t.Fatal(err)
	}
	a.RestoreConversation(saved.Messages, saved.Conversation)
	if got := a.HandoffContext(); !strings.Contains(got, "No active rejected approaches") || strings.Contains(got, "increase timeout") {
		t.Fatal("empty checkpoint must survive restart and supersede older notes")
	}
	if !strings.Contains(a.History[len(a.History)-1].Content.(string), "decision removed") {
		t.Fatal("removal was not recorded in history")
	}
	a.ClearHistory()
	if a.HandoffContext() != "" || len(a.History) != 0 {
		t.Fatal("clear left handoff state behind")
	}
}

func TestHandoffRedactsEvidenceBeforeStorageAndTransfer(t *testing.T) {
	a := &Agent{}
	// Construct a synthetic credential shape, never use a real credential.
	credential := "sk-ant-" + strings.Repeat("x", 40)
	evidence := `{"api_key":"` + credential + `","output":"test failed"}`
	if err := a.RejectApproach("try another API key", evidence); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(a.Conversation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), credential) || strings.Contains(a.HandoffContext(), credential) {
		t.Fatal("credential was retained in live checkpoint or transcript")
	}
	if !strings.Contains(a.HandoffContext(), "test failed") {
		t.Fatal("redaction lost non-sensitive evidence")
	}
}

func TestHandoffInLocalAndConnectedSystemPrompts(t *testing.T) {
	for _, local := range []bool{true, false} {
		a := &Agent{Cfg: AgentConfig{Context: &api.SessionContext{LocalOnly: local}}, sessionsFetched: true}
		if err := a.RejectApproach("increase timeout", "test still fails"); err != nil {
			t.Fatal(err)
		}
		if got := a.buildSystemPrompt(); strings.Count(got, "Handoff checkpoint") != 1 || !strings.Contains(got, "test still fails") {
			t.Fatal("system prompt must include the checkpoint exactly once")
		}
	}
}
