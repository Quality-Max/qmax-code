package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. Terminal.PrintError writes via fmt.Printf
// directly to os.Stdout, so this is the only way to observe it from a test.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

// A real Cerebras request failure (bad model, auth error, malformed request,
// network hiccup, etc.) must surface its actual reason to the user rather
// than the generic top-level "cerebras backend request failed" fallback.
//
// Regression test for a bug where the cleanup `cancel()` call ran before the
// `ctx.Err() != nil` check meant to detect a genuine user-initiated
// interrupt (via Agent.CancelCurrent). Since cancel() unconditionally marks
// the context done, ctx.Err() was always non-nil by the time it was checked,
// so every failure — interrupted or not — took the silent "interrupted"
// branch and the detailed term.PrintError message never ran.
func TestRunCerebrasAgentSurfacesRequestErrorDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"invalid model gpt-oss-120b"}}`))
	}))
	defer srv.Close()

	a := &Agent{
		Cfg: AgentConfig{Context: &api.SessionContext{}},
		Cerebras: &CerebrasClient{
			BaseURL: srv.URL,
			Model:   "gpt-oss-120b",
			APIKey:  "csk-test",
			HTTP:    srv.Client(),
		},
	}

	var out string
	var ok bool
	out = captureStdout(t, func() {
		_, ok = a.RunCerebrasAgent(&tui.Terminal{})
	})

	if ok {
		t.Fatalf("expected ok=false for a failed request")
	}
	if !strings.Contains(out, "Cerebras request failed") {
		t.Fatalf("expected the detailed Cerebras error to be printed, got: %q", out)
	}
	if !strings.Contains(out, "invalid model gpt-oss-120b") {
		t.Fatalf("expected the underlying server error to be surfaced, got: %q", out)
	}
}
