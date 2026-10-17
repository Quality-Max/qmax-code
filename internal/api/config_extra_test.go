package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Additional config.go coverage: narration normalization, Cerebras key
// validation, env overrides, and failure branches. Tests that mutate HOME or
// PATH must not run in parallel.

// emptyPATH replaces PATH with an empty temp dir for the duration of the
// test so OS-keychain helper binaries (security, secret-tool) cannot be
// resolved. This lets us exercise the keychain failure branches without
// touching the real OS keychain.
func emptyPATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestNormalizeNarrateToolCalls(t *testing.T) {
	cases := []struct{ in, want string }{
		{"off", "off"},
		{"NONE", "off"},     // case-insensitive
		{" Silent ", "off"}, // trimmed aliases
		{"full", "full"},
		{"VERBOSE", "full"},
		{"detailed", "full"},
		{"brief", "brief"},
		{"", "brief"}, // empty falls back to default
		{"nonsense", "brief"},
	}
	for _, tc := range cases {
		if got := NormalizeNarrateToolCalls(tc.in); got != tc.want {
			t.Errorf("NormalizeNarrateToolCalls(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateCerebrasKey(t *testing.T) {
	cases := []struct {
		key       string
		wantLooks bool
		wantErr   bool
	}{
		{"", false, true},
		{"csk-abc123", true, false},
		{"sk-proj-xyz", false, false},   // wrong prefix: soft signal, not an error
		{"csk-abc 123", false, true},    // embedded space
		{"csk-abc\t123", false, true},   // tab
		{"csk-bad\x01key", false, true}, // non-printable byte
	}
	for _, tc := range cases {
		looks, err := ValidateCerebrasKey(tc.key)
		if looks != tc.wantLooks {
			t.Errorf("ValidateCerebrasKey(%q) looks = %v, want %v", tc.key, looks, tc.wantLooks)
		}
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateCerebrasKey(%q) err = %v, want error %v", tc.key, err, tc.wantErr)
		}
	}
}

func TestLoadQMaxCodeConfig_EnvOverridesFileAndKeychain(t *testing.T) {
	emptyPATH(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, QmaxCodeConfigDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// File values must lose to env vars for Ollama and Cerebras.
	content := `{"ollama_url":"http://from-file:11434","cerebras_base_url":"http://file"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OLLAMA_BASE_URL", "http://env-ollama:11434")
	t.Setenv("OLLAMA_MODEL", "gemma3:4b")
	t.Setenv("CEREBRAS_API_KEY", "csk-env-key")
	t.Setenv("CEREBRAS_API_BASE", "http://env-cerebras")
	t.Setenv("CEREBRAS_MODEL", "gpt-oss-120b")
	t.Setenv("CEREBRAS_REASONING_EFFORT", "high")

	cfg := LoadQMaxCodeConfig()
	if cfg.OllamaURL != "http://env-ollama:11434" {
		t.Errorf("OllamaURL = %q, want env value to win", cfg.OllamaURL)
	}
	if cfg.OllamaModel != "gemma3:4b" {
		t.Errorf("OllamaModel = %q", cfg.OllamaModel)
	}
	if cfg.CerebrasKey != "csk-env-key" {
		t.Errorf("CerebrasKey = %q, want env value", cfg.CerebrasKey)
	}
	if cfg.CerebrasBaseURL != "http://env-cerebras" {
		t.Errorf("CerebrasBaseURL = %q", cfg.CerebrasBaseURL)
	}
	if cfg.CerebrasModel != "gpt-oss-120b" {
		t.Errorf("CerebrasModel = %q", cfg.CerebrasModel)
	}
	if cfg.CerebrasReasoningEffort != "high" {
		t.Errorf("CerebrasReasoningEffort = %q", cfg.CerebrasReasoningEffort)
	}
}

func TestLoadQMaxCodeConfig_MergesFileOverDefaults(t *testing.T) {
	emptyPATH(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, QmaxCodeConfigDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"default_model":"haiku","backend":"cc","local_only":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := LoadQMaxCodeConfig()
	if cfg.DefaultModel != "haiku" {
		t.Errorf("DefaultModel = %q, want haiku from file", cfg.DefaultModel)
	}
	if cfg.Backend != "cc" {
		t.Errorf("Backend = %q, want cc", cfg.Backend)
	}
	if !cfg.LocalOnly {
		t.Error("LocalOnly should be true")
	}
	// Unset file fields keep the built-in defaults.
	if !cfg.AutoSave {
		t.Error("AutoSave should stay true when the file omits it")
	}
	if cfg.MaxTokenBudget != 200000 {
		t.Errorf("MaxTokenBudget = %d, want default 200000", cfg.MaxTokenBudget)
	}
}

func TestLoadQMaxCodeConfig_HomeUnsetKeepsDefaults(t *testing.T) {
	emptyPATH(t)
	t.Setenv("HOME", "")
	cfg := LoadQMaxCodeConfig()
	if cfg.DefaultModel != "auto" || !cfg.AutoSave || cfg.MaxTokenBudget != 200000 || cfg.NarrateToolCalls != "brief" {
		t.Error("defaults not preserved without HOME")
	}
}

func TestConfigSave_FailsWhenConfigDirIsAFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Occupy the ~/.qmax-code path with a regular file so MkdirAll fails.
	if err := os.WriteFile(filepath.Join(home, QmaxCodeConfigDir), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&Config{}).Save(); err == nil {
		t.Error("expected error when config dir cannot be created")
	}
}

func TestConfigSaveRoundTripPreservesFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := &Config{
		AnthropicKey:            "fixture-anthropic-key",
		CerebrasKey:             "fixture-cerebras-key",
		DefaultModel:            "sonnet",
		Backend:                 "opencode",
		Theme:                   "ocean",
		EnabledProviders:        []string{"groq", "openrouter"},
		CloudSync:               func(b bool) *bool { return &b }(true),
		CerebrasReasoningEffort: "medium",
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, QmaxCodeConfigDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("saved config not valid JSON: %v", err)
	}
	for key, value := range raw {
		if strings.Contains(strings.ToLower(key), "key") || value == cfg.AnthropicKey || value == cfg.CerebrasKey {
			t.Fatal("API credentials must not be persisted in config JSON")
		}
	}
	if !strings.Contains(string(data), `"cerebras_reasoning_effort": "medium"`) {
		t.Errorf("reasoning effort not persisted: %s", data)
	}
}

func TestSaveAnthropicAndCerebrasKeysFailWithoutKeychainHelper(t *testing.T) {
	// The key cannot be stored when the OS keychain helper is unavailable;
	// the wrappers must surface that error rather than dropping it.
	emptyPATH(t)
	if err := SaveAnthropicKey("sk-test"); err == nil {
		t.Error("SaveAnthropicKey should fail without a keychain helper")
	}
	if err := SaveCerebrasKey("csk-test"); err == nil {
		t.Error("SaveCerebrasKey should fail without a keychain helper")
	}
}
