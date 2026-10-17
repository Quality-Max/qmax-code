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

// Mirrors codex_connection_test.go: ConnectClaudeCode must send the bearer
// token plus the credentials JSON, return a typed HTTPStatusError on
// failure, and never leak the credential through the error string.

func TestConnectClaudeCodeSendsBearerAndCredentials(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"connected":true,"status":"connected","account_label":"user@example.com"}`))
	}))
	defer server.Close()

	client := &APIClient{BaseURL: server.URL, APIKey: "qm-token", HTTP: server.Client()}
	credentials := `{"oauth_account_id":"acc-1"}`
	connection, err := client.ConnectClaudeCode(context.Background(), credentials)
	if err != nil {
		t.Fatalf("ConnectClaudeCode: %v", err)
	}
	if gotPath != "/api/integrations/claude-code/connect" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer qm-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotBody["auth_json"] != credentials {
		t.Fatalf("auth_json = %v, want the credentials verbatim", gotBody["auth_json"])
	}
	if !connection.Connected || connection.Status != "connected" || connection.AccountLabel != "user@example.com" {
		t.Fatalf("connection = %+v", connection)
	}
}

func TestConnectClaudeCodeReturnsTypedHTTPErrorWithoutCredentialLeak(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	client := &APIClient{BaseURL: server.URL, APIKey: "qm-token", HTTP: server.Client()}
	_, err := client.ConnectClaudeCode(context.Background(), `{"secret":"super-secret-oauth-token"}`)

	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected HTTPStatusError(401), got %v", err)
	}
	if statusErr.Message != "unauthorized" {
		t.Fatalf("message = %q, want server detail", statusErr.Message)
	}
	if strings.Contains(err.Error(), "super-secret-oauth-token") {
		t.Fatal("error leaked Claude Code credential")
	}
}

func TestConnectClaudeCodeFallsBackToStatusText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `upstream broke`, http.StatusBadGateway) // non-JSON body
	}))
	defer server.Close()

	client := &APIClient{BaseURL: server.URL, APIKey: "qm-token", HTTP: server.Client()}
	_, err := client.ConnectClaudeCode(context.Background(), `{}`)

	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected HTTPStatusError(502), got %v", err)
	}
	if statusErr.Message != http.StatusText(http.StatusBadGateway) {
		t.Fatalf("message = %q, want %q", statusErr.Message, http.StatusText(http.StatusBadGateway))
	}
}

func TestConnectClaudeCodeFailsOnNonJSONSuccessBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server.Close()

	client := &APIClient{BaseURL: server.URL, APIKey: "qm-token", HTTP: server.Client()}
	if _, err := client.ConnectClaudeCode(context.Background(), `{}`); err == nil {
		t.Fatal("expected decode error for non-JSON 200 body")
	}
}

func TestConnectClaudeCodeFailsOnInvalidBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	client := &APIClient{BaseURL: "http://exa mple.com", APIKey: "qm-token", HTTP: server.Client()}
	_, err := client.ConnectClaudeCode(context.Background(), `{}`)
	if err == nil || !strings.Contains(err.Error(), "create Claude Code connection request") {
		t.Fatalf("expected request-build error, got %v", err)
	}
}
