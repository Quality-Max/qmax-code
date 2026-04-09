package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateSessionID(t *testing.T) {
	id1 := generateSessionID()
	id2 := generateSessionID()
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

	history := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	usage := TokenUsage{InputTokens: 100, OutputTokens: 50, Requests: 1}

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
	_ = SaveSession("aaa", []Message{{Role: "user", Content: "1"}}, 1, TokenUsage{}, "sonnet")
	time.Sleep(10 * time.Millisecond)
	_ = SaveSession("bbb", []Message{{Role: "user", Content: "2"}}, 2, TokenUsage{}, "sonnet")
	time.Sleep(10 * time.Millisecond)
	_ = SaveSession("ccc", []Message{{Role: "user", Content: "3"}}, 3, TokenUsage{}, "sonnet")

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

	_ = SaveSession("a1", []Message{{Role: "user", Content: "1"}}, 0, TokenUsage{}, "")
	_ = SaveSession("a2", []Message{{Role: "user", Content: "2"}}, 0, TokenUsage{}, "")
	_ = SaveSession("a3", []Message{{Role: "user", Content: "3"}}, 0, TokenUsage{}, "")

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

	_ = SaveSession("first", []Message{{Role: "user", Content: "old"}}, 1, TokenUsage{}, "")
	time.Sleep(10 * time.Millisecond)
	_ = SaveSession("second", []Message{{Role: "user", Content: "new"}}, 2, TokenUsage{}, "")

	session, err := LoadLastSession()
	if err != nil {
		t.Fatalf("LoadLastSession failed: %v", err)
	}
	if session.ID != "second" {
		t.Errorf("Should load most recent session, got %s", session.ID)
	}
}

func TestSanitizeSessionMessages(t *testing.T) {
	// Simulate corrupted messages as they come from JSON deserialization
	messages := []Message{
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
					"type":  "tool_use",
					"id":    "abc",
					"name":  "test",
					// CORRUPTED: missing input field
				},
			},
		},
	}

	sanitizeSessionMessages(messages)

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
