package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	receipt "github.com/Quality-Max/qmax-receipt"
	"github.com/coder/websocket"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type failingReadCloser struct{ read bool }

func (b *failingReadCloser) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.ErrUnexpectedEOF
	}
	b.read = true
	copy(p, "abc")
	return 3, io.ErrUnexpectedEOF
}

func (b *failingReadCloser) Close() error { return nil }

// TestRecordsRequestHashAndSize drives a real request through the recording
// client and asserts the receipt captured the method, category, byte size and
// content hash — and never the content itself.
func TestRecordsRequestHashAndSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	rec := receipt.NewCurrent("test:session")

	body := strings.Repeat("prompt-", 1000) // ~7KB
	ctx := WithModel(context.Background(), "claude-sonnet-5")
	req, err := NewRequest(ctx, http.MethodPost, srv.URL+"/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := NewClient(10 * time.Second).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if rec.EntryCount() != 1 {
		t.Fatalf("expected 1 recorded entry, got %d", rec.EntryCount())
	}
	e := rec.Entries[0]
	if e.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", e.Method)
	}
	if e.Category != "llm-prompt" {
		t.Errorf("category = %q, want llm-prompt", e.Category)
	}
	if e.ReqBytes != int64(len(body)) {
		t.Errorf("req_bytes = %d, want %d", e.ReqBytes, len(body))
	}
	wantHash := sha256.Sum256([]byte(body))
	if e.ReqSHA256 != hex.EncodeToString(wantHash[:]) {
		t.Errorf("req_sha256 = %q, want %q", e.ReqSHA256, hex.EncodeToString(wantHash[:]))
	}
	if e.Model == nil || *e.Model != "claude-sonnet-5" {
		t.Errorf("model = %v, want claude-sonnet-5", e.Model)
	}
	if e.RespStatus != http.StatusOK {
		t.Errorf("resp_status = %d, want 200", e.RespStatus)
	}
}

// TestTransportErrorStillRecorded proves an egress attempt that never connects
// is still accounted for — no request leaves the machine unrecorded.
func TestTransportErrorStillRecorded(t *testing.T) {
	rec := receipt.NewCurrent("test:err")

	// Reserved-for-documentation TEST-NET-1 address; connection will fail fast.
	req, err := NewRequest(context.Background(), http.MethodGet, "http://192.0.2.1:1/api/projects", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, _ = NewClient(500 * time.Millisecond).Do(req)

	if rec.EntryCount() != 1 {
		t.Fatalf("expected the failed attempt to be recorded, got %d entries", rec.EntryCount())
	}
	if note := rec.Entries[0].Note; !strings.Contains(note, "transport-error") {
		t.Errorf("note = %q, want transport-error", note)
	}
	if rec.Entries[0].Category != "cloud-api" {
		t.Errorf("category = %q, want cloud-api", rec.Entries[0].Category)
	}
}

func TestTransportErrorNoteExcludesErrorText(t *testing.T) {
	rec := receipt.NewCurrent("test:private-transport-error")
	req, err := NewRequest(context.Background(), http.MethodGet, "http://example.test/api/projects", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	transportErr := errors.New("not-for-receipt")
	transport := &receiptTransport{base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, transportErr
	})}
	if _, err := transport.RoundTrip(req); !errors.Is(err, transportErr) {
		t.Fatalf("RoundTrip error = %v, want %v", err, transportErr)
	}

	if rec.EntryCount() != 1 {
		t.Fatalf("entries = %d, want 1", rec.EntryCount())
	}
	note := rec.Entries[0].Note
	if note != "transport-error" {
		t.Errorf("note = %q, want generic transport error category", note)
	}
	if strings.Contains(note, transportErr.Error()) {
		t.Errorf("note = %q, must not include the transport error text", note)
	}
}

func TestIncompleteRequestBodyIsNotSignedAsACompleteHash(t *testing.T) {
	rec := receipt.NewCurrent("test:early-response")
	req, err := NewRequest(context.Background(), http.MethodPost, "http://example.test/api/projects", io.NopCloser(strings.NewReader("abcdef")))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	transport := &receiptTransport{base: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		buf := make([]byte, 3)
		if _, err := req.Body.Read(buf); err != nil {
			t.Fatalf("read partial body: %v", err)
		}
		return &http.Response{StatusCode: http.StatusRequestEntityTooLarge, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if rec.EntryCount() != 1 {
		t.Fatalf("entries = %d, want 1", rec.EntryCount())
	}
	e := rec.Entries[0]
	if e.ReqBytes != 0 || e.ReqSHA256 != "" {
		t.Errorf("incomplete metadata = bytes:%d hash:%q, want unavailable", e.ReqBytes, e.ReqSHA256)
	}
	if !strings.Contains(e.Note, "request-body-incomplete") {
		t.Errorf("note = %q, want incomplete-body marker", e.Note)
	}
}

func TestRequestBodyReadErrorIsRecordedAsIncomplete(t *testing.T) {
	rec := receipt.NewCurrent("test:body-read-error")
	req, err := NewRequest(context.Background(), http.MethodPost, "http://example.test/api/projects", &failingReadCloser{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	transport := &receiptTransport{base: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		buf := make([]byte, 8)
		_, err := req.Body.Read(buf)
		return nil, err
	})}
	if _, err := transport.RoundTrip(req); err == nil {
		t.Fatal("RoundTrip error = nil, want body read error")
	}

	if rec.EntryCount() != 1 {
		t.Fatalf("entries = %d, want 1", rec.EntryCount())
	}
	e := rec.Entries[0]
	if e.ReqBytes != 0 || e.ReqSHA256 != "" {
		t.Errorf("incomplete metadata = bytes:%d hash:%q, want unavailable", e.ReqBytes, e.ReqSHA256)
	}
	if !strings.Contains(e.Note, "request-body-incomplete") || !strings.Contains(e.Note, "transport-error") {
		t.Errorf("note = %q, want incomplete-body and transport-error markers", e.Note)
	}
}

func TestRequestWithoutReceiptContextIsRecorded(t *testing.T) {
	req, err := NewRequest(context.Background(), http.MethodGet, "http://example.test/api/projects", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	rec := receipt.FromContext(req.Context())
	before := rec.EntryCount()
	transport := &receiptTransport{base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got := rec.EntryCount(); got != before+1 {
		t.Errorf("entries = %d, want %d", got, before+1)
	}
}

func TestAppendNote(t *testing.T) {
	tests := []struct {
		existing string
		next     string
		want     string
	}{
		{want: ""},
		{next: "next", want: "next"},
		{existing: "existing", want: "existing"},
		{existing: "first", next: "second", want: "first; second"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := appendNote(tt.existing, tt.next); got != tt.want {
				t.Errorf("appendNote(%q, %q) = %q, want %q", tt.existing, tt.next, got, tt.want)
			}
		})
	}
}

func TestWebSocketHandshakeIsRecorded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err == nil {
			_ = conn.Close(websocket.StatusNormalClosure, "")
		}
	}))
	defer srv.Close()

	rec := receipt.NewCurrent("test:vnc")
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := DialWebSocket(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("DialWebSocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if rec.EntryCount() != 1 {
		t.Fatalf("entries = %d, want 1", rec.EntryCount())
	}
	if got := rec.Entries[0].Category; got != "vnc-control" {
		t.Errorf("category = %q, want vnc-control", got)
	}
}

func TestFailedWebSocketHandshakeIsRecorded(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	rec := receipt.NewCurrent("test:vnc-failed-handshake")
	conn, _, err := DialWebSocket(context.Background(), "ws://"+addr, nil)
	if err == nil {
		if conn != nil {
			_ = conn.Close(websocket.StatusNormalClosure, "")
		}
		t.Fatal("DialWebSocket error = nil, want connection failure")
	}

	if rec.EntryCount() != 1 {
		t.Fatalf("entries = %d, want 1", rec.EntryCount())
	}
	e := rec.Entries[0]
	if e.Category != "vnc-control" {
		t.Errorf("category = %q, want vnc-control", e.Category)
	}
	if e.Note != "transport-error" {
		t.Errorf("note = %q, want generic transport error", e.Note)
	}
}

// TestFinalizeAndVerifyRoundTrip proves qmax-code can produce a signed receipt
// and verify it offline through the shared module.
func TestFinalizeAndVerifyRoundTrip(t *testing.T) {
	receipt.BaseDir = t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rec := receipt.NewCurrent("test:roundtrip")
	req, _ := NewRequest(context.Background(), http.MethodGet, srv.URL+"/api/projects", nil)
	resp, err := NewClient(5 * time.Second).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	path, err := rec.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	loaded, err := receipt.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := receipt.Verify(loaded); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if loaded.Summary.TotalRequests != 1 {
		t.Errorf("total_requests = %d, want 1", loaded.Summary.TotalRequests)
	}
}
