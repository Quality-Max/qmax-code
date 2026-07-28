package setup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestMintCLIAPIToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/api/auth/api-token" {
			t.Errorf("path = %q, want /api/auth/api-token", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer qm-user-access" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}

		var body struct {
			Name        string `json:"name"`
			ExpiresDays int    `json:"expires_days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Name != "qmax-code CLI" {
			t.Errorf("name = %q, want qmax-code CLI", body.Name)
		}
		if body.ExpiresDays != cliAPITokenLifetimeDays {
			t.Errorf("expires_days = %d, want %d", body.ExpiresDays, cliAPITokenLifetimeDays)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"qm-registered-api-token"}`))
	}))
	defer server.Close()

	token, err := mintCLIAPIToken(context.Background(), server.Client(), server.URL+"/", "qm-user-access")
	if err != nil {
		t.Fatalf("mintCLIAPIToken: %v", err)
	}
	if token != "qm-registered-api-token" {
		t.Fatalf("token = %q, want registered API token", token)
	}
}

func TestMintCLIAPITokenRejectsFailedOrInvalidResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "server failure", statusCode: http.StatusInternalServerError, body: `{"detail":"failed"}`},
		{name: "missing prefix", statusCode: http.StatusOK, body: `{"token":"not-an-api-token"}`},
		{name: "empty prefixed token", statusCode: http.StatusOK, body: `{"token":"qm-"}`},
		{name: "invalid json", statusCode: http.StatusOK, body: `{`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			if _, err := mintCLIAPIToken(context.Background(), server.Client(), server.URL, "qm-user-access"); err == nil {
				t.Fatal("mintCLIAPIToken unexpectedly succeeded")
			}
		})
	}
}

func TestCompleteBrowserLoginMintsAndSavesRegisteredToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"qm-registered-api-token"}`))
	}))
	defer server.Close()

	poll := cliPollResponse{
		Token:  "qm-browser-access-token",
		Email:  "user@example.test",
		UserID: "user-123",
	}
	var saved *api.AuthConfig
	cfg, err := completeBrowserLogin(
		context.Background(),
		server.Client(),
		server.URL,
		poll,
		func(got *api.AuthConfig) error {
			saved = got
			return nil
		},
	)
	if err != nil {
		t.Fatalf("completeBrowserLogin: %v", err)
	}
	if cfg.APIKey != "qm-registered-api-token" {
		t.Fatalf("APIKey = %q, want minted registered token", cfg.APIKey)
	}
	if cfg.APIKey == poll.Token {
		t.Fatal("saved browser access token instead of minted registered token")
	}
	if saved != cfg {
		t.Fatal("saved config does not match returned config")
	}
	if cfg.Email != poll.Email || cfg.UserID != poll.UserID || cfg.CloudURL != server.URL {
		t.Fatalf("metadata not preserved: %+v", cfg)
	}
}

func TestCompleteBrowserLoginReturnsConfigWhenSaveFails(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"qm-registered-api-token"}`))
	}))
	defer server.Close()

	saveErr := errors.New("save failed")
	cfg, err := completeBrowserLogin(
		context.Background(),
		server.Client(),
		server.URL,
		cliPollResponse{Token: "qm-browser-access-token"},
		func(*api.AuthConfig) error { return saveErr },
	)
	if !errors.Is(err, saveErr) {
		t.Fatalf("error = %v, want save failure", err)
	}
	if cfg == nil || cfg.APIKey != "qm-registered-api-token" {
		t.Fatalf("config = %+v, want minted token for recovery", cfg)
	}
}

func TestBrowserLoginHTTPClientDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected = true
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	req, err := http.NewRequest(http.MethodPost, source.URL, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	resp, err := newBrowserLoginHTTPClient().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTemporaryRedirect)
	}
	if redirected {
		t.Fatal("redirect target was contacted")
	}
}
