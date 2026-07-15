package sysutil

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	receipt "github.com/Quality-Max/qmax-receipt"
	"github.com/getsentry/sentry-go"
)

func TestErrorReportingUsesReceiptClient(t *testing.T) {
	oldInit := initSentry
	t.Cleanup(func() { initSentry = oldInit })
	t.Setenv(telemetryEnabledEnv, "1")
	t.Setenv(telemetryDSNEnv, "https://public@example.test/1")

	var client *http.Client
	initSentry = func(options sentry.ClientOptions) error {
		client = options.HTTPClient
		return nil
	}
	InitErrorReporting("test")
	if client == nil {
		t.Fatal("InitErrorReporting did not configure an HTTP client")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rec := receipt.NewCurrent("test:telemetry")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/api/42/envelope/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	deadline := time.Now().Add(time.Second)
	for rec.EntryCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if rec.EntryCount() != 1 {
		t.Fatalf("entries = %d, want 1", rec.EntryCount())
	}
	if got := rec.Entries[0].Category; got != "telemetry" {
		t.Errorf("category = %q, want telemetry", got)
	}
}
