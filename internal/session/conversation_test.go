package session

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestDurableSessionIsAtomicPrivateAndRetained(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	history := []api.Message{{Role: "user", Content: "retain this requirement"}}
	state := api.ConversationState{Transcript: history, Native: map[string]api.NativeConversation{"cc": {ID: "test-session", Directory: t.TempDir(), Cursor: 1}}}
	if err := SaveSession("durable", history, 0, api.TokenUsage{}, "model", state); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(sessionFilePath("durable"))
	if err != nil {
		t.Fatal(err)
	}
	var first Session
	if err := json.Unmarshal(original, &first); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sessionFilePath("durable"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("session must be owner-only")
	}
	// An unencodable update must never destroy the previous checkpoint.
	invalid := []api.Message{{Role: "user", Content: make(chan int)}}
	if err := SaveSession("durable", invalid, 0, api.TokenUsage{}, "", state); err == nil {
		t.Fatal("expected encoding error")
	}
	after, err := os.ReadFile(sessionFilePath("durable"))
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("failed save changed existing session")
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(sessionFilePath("durable"), old, old); err != nil {
		t.Fatal(err)
	}
	if CleanupOldSessions() != 0 {
		t.Fatal("durable conversation expired")
	}
	if err := SaveSession("durable", history, 0, api.TokenUsage{}, "new-model", state); err != nil {
		t.Fatal(err)
	}
	restored, err := LoadSession("durable")
	if err != nil || !restored.CreatedAt.Equal(first.CreatedAt) || restored.Conversation.Native["cc"].Cursor != 1 {
		t.Fatal("checkpoint metadata did not survive saving")
	}
}

func TestConversationRedactionDoesNotMutateLiveToolInput(t *testing.T) {
	input := map[string]any{"password": "synthetic-sensitive-value", "path": "go.mod"}
	history := []api.Message{{Role: "assistant", Content: []api.ContentBlock{{Type: "tool_use", ID: "call-1", Name: "example", Input: input}}}}
	t.Setenv("HOME", t.TempDir())
	state := api.ConversationState{Transcript: append(history, api.Message{Role: "assistant", Content: `{"access_token":"synthetic-sensitive-value","output":"useful result"}`})}
	if err := SaveSession("redacted", history, 0, api.TokenUsage{}, "", state); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sessionFilePath("redacted"))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) || strings.Contains(string(data), "synthetic-sensitive-value") || !strings.Contains(string(data), "useful result") {
		t.Fatal("redaction leaked a credential field or lost ordinary content")
	}
	if input["password"] != "synthetic-sensitive-value" {
		t.Fatal("save mutated live input")
	}
}

func TestRetainedRedactionKeepsOrdinaryCodeAndProse(t *testing.T) {
	// Retained content is replayed as the conversation itself, so a false
	// positive here silently corrupts the only copy of the user's context.
	for _, keep := range []string{
		"token := lexer.Next()",
		"const token = parseHeader(req)",
		"install qm-code from the qm-cli repo",
		"apiKey: config.Value",
		"password = os.Getenv(name)",
	} {
		data, err := MarshalRedacted(keep)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "REDACTED") {
			t.Fatalf("redaction corrupted ordinary content: %s -> %s", keep, data)
		}
	}
	for _, drop := range []string{
		"use sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAA now",
		"Authorization: Bearer AAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"api_key=AAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"postgres://user:hunter2@db.example.com:5432/app",
	} {
		data, err := MarshalRedacted(drop)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "REDACTED") {
			t.Fatalf("credential survived redaction: %s -> %s", drop, data)
		}
	}
}

func TestTurnCountIgnoresToolTraffic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	transcript := []api.Message{{Role: "user", Content: "fix the bug"}}
	for i := 0; i < 5; i++ {
		transcript = append(transcript,
			api.Message{Role: "assistant", Content: []api.ContentBlock{{Type: "tool_use", ID: "call", Name: "read"}}},
			api.Message{Role: "user", Content: []api.ContentBlock{{Type: "tool_result", ToolUseID: "call", Content: "ok"}}})
	}
	transcript = append(transcript, api.Message{Role: "assistant", Content: "done"})
	if err := SaveSession("turns", transcript, 0, api.TokenUsage{}, "m", api.ConversationState{Transcript: transcript}); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadSession("turns")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Turns != 1 {
		t.Fatalf("one prompt with five tool calls reported %d turns", saved.Turns)
	}
}

func TestDurableSessionsExpireEventually(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	history := []api.Message{{Role: "user", Content: "old work"}}
	state := api.ConversationState{Transcript: history}
	if err := SaveSession("ancient", history, 0, api.TokenUsage{}, "", state); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-durableSessionTTL - 24*time.Hour)
	if err := os.Chtimes(sessionFilePath("ancient"), old, old); err != nil {
		t.Fatal(err)
	}
	if CleanupOldSessions() != 1 {
		t.Fatal("portable transcripts never expire — disk growth is unbounded")
	}
}

func TestSessionFileDoesNotStoreHistoryTwice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	body := strings.Repeat("duplicated requirement detail ", 200)
	history := []api.Message{{Role: "user", Content: body}}
	if err := SaveSession("dedup", history, 0, api.TokenUsage{}, "", api.ConversationState{Transcript: history}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sessionFilePath("dedup"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "duplicated requirement detail") != 200 {
		t.Fatal("session stored the same conversation in both messages and transcript")
	}
	saved, err := LoadSession("dedup")
	if err != nil || len(saved.Messages) != 1 {
		t.Fatal("Messages was not rehydrated from the transcript for existing callers")
	}
}

func TestDurableDetectionIsIndependentOfKeyOffsetAndEscaping(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// MarshalRedacted encodes through map[string]any, so keys are emitted in
	// sorted order: the native checkpoints precede the transcript. A handful of
	// backends pushes the transcript key well past any small fixed-size head
	// read, and misclassifying here expires a durable session 83 days early.
	native := map[string]api.NativeConversation{}
	for _, backend := range []string{"cc", "codex", "agy", "opencode"} {
		native[backend] = api.NativeConversation{
			ID:          strings.Repeat("a", 36),
			Model:       "claude-opus-4-5-20260101",
			RolloutPath: "/Users/someone/.codex/sessions/2026/09/08/" + strings.Repeat("r", 40) + ".jsonl",
			Directory:   "/Users/someone/conductor/workspaces/qmax-code/" + strings.Repeat("w", 40),
			Cursor:      12,
		}
	}
	history := []api.Message{{Role: "user", Content: "real work"}}
	if err := SaveSession("offset", history, 0, api.TokenUsage{}, "", api.ConversationState{Transcript: history, Native: native}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(sessionFilePath("offset"))
	if err != nil {
		t.Fatal(err)
	}
	if at := bytes.Index(raw, []byte(`"transcript":`)); at < 512 {
		t.Fatalf("fixture no longer exercises a distant key (offset %d)", at)
	}
	if !hasDurableTranscript(sessionFilePath("offset")) {
		t.Fatal("durable session misclassified as legacy because of the key's offset")
	}

	// A pasted JSON literal in message content is escaped, so it must not be
	// mistaken for the real key in either direction.
	pasted := []api.Message{{Role: "user", Content: `look at {"transcript":null} and {"transcript":[1]}`}}
	if err := SaveSession("pasted", pasted, 0, api.TokenUsage{}, "", api.ConversationState{Transcript: pasted}); err != nil {
		t.Fatal(err)
	}
	if !hasDurableTranscript(sessionFilePath("pasted")) {
		t.Fatal("escaped content in a message was read as the transcript key")
	}
	if err := SaveSession("legacy", pasted, 0, api.TokenUsage{}, "", api.ConversationState{}); err != nil {
		t.Fatal(err)
	}
	if hasDurableTranscript(sessionFilePath("legacy")) {
		t.Fatal("session without a transcript was treated as durable")
	}
}
