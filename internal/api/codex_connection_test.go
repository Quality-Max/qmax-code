package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectCodexUsesBearerAndDoesNotSendUserID(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"connected":true,"status":"connected","account_label":"user@example.com"}`))
	}))
	defer server.Close()

	client := &APIClient{BaseURL: server.URL, APIKey: "qm-token", HTTP: server.Client()}
	connection, err := client.ConnectCodex(context.Background(), `{"tokens":{"access_token":"a","refresh_token":"r"}}`)
	if err != nil {
		t.Fatalf("ConnectCodex: %v", err)
	}
	if gotAuth != "Bearer qm-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if _, ok := gotBody["user_id"]; ok {
		t.Fatal("request must not contain a target user_id")
	}
	if connection.AccountLabel != "user@example.com" {
		t.Fatalf("AccountLabel = %q", connection.AccountLabel)
	}
}

func TestConnectCodexReturnsTypedHTTPErrorWithoutCredentialLeak(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	client := &APIClient{BaseURL: server.URL, APIKey: "qm-token", HTTP: server.Client()}
	_, err := client.ConnectCodex(context.Background(), `{"tokens":{"refresh_token":"secret-refresh-token"}}`)

	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected HTTPStatusError(401), got %v", err)
	}
	if strings.Contains(err.Error(), "secret-refresh-token") {
		t.Fatal("error leaked Codex credential")
	}
}
