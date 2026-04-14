package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
	loaded := LoadQMaxCodeConfig()
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

func TestSetConfigField_UnsetEqualsEmptyValue(t *testing.T) {
	withTempHome(t)

	_ = setConfigField("default_framework", "rust_cargo")
	_ = setConfigField("default_framework", "") // "unset" semantics

	loaded := LoadQMaxCodeConfig()
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

	loaded := LoadQMaxCodeConfig()
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
		loaded := LoadQMaxCodeConfig()
		if !loaded.Professional {
			t.Errorf("expected Professional true for value %q", v)
		}
	}

	// All these should coerce to false.
	for _, v := range []string{"false", "no", "0", "off", ""} {
		_ = setConfigField("professional", v)
		loaded := LoadQMaxCodeConfig()
		if loaded.Professional {
			t.Errorf("expected Professional false for value %q", v)
		}
	}

	// Nonsense bool value rejected.
	if err := setConfigField("professional", "maybe"); err == nil {
		t.Error("expected bool parse error")
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
