package setup

import "testing"

func TestCodexDefersPermissionPolicyToCodexConfiguration(t *testing.T) {
	if qmaxSelectsPermissionMode("codex") {
		t.Fatal("Codex activation must not present a qmax permission selector")
	}
	for _, backend := range []string{"cc", "opencode"} {
		if !qmaxSelectsPermissionMode(backend) {
			t.Fatalf("%s unexpectedly stopped using qmax permission selection", backend)
		}
	}
}
