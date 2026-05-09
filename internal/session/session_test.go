package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestGenerateSessionID(t *testing.T) {
	id1 := GenerateSessionID()
	id2 := GenerateSessionID()
	if id1 == id2 {
		t.Error("Session IDs should be unique")
	}
	if len(id1) != 8 {
		t.Errorf("Session ID should be 8 hex chars, got %d: %s", len(id1), id1)
	}
}

func TestSaveAndLoadSession(t *testing.T) {
	// Use temp dir
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	history := []api.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	usage := api.TokenUsage{InputTokens: 100, OutputTokens: 50, Requests: 1}

	err := SaveSession("test123", history, 42, usage, "sonnet")
	if err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	session, err := LoadSession("test123")
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	if session.ID != "test123" {
		t.Errorf("ID: got %s, want test123", session.ID)
	}
	if session.ProjectID != 42 {
		t.Errorf("ProjectID: got %d, want 42", session.ProjectID)
	}
	if session.Usage.InputTokens != 100 {
		t.Errorf("InputTokens: got %d, want 100", session.Usage.InputTokens)
	}
	if session.Turns != 1 {
		t.Errorf("Turns: got %d, want 1", session.Turns)
	}
}

func TestListSessions(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create 3 sessions
	_ = SaveSession("aaa", []api.Message{{Role: "user", Content: "1"}}, 1, api.TokenUsage{}, "sonnet")
	time.Sleep(10 * time.Millisecond)
	_ = SaveSession("bbb", []api.Message{{Role: "user", Content: "2"}}, 2, api.TokenUsage{}, "sonnet")
	time.Sleep(10 * time.Millisecond)
	_ = SaveSession("ccc", []api.Message{{Role: "user", Content: "3"}}, 3, api.TokenUsage{}, "sonnet")

	sessions, err := ListSessions(10)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("Expected 3 sessions, got %d", len(sessions))
	}
	// Should be sorted newest first
	if sessions[0].ID != "ccc" {
		t.Errorf("First session should be ccc (newest), got %s", sessions[0].ID)
	}
}

func TestListSessions_Limit(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	_ = SaveSession("a1", []api.Message{{Role: "user", Content: "1"}}, 0, api.TokenUsage{}, "")
	_ = SaveSession("a2", []api.Message{{Role: "user", Content: "2"}}, 0, api.TokenUsage{}, "")
	_ = SaveSession("a3", []api.Message{{Role: "user", Content: "3"}}, 0, api.TokenUsage{}, "")

	sessions, err := ListSessions(2)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("Expected 2 sessions (limited), got %d", len(sessions))
	}
}

func TestCleanupOldSessions(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create sessions dir
	dir := filepath.Join(tmpDir, ".qmax-code", "sessions")
	_ = os.MkdirAll(dir, 0700)

	// Create an old file (modify time > 7 days ago)
	oldFile := filepath.Join(dir, "old123.json")
	_ = os.WriteFile(oldFile, []byte(`{"id":"old123"}`), 0600)
	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	_ = os.Chtimes(oldFile, oldTime, oldTime)

	// Create a recent file
	newFile := filepath.Join(dir, "new456.json")
	_ = os.WriteFile(newFile, []byte(`{"id":"new456"}`), 0600)

	removed := CleanupOldSessions()
	if removed != 1 {
		t.Errorf("Expected 1 removed, got %d", removed)
	}

	// Old should be gone, new should remain
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("Old file should have been removed")
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Error("New file should still exist")
	}
}

func TestLoadLastSession(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	_ = SaveSession("first", []api.Message{{Role: "user", Content: "old"}}, 1, api.TokenUsage{}, "")
	time.Sleep(10 * time.Millisecond)
	_ = SaveSession("second", []api.Message{{Role: "user", Content: "new"}}, 2, api.TokenUsage{}, "")

	session, err := LoadLastSession()
	if err != nil {
		t.Fatalf("LoadLastSession failed: %v", err)
	}
	if session.ID != "second" {
		t.Errorf("Should load most recent session, got %s", session.ID)
	}
}

// ---- sessionSummary ----

func TestSessionSummary_EmptyHistory(t *testing.T) {
	if got := SummaryFor(nil); got != "" {
		t.Errorf("expected empty string for nil history, got %q", got)
	}
	if got := SummaryFor([]api.Message{}); got != "" {
		t.Errorf("expected empty string for empty history, got %q", got)
	}
}

func TestSessionSummary_StringContent(t *testing.T) {
	history := []api.Message{
		{Role: "user", Content: "hello world"},
	}
	got := SummaryFor(history)
	if got != "hello world  [1 turns]" {
		t.Errorf("got %q, want %q", got, "hello world  [1 turns]")
	}
}

func TestSessionSummary_BlockContent(t *testing.T) {
	history := []api.Message{
		{Role: "user", Content: []interface{}{
			map[string]interface{}{"type": "text", "text": "fix the bug"},
		}},
	}
	got := SummaryFor(history)
	if got != "fix the bug  [1 turns]" {
		t.Errorf("got %q, want %q", got, "fix the bug  [1 turns]")
	}
}

func TestSessionSummary_MultiTurn(t *testing.T) {
	history := []api.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply1"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "reply2"},
		{Role: "user", Content: "third"},
	}
	got := SummaryFor(history)
	if got != "first  [3 turns]" {
		t.Errorf("got %q, want %q", got, "first  [3 turns]")
	}
}

func TestSessionSummary_TruncatesLongMessage(t *testing.T) {
	long := strings.Repeat("a", 250)
	history := []api.Message{{Role: "user", Content: long}}
	got := SummaryFor(history)
	// Should be 200 bytes of 'a' + "…" + "  [1 turns]"
	if !strings.HasPrefix(got, strings.Repeat("a", 200)+"…") {
		t.Errorf("expected truncation at 200 chars + ellipsis, got %q", got[:min(len(got), 30)])
	}
	if strings.Contains(got, strings.Repeat("a", 201)) {
		t.Errorf("content exceeds 200 chars: %q", got[:min(len(got), 30)])
	}
}

func TestSessionSummary_NoUserMessages(t *testing.T) {
	history := []api.Message{
		{Role: "assistant", Content: "hi"},
		{Role: "assistant", Content: "bye"},
	}
	got := SummaryFor(history)
	if got != "0 turns" {
		t.Errorf("got %q, want %q", got, "0 turns")
	}
}

func TestSessionSummary_BlockContent_SkipsNonTextBlocks(t *testing.T) {
	history := []api.Message{
		{Role: "user", Content: []interface{}{
			map[string]interface{}{"type": "image", "source": "..."},
			map[string]interface{}{"type": "text", "text": "look at this"},
		}},
	}
	got := SummaryFor(history)
	if got != "look at this  [1 turns]" {
		t.Errorf("got %q, want %q", got, "look at this  [1 turns]")
	}
}

func TestSanitizeSessionMessages(t *testing.T) {
	// Simulate corrupted messages as they come from JSON deserialization
	messages := []api.Message{
		{
			Role: "assistant",
			Content: []interface{}{
				map[string]interface{}{
					"type":  "text",
					"text":  "hello",
					"input": map[string]interface{}{}, // CORRUPTED: text blocks shouldn't have input
				},
			},
		},
		{
			Role: "assistant",
			Content: []interface{}{
				map[string]interface{}{
					"type": "tool_use",
					"id":   "abc",
					"name": "test",
					// CORRUPTED: missing input field
				},
			},
		},
	}

	SanitizeSessionMessages(messages)

	// Check text block — input should be removed
	blocks0 := messages[0].Content.([]interface{})
	block0 := blocks0[0].(map[string]interface{})
	if _, exists := block0["input"]; exists {
		t.Error("text block still has 'input' field after sanitization")
	}

	// Check tool_use block — input should be added
	blocks1 := messages[1].Content.([]interface{})
	block1 := blocks1[0].(map[string]interface{})
	if block1["input"] == nil {
		t.Error("tool_use block still has nil 'input' after sanitization")
	}
}

func TestLoadSessionRejectsTraversalIDs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, bad := range []string{
		"../etc/passwd",
		"../../sessions/abc",
		"foo/bar",
		`fooar`,
		"with spaces",
		"",
		strings.Repeat("a", 65),
		"with.dot",
		"x..y",
	} {
		_, err := LoadSession(bad)
		if err == nil {
			t.Errorf("LoadSession(%q) returned no error; want validation failure", bad)
			continue
		}
		if !strings.Contains(err.Error(), "invalid session ID") {
			t.Errorf("LoadSession(%q): err=%v; want \"invalid session ID\" message", bad, err)
		}
	}
}

func TestLoadSessionAcceptsValidIDs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Create a valid session first so the file exists.
	id := GenerateSessionID()
	if err := SaveSession(id, nil, 0, api.TokenUsage{}, "model"); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if _, err := LoadSession(id); err != nil {
		t.Errorf("LoadSession(%q) failed for valid ID: %v", id, err)
	}
	// Hyphens and underscores are also valid.
	for _, good := range []string{"abc-123", "abc_123", "ABC", "0123456789"} {
		if !isValidSessionID(good) {
			t.Errorf("isValidSessionID(%q) = false; want true", good)
		}
	}
}
