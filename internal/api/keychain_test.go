package api

import "testing"

// keychain.go shells out to the real OS keychain (macOS `security`,
// Linux `secret-tool`). Success paths would create or read real user
// keychain entries, so these tests only cover the deterministic failure
// branch where the helper binary cannot be resolved. The env-var fallback
// paths that sit on top of the keychain are covered in
// config_extra_test.go and providers_key_test.go.

func TestSaveToKeychainFailsWithoutHelper(t *testing.T) {
	emptyPATH(t)
	if err := SaveToKeychain("no-helper-account", "secret"); err == nil {
		t.Error("SaveToKeychain should fail when the keychain helper is unavailable")
	}
}

func TestLoadFromKeychainFailsWithoutHelper(t *testing.T) {
	emptyPATH(t)
	key, err := LoadFromKeychain("no-helper-account")
	if err == nil {
		t.Error("LoadFromKeychain should fail when the keychain helper is unavailable")
	}
	if key != "" {
		t.Errorf("expected empty key on failure, got %q", key)
	}
}

func TestDeleteFromKeychainFailsWithoutHelper(t *testing.T) {
	emptyPATH(t)
	if err := DeleteFromKeychain("no-helper-account"); err == nil {
		t.Error("DeleteFromKeychain should fail when the keychain helper is unavailable")
	}
}
