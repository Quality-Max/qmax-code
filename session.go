package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Session represents a saved conversation session.
type Session struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ProjectID int        `json:"project_id,omitempty"`
	Messages  []Message  `json:"messages"`
	Usage     TokenUsage `json:"usage"`
}

const sessionDir = ".qmax-code"
const sessionFile = "last-session.json"

// SaveSession persists the current conversation to disk.
func SaveSession(history []Message, projectID int, usage TokenUsage) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, sessionDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	session := Session{
		ID:        time.Now().Format("20060102-150405"),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		ProjectID: projectID,
		Messages:  history,
		Usage:     usage,
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, sessionFile), data, 0600)
}

// LoadLastSession loads the most recent saved session.
func LoadLastSession() (*Session, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(home, sessionDir, sessionFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}

	return &session, nil
}
