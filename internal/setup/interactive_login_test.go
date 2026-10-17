package setup

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
)

// withNoPATH removes PATH for the duration of the test so openBrowser's
// exec.Command("open", ...) fails at LookPath and never spawns a process.
// Browser-based flows still run everything else (HTTP, polling, saving).
func withNoPATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "")
}

func TestLoginInteractiveReadsKeyFromPipeAndValidates(t *testing.T) {
	withTempHome(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" {
			t.Errorf("path = %q, want /api/me", r.URL.Path)
		}
		// LoginWithAPIKey strips the qmax- prefix before sending the bearer.
		if got := r.Header.Get("Authorization"); got != "Bearer piped-key-123" {
			t.Errorf("Authorization = %q, want stripped piped key", got)
		}
		_, _ = w.Write([]byte(`{"email":"pipe@example.test","id":"u-pipe"}`))
	}))
	defer server.Close()
	t.Setenv("QUALITYMAX_URL", server.URL)

	// Piped stdin: ReadSecret cannot enter raw mode, so it takes the plain
	// read + mask-after fallback.
	withStdinPipe(t, "  qm-piped-key-123  \n")

	auth, err := LoginInteractive()
	if err != nil {
		t.Fatalf("LoginInteractive: %v", err)
	}
	if auth.APIKey != "qm-piped-key-123" {
		t.Fatalf("APIKey = %q, want trimmed pasted key", auth.APIKey)
	}
	if auth.Email != "pipe@example.test" || auth.UserID != "u-pipe" {
		t.Fatalf("auth metadata = %+v", auth)
	}
	if auth.CloudURL != server.URL {
		t.Fatalf("CloudURL = %q, want %q", auth.CloudURL, server.URL)
	}
}

func TestLoginInteractiveEmptyKeyReturnsSentinel(t *testing.T) {
	withStdinPipe(t, "\n")
	if _, err := LoginInteractive(); !errors.Is(err, ErrEmptyAPIKey) {
		t.Fatalf("LoginInteractive() error = %v, want ErrEmptyAPIKey", err)
	}
}

func TestLoginViaBrowserPendingThenAuthorizedSavesRegisteredToken(t *testing.T) {
	home := withTempHome(t)
	withNoPATH(t)

	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/cli-login":
			if r.Method != http.MethodPost {
				t.Errorf("cli-login method = %q, want POST", r.Method)
			}
			_, _ = w.Write([]byte(`{"code":"c&1","expires_at":"soon","auth_url":""}`))
		case "/api/auth/cli-poll":
			polls++
			// The code is server-supplied and URL-reserved; it must arrive
			// percent-encoded so the request is well-formed.
			if got := r.URL.Query().Get("code"); got != "c&1" {
				t.Errorf("poll code = %q, want c&1", got)
			}
			if polls == 1 {
				_, _ = w.Write([]byte(`{"status":"pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"authorized","token":"qm-user-access","email":"browser@example.test","user_id":"u-9"}`))
		case "/api/auth/api-token":
			if got := r.Header.Get("Authorization"); got != "Bearer qm-user-access" {
				t.Errorf("api-token Authorization = %q", got)
			}
			_, _ = w.Write([]byte(`{"token":"qm-registered-token"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	t.Setenv("QUALITYMAX_URL", server.URL)

	auth, err := LoginViaBrowser()
	if err != nil {
		t.Fatalf("LoginViaBrowser: %v", err)
	}
	if polls != 2 {
		t.Fatalf("polls = %d, want 2 (pending then authorized)", polls)
	}
	if auth.APIKey != "qm-registered-token" {
		t.Fatalf("APIKey = %q, want minted registered token", auth.APIKey)
	}
	if auth.Email != "browser@example.test" || auth.UserID != "u-9" {
		t.Fatalf("auth metadata = %+v", auth)
	}

	// The browser access token is an exchange input, never the persisted key.
	data, err := os.ReadFile(filepath.Join(home, ".qmax-code", "auth.json"))
	if err != nil {
		t.Fatalf("read saved auth: %v", err)
	}
	var saved api.AuthConfig
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("decode saved auth: %v", err)
	}
	if saved.APIKey != "qm-registered-token" {
		t.Fatalf("saved APIKey = %q, want registered token", saved.APIKey)
	}
	if saved.Email != "browser@example.test" {
		t.Fatalf("saved email = %q", saved.Email)
	}
}

func TestLoginViaBrowserExpiredCodeFailsFast(t *testing.T) {
	withTempHome(t)
	withNoPATH(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/auth/cli-login" {
			_, _ = w.Write([]byte(`{"code":"expired-code","auth_url":""}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"expired"}`))
	}))
	defer server.Close()
	t.Setenv("QUALITYMAX_URL", server.URL)

	_, err := LoginViaBrowser()
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("LoginViaBrowser() error = %v, want expired failure", err)
	}
}

func TestLoginViaBrowserRejectsBadHandshake(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{
			name: "non-200 login endpoint",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			},
			wantErr: "CLI login failed",
		},
		{
			name: "invalid json login response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("not-json"))
			},
			wantErr: "invalid response",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			withTempHome(t)
			server := httptest.NewServer(tt.handler)
			defer server.Close()
			t.Setenv("QUALITYMAX_URL", server.URL)

			_, err := LoginViaBrowser()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoginViaBrowser() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoginViaBrowserUnreachableCloud(t *testing.T) {
	withTempHome(t)
	withNoPATH(t)

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()
	t.Setenv("QUALITYMAX_URL", url)

	_, err := LoginViaBrowser()
	if err == nil || !strings.Contains(err.Error(), "cannot reach QualityMax") {
		t.Fatalf("LoginViaBrowser() error = %v, want unreachable failure", err)
	}
}
