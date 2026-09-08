package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/session"
	"github.com/qualitymax/qmax-code/internal/tui"
)

type conversationSpy struct {
	TurnTranscript
	prompt string
	fail   bool
}

func (s *conversationSpy) Run(prompt string, _ *tui.Terminal) (string, error) {
	s.prompt = prompt
	s.record("assistant", map[string]string{"tool": "read_file", "output": "exact file contents"})
	if s.fail {
		return "partial answer", errors.New("failed")
	}
	return "answer", nil
}
func (*conversationSpy) Cancel()               {}
func (*conversationSpy) SetOutputVerbose(bool) {}
func (*conversationSpy) Cleanup()              {}

func TestClaudeTranscriptDoesNotRecaptureEchoedHandoff(t *testing.T) {
	cli := &CCAgent{}
	// A zero-value Terminal renders headlessly. tui.NewTerminal() starts a
	// readline ioloop whose Close races with it under -race.
	term := &tui.Terminal{}
	cli.parseStream(strings.NewReader(`{"type":"user","message":{"content":[{"type":"text","text":"<conversation_history>earlier context</conversation_history>"}]}}`+"\n"), term)
	if len(cli.take()) != 0 {
		t.Fatal("echoed prompt duplicated the portable conversation")
	}
}

func TestConversationHandoffIncludesToolsAndPartialFailure(t *testing.T) {
	a := &Agent{History: []api.Message{{Role: "user", Content: "Keep the compatibility constraint"}}}
	first := &conversationSpy{fail: true}
	if _, err := a.RunCLI(first, "inspect", nil); err == nil {
		t.Fatal("expected failure")
	}
	second := &conversationSpy{}
	if _, err := a.RunCLI(second, "continue", nil); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"compatibility constraint", "exact file contents", "partial answer", "completion was not confirmed", "Current user request:\ncontinue"} {
		if !strings.Contains(second.prompt, text) {
			t.Fatalf("handoff missing %q", text)
		}
	}
	if strings.Contains(a.Conversation.Transcript[1].Content.(string), "conversation_history") {
		t.Fatal("stored handoff instead of original prompt")
	}
}

func TestCompactionKeepsFullDurableTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &Agent{}
	t.Cleanup(a.CleanupConversation)
	detail := strings.Repeat("requirement detail ", 30) + "critical tail"
	for i := 0; i < 25; i++ {
		a.AppendHistory(api.Message{Role: "user", Content: detail}, api.Message{Role: "assistant", Content: "ack"})
	}
	a.compressHistory()
	if len(a.History) >= 50 || len(a.Conversation.Transcript) != 50 {
		t.Fatal("compaction deleted archive or did not compact")
	}
	if err := session.SaveSession("archive", a.History, 0, a.Usage, "", a.Conversation); err != nil {
		t.Fatal(err)
	}
	saved, err := session.LoadSession("archive")
	if err != nil {
		t.Fatal(err)
	}
	restored := &Agent{}
	restored.RestoreConversation(saved.Messages, saved.Conversation)
	cli := &conversationSpy{}
	if _, err := restored.RunCLI(cli, "continue", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Count(cli.prompt, "critical tail") != 25 {
		t.Fatal("restart handoff lost archived details")
	}
	restored.ClearHistory()
	if len(restored.Conversation.Transcript) != 0 || len(restored.Conversation.Native) != 0 {
		t.Fatal("clear retained old context")
	}
}

func TestNativeConversationSurvivesModelEffortSwitchAndRestart(t *testing.T) {
	home := withTempHome(t)
	promptPath := filepath.Join(home, "prompt.txt")
	t.Setenv("QMAX_TEST_PROMPT_PATH", promptPath)
	bin := writeFakeCLI(t, "continuity-switch", `#!/bin/sh
cat > "$QMAX_TEST_PROMPT_PATH"
printf '%s\n' '{"type":"thread.started","thread_id":"abcdef12-3456-4abc-8def-1234567890ab"}' '{"type":"item.completed","item":{"type":"command_execution","command":"go test ./...","aggregated_output":"all tests passed","exit_code":0}}' '{"type":"item.completed","item":{"type":"agent_message","text":"first result"}}'
`)
	a := &Agent{}
	first := NewCodexAgent(bin, "gpt-6-astra", "high", false, &api.SessionContext{})
	if _, err := a.RunCLI(first, "original requirement", nil); err != nil {
		t.Fatal(err)
	}
	next := NewCodexAgent(bin, "gpt-6-astra", "low", false, &api.SessionContext{})
	if _, err := a.RunCLI(next, "follow up", nil); err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(prompt), "conversation_history") {
		t.Fatal("effort switch replayed an already-seen transcript")
	}
	if next.getContinuity().Checkpoint().ThreadID != firstAdapterThreadID {
		t.Fatal("effort switch dropped native thread")
	}
	// An embedded provider adds a turn while Codex is inactive.
	a.AppendHistory(api.Message{Role: "user", Content: "other provider request"}, api.Message{Role: "assistant", Content: "other provider decision"})
	if err := session.SaveSession("switch", a.History, 0, a.Usage, "", a.Conversation); err != nil {
		t.Fatal(err)
	}
	saved, err := session.LoadSession("switch")
	if err != nil {
		t.Fatal(err)
	}
	restored := &Agent{}
	restored.RestoreConversation(saved.Messages, saved.Conversation)
	changed := NewCodexAgent(bin, "gpt-5.4", "medium", false, &api.SessionContext{})
	if _, err := restored.RunCLI(changed, "finish", nil); err != nil {
		t.Fatal(err)
	}
	prompt, err = os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), "other provider decision") || strings.Contains(string(prompt), "original requirement") {
		t.Fatal("switch-back did not transfer only missing context")
	}
	if changed.getContinuity().Checkpoint().Model != "gpt-5.4" {
		t.Fatal("model change was ignored")
	}
	other := &conversationSpy{}
	if _, err := restored.RunCLI(other, "review", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(other.prompt, "all tests passed") {
		t.Fatal("Codex tool output was not transferred")
	}
}

func TestNativeRestoreAndResetAllBackends(t *testing.T) {
	cases := []struct {
		name string
		cli  CLIAgent
		id   string
	}{
		{"cc", NewCCAgent("unused", "", "high", "standard", false, nil), firstAdapterThreadID},
		{"codex", NewCodexAgent("unused", "", "high", false, nil), firstAdapterThreadID},
		{"agy", NewAgyAgent("unused", "", "high", "standard", false, nil), firstAdapterThreadID},
		{"opencode", NewOpenCodeAgent("unused", "", "high", "standard", false, nil, nil), "ses_abc123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := restoreCLINative(tc.cli, api.NativeConversation{ID: tc.id}); err != nil {
				t.Fatal(err)
			}
			_, state := cliNative(tc.cli)
			if state.ID != tc.id {
				t.Fatal("native ID lost")
			}
			tc.cli.(ConversationResetter).ResetConversation()
			_, state = cliNative(tc.cli)
			if state.ID != "" {
				t.Fatal("clear retained native ID")
			}
		})
	}
}

func TestOpenCodeNestedToolOutputSurvivesParsing(t *testing.T) {
	var event ocEvent
	err := json.Unmarshal([]byte(`{"type":"tool_use","part":{"id":"tool1","type":"tool","tool":"read","state":{"status":"completed","input":{"filePath":"sample.go"},"output":"file content"}}}`), &event)
	if err != nil {
		t.Fatal(err)
	}
	if event.Part.State != "completed" || event.Part.Output != "file content" || !strings.Contains(string(event.Part.Input), "sample.go") {
		t.Fatal("nested tool state lost")
	}
}

func TestLargeHandoffRetainsSearchableArchiveAcrossSwitches(t *testing.T) {
	a := &Agent{}
	t.Cleanup(a.CleanupConversation)
	old := strings.Repeat("earlier detailed requirement ", 4000) + "exact-old-tail"
	a.AppendHistory(api.Message{Role: "user", Content: old}, api.Message{Role: "assistant", Content: "recent decision"})
	cli := &conversationSpy{}
	if _, err := a.RunCLI(cli, "continue", nil); err != nil {
		t.Fatal(err)
	}
	if len(cli.prompt) >= maxInlineHandoffBytes+1024 || !strings.Contains(cli.prompt, a.contextArchive) {
		t.Fatal("large handoff was not bounded with an archive reference")
	}
	path := a.contextArchive
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "exact-old-tail") {
		t.Fatal("archive lost old details")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("archive is not owner-only")
	}
	// A native resume with no missing messages still refreshes its file reference.
	prompt, err := a.prepareHandoff(nil, "next")
	if err != nil || !strings.Contains(prompt, path) || a.contextArchive != path {
		t.Fatal("native resume lost the stable archive reference")
	}
	data, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "exact file contents") {
		t.Fatal("archive was not refreshed with intervening tool content")
	}
	a.ClearHistory()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("clear left the temporary archive on disk")
	}
}

func TestCLIContextIsUsableByBuiltInProvider(t *testing.T) {
	a := &Agent{}
	if _, err := a.RunCLI(&conversationSpy{}, "keep this requirement", nil); err != nil {
		t.Fatal(err)
	}
	messages := historyToOpenAI("QA system prompt", a.History)
	encoded, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"keep this requirement", "exact file contents", "answer"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("built-in request lost %q from CLI history", want)
		}
	}
}

func TestCompactionAfterRestoreKeepsToolPairsAndArchive(t *testing.T) {
	a := &Agent{}
	t.Cleanup(a.CleanupConversation)
	for i := 0; i < 36; i++ {
		a.AppendHistory(api.Message{Role: "user", Content: "earlier"})
	}
	a.AppendHistory(api.Message{Role: "assistant", Content: []api.ContentBlock{{Type: "tool_use", ID: "call-1", Name: "read_file", Input: map[string]any{"path": "go.mod"}}}})
	a.AppendHistory(api.Message{Role: "user", Content: []api.ContentBlock{{Type: "tool_result", ToolUseID: "call-1", Content: "full tool result"}}})
	for i := 0; i < 5; i++ {
		a.AppendHistory(api.Message{Role: "assistant", Content: "later"})
	}
	data, _ := json.Marshal(a.Conversation)
	var state api.ConversationState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	a.RestoreConversation(nil, state)
	a.compressHistory()
	blocks, _, _ := normalizeContent(a.History[2].Content)
	if len(blocks) > 0 && blocks[0].Type == "tool_result" {
		t.Fatal("deserialized tool result became orphaned at the compaction boundary")
	}
	archive, err := os.ReadFile(a.contextArchive)
	if err != nil || !strings.Contains(string(archive), "full tool result") {
		t.Fatal("compaction lost tool output from archive")
	}
}

func TestHandoffRepresentsEveryUndeliveredMessage(t *testing.T) {
	// Once an archive exists the handoff is condensed, but the native cursor
	// advances past everything it covered — so nothing may be silently absent.
	a := &Agent{}
	t.Cleanup(a.CleanupConversation)
	a.Conversation.Transcript = []api.Message{{Role: "user", Content: strings.Repeat("x", 60*1024)}}
	if _, err := a.prepareHandoff(a.Conversation.Transcript, "go"); err != nil {
		t.Fatal(err)
	}
	if a.contextArchive == "" {
		t.Fatal("expected an archive after an oversized handoff")
	}

	messages := make([]api.Message, 40)
	for i := range messages {
		messages[i] = api.Message{Role: "user", Content: fmt.Sprintf("decision-marker-%02d %s", i, strings.Repeat("detail ", 400))}
	}
	a.Conversation.Transcript = messages
	handoff, err := a.prepareHandoff(messages, "next")
	if err != nil {
		t.Fatal(err)
	}
	if len(handoff) > maxInlineHandoffBytes {
		t.Fatalf("handoff exceeded the inline budget: %d bytes", len(handoff))
	}
	for i := range messages {
		if !strings.Contains(handoff, fmt.Sprintf("decision-marker-%02d", i)) {
			t.Fatalf("undelivered message %d was dropped from the handoff; the cursor would skip it forever", i)
		}
	}
	if !strings.Contains(handoff, handoffTruncationMarker) {
		t.Fatal("bodies were not abbreviated, so the budget was met some other way")
	}
}

func TestCompactionSurvivesUnwritableTempDir(t *testing.T) {
	// The archive is an optimization; a read-only temp dir must not fail turns.
	blocked := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", blocked)
	a := &Agent{}
	t.Cleanup(a.CleanupConversation)
	detail := strings.Repeat("requirement detail ", 40)
	for i := 0; i < 25; i++ {
		a.AppendHistory(api.Message{Role: "user", Content: detail}, api.Message{Role: "assistant", Content: "ack"})
	}
	a.compressHistory()
	if len(a.History) >= 50 {
		t.Fatal("compaction did not run when the archive was unavailable")
	}
	if len(a.Conversation.Transcript) != 50 {
		t.Fatal("compaction lost the durable transcript")
	}
	cli := &conversationSpy{}
	if _, err := a.RunCLI(cli, "continue", nil); err != nil {
		t.Fatalf("an unwritable temp dir failed the turn: %v", err)
	}
	if !strings.Contains(cli.prompt, "requirement detail") {
		t.Fatal("handoff lost context when the archive was unavailable")
	}
}

func TestClaudeTranscriptRecordsToolsOnceAsAssistantActivity(t *testing.T) {
	cli := &CCAgent{}
	term := &tui.Terminal{}
	stream := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"path":"go.mod"}}]}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"module x"}]}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"THE ANSWER"}]}}` + "\n" +
		`{"type":"result","result":"THE ANSWER"}` + "\n"
	response := cli.parseStream(strings.NewReader(stream), term)
	records := cli.take()
	for _, rec := range records {
		if rec.Role != "assistant" {
			t.Fatalf("CLI tool activity recorded as %q, which inflates the session turn count", rec.Role)
		}
		if strings.Contains(fmt.Sprint(rec.Content), "THE ANSWER") {
			t.Fatal("assistant text recorded alongside the result event — every reply would be stored twice")
		}
	}
	if len(records) != 2 || response != "THE ANSWER" {
		t.Fatalf("expected the tool_use/tool_result pair plus one response, got %d records and %q", len(records), response)
	}
}

func TestOpenCodeNestedStateKeepsTopLevelToolInput(t *testing.T) {
	var part ocPart
	raw := `{"type":"tool","tool":"read","input":{"filePath":"sample.go"},"state":{"status":"completed","output":"done"}}`
	if err := json.Unmarshal([]byte(raw), &part); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(part.Input), "sample.go") {
		t.Fatal("nested state clobbered the top-level tool input used for file snapshots")
	}
	if part.State != "completed" || part.Output != "done" {
		t.Fatal("nested tool state lost")
	}
}
