package setup

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestMaskKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"short", "••••"},                    // short keys never reveal length
		{"12345678", "••••"},                 // exactly 8 chars still masked
		{"qm-abcdefgh1234", "qm-a•••1234"},   // long keys show first/last 4
		{"sk-ant-0123456789", "sk-a•••6789"}, // anthropic-style key
	}
	for _, tt := range tests {
		if got := maskKey(tt.key); got != tt.want {
			t.Errorf("maskKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestReadSecretPipeFallbackTrimsAndReturns(t *testing.T) {
	// Piped stdin: EnableRawMode fails, so ReadSecret uses the plain-reader
	// fallback and must still return the trimmed secret.
	withStdinPipe(t, "  qm-secret-value-987  \n")
	got := ReadSecret("prompt: ")
	if got != "qm-secret-value-987" {
		t.Fatalf("ReadSecret = %q, want trimmed key", got)
	}
}

func TestReadSecretPipeFallbackEmptyInput(t *testing.T) {
	withStdinPipe(t, "\n")
	if got := ReadSecret("prompt: "); got != "" {
		t.Fatalf("ReadSecret = %q, want empty", got)
	}
}

func TestWaitForEnterConsumesALine(t *testing.T) {
	withStdinPipe(t, "\n")
	waitForEnter() // returns as soon as the line arrives
}

func TestParseJSONAndMapValueHelpers(t *testing.T) {
	var projects []map[string]interface{}
	if err := parseJSON(`[{"id":1}]`, &projects); err != nil || len(projects) != 1 {
		t.Fatalf("parseJSON valid = err:%v len:%d, want 1 entry", err, len(projects))
	}
	if err := parseJSON(`{`, &projects); err == nil {
		t.Fatal("parseJSON must reject invalid JSON")
	}

	m := map[string]interface{}{
		"name":      "alpha",
		"float":     7.0,
		"int":       3,
		"null":      nil,
		"wrongtype": "7",
	}
	if got := strVal2(m, "name"); got != "alpha" {
		t.Errorf("strVal2(name) = %q, want alpha", got)
	}
	if got := strVal2(m, "missing"); got != "" {
		t.Errorf("strVal2(missing) = %q, want empty", got)
	}
	if got := strVal2(m, "null"); got != "" {
		t.Errorf("strVal2(null) = %q, want empty", got)
	}
	if got := intVal2(m, "float"); got != 7 {
		t.Errorf("intVal2(float) = %d, want 7 (JSON numbers decode as float64)", got)
	}
	if got := intVal2(m, "int"); got != 3 {
		t.Errorf("intVal2(int) = %d, want 3", got)
	}
	if got := intVal2(m, "missing"); got != 0 {
		t.Errorf("intVal2(missing) = %d, want 0", got)
	}
	if got := intVal2(m, "null"); got != 0 {
		t.Errorf("intVal2(null) = %d, want 0", got)
	}
	if got := intVal2(m, "wrongtype"); got != 0 {
		t.Errorf("intVal2(string) = %d, want 0", got)
	}
}

func TestChooseFromRawInputClampsAndFiltersDigits(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "down clamps at last option", input: "jjjjj\n", want: 3},
		{name: "digit beyond range ignored", input: "9j\n", want: 1},
		{name: "multi-digit candidate ignored", input: "12\n", want: 0},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			redraws := 0
			got, confirmed := chooseFromRawInput(
				bufio.NewReader(strings.NewReader(tt.input)),
				4,
				func(int, string) { redraws++ },
			)
			if !confirmed {
				t.Fatal("input did not confirm a selection")
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

func TestChooseFromRawInputUpClampsAtFirstOption(t *testing.T) {
	// Up at the top is a no-op: the selection stays put and, by design, no
	// redraw is requested.
	got, confirmed := chooseFromRawInput(
		bufio.NewReader(strings.NewReader("kkk\n")),
		4,
		func(int, string) {},
	)
	if !confirmed || got != 0 {
		t.Fatalf("selection = (%d, %v), want (0, true)", got, confirmed)
	}
}

func TestChooseFromRawInputCancel(t *testing.T) {
	// Ctrl+C cancels but is still "confirmed" (a deliberate choice of option 0).
	got, confirmed := chooseFromRawInput(
		bufio.NewReader(strings.NewReader("\x03")),
		4,
		func(int, string) {},
	)
	if !confirmed || got != 0 {
		t.Fatalf("cancel = (%d, %v), want (0, true)", got, confirmed)
	}
}

func TestChooseFromRawInputEOFIsNotConfirmed(t *testing.T) {
	if _, confirmed := chooseFromRawInput(
		bufio.NewReader(strings.NewReader("")),
		4,
		func(int, string) {},
	); confirmed {
		t.Fatal("EOF must not confirm a selection")
	}
}

func TestReadChooserEventTruncatedEscapeSequences(t *testing.T) {
	// A lone ESC or a CSI cut off mid-sequence must never confirm or move the
	// selection; the reader just runs out of bytes.
	for _, input := range []string{"\x1b", "\x1b[", "\x1b[?"} {
		if _, confirmed := chooseFromRawInput(
			bufio.NewReader(strings.NewReader(input)),
			3,
			func(int, string) {},
		); confirmed {
			t.Fatalf("truncated escape %q must not confirm", input)
		}
	}
}

func TestPromptAnthropicKeyPasteEmptyKeySkipsKeychain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "")

	origSave := saveAnthropicKey
	saveAnthropicKey = func(string) error {
		t.Error("empty paste must not write the keychain")
		return nil
	}
	defer func() { saveAnthropicKey = origSave }()

	origPrompt := promptAPIKey
	promptAPIKey = func(string) string { return "" }
	defer func() { promptAPIKey = origPrompt }()

	// "1" selects "Paste Anthropic key" via the numeric fallback.
	withStdinPipe(t, "1\n")

	if got := promptAnthropicKey(); got != "" {
		t.Fatalf("promptAnthropicKey() = %q, want empty for empty paste", got)
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		t.Fatal("empty paste must not export ANTHROPIC_API_KEY")
	}
}

func TestPromptAnthropicKeyKeychainFailureStillSetsEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "")

	origSave := saveAnthropicKey
	saveAnthropicKey = func(string) error { return errors.New("keychain locked") }
	defer func() { saveAnthropicKey = origSave }()

	origPrompt := promptAPIKey
	promptAPIKey = func(string) string { return "sk-ant-session-only" }
	defer func() { promptAPIKey = origPrompt }()

	withStdinPipe(t, "1\n")

	if got := promptAnthropicKey(); got != "sk-ant-session-only" {
		t.Fatalf("promptAnthropicKey() = %q, want pasted key despite keychain failure", got)
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "sk-ant-session-only" {
		t.Fatal("key must remain set for this session when keychain save fails")
	}
}
