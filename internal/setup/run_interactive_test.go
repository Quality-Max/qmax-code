package setup

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
)

// withStdinPipe replaces os.Stdin with a pipe preloaded with input and the
// write end already closed, so trailing prompts observe EOF. Restores the
// original stdin in cleanup. Tests using it must not run in parallel.
func withStdinPipe(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write stdin fixture: %v", err)
	}
	w.Close()

	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		r.Close()
	})
}

func TestRunInteractiveStandaloneChoiceSkipsAccount(t *testing.T) {
	withTempHome(t)
	// "4" = "Skip — use standalone local mode (no account needed)".
	withStdinPipe(t, "4\n")

	auth, projectID, err := RunInteractive()
	if !errors.Is(err, ErrStandaloneSkip) {
		t.Fatalf("RunInteractive() error = %v, want ErrStandaloneSkip", err)
	}
	if auth != nil || projectID != 0 {
		t.Fatalf("standalone exit returned auth=%v projectID=%d, want nil/0", auth, projectID)
	}
	if cfg := api.LoadQMaxCodeConfig(); !cfg.LocalOnly {
		t.Fatal("standalone choice did not persist local_only=true")
	}
}

func TestRunInteractiveAPIKeyFlowEndsLoggedInWithProject(t *testing.T) {
	withTempHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "env-provided-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			if got := r.Header.Get("Authorization"); got != "Bearer interactive-flow" {
				t.Errorf("Authorization = %q, want stripped flow key", got)
			}
			_, _ = w.Write([]byte(`{"email":"flow@example.test","id":"u-flow"}`))
		case "/api/projects":
			_, _ = w.Write([]byte(`[{"id":7,"name":"Alpha"},{"id":8,"name":"Beta"}]`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	t.Setenv("QUALITYMAX_URL", server.URL)

	// The key paste is stubbed; only the menu choice comes from stdin.
	origPrompt := promptAPIKey
	promptAPIKey = func(string) string { return "qm-interactive-flow" }
	defer func() { promptAPIKey = origPrompt }()

	// "3" = "I have an API key already". Every later menu hits EOF and takes
	// its default: the project chooser picks the first listed project.
	withStdinPipe(t, "3\n")

	auth, projectID, err := RunInteractive()
	if err != nil {
		t.Fatalf("RunInteractive: %v", err)
	}
	if auth == nil || auth.Email != "flow@example.test" {
		t.Fatalf("auth = %+v, want logged-in config", auth)
	}
	if projectID != 7 {
		t.Fatalf("projectID = %d, want 7 (first project is the EOF default)", projectID)
	}
	if cfg := api.LoadQMaxCodeConfig(); cfg.DefaultProject != 7 {
		t.Fatalf("DefaultProject = %d, want 7 persisted", cfg.DefaultProject)
	}
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "env-provided-key" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want the pre-existing env key kept", got)
	}
}

func TestRunInteractiveEmptyKeyTwiceFailsWithWrappedSentinel(t *testing.T) {
	withTempHome(t)

	// Both the initial paste and the retry-after-recovery paste come back
	// empty; the recovery menu then hits EOF and defaults to "try again".
	origPrompt := promptAPIKey
	promptAPIKey = func(string) string { return "" }
	defer func() { promptAPIKey = origPrompt }()

	withStdinPipe(t, "3\n")

	auth, _, err := RunInteractive()
	if err == nil || !errors.Is(err, ErrEmptyAPIKey) {
		t.Fatalf("RunInteractive() error = %v, want wrapped ErrEmptyAPIKey", err)
	}
	if !strings.Contains(err.Error(), "interactive login") {
		t.Fatalf("error = %v, want interactive login wrapper", err)
	}
	if auth != nil {
		t.Fatalf("auth = %+v, want nil on failure", auth)
	}
}
