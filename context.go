package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// SessionContext holds the runtime context for the agent session.
type SessionContext struct {
	ProjectID int
	QMaxCfg   QMaxConfig
}

// QMaxConfig mirrors the qmax CLI config (~/.qmax/config.json).
type QMaxConfig struct {
	CloudURL string `json:"cloud_url"`
	Token    string `json:"token"`
	Email    string `json:"email"`
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
