package session

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/api"
)

func TestSessionFilePathValidatesIDs(t *testing.T) {
	for _, bad := range []string{"", "../escape", "a/b", "spaces here", "with.dot", strings.Repeat("a", 65)} {
		if got := sessionFilePath(bad); got != "" {
			t.Errorf("sessionFilePath(%q) = %q, want empty for invalid ID", bad, got)
		}
		if IsValidSessionID(bad) {
			t.Errorf("IsValidSessionID(%q) = true, want false", bad)
		}
	}
	if got := sessionFilePath("valid-1_X"); got == "" {
		t.Error("sessionFilePath(valid-1_X) = empty, want a real path")
	}
}

func TestSaveSessionRejectsInvalidID(t *testing.T) {
	withTempHome(t)
	err := SaveSession("bad id", nil, 0, api.TokenUsage{}, "")
	if err == nil || !strings.Contains(err.Error(), "invalid session ID") {
		t.Fatalf("SaveSession(bad id) error = %v, want invalid session ID", err)
	}
}

func TestSessionPathsFailWithoutHome(t *testing.T) {
	orig := os.Getenv("HOME")
	os.Unsetenv("HOME")
	defer os.Setenv("HOME", orig)

	if got := sessionDirPath(); got != "" {
		t.Errorf("sessionDirPath without HOME = %q, want empty", got)
	}
	err := SaveSession("abc123", nil, 0, api.TokenUsage{}, "")
	if err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Fatalf("SaveSession without HOME error = %v, want cannot-determine-home failure", err)
	}
}

func TestLoadSessionCorruptFileFails(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".qmax-code", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession("corrupt"); err == nil {
		t.Fatal("corrupt session file must fail to load")
	}
}

func TestLoadSessionRehydratesMessagesFromTranscript(t *testing.T) {
	withTempHome(t)

	history := []api.Message{{Role: "user", Content: "transcript only"}}
	state := api.ConversationState{Transcript: history}
	if err := SaveSession("rehydrate", history, 0, api.TokenUsage{}, "m", state); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	loaded, err := LoadSession("rehydrate")
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].Role != "user" {
		t.Fatalf("messages not rehydrated from transcript: %+v", loaded.Messages)
	}
	if len(loaded.Conversation.Transcript) != 1 {
		t.Fatalf("transcript not preserved: %+v", loaded.Conversation)
	}
}

func TestListSessionsSkipsMalformedEntries(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".qmax-code", "sessions")

	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dir.json"), 0700); err != nil {
		t.Fatal(err) // directory with a .json name must be skipped
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	good := `{"id":"good","updated_at":"2026-01-02T15:04:05Z","turns":3,"model":"opus","project_id":11}`
	if err := os.WriteFile(filepath.Join(dir, "good.json"), []byte(good), 0600); err != nil {
		t.Fatal(err)
	}

	summaries, err := ListSessions(0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries = %+v, want only the good session", summaries)
	}
	s := summaries[0]
	if s.ID != "good" || s.Turns != 3 || s.Model != "opus" || s.ProjectID != 11 {
		t.Fatalf("summary = %+v, want good session fields", s)
	}
}

func TestListSessionsAndLastSessionFailWithoutDir(t *testing.T) {
	withTempHome(t)

	if _, err := ListSessions(10); err == nil {
		t.Fatal("missing sessions directory must be an error")
	}
	if _, err := LoadLastSession(); err == nil || !strings.Contains(err.Error(), "no sessions found") {
		t.Fatalf("LoadLastSession error = %v, want no sessions found", err)
	}
}

func TestCleanupOldSessionsHonorsDurableWindow(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".qmax-code", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	write := func(name, content string, age time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().Add(-age)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		return path
	}

	durableBody := `{"conversation":{"transcript":[{"role":"user","content":"hi"}]}}`
	legacy := write("legacy.json", `{"id":"legacy"}`, 8*24*time.Hour)
	durableMid := write("durable-mid.json", durableBody, 30*24*time.Hour)
	durableAncient := write("durable-ancient.json", durableBody, 100*24*time.Hour)
	recent := write("recent.json", `{"id":"recent"}`, time.Hour)

	removed := CleanupOldSessions()
	if removed != 2 {
		t.Fatalf("removed = %d, want 2 (legacy + ancient durable)", removed)
	}
	for _, path := range []string{legacy, durableAncient} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed", filepath.Base(path))
		}
	}
	for _, path := range []string{durableMid, recent} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s should have been kept: %v", filepath.Base(path), err)
		}
	}
}

func TestHasDurableTranscriptScansChunkBoundaries(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain.json")
	if err := os.WriteFile(plain, []byte(`{"id":"x"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if hasDurableTranscript(plain) {
		t.Fatal("session without a transcript must not be durable")
	}

	if hasDurableTranscript(filepath.Join(dir, "missing.json")) {
		t.Fatal("missing file must not be durable")
	}

	withKey := filepath.Join(dir, "with-key.json")
	if err := os.WriteFile(withKey, []byte(`{"conversation":{"transcript":[`), 0600); err != nil {
		t.Fatal(err)
	}
	if !hasDurableTranscript(withKey) {
		t.Fatal("transcript key must be detected")
	}

	// A key straddling the 64 KiB chunk boundary is only findable through the
	// carried overlap between reads.
	const chunk = 64 << 10
	var b []byte
	b = append(b, bytes.Repeat([]byte("x"), chunk-5)...)
	b = append(b, `"transcript":[`...)
	b = append(b, bytes.Repeat([]byte("y"), 64)...)
	straddle := filepath.Join(dir, "straddle.json")
	if err := os.WriteFile(straddle, b, 0600); err != nil {
		t.Fatal(err)
	}
	if !hasDurableTranscript(straddle) {
		t.Fatal("key straddling the chunk boundary was not detected")
	}
}

func TestIsToolResultOnlyShapes(t *testing.T) {
	block := func(blockType string) api.ContentBlock { return api.ContentBlock{Type: blockType} }
	tests := []struct {
		name    string
		content interface{}
		want    bool
	}{
		{"live tool_result blocks", []api.ContentBlock{block("tool_result")}, true},
		{"live mixed blocks", []api.ContentBlock{block("tool_result"), block("text")}, false},
		{"live empty blocks", []api.ContentBlock{}, false},
		{"decoded tool_result blocks", []interface{}{map[string]interface{}{"type": "tool_result"}}, true},
		{"decoded non-tool block", []interface{}{map[string]interface{}{"type": "text"}}, false},
		{"decoded non-map entry", []interface{}{"tool_result"}, false},
		{"decoded empty blocks", []interface{}{}, false},
		{"plain string", "tool_result", false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := isToolResultOnly(tt.content); got != tt.want {
				t.Fatalf("isToolResultOnly(%v) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestCountTurnsSkipsToolResultOnlyUserMessages(t *testing.T) {
	history := []api.Message{
		{Role: "user", Content: "real prompt"},
		{Role: "user", Content: []api.ContentBlock{{Type: "tool_result", ToolUseID: "t1", Content: "ok"}}},
		{Role: "user", Content: []interface{}{map[string]interface{}{"type": "tool_result"}}},
		{Role: "assistant", Content: "done"},
	}
	if got := CountTurns(history); got != 1 {
		t.Fatalf("CountTurns = %d, want 1 (only the typed prompt)", got)
	}
}

func TestSummaryForBlockContentWithoutTextFallsBackToTurnCount(t *testing.T) {
	history := []api.Message{{
		Role:    "user",
		Content: []interface{}{map[string]interface{}{"type": "image", "source": "..."}},
	}}
	if got := SummaryFor(history); got != "1 turns" {
		t.Fatalf("SummaryFor = %q, want %q", got, "1 turns")
	}
}

func TestMarshalRedactedRecursesIntoJSONStrings(t *testing.T) {
	nested := `{"api_key":"synthetic-nested-secret","note":"keep me"}`
	data, err := MarshalRedacted(nested)
	if err != nil {
		t.Fatalf("MarshalRedacted: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("nested credential not redacted: %s", out)
	}
	if strings.Contains(out, "synthetic-nested-secret") {
		t.Fatalf("nested credential leaked: %s", out)
	}
	if !strings.Contains(out, "keep me") {
		t.Fatalf("ordinary nested content lost: %s", out)
	}
}

func TestMarshalRedactedRejectsUnencodableValue(t *testing.T) {
	if _, err := MarshalRedacted(make(chan int)); err == nil {
		t.Fatal("unencodable value must fail instead of producing output")
	}
}
