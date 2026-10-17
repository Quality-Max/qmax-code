package api

import "testing"

// Provider key storage sits on the OS keychain with env-var fallback. These
// tests cover the unknown-provider rejection and the deterministic
// env-fallback / helper-unavailable paths; they never touch the real
// keychain (PATH is emptied for the keychain lookups).

func TestSaveProviderKeyRejectsUnknownProvider(t *testing.T) {
	err := SaveProviderKey("nope", "k")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestSaveProviderKeyFailsWithoutKeychainHelper(t *testing.T) {
	emptyPATH(t)
	if err := SaveProviderKey("groq", "gsk-key"); err == nil {
		t.Error("SaveProviderKey should fail when the keychain helper is unavailable")
	}
}

func TestDeleteProviderKeyFailsWithoutKeychainHelper(t *testing.T) {
	emptyPATH(t)
	if err := DeleteProviderKey("groq"); err == nil {
		t.Error("DeleteProviderKey should fail when the keychain helper is unavailable")
	}
}

func TestLoadProviderKeyFallsBackToEnvVar(t *testing.T) {
	emptyPATH(t)
	t.Setenv("GROQ_API_KEY", "  gsk-trimmed  ")
	if got := LoadProviderKey("groq"); got != "gsk-trimmed" {
		t.Errorf("LoadProviderKey = %q, want env value trimmed to gsk-trimmed", got)
	}
}

func TestLoadProviderKeyEmptyWhenNoKeyAnywhere(t *testing.T) {
	emptyPATH(t)
	t.Setenv("GROQ_API_KEY", "")
	if got := LoadProviderKey("groq"); got != "" {
		t.Errorf("LoadProviderKey = %q, want empty with no keychain entry and no env var", got)
	}
}

func TestProviderKeySetUsesEnvFallback(t *testing.T) {
	emptyPATH(t)
	t.Setenv("GROQ_API_KEY", "gsk-live")
	if !ProviderKeySet("groq") {
		t.Error("ProviderKeySet should be true when the env var is set")
	}
	t.Setenv("GROQ_API_KEY", "")
	if ProviderKeySet("groq") {
		t.Error("ProviderKeySet should be false with no key available")
	}
}
