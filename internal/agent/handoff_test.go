package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/session"
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

func TestHandoffCheckpointAccompaniesNativeResumeWithNoMissingEntries(t *testing.T) {
	home := withTempHome(t)
	promptPath := filepath.Join(home, "prompt.txt")
	t.Setenv("QMAX_TEST_PROMPT_PATH", promptPath)
	bin := writeFakeCLI(t, "handoff-native", `#!/bin/sh
cat > "$QMAX_TEST_PROMPT_PATH"
printf '%s\n' '{"type":"thread.started","thread_id":"abcdef12-3456-4abc-8def-1234567890ab"}' '{"type":"item.completed","item":{"type":"agent_message","text":"continue investigation"}}'
`)
	a := &Agent{}
	if err := a.RejectApproach("Increase the timeout", "60s still fails in TestReconnect"); err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{"investigate", "continue"} {
		cli := NewCodexAgent(bin, "gpt-6-astra", "high", false, "", &api.SessionContext{})
		if _, err := a.RunCLI(cli, request, nil); err != nil {
			t.Fatal(err)
		}
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(prompt), "conversation_history") {
		t.Fatal("native resume should not replay entries it already saw")
	}
	if !strings.Contains(string(prompt), "60s still fails in TestReconnect") {
		t.Fatal("native resume omitted the current checkpoint")
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
