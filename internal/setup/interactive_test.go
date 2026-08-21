package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestParseChoiceLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
		n    int
		want int
	}{
		{name: "first option", line: "1", n: 4, want: 0},
		{name: "last option", line: "4", n: 4, want: 3},
		{name: "middle option", line: "3", n: 4, want: 2},
		{name: "empty line", line: "", n: 4, want: -1},
		{name: "zero", line: "0", n: 4, want: -1},
		{name: "above range", line: "5", n: 4, want: -1},
		{name: "negative", line: "-1", n: 4, want: -1},
		{name: "non-numeric", line: "yes", n: 4, want: -1},
		{name: "padded digit", line: " 2 ", n: 4, want: 1},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parseChoiceLine(tt.line, tt.n); got != tt.want {
				t.Fatalf("parseChoiceLine(%q, %d) = %d, want %d", tt.line, tt.n, got, tt.want)
			}
		})
	}
}

func TestChooseFromRawInputNavigation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "down arrow", input: "\x1b[B\n", want: 1},
		{name: "up arrow", input: "j\x1b[A\n", want: 0},
		{name: "vim and arrow navigation", input: "j\x1b[Bk\n", want: 1},
		{name: "direct digit", input: "3\n", want: 2},
		{name: "ordinary key after escape is preserved", input: "\x1bj\n", want: 1},
		{name: "malformed CSI is ignored as a unit", input: "\x1b[?1049hj\n", want: 1},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			redraws := 0
			got, confirmed := chooseFromRawInput(
				bufio.NewReader(strings.NewReader(tt.input)),
				4,
				func(int, string) { redraws++ },
			)
			if !confirmed {
				t.Fatal("raw input did not confirm a selection")
			}
			if got != tt.want {
				t.Fatalf("selection = %d, want %d", got, tt.want)
			}
			if redraws == 0 {
				t.Fatal("navigation did not request a menu redraw")
			}
		})
	}
}

func TestUseStandaloneModePersistsLocalOnly(t *testing.T) {
	// api config resolves ~/.qmax-code from HOME at call time, so a temp
	// HOME isolates the persisted local_only write.
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", t.TempDir())
	defer os.Setenv("HOME", origHome)

	if err := useStandaloneMode(); !errors.Is(err, ErrStandaloneSkip) {
		t.Fatalf("useStandaloneMode() error = %v, want ErrStandaloneSkip", err)
	}

	cfg := api.LoadQMaxCodeConfig()
	if !cfg.LocalOnly {
		t.Fatal("local_only not persisted after standalone selection")
	}

	// Second run must stay idempotent (no error, still standalone).
	if err := useStandaloneMode(); !errors.Is(err, ErrStandaloneSkip) {
		t.Fatalf("second useStandaloneMode() error = %v, want ErrStandaloneSkip", err)
	}
}

func TestLoginWithKeyPromptEmptyInputReturnsSentinel(t *testing.T) {
	// Empty stdin must surface ErrEmptyAPIKey (not a generic error) so the
	// onboarding retry/standalone branch can match on it.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString("\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	w.Close()

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	_, err = loginWithKeyPrompt()
	if !errors.Is(err, ErrEmptyAPIKey) {
		t.Fatalf("loginWithKeyPrompt() error = %v, want ErrEmptyAPIKey", err)
	}
}

func TestRecoverFromEmptyKeyStandaloneBranch(t *testing.T) {
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", t.TempDir())
	defer os.Setenv("HOME", origHome)

	// Piped stdin → PromptChoice takes the numeric fallback; "2" selects
	// "Skip — use standalone local mode".
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString("2\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	w.Close()

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	_, err = recoverFromEmptyKey()
	if !errors.Is(err, ErrStandaloneSkip) {
		t.Fatalf("recoverFromEmptyKey() error = %v, want ErrStandaloneSkip", err)
	}
	if cfg := api.LoadQMaxCodeConfig(); !cfg.LocalOnly {
		t.Fatal("standalone branch did not persist local_only")
	}
}

func TestRecoverFromEmptyKeyRetryBranchAsksForKeyAgain(t *testing.T) {
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", t.TempDir())
	defer os.Setenv("HOME", origHome)

	// "1" selects "Try again — paste a key"; the retry prompt then hits EOF
	// (numeric reader buffers the whole pipe) and must surface the sentinel
	// again rather than a generic failure.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString("1\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	w.Close()

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	_, err = recoverFromEmptyKey()
	if !errors.Is(err, ErrEmptyAPIKey) {
		t.Fatalf("recoverFromEmptyKey() error = %v, want ErrEmptyAPIKey from retry prompt", err)
	}
	if cfg := api.LoadQMaxCodeConfig(); cfg.LocalOnly {
		t.Fatal("retry branch must not persist standalone mode")
	}
}
