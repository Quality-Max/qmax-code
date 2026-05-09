package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/sysutil"
)

// ─── annotateWithClientNote ───────────────────────────────────────────────────

func TestAnnotateWithClientNote(t *testing.T) {
	raw := `{"status":"running","progress":88,"execution_id":"exec_123"}`
	note := "Do NOT call check_test_status again"
	got := annotateWithClientNote(raw, note)

	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("result not valid JSON: %v\nraw: %s", err, got)
	}
	var gotNote string
	if err := json.Unmarshal(m["client_note"], &gotNote); err != nil {
		t.Fatalf("client_note not a JSON string: %v", err)
	}
	if gotNote != note {
		t.Errorf("client_note = %q, want %q", gotNote, note)
	}
	// Original fields must be preserved with their types intact.
	var pct json.Number
	if err := json.Unmarshal(m["progress"], &pct); err != nil {
		t.Errorf("progress field corrupted: %v", err)
	}
	if pct.String() != "88" {
		t.Errorf("progress = %s, want 88", pct)
	}
}

func TestAnnotateWithClientNotePreservesTypes(t *testing.T) {
	// Float64-ification bug: ensure integer fields don't become 88.0
	raw := `{"execution_time":33,"progress":88,"nested":{"a":1}}`
	got := annotateWithClientNote(raw, "note")
	// Must not contain ".0" representation of integers
	if strings.Contains(got, "88.0") || strings.Contains(got, "33.0") {
		t.Errorf("integer fields were float64-ified: %s", got)
	}
}

func TestAnnotateWithClientNoteBadJSON(t *testing.T) {
	raw := "not json"
	got := annotateWithClientNote(raw, "note")
	if got != raw {
		t.Errorf("bad JSON input: expected passthrough %q, got %q", raw, got)
	}
}

func TestAnnotateWithClientNoteEmptyNote(t *testing.T) {
	raw := `{"status":"passed"}`
	got := annotateWithClientNote(raw, "")
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := m["client_note"]; !ok {
		t.Error("client_note key missing even for empty note")
	}
}

// ─── captureLiveURL: additional contract cases ────────────────────────────────

// Non-string live_browser_url (e.g. null from server) → treat as absent.
func TestCaptureLiveURLNullValue(t *testing.T) {
	sctx := &api.SessionContext{LastLiveURL: "old"}
	captureLiveURL(sctx, `{"status":"running","live_browser_url":null}`)
	if sctx.LastLiveURL != "old" {
		t.Errorf("null URL should not overwrite; got %q", sctx.LastLiveURL)
	}
}

// Numeric live_browser_url (server bug) → treat as absent, no panic.
func TestCaptureLiveURLNumericValue(t *testing.T) {
	sctx := &api.SessionContext{}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panicked on numeric live_browser_url: %v", r)
		}
	}()
	captureLiveURL(sctx, `{"status":"running","live_browser_url":12345}`)
	if sctx.LastLiveURL != "" {
		t.Errorf("numeric URL should not be captured; got %q", sctx.LastLiveURL)
	}
}

// LiveFeed=true + first status → sandboxModeLogged should be set.
func TestCaptureLiveURLSetsSandboxModeLogged(t *testing.T) {
	sctx := &api.SessionContext{LiveFeed: true}
	raw := `{"status":"running","progress":10,"is_e2b":true,"live_browser_url":"https://host/vnc.html"}`
	captureLiveURL(sctx, raw)
	if !sctx.SandboxModeLogged {
		t.Error("sandboxModeLogged should be true after first status with LiveFeed=true")
	}
	if sctx.LastLiveURL == "" {
		t.Error("expected LastLiveURL to be set")
	}
}

// LiveFeed=true + is_e2b=false → sandboxFallbackSeen should be set.
func TestCaptureLiveURLDetectsFallback(t *testing.T) {
	sctx := &api.SessionContext{LiveFeed: true}
	raw := `{"status":"running","progress":10,"is_e2b":false}`
	captureLiveURL(sctx, raw)
	if !sctx.SandboxFallbackSeen {
		t.Error("sandboxFallbackSeen should be true when is_e2b=false")
	}
}

// Second call after sandboxModeLogged should not re-log (gate stays closed).
func TestCaptureLiveURLOnlyLogsFirstStatus(t *testing.T) {
	sctx := &api.SessionContext{LiveFeed: true, SandboxModeLogged: true}
	// If we log a second time, the test would need stderr capture. Instead,
	// verify the gate field stays true and no URL is double-set.
	url := "https://host/vnc.html"
	sctx.LastLiveURL = url
	captureLiveURL(sctx, `{"status":"running","progress":20}`)
	if sctx.LastLiveURL != url {
		t.Errorf("subsequent call without URL should not clear previous; got %q", sctx.LastLiveURL)
	}
}

// ─── drainLiveURLFromChild / persistLiveURLForParent round-trip ──────────────

func TestPersistAndDrainLiveURL(t *testing.T) {
	// Reset the singleton so sysutil.LiveURLFilePath() recomputes on next call.
	resetLiveURLFileForTest()

	// sysutil.LiveURLFilePath() is the parent-side path (computed once via sync.Once).
	// cc_agent.go sets QMAX_LIVE_URL_FILE to this same path so the child
	// (MCP subprocess) knows where to write. Replicate that setup here.
	parentPath := sysutil.LiveURLFilePath()
	if parentPath == "" {
		t.Skip("no home directory available")
	}
	t.Cleanup(func() { os.Remove(parentPath) })
	t.Setenv("QMAX_LIVE_URL_FILE", parentPath)

	want := "https://6080-test.e2b.app/vnc.html"
	sysutil.PersistLiveURLForParent(want) // child writes to QMAX_LIVE_URL_FILE path

	got := sysutil.DrainLiveURLFromChild() // parent reads from sysutil.LiveURLFilePath()
	if got != want {
		t.Errorf("drain = %q, want %q", got, want)
	}
	// Second drain → empty (file was removed after first read).
	if second := sysutil.DrainLiveURLFromChild(); second != "" {
		t.Errorf("second drain should be empty, got %q", second)
	}
}

func TestPersistLiveURLNoopWhenEnvUnset(t *testing.T) {
	t.Setenv("QMAX_LIVE_URL_FILE", "")
	// Must not panic or create any file.
	sysutil.PersistLiveURLForParent("https://host/vnc.html")
}

func TestDrainLiveURLFromChildMissingFile(t *testing.T) {
	resetLiveURLFileForTest()
	t.Setenv("QMAX_LIVE_URL_FILE", "")
	// The parent-side path doesn't exist yet → should return "".
	got := sysutil.DrainLiveURLFromChild()
	if got != "" {
		t.Errorf("missing file should return empty, got %q", got)
	}
}

// ─── execID side-channel round-trip ─────────────────────────────────────────

func TestPersistAndDrainExecID(t *testing.T) {
	resetExecIDFileForTest()

	parentPath := sysutil.ExecIDFilePath()
	if parentPath == "" {
		t.Skip("no home directory available")
	}
	t.Cleanup(func() { os.Remove(parentPath) })
	t.Setenv("QMAX_EXEC_ID_FILE", parentPath)

	want := "exec_6120_1778065167"
	sysutil.PersistExecIDForParent(want)

	got := sysutil.DrainExecIDFromChild()
	if got != want {
		t.Errorf("drain = %q, want %q", got, want)
	}
	// Second drain → empty (file was removed).
	if second := sysutil.DrainExecIDFromChild(); second != "" {
		t.Errorf("second drain should be empty, got %q", second)
	}
}

func TestPersistExecIDNoopWhenEnvUnset(t *testing.T) {
	t.Setenv("QMAX_EXEC_ID_FILE", "")
	// Must not panic or create any file.
	sysutil.PersistExecIDForParent("exec_abc_123")
}

func TestDrainExecIDFromChildMissingFile(t *testing.T) {
	resetExecIDFileForTest()
	// The parent-side path doesn't exist yet → should return "".
	got := sysutil.DrainExecIDFromChild()
	if got != "" {
		t.Errorf("missing file should return empty, got %q", got)
	}
}

// TestAnnotatePreservesExecutionID verifies the execution_id survives round-trip.
func TestAnnotatePreservesExecutionID(t *testing.T) {
	raw := `{"execution_id":"exec_6120_1778065167","status":"running","progress":42}`
	got := annotateWithClientNote(raw, "test note")
	if !strings.Contains(got, "exec_6120_1778065167") {
		t.Errorf("execution_id not preserved in annotated output: %s", got)
	}
}

// TestRunTestWithProgressLiveFeedFastReturn verifies that runTestWithProgress
// returns immediately when LiveFeed=true, writing the execID to the side channel.
func TestRunTestWithProgressLiveFeedFastReturn(t *testing.T) {
	resetExecIDFileForTest()

	parentPath := sysutil.ExecIDFilePath()
	if parentPath == "" {
		t.Skip("no home directory")
	}
	t.Cleanup(func() { os.Remove(parentPath) })
	t.Setenv("QMAX_EXEC_ID_FILE", parentPath)

	// Fake API server: RunTest returns an execution_id immediately.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"execution_id":"exec_fast_123","status":"queued"}`)
	}))
	defer srv.Close()

	client := &api.APIClient{BaseURL: srv.URL, HTTP: srv.Client()}
	sctx := &api.SessionContext{LiveFeed: true, API: client}

	result := runTestWithProgress(t.Context(), client, sctx, 42, true, "", "")

	// Must have returned early (contains client_note).
	if !strings.Contains(result, "client_note") {
		t.Errorf("expected client_note in fast-return result: %s", result)
	}
	if !strings.Contains(result, "exec_fast_123") {
		t.Errorf("execution_id missing from result: %s", result)
	}

	// execID must be persisted in the side-channel file.
	got := sysutil.DrainExecIDFromChild()
	if got != "exec_fast_123" {
		t.Errorf("side-channel execID = %q, want exec_fast_123", got)
	}
}
