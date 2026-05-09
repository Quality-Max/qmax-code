package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
	"github.com/qualitymax/qmax-code/internal/vnc"
)

// Tests for the REPL-side live-feed plumbing (maybeLaunchLiveFeed,
// waitForLiveFeedURL). These previously lived in live_feed_extra_test.go but
// moved here when the agent layer was extracted, since the functions under
// test stayed in main (they belong to the REPL flow, not the agent).

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
			fmt.Fprintf(w, `{"status":"running","progress":%d}`, n*10)
		} else {
			fmt.Fprintf(w, `{"status":"running","progress":50,"live_browser_url":%q}`, wantURL)
		}
	}))
	defer srv.Close()

	client := &api.APIClient{BaseURL: srv.URL, HTTP: srv.Client()}
	start := time.Now()
	got := waitForLiveFeedURL(client, "exec_test", 30*time.Second)
	elapsed := time.Since(start)

	if got != wantURL {
		t.Errorf("waitForLiveFeedURL = %q, want %q", got, wantURL)
	}
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

	client := &api.APIClient{BaseURL: srv.URL, HTTP: srv.Client()}
	start := time.Now()
	got := waitForLiveFeedURL(client, "exec_test", 30*time.Second)
	elapsed := time.Since(start)

	if got != "" {
		t.Errorf("expected empty URL on failed status, got %q", got)
	}
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

	client := &api.APIClient{BaseURL: srv.URL, HTTP: srv.Client()}
	got := waitForLiveFeedURL(client, "exec_test", 3*time.Second)
	if got != "" {
		t.Errorf("expected empty URL on timeout, got %q", got)
	}
}
