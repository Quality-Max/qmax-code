package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
)

func withTempHome(t *testing.T) string {
	t.Helper()
	orig := os.Getenv("HOME")
	tmp := t.TempDir()
	os.Setenv("HOME", tmp)
	t.Cleanup(func() { os.Setenv("HOME", orig) })
	return tmp
}

func TestSetConfigField_DefaultFrameworkHappyPath(t *testing.T) {
	withTempHome(t)

	if err := setConfigField("default_framework", "rust_cargo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	loaded := api.LoadQMaxCodeConfig()
	if loaded.DefaultFramework != "rust_cargo" {
		t.Errorf("DefaultFramework: got %q, want rust_cargo", loaded.DefaultFramework)
	}
}

func TestSetConfigField_DefaultFrameworkRejectsBadValues(t *testing.T) {
	withTempHome(t)

	if err := setConfigField("default_framework", "bash"); err == nil {
		t.Error("expected error for invalid framework")
	}
	if err := setConfigField("default_framework", "../admin"); err == nil {
		t.Error("expected error for path-traversal-shaped value")
	}
}

func TestSetConfigField_BackendValidation(t *testing.T) {
	withTempHome(t)

	for _, b := range []string{"api", "cc", "codex", "cerebras", ""} {
		if err := setConfigField("backend", b); err != nil {
			t.Errorf("backend %q should be accepted: %v", b, err)
		}
	}
	if err := setConfigField("backend", "gpt"); err == nil {
		t.Error("expected error for unknown backend")
	}

	if err := setConfigField("backend", "cerebras"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded := api.LoadQMaxCodeConfig(); loaded.Backend != "cerebras" {
		t.Errorf("Backend: got %q, want cerebras", loaded.Backend)
	}
}

func TestSetConfigField_CerebrasModelAndBaseURL(t *testing.T) {
	withTempHome(t)

	if err := setConfigField("cerebras_model", "zai-glm-4.7"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := setConfigField("cerebras_base_url", "https://proxy.internal/v1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	loaded := api.LoadQMaxCodeConfig()
	if loaded.CerebrasModel != "zai-glm-4.7" {
		t.Errorf("CerebrasModel: got %q, want zai-glm-4.7", loaded.CerebrasModel)
	}
	if loaded.CerebrasBaseURL != "https://proxy.internal/v1" {
		t.Errorf("CerebrasBaseURL: got %q", loaded.CerebrasBaseURL)
	}
}

func TestSetConfigField_DefaultModelValidation(t *testing.T) {
	withTempHome(t)

	if err := setConfigField("default_model", "sonnet"); err != nil {
		t.Fatalf("expected shorthand model to be accepted: %v", err)
	}
	loaded := api.LoadQMaxCodeConfig()
	if loaded.DefaultModel != api.ModelSonnet {
		t.Errorf("DefaultModel: got %q, want %q", loaded.DefaultModel, api.ModelSonnet)
	}

	if err := setConfigField("default_model", "claude-future-model-9-0"); err == nil {
		t.Fatal("expected unknown Claude model ID to be rejected")
	}
}

func TestSetConfigField_UnsetEqualsEmptyValue(t *testing.T) {
	withTempHome(t)

	_ = setConfigField("default_framework", "rust_cargo")
	_ = setConfigField("default_framework", "") // "unset" semantics

	loaded := api.LoadQMaxCodeConfig()
	if loaded.DefaultFramework != "" {
		t.Errorf("expected framework cleared, got %q", loaded.DefaultFramework)
	}
}

func TestSetConfigField_IntValidation(t *testing.T) {
	withTempHome(t)

	if err := setConfigField("default_project", "not-a-number"); err == nil {
		t.Error("expected parse error")
	}
	if err := setConfigField("max_token_budget", "abc"); err == nil {
		t.Error("expected parse error")
	}

	if err := setConfigField("default_project", "42"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := setConfigField("max_token_budget", "100000"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loaded := api.LoadQMaxCodeConfig()
	if loaded.DefaultProject != 42 {
		t.Errorf("DefaultProject: got %d, want 42", loaded.DefaultProject)
	}
	if loaded.MaxTokenBudget != 100000 {
		t.Errorf("MaxTokenBudget: got %d, want 100000", loaded.MaxTokenBudget)
	}
}

func TestSetConfigField_BoolForms(t *testing.T) {
	withTempHome(t)

	// All these should coerce to true.
	for _, v := range []string{"true", "yes", "1", "on"} {
		if err := setConfigField("professional", v); err != nil {
			t.Errorf("expected %q to be true, got error: %v", v, err)
		}
		loaded := api.LoadQMaxCodeConfig()
		if !loaded.Professional {
			t.Errorf("expected Professional true for value %q", v)
		}
	}

	// All these should coerce to false.
	for _, v := range []string{"false", "no", "0", "off", ""} {
		_ = setConfigField("professional", v)
		loaded := api.LoadQMaxCodeConfig()
		if loaded.Professional {
			t.Errorf("expected Professional false for value %q", v)
		}
	}

	// Nonsense bool value rejected.
	if err := setConfigField("professional", "maybe"); err == nil {
		t.Error("expected bool parse error")
	}
}

func TestSetConfigField_OutputVerboseBoolForms(t *testing.T) {
	withTempHome(t)

	if err := setConfigField("output_verbose", "true"); err != nil {
		t.Fatalf("set output_verbose true: %v", err)
	}
	loaded := api.LoadQMaxCodeConfig()
	if !loaded.OutputVerbose {
		t.Fatal("expected OutputVerbose=true")
	}

	if err := setConfigField("output_verbose", "off"); err != nil {
		t.Fatalf("set output_verbose off: %v", err)
	}
	loaded = api.LoadQMaxCodeConfig()
	if loaded.OutputVerbose {
		t.Fatal("expected OutputVerbose=false")
	}

	if err := setConfigField("output_verbose", "maybe"); err == nil {
		t.Fatal("expected error for invalid output_verbose value")
	}
}

func TestSetConfigField_UnknownKey(t *testing.T) {
	withTempHome(t)

	if err := setConfigField("made_up_key", "x"); err == nil {
		t.Error("expected unknown-key error")
	}
}

func TestSetConfigField_PersistsToDisk(t *testing.T) {
	// Sanity check: the JSON file actually gets written with the value.
	tmp := withTempHome(t)

	_ = setConfigField("default_framework", "go_test")

	data, err := os.ReadFile(filepath.Join(tmp, ".qmax-code", "config.json"))
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("config JSON malformed: %v", err)
	}
	if parsed["default_framework"] != "go_test" {
		t.Errorf("file did not contain default_framework=go_test; got %v", parsed["default_framework"])
	}
}

func TestSetConfigField_ThemeHappyPath(t *testing.T) {
	withTempHome(t)

	for _, name := range tui.ThemeNames() {
		if err := setConfigField("theme", name); err != nil {
			t.Errorf("setConfigField(\"theme\", %q) unexpected error: %v", name, err)
		}
		loaded := api.LoadQMaxCodeConfig()
		if loaded.Theme != name {
			t.Errorf("theme %q: loaded.Theme = %q, want %q", name, loaded.Theme, name)
		}
	}
}

func TestSetConfigField_ThemeRejectsBadValue(t *testing.T) {
	withTempHome(t)

	for _, bad := range []string{"dark", "light", "HISTORIC", "ocean2", "../evil"} {
		if err := setConfigField("theme", bad); err == nil {
			t.Errorf("setConfigField(\"theme\", %q): expected error, got nil", bad)
		}
	}
}

func TestSetConfigField_ThemeEmptyUnsets(t *testing.T) {
	withTempHome(t)

	_ = setConfigField("theme", "neon")
	if err := setConfigField("theme", ""); err != nil {
		t.Fatalf("setConfigField(\"theme\", \"\") unexpected error: %v", err)
	}
	loaded := api.LoadQMaxCodeConfig()
	if loaded.Theme != "" {
		t.Errorf("expected Theme cleared, got %q", loaded.Theme)
	}
}

func TestSetConfigField_ThemePersistsToDisk(t *testing.T) {
	withTempHome(t)

	if err := setConfigField("theme", "aurora"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	loaded := api.LoadQMaxCodeConfig()
	if loaded.Theme != "aurora" {
		t.Errorf("loaded.Theme = %q, want \"aurora\"", loaded.Theme)
	}
}

func TestConfigSaveTheme_PersistsLocalSelection(t *testing.T) {
	tmp := withTempHome(t)
	cfg := api.DefaultConfig()

	if err := tui.SaveTheme(cfg, "radiance"); err != nil {
		t.Fatalf("tui.SaveTheme(\"radiance\") unexpected error: %v", err)
	}

	loaded := api.LoadQMaxCodeConfig()
	if loaded.Theme != "radiance" {
		t.Errorf("loaded.Theme = %q, want \"radiance\"", loaded.Theme)
	}

	data, err := os.ReadFile(filepath.Join(tmp, ".qmax-code", "config.json"))
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("config JSON malformed: %v", err)
	}
	if parsed["theme"] != "radiance" {
		t.Errorf("file did not contain theme=radiance; got %v", parsed["theme"])
	}
}

func TestConfigSaveTheme_RejectsInvalidWithoutOverwriting(t *testing.T) {
	withTempHome(t)
	cfg := api.DefaultConfig()
	if err := tui.SaveTheme(cfg, "ocean"); err != nil {
		t.Fatalf("tui.SaveTheme(\"ocean\") unexpected error: %v", err)
	}

	if err := tui.SaveTheme(cfg, "../evil"); err == nil {
		t.Fatal("tui.SaveTheme(\"../evil\") expected error, got nil")
	}

	loaded := api.LoadQMaxCodeConfig()
	if loaded.Theme != "ocean" {
		t.Errorf("invalid theme overwrote persisted selection: got %q, want \"ocean\"", loaded.Theme)
	}
	if cfg.Theme != "ocean" {
		t.Errorf("invalid theme overwrote in-memory selection: got %q, want \"ocean\"", cfg.Theme)
	}
}

func TestConfigSaveTheme_EmptyClearsLocalSelection(t *testing.T) {
	withTempHome(t)
	cfg := api.DefaultConfig()
	if err := tui.SaveTheme(cfg, "neon"); err != nil {
		t.Fatalf("tui.SaveTheme(\"neon\") unexpected error: %v", err)
	}
	if err := tui.SaveTheme(cfg, ""); err != nil {
		t.Fatalf("tui.SaveTheme(\"\") unexpected error: %v", err)
	}

	loaded := api.LoadQMaxCodeConfig()
	if loaded.Theme != "" {
		t.Errorf("loaded.Theme = %q, want empty", loaded.Theme)
	}
}

func TestSetConfigField_CloudSyncHappyPath(t *testing.T) {
	withTempHome(t)

	if err := setConfigField("cloud_sync", "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	loaded := api.LoadQMaxCodeConfig()
	if loaded.CloudSync == nil || !*loaded.CloudSync {
		t.Error("expected CloudSync=true after setConfigField(cloud_sync, true)")
	}

	if err := setConfigField("cloud_sync", "false"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	loaded = api.LoadQMaxCodeConfig()
	if loaded.CloudSync == nil || *loaded.CloudSync {
		t.Error("expected CloudSync=false after setConfigField(cloud_sync, false)")
	}
}

func TestSetConfigField_CloudSyncBoolForms(t *testing.T) {
	withTempHome(t)

	for _, v := range []string{"true", "yes", "1", "on"} {
		if err := setConfigField("cloud_sync", v); err != nil {
			t.Errorf("setConfigField(cloud_sync, %q) unexpected error: %v", v, err)
		}
		loaded := api.LoadQMaxCodeConfig()
		if loaded.CloudSync == nil || !*loaded.CloudSync {
			t.Errorf("expected CloudSync=true for value %q", v)
		}
	}
	for _, v := range []string{"false", "no", "0", "off"} {
		if err := setConfigField("cloud_sync", v); err != nil {
			t.Errorf("setConfigField(cloud_sync, %q) unexpected error: %v", v, err)
		}
		loaded := api.LoadQMaxCodeConfig()
		if loaded.CloudSync == nil || *loaded.CloudSync {
			t.Errorf("expected CloudSync=false for value %q", v)
		}
	}
}

func TestSetConfigField_CloudSyncUnsetClearsToNil(t *testing.T) {
	withTempHome(t)

	_ = setConfigField("cloud_sync", "true")
	if err := setConfigField("cloud_sync", ""); err != nil {
		t.Fatalf("unset should not error: %v", err)
	}
	loaded := api.LoadQMaxCodeConfig()
	if loaded.CloudSync != nil {
		t.Errorf("expected CloudSync=nil after unset, got %v", *loaded.CloudSync)
	}
}

func TestSetConfigField_CloudSyncRejectsInvalidValue(t *testing.T) {
	withTempHome(t)

	if err := setConfigField("cloud_sync", "maybe"); err == nil {
		t.Error("expected error for invalid cloud_sync value")
	}
}

func TestParseConfigBool_KnownValues(t *testing.T) {
	trueVals := []string{"true", "yes", "1", "on"}
	falseVals := []string{"false", "no", "0", "off", ""}
	for _, v := range trueVals {
		if b, err := parseConfigBool(v); err != nil || !b {
			t.Errorf("parseConfigBool(%q) = %v, %v; want true, nil", v, b, err)
		}
	}
	for _, v := range falseVals {
		if b, err := parseConfigBool(v); err != nil || b {
			t.Errorf("parseConfigBool(%q) = %v, %v; want false, nil", v, b, err)
		}
	}
	if _, err := parseConfigBool("banana"); err == nil {
		t.Error("expected error on nonsense bool")
	}
}
