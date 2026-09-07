package setup

import "testing"

func TestCodexDefersPermissionPolicyToCodexConfiguration(t *testing.T) {
	if qmaxSelectsPermissionMode("codex") {
		t.Fatal("Codex activation must not present a qmax permission selector")
	}
	for _, backend := range []string{"cc", "opencode", "agy"} {
		if !qmaxSelectsPermissionMode(backend) {
			t.Fatalf("%s unexpectedly stopped using qmax permission selection", backend)
		}
	}
}

func TestOrchConsentCLICommandUsesAgyBinary(t *testing.T) {
	if got := orchConsentCLICommand("agy", "Antigravity"); got != "agy" {
		t.Fatalf("agy command = %q, want agy", got)
	}
	if got := orchConsentCLICommand("cc", "Claude Code"); got != "claude code" {
		t.Fatalf("cc command = %q, want claude code", got)
	}
	if got := orchConsentCLICommand("codex", "Codex"); got != "codex" {
		t.Fatalf("codex command = %q, want codex", got)
	}
	if got := orchConsentCLICommand("opencode", "opencode"); got != "opencode" {
		t.Fatalf("opencode command = %q, want opencode", got)
	}
}
