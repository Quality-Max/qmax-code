package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/sysutil"
	"github.com/qualitymax/qmax-code/internal/tui"
	"github.com/qualitymax/qmax-code/internal/vnc"
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

// ─── maybeLaunchLiveFeed: logic paths ────────────────────────────────────────

// LiveFeed=false → no-op; preStream should be closed.
func TestMaybeLaunchLiveFeedLiveFeedOff(t *testing.T) {
	srv, wsURL := fakeRFBServerForFeed(t)
	defer srv.Close()

	stream, err := vnc.DialVNC(context.Background(), wsURL, 1)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	sctx := &api.SessionContext{LiveFeed: false, LastLiveURL: "https://host/vnc.html"}
	term := &tui.Terminal{}
	maybeLaunchLiveFeed(sctx, term, stream, "")

	// Stream should be closed (Close is idempotent, calling again should not panic).
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("stream.Close after maybeLaunchLiveFeed panicked: %v", r)
		}
	}()
	stream.Close()
}

// LiveFeed=true, no URL, sandboxModeLogged=false → no diagnostic, no launch.
func TestMaybeLaunchLiveFeedNoURLNoLog(t *testing.T) {
	sctx := &api.SessionContext{LiveFeed: true, SandboxModeLogged: false}
	term := &tui.Terminal{}
	// Should be a pure no-op (no panic, no launch).
	maybeLaunchLiveFeed(sctx, term, nil, "")
}

// nil sctx → must not panic (preStream is closed if non-nil).
func TestMaybeLaunchLiveFeedNilSctx(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil sctx panicked: %v", r)
		}
	}()
	maybeLaunchLiveFeed(nil, &tui.Terminal{}, nil, "")
}

// ─── runTestWithProgress early-return when LiveFeed + URL captured ────────────

// fakeRFBServerForFeed mirrors the helper in internal/vnc tests. We can't
// reach into that package's test-only symbols from here, so we duplicate the
// minimal RFB handshake. Kept self-contained for the same reason.
func fakeRFBServerForFeed(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/websockify", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:       []string{"binary"},
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		nc := websocket.NetConn(r.Context(), conn, websocket.MessageBinary)

		_, _ = nc.Write([]byte("RFB 003.008\n"))
		_, _ = nc.Read(make([]byte, 12))
		_, _ = nc.Write([]byte{1, 1})
		_, _ = nc.Read(make([]byte, 1))
		_, _ = nc.Write([]byte{0, 0, 0, 0})
		_, _ = nc.Read(make([]byte, 1))

		si := make([]byte, 24+4)
		binary.BigEndian.PutUint16(si[0:], 800)
		binary.BigEndian.PutUint16(si[2:], 600)
		binary.BigEndian.PutUint32(si[20:], 0)
		_, _ = nc.Write(si)

		buf := make([]byte, 4096)
		for {
			if _, err := nc.Read(buf); err != nil {
				return
			}
		}
	})

	srv := httptest.NewServer(mux)
	wsURL := "ws://" + srv.Listener.Addr().String() + "/websockify"
	return srv, wsURL
}

// TestAnnotatePreservesExecutionID verifies the execution_id survives round-trip.
func TestAnnotatePreservesExecutionID(t *testing.T) {
	raw := `{"execution_id":"exec_6120_1778065167","status":"running","progress":42}`
	got := annotateWithClientNote(raw, "test note")
	if !strings.Contains(got, "exec_6120_1778065167") {
		t.Errorf("execution_id not preserved in annotated output: %s", got)
	}
}

// ─── waitForLiveFeedURL ───────────────────────────────────────────────────────

// TestWaitForLiveFeedURLReturnsWhenURLAppears verifies that the function
// returns as soon as live_browser_url appears in the status response.
func TestWaitForLiveFeedURLReturnsWhenURLAppears(t *testing.T) {
	var calls atomic.Int32
	const wantURL = "https://6080-abc.e2b.app/vnc.html"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			// First two polls: URL not yet available.
			fmt.Fprintf(w, `{"status":"running","progress":%d}`, n*10)
		} else {
			// Third poll: URL appears.
			fmt.Fprintf(w, `{"status":"running","progress":50,"live_browser_url":%q}`, wantURL)
		}
	}))
	defer srv.Close()

	api := &api.APIClient{BaseURL: srv.URL, HTTP: srv.Client()}
	start := time.Now()
	got := waitForLiveFeedURL(api, "exec_test", 30*time.Second)
	elapsed := time.Since(start)

	if got != wantURL {
		t.Errorf("waitForLiveFeedURL = %q, want %q", got, wantURL)
	}
	// Should have waited at least one 2s sleep before URL appeared.
	if elapsed < 1500*time.Millisecond {
		t.Errorf("returned too fast (%v); expected ≥1.5s", elapsed)
	}
	if calls.Load() < 3 {
		t.Errorf("expected ≥3 API calls, got %d", calls.Load())
	}
}

// TestWaitForLiveFeedURLStopsOnFinalStatus verifies that the function returns
// "" immediately when the test has already finished (no URL will ever appear).
func TestWaitForLiveFeedURLStopsOnFinalStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"failed","progress":100}`)
	}))
	defer srv.Close()

	api := &api.APIClient{BaseURL: srv.URL, HTTP: srv.Client()}
	start := time.Now()
	got := waitForLiveFeedURL(api, "exec_test", 30*time.Second)
	elapsed := time.Since(start)

	if got != "" {
		t.Errorf("expected empty URL on failed status, got %q", got)
	}
	// Should not have waited the full timeout.
	if elapsed > 10*time.Second {
		t.Errorf("took too long (%v); should stop on failed status", elapsed)
	}
}

// TestWaitForLiveFeedURLTimeout verifies that the function returns "" when
// the timeout expires without a URL.
func TestWaitForLiveFeedURLTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"running","progress":10}`)
	}))
	defer srv.Close()

	api := &api.APIClient{BaseURL: srv.URL, HTTP: srv.Client()}
	// Use a very short timeout so the test doesn't take long.
	got := waitForLiveFeedURL(api, "exec_test", 3*time.Second)
	if got != "" {
		t.Errorf("expected empty URL on timeout, got %q", got)
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
