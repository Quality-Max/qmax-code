package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	receipt "github.com/Quality-Max/qmax-receipt"
)

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
