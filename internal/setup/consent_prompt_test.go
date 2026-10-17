package setup

import (
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
)

// PromptOrchConsent is driven entirely through piped stdin: it builds one
// bufio.Reader over os.Stdin and reuses it for every prompt, so a multi-line
// fixture answers each question in order.
func TestPromptOrchConsentFlows(t *testing.T) {
	tests := []struct {
		name        string
		backend     string
		input       string
		seedMode    string
		seedGlobal  bool
		preInstall  bool // install the backend's MCP config before prompting
		wantProceed bool
		wantMode    string
		wantGlobal  bool
	}{
		{
			name: "cc standard mode plus global install", backend: "cc",
			input: "1\ny\n", wantProceed: true, wantMode: "standard", wantGlobal: true,
		},
		{
			name: "cc unattended confirmed then global install", backend: "cc",
			input: "2\ny\ny\n", wantProceed: true, wantMode: "unattended", wantGlobal: true,
		},
		{
			name: "unattended confirmation declined cancels", backend: "cc",
			input: "2\nn\n", wantProceed: false,
		},
		{
			name: "unknown answer cancels activation", backend: "cc",
			input: "n\n", wantProceed: false,
		},
		{
			name: "empty line defaults to standard, EOF declines install", backend: "cc",
			input: "\n", wantProceed: true, wantMode: "standard", wantGlobal: false,
		},
		{
			name: "eof everywhere still defaults to standard", backend: "cc",
			input: "", wantProceed: true, wantMode: "standard", wantGlobal: false,
		},
		{
			name: "agy accepts the s alias for standard", backend: "agy",
			input: "s\n", wantProceed: true, wantMode: "standard", wantGlobal: false,
		},
		{
			name: "codex skips the permission prompt entirely", backend: "codex",
			input: "y\n", wantProceed: true, wantMode: "", wantGlobal: true,
		},
		{
			name: "opencode skips the global install prompt", backend: "opencode",
			input: "1\n", wantProceed: true, wantMode: "standard", wantGlobal: false,
		},
		{
			name: "pre-consented config never prompts", backend: "cc",
			input: "", seedMode: "standard", seedGlobal: true,
			wantProceed: true, wantMode: "standard", wantGlobal: true,
		},
		{
			name: "already-installed backend skips the install prompt", backend: "cc",
			input: "1\n", preInstall: true,
			wantProceed: true, wantMode: "standard", wantGlobal: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			withTempHome(t)
			if tt.preInstall {
				if _, err := InstallCC(); err != nil {
					t.Fatalf("pre-install cc MCP config: %v", err)
				}
			}

			cfg := &api.Config{
				OrchPermissionMode: tt.seedMode,
				OrchGlobalInstall:  tt.seedGlobal,
			}
			withStdinPipe(t, tt.input)

			res := PromptOrchConsent(cfg, tt.backend)

			if res.Proceed != tt.wantProceed {
				t.Fatalf("Proceed = %v, want %v", res.Proceed, tt.wantProceed)
			}
			if res.PermissionMode != tt.wantMode {
				t.Fatalf("PermissionMode = %q, want %q", res.PermissionMode, tt.wantMode)
			}
			if res.GlobalInstall != tt.wantGlobal {
				t.Fatalf("GlobalInstall = %v, want %v", res.GlobalInstall, tt.wantGlobal)
			}
		})
	}
}
