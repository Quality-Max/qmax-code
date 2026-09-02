package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// TestRedactStderrTail pins the defensive redaction applied to the stderr tail
// before it lands in a returned error (review finding on PR #181): a crashing
// subprocess must never echo a credential into the TUI or logs.
func TestRedactStderrTail(t *testing.T) {
	in := "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig\n" +
		"api_key=gsk_0123456789abcdefghij\n" +
		"sk-proj-0123456789abcdefghij\n" +
		"Error: Unexpected error G.includes is not a function"
	got := redactStderrTail(in)

	for _, leaked := range []string{"gsk_0123456789", "sk-proj-0123456789", "eyJhbGciOiJIUzI1NiIs"} {
		if strings.Contains(got, leaked) {
			t.Errorf("stderr tail leaked credential %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "G.includes is not a function") {
		t.Errorf("redaction must keep the diagnostic text, got: %s", got)
	}
	if !strings.Contains(got, "<redacted>") {
		t.Errorf("redaction should mark removed values, got: %s", got)
	}
}

// The Run-level tests drive the real spawn/parse/wait path through a stub
// `opencode` binary. The stub must answer the `run --help` probe
// (openCodeSupportsAutoFlag) with exit 0 so the flag support check is
// deterministic regardless of the package-global sync.Once state, and it logs
// every non-help invocation to a counts file so retry behaviour is assertable.

func writeOpenCodeStub(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a shell script")
	}
	stub := filepath.Join(dir, name)
	script := "#!/bin/sh\nif [ \"$2\" = \"--help\" ]; then exit 0; fi\n" + body
	if err := os.WriteFile(stub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return stub
}

func stubInvocationCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(strings.TrimSpace(string(data)), "\n") + 1
}

// TestOpenCodeRunRetriesAfterInternalCrash pins the resilience fix for the
// opencode internal JS crash ("G.includes is not a function", upstream
// anomalyco/opencode#28117 class): the process dies mid-turn with an empty
// result and no error event in the stream. The turn must be retried exactly
// once, and the retry must complete it.
func TestOpenCodeRunRetriesAfterInternalCrash(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "crashed-once")
	counts := filepath.Join(dir, "counts")
	stub := writeOpenCodeStub(t, dir, "opencode-stub",
		"echo x >> \""+counts+"\"\n"+
			"if [ ! -f \""+marker+"\" ]; then\n"+
			"  echo 'Error: Unexpected error G.includes is not a function' >&2\n"+
			"  touch \""+marker+"\"\n"+
			"  exit 1\n"+
			"fi\n"+
			`echo '{"type":"text","timestamp":1784907424583,"sessionID":"ses_retry0001","part":{"id":"prt_b","type":"text","text":"recovered"}}'`+"\n")

	a := &OpenCodeAgent{openCodeBin: stub, cfg: &api.Config{}}
	result, err := a.Run("hello", &tui.Terminal{})
	if err != nil {
		t.Fatalf("Run should recover via retry: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("result = %q, want %q", result, "recovered")
	}
	if n := stubInvocationCount(t, counts); n != 2 {
		t.Fatalf("stub invoked %d times, want exactly 2 (crash + retry)", n)
	}
	if a.sessionID != "ses_retry0001" {
		t.Errorf("sessionID = %q, want captured from the retry stream", a.sessionID)
	}
}

// TestOpenCodeRunSurfacesProviderRefusalWithoutRetry pins the diagnostics fix
// for masked provider refusals (model not in the subscription plan): the
// stream carries a status-code-less "Unexpected server error" event and the
// process exits 1 with no result. The real message must be surfaced — with a
// pointer to opencode's log where the entitlement text lives — and the turn
// must NOT be retried, because a provider refusal is deterministic.
func TestOpenCodeRunSurfacesProviderRefusalWithoutRetry(t *testing.T) {
	dir := t.TempDir()
	counts := filepath.Join(dir, "counts")
	stub := writeOpenCodeStub(t, dir, "opencode-stub",
		"echo x >> \""+counts+"\"\n"+
			`echo '{"type":"error","timestamp":1788348788916,"sessionID":"ses_refuse01","error":{"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_1f79e3db"}}}'`+"\n"+
			"exit 1\n")

	a := &OpenCodeAgent{openCodeBin: stub, cfg: &api.Config{}}
	result, err := a.Run("hello", &tui.Terminal{})
	if err == nil {
		t.Fatal("Run must fail when the provider refuses the turn")
	}
	if result != "" {
		t.Errorf("result = %q, want empty on refusal", result)
	}
	for _, want := range []string{"Unexpected server error", "opencode.log"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must mention %q so the real cause is findable", err.Error(), want)
		}
	}
	if n := stubInvocationCount(t, counts); n != 1 {
		t.Fatalf("stub invoked %d times, want exactly 1 — a provider refusal must not be retried", n)
	}
}

// TestOpenCodeRunCrashTwiceIncludesStderrTail ensures that when both attempts
// die in an internal crash, the returned error carries the stderr tail — the
// only witness to what opencode actually printed before dying.
func TestOpenCodeRunCrashTwiceIncludesStderrTail(t *testing.T) {
	dir := t.TempDir()
	counts := filepath.Join(dir, "counts")
	stub := writeOpenCodeStub(t, dir, "opencode-stub",
		"echo x >> \""+counts+"\"\n"+
			"echo 'Error: Unexpected error G.includes is not a function' >&2\n"+
			"exit 1\n")

	a := &OpenCodeAgent{openCodeBin: stub, cfg: &api.Config{}}
	_, err := a.Run("hello", &tui.Terminal{})
	if err == nil {
		t.Fatal("Run must fail when both attempts crash")
	}
	if !strings.Contains(err.Error(), "G.includes is not a function") {
		t.Errorf("error should include the stderr tail, got: %v", err)
	}
	if n := stubInvocationCount(t, counts); n != 2 {
		t.Fatalf("stub invoked %d times, want exactly 2 (crash + one retry)", n)
	}
}
