package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds persistent user preferences for qmax-code.
type Config struct {
	DefaultModel   string `json:"default_model,omitempty"`   // "auto", "sonnet", "opus", "haiku"
	DefaultProject int    `json:"default_project,omitempty"`
	// DefaultFramework is set by the first-run wizard based on filesystem
	// detection (Cargo.toml → rust_cargo, go.mod → go_test, etc.). The agent
	// reads this to default the `framework` param on generate_test_code so
	// Rust/Go users don't get Playwright scripts by accident.
	DefaultFramework string `json:"default_framework,omitempty"` // "", "playwright", "pytest", "rust_cargo", "go_test"
	Professional   bool   `json:"professional,omitempty"` // disable cat personality
	AutoSave       bool   `json:"auto_save"`              // auto-save session on exit (default true)
	MaxTokenBudget int    `json:"max_token_budget,omitempty"`
	AnthropicKey string `json:"-"` // NOT stored in JSON — use keychain instead
}

const qmaxCodeConfigDir = ".qmax-code"
const qmaxCodeConfigFile = "config.json"

// LoadQMaxCodeConfig reads persistent user preferences from ~/.qmax-code/config.json
// and loads the Anthropic key from the OS keychain.
func LoadQMaxCodeConfig() *Config {
	cfg := defaultConfig()

	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, qmaxCodeConfigDir, qmaxCodeConfigFile)
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, cfg)
		}
	}

	// Load Anthropic key from OS keychain (never stored in JSON)
	if key, err := LoadFromKeychain("anthropic_key"); err == nil && key != "" {
		cfg.AnthropicKey = key
	}

	return cfg
}

// SaveAnthropicKey securely stores the Anthropic API key in the OS keychain.
func SaveAnthropicKey(key string) error {
	return SaveToKeychain("anthropic_key", key)
}

// Save persists the config to ~/.qmax-code/config.json.
func (c *Config) Save() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, qmaxCodeConfigDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, qmaxCodeConfigFile), data, 0600)
}

func defaultConfig() *Config {
	return &Config{
		DefaultModel:   "auto",
		AutoSave:       true,
		MaxTokenBudget: 200000,
	}
}
