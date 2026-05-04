package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Tests for the cloud agent-session sync added in PR #61:
//   - CreateAgentSession  (POST /api/agent-sessions)
//   - CompleteAgentSession (PATCH /api/agent-sessions/:id)
//   - patch() HTTP helper shape

// ---- CreateAgentSession ----

func TestCreateAgentSession_PostsCorrectEndpointAndBody(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody map[string]interface{}

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"session_id":"cloud-abc123"}`))
	})

	id := client.CreateAgentSession(context.Background(), 184, "claude-sonnet-4-6")

	if gotMethod != "POST" {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	if gotPath != "/api/agent-sessions" {
		t.Errorf("path: got %q, want /api/agent-sessions", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer qm-test-key") {
		t.Errorf("auth header: got %q", gotAuth)
	}
	if gotBody["project_id"].(float64) != 184 {
		t.Errorf("project_id: got %v, want 184", gotBody["project_id"])
	}
	if gotBody["model"] != "claude-sonnet-4-6" {
		t.Errorf("model: got %v, want claude-sonnet-4-6", gotBody["model"])
	}
	if id != "cloud-abc123" {
		t.Errorf("session_id: got %q, want cloud-abc123", id)
	}
}

func TestCreateAgentSession_ReturnsEmptyOnServerError(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal"}`))
	})

	id := client.CreateAgentSession(context.Background(), 184, "claude-sonnet-4-6")
	if id != "" {
		t.Errorf("expected empty string on server error, got %q", id)
	}
}

func TestCreateAgentSession_ReturnsEmptyOnMissingField(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"something-else"}`))
	})

	id := client.CreateAgentSession(context.Background(), 184, "claude-sonnet-4-6")
	if id != "" {
		t.Errorf("expected empty when session_id field absent, got %q", id)
	}
}

func TestCreateAgentSession_ReturnsEmptyOnBadJSON(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	})

	id := client.CreateAgentSession(context.Background(), 184, "claude-sonnet-4-6")
	if id != "" {
		t.Errorf("expected empty on non-JSON response, got %q", id)
	}
}

// ---- CompleteAgentSession ----

func TestCompleteAgentSession_PatchesToCorrectEndpoint(t *testing.T) {
	var gotMethod, gotPath string

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	})

	client.CompleteAgentSession(context.Background(), "cloud-abc123", 500, "fix the bug  [3 turns]")

	if gotMethod != "PATCH" {
		t.Errorf("method: got %q, want PATCH", gotMethod)
	}
	if gotPath != "/api/agent-sessions/cloud-abc123" {
		t.Errorf("path: got %q, want /api/agent-sessions/cloud-abc123", gotPath)
	}
}

func TestCompleteAgentSession_BodyFields(t *testing.T) {
	var gotBody map[string]interface{}

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	})

	client.CompleteAgentSession(context.Background(), "xyz", 1234, "do something  [2 turns]")

	if gotBody["status"] != "complete" {
		t.Errorf("status: got %v, want complete", gotBody["status"])
	}
	if gotBody["total_tokens"].(float64) != 1234 {
		t.Errorf("total_tokens: got %v, want 1234", gotBody["total_tokens"])
	}
	if gotBody["summary"] != "do something  [2 turns]" {
		t.Errorf("summary: got %v", gotBody["summary"])
	}
	endedAt, ok := gotBody["ended_at"].(string)
	if !ok || endedAt == "" {
		t.Errorf("ended_at missing or empty: %v", gotBody["ended_at"])
	}
	// Verify ended_at is a valid RFC3339 timestamp
	if _, err := time.Parse(time.RFC3339, endedAt); err != nil {
		t.Errorf("ended_at not valid RFC3339: %q", endedAt)
	}
}

func TestCompleteAgentSession_OmitsSummaryWhenEmpty(t *testing.T) {
	var gotBody map[string]interface{}

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	})

	client.CompleteAgentSession(context.Background(), "xyz", 0, "")

	if _, ok := gotBody["summary"]; ok {
		t.Errorf("summary should be omitted when empty, got %+v", gotBody)
	}
}

func TestCompleteAgentSession_SilentOnServerError(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	})

	// Must not panic and must return quickly — cloud failure never blocks local op.
	done := make(chan struct{})
	go func() {
		client.CompleteAgentSession(context.Background(), "xyz", 100, "summary")
		close(done)
	}()
	select {
	case <-done:
		// success
	case <-time.After(3 * time.Second):
		t.Error("CompleteAgentSession blocked on server error")
	}
}

// ---- patch() HTTP helper ----

// ---- UploadSessionMessages ----

func TestUploadSessionMessages_PostsToCorrectEndpoint(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"appended":2}`))
	})

	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	}
	client.UploadSessionMessages(context.Background(), "sess-123", msgs)

	if gotMethod != "POST" {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	// Server exposes /events, not /messages. The 405 from /messages is the bug
	// this test now guards against.
	if gotPath != "/api/agent-sessions/sess-123/events" {
		t.Errorf("path: got %q, want /api/agent-sessions/sess-123/events", gotPath)
	}
	events, ok := gotBody["events"].([]interface{})
	if !ok {
		t.Fatalf("body missing 'events' array: %+v", gotBody)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	for i, ev := range events {
		m, ok := ev.(map[string]interface{})
		if !ok {
			t.Fatalf("event %d not an object: %v", i, ev)
		}
		if m["type"] != "message" {
			t.Errorf("event %d type: got %v, want \"message\"", i, m["type"])
		}
		// Server stores message bodies under "payload" — using "data" silently
		// drops the content (event lands with payload={}). Guard against regression.
		payload, ok := m["payload"].(map[string]interface{})
		if !ok {
			t.Fatalf("event %d missing payload object: %v", i, m)
		}
		if payload["role"] == nil || payload["content"] == nil {
			t.Errorf("event %d payload missing role/content: %v", i, payload)
		}
		if _, hasData := m["data"]; hasData {
			t.Errorf("event %d should not use 'data' key (server expects 'payload'): %v", i, m)
		}
	}
	first := events[0].(map[string]interface{})["payload"].(map[string]interface{})
	if first["role"] != "user" || first["content"] != "hello" {
		t.Errorf("first event payload mismatch: %+v", first)
	}
}

func TestUploadSessionMessages_SkipsWhenEmpty(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{}`))
	})

	client.UploadSessionMessages(context.Background(), "sess-123", nil)
	client.UploadSessionMessages(context.Background(), "sess-123", []Message{})

	if calls != 0 {
		t.Errorf("expected 0 HTTP calls for empty messages, got %d", calls)
	}
}

// ---- trimMessagesToFit ----

func TestTrimMessagesToFit_NoTrimWhenUnderLimit(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	result := trimMessagesToFit(msgs, 1024*1024)
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
}

func TestTrimMessagesToFit_TrimsOldestMessages(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: strings.Repeat("A", 500)},
		{Role: "assistant", Content: strings.Repeat("B", 500)},
		{Role: "user", Content: strings.Repeat("C", 500)},
		{Role: "assistant", Content: "short"},
	}
	// Set a tight limit that can only fit the last 2 messages
	result := trimMessagesToFit(msgs, 600)
	if len(result) >= len(msgs) {
		t.Errorf("expected trimming, got %d messages (same as input)", len(result))
	}
	// Last message should always be preserved
	last := result[len(result)-1]
	if last.Content != "short" {
		t.Errorf("last message should be preserved, got content %q", last.Content)
	}
}

func TestTrimMessagesToFit_ReturnsAtLeastLastMessage(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: strings.Repeat("X", 10000)},
		{Role: "assistant", Content: strings.Repeat("Y", 10000)},
	}
	// Impossibly small limit
	result := trimMessagesToFit(msgs, 10)
	if len(result) < 1 {
		t.Error("should return at least 1 message")
	}
}

func TestPatch_SendsPatchMethodWithAuthAndContentType(t *testing.T) {
	var gotMethod, gotAuth, gotCT string
	var gotBody map[string]interface{}

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	out := client.patch(context.Background(), "/api/agent-sessions/test-id", map[string]interface{}{
		"status": "complete",
	})

	if gotMethod != "PATCH" {
		t.Errorf("method: got %q, want PATCH", gotMethod)
	}
	if !strings.HasPrefix(gotAuth, "Bearer qm-test-key") {
		t.Errorf("auth header missing: got %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", gotCT)
	}
	if gotBody["status"] != "complete" {
		t.Errorf("body not marshaled correctly: %+v", gotBody)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("expected response body returned, got %q", out)
	}
}
