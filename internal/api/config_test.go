package api

import (
	"os"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DefaultModel != "auto" {
		t.Errorf("DefaultModel: got %s, want auto", cfg.DefaultModel)
	}
	if !cfg.AutoSave {
		t.Error("AutoSave should be true by default")
	}
	if cfg.MaxTokenBudget != 200000 {
		t.Errorf("MaxTokenBudget: got %d, want 200000", cfg.MaxTokenBudget)
	}
}

func TestConfigSaveAndLoad(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	cfg := &Config{
		DefaultModel:   "opus",
		DefaultProject: 42,
		Professional:   true,
		AutoSave:       false,
		MaxTokenBudget: 50000,
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded := LoadQMaxCodeConfig()
	if loaded.DefaultModel != "opus" {
		t.Errorf("DefaultModel: got %s, want opus", loaded.DefaultModel)
	}
	if loaded.DefaultProject != 42 {
		t.Errorf("DefaultProject: got %d, want 42", loaded.DefaultProject)
	}
	if !loaded.Professional {
		t.Error("Professional should be true")
	}
}
