package vnc

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// ─── normalizeNoVNCURL additional cases ──────────────────────────────────────

func TestNormalizeNoVNCURLEdgeCases(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		// Bare host:port — assume wss (cloud sandbox exposed ports are HTTPS)
		{"6080-abc.e2b.app", "wss://6080-abc.e2b.app/websockify", false},
		// Already a wss URL with /websockify — pass through
		{"wss://6080-abc.e2b.app/websockify", "wss://6080-abc.e2b.app/websockify", false},
		// noVNC HTML5 URL with query params — strip to /websockify
		{"https://6080-if0q8pr0ddk1wis53med7.e2b.app/vnc.html?autoconnect=true&resize=scale&reconnect=true", "wss://6080-if0q8pr0ddk1wis53med7.e2b.app/websockify", false},
		// Empty path treated as root → /websockify
		{"https://host.example", "wss://host.example/websockify", false},
		// Custom non-standard path preserved
		{"wss://host.example/custom/path", "wss://host.example/custom/path", false},
		// ftp:// rejected
		{"ftp://host.example/vnc.html", "", true},
		// mailto: rejected
		{"mailto:someone@example.com", "", true},
	}
	for _, c := range cases {
		got, err := normalizeNoVNCURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeNoVNCURL(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeNoVNCURL(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeNoVNCURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ─── isStandardNoVNCPath ──────────────────────────────────────────────────────

func TestIsStandardNoVNCPath(t *testing.T) {
	standard := []string{"", "/", "/vnc.html", "/vnc_lite.html"}
	for _, p := range standard {
		if !isStandardNoVNCPath(p) {
			t.Errorf("isStandardNoVNCPath(%q) = false, want true", p)
		}
	}
	nonStandard := []string{"/websockify", "/custom", "/noVNC/vnc.html", "/vnc.html.bak"}
	for _, p := range nonStandard {
		if isStandardNoVNCPath(p) {
			t.Errorf("isStandardNoVNCPath(%q) = true, want false", p)
		}
	}
}

// ─── DialVNC retry + mock WebSocket server ────────────────────────────────────

// fakeRFBServer starts an httptest server that upgrades to WebSocket and then
// speaks a minimal RFB 3.8 handshake with security-type None. After handshake
// it closes. Returns the server and its ws:// URL at /websockify.
func fakeRFBServer(t *testing.T) (*httptest.Server, string) {
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

		// RFB 3.8 handshake
		_, _ = nc.Write([]byte("RFB 003.008\n")) // ProtocolVersion
		clientVer := make([]byte, 12)
		_, _ = nc.Read(clientVer)
		_, _ = nc.Write([]byte{1, 1})       // 1 security type: None
		_, _ = nc.Read(make([]byte, 1))     // client selects type 1
		_, _ = nc.Write([]byte{0, 0, 0, 0}) // SecurityResult OK
		_, _ = nc.Read(make([]byte, 1))     // ClientInit shared-flag

		// ServerInit: 24-byte header (width=800, height=600) + 0-length name
		si := make([]byte, 24+4)
		binary.BigEndian.PutUint16(si[0:], 800)
		binary.BigEndian.PutUint16(si[2:], 600)
		// pixel format bytes (si[4:20]) left as zero — overridden by SetPixelFormat
		binary.BigEndian.PutUint32(si[20:], 0) // name length = 0
		_, _ = nc.Write(si)

		// Drain any client messages (SetPixelFormat, SetEncodings, FBUpdateReq)
		// until the connection is closed from the other side.
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

func TestDialVNCHandshake(t *testing.T) {
	srv, wsURL := fakeRFBServer(t)
	defer srv.Close()

	stream, err := DialVNC(context.Background(), wsURL, 1)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	defer stream.Close()

	if stream.width != 800 || stream.height != 600 {
		t.Errorf("server init size = %dx%d, want 800x600", stream.width, stream.height)
	}
	if len(stream.fb) != 800*600*4 {
		t.Errorf("framebuffer size = %d, want %d", len(stream.fb), 800*600*4)
	}
}

// TestDialVNCRetryOn502 verifies that DialVNC retries when the server returns
// a 502 gateway error, and eventually succeeds when the server becomes ready.
//
// Note: dialOnce sends two HTTP requests per attempt (one with the "binary"
// subprotocol, one without). So the server receives requestsPerAttempt = 2
// before a retry gap fires. With failUntil=4 server-side requests, DialVNC
// makes one full attempt (2 requests, both 502) → 3s gap → second attempt
// (requests 3 and 4, both 502 again → but now attempt==1 and next would be
// attempt 2) → actually failUntil=3 gives us 1 retry gap = 3s.
func TestDialVNCRetryOn502(t *testing.T) {
	var attempts int
	// Fail first 3 HTTP requests (= first dialOnce's 2 sub-requests + one
	// from the second dialOnce), then succeed.
	const failUntil = 3
	mux := http.NewServeMux()
	mux.HandleFunc("/websockify", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= failUntil {
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
			return
		}
		// Succeed: accept the WebSocket and close cleanly.
		wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		wsConn.Close(websocket.StatusNormalClosure, "")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws://" + srv.Listener.Addr().String() + "/websockify"
	start := time.Now()
	// DialVNC will fail to complete RFB (no handshake bytes), but the retry
	// logic fires before that. We just care about timing + attempt count.
	DialVNC(context.Background(), wsURL, 1) //nolint:errcheck
	elapsed := time.Since(start)

	// At least one 3-second retry gap must have fired.
	if elapsed < 2500*time.Millisecond {
		t.Errorf("retry loop finished too fast (%v); expected ≥2.5s for ≥1 retry gap", elapsed)
	}
	if attempts < failUntil {
		t.Errorf("expected ≥%d HTTP requests, got %d", failUntil, attempts)
	}
}

// TestDialVNCNoRetryOnNon5xx verifies that non-gateway errors are not retried.
// dialOnce sends up to 2 HTTP requests per attempt (binary + bare subprotocol),
// so we assert no 3-second gap occurred rather than a fixed request count.
func TestDialVNCNoRetryOnNon5xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/websockify", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden) // 403 — not a gateway error
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws://" + srv.Listener.Addr().String() + "/websockify"
	start := time.Now()
	DialVNC(context.Background(), wsURL, 1) //nolint:errcheck
	elapsed := time.Since(start)

	// No retry gap should have fired (each gap is 3s).
	if elapsed > 2*time.Second {
		t.Errorf("non-5xx error should not be retried; elapsed %v (expected <2s)", elapsed)
	}
}

// ─── VNCStream.Close idempotency ──────────────────────────────────────────────

func TestVNCStreamCloseIdempotent(t *testing.T) {
	srv, wsURL := fakeRFBServer(t)
	defer srv.Close()

	stream, err := DialVNC(context.Background(), wsURL, 1)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	// Closing twice must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Close panicked: %v", r)
		}
	}()
	stream.Close()
	stream.Close()
}
