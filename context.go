package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SessionContext holds the runtime context for the agent session.
type SessionContext struct {
	ProjectID int
	QMaxCfg   QMaxConfig
	QMaxBin   string // resolved path to qmax binary
	QMaxInfo  string // output of `qmax status` at startup
}

// QMaxConfig mirrors the qmax CLI config (~/.qmax/config.json).
type QMaxConfig struct {
	CloudURL string `json:"cloud_url"`
	Token    string `json:"token"`
	Email    string `json:"email"`
}

// TokenUsage tracks cumulative token usage across the session.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	Requests     int
}

// TotalTokens returns the total token count.
func (u *TokenUsage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens
}

// EstimatedCost returns estimated cost in USD.
// Pricing: Sonnet input=$3/MTok output=$15/MTok, Opus input=$15/MTok output=$75/MTok, Haiku input=$0.25/MTok output=$1.25/MTok
func (u *TokenUsage) EstimatedCost(model string) float64 {
	var inputRate, outputRate float64
	switch {
	case strings.Contains(model, "opus"):
		inputRate, outputRate = 15.0, 75.0
	case strings.Contains(model, "haiku"):
		inputRate, outputRate = 0.25, 1.25
	default: // sonnet
		inputRate, outputRate = 3.0, 15.0
	}
	return (float64(u.InputTokens)/1_000_000)*inputRate + (float64(u.OutputTokens)/1_000_000)*outputRate
}

// loadQMaxConfig reads the qmax CLI config file.
// Returns an empty config if the file doesn't exist — the user can log in via the qmax CLI.
func loadQMaxConfig() QMaxConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return QMaxConfig{}
	}

	configPath := filepath.Join(home, ".qmax", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return QMaxConfig{}
	}

	var cfg QMaxConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return QMaxConfig{}
	}

	return cfg
}

// discoverQMaxBinary finds the qmax binary, checking multiple locations.
// Order: ./qmax, ~/.qmax/qmax, then PATH.
func discoverQMaxBinary() string {
	// 1. Current directory
	if _, err := os.Stat("./qmax"); err == nil {
		return "./qmax"
	}

	// 2. ~/.qmax/qmax
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".qmax", "qmax")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// 3. PATH
	if p, err := exec.LookPath("qmax"); err == nil {
		return p
	}

	return ""
}

// probeQMaxStatus runs `qmax status` to get auth/account info.
func probeQMaxStatus(binary string) string {
	if binary == "" {
		return ""
	}
	cmd := exec.Command(binary, "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// formatQMaxInstallHint returns install instructions when qmax is missing.
func formatQMaxInstallHint() string {
	return fmt.Sprintf(`qmax CLI not found. Install it:

  curl -fsSL https://get.qualitymax.io/cli | sh

Or download from: https://docs.qualitymax.io/cli`)
}
