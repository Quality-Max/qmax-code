package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session represents a saved conversation session.
type Session struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ProjectID int        `json:"project_id,omitempty"`
	Model     string     `json:"model,omitempty"`
	Messages  []Message  `json:"messages"`
	Usage     TokenUsage `json:"usage"`
	Turns     int        `json:"turns"`
}

const sessionsSubDir = "sessions"
const sessionTTL = 7 * 24 * time.Hour // 7 days

// generateSessionID creates a short random hex ID like Claude Code uses.
func generateSessionID() string {
	b := make([]byte, 4) // 8 hex chars
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sessionDir returns ~/.qmax-code/sessions/
func sessionDirPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".qmax-code", sessionsSubDir)
}

// sessionFilePath returns the path for a specific session.
func sessionFilePath(id string) string {
	dir := sessionDirPath()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, id+".json")
}

// SaveSession persists the current conversation to disk.
// Called after every message exchange for crash safety.
func SaveSession(sessionID string, history []Message, projectID int, usage TokenUsage, model string) error {
	dir := sessionDirPath()
	if dir == "" {
		return fmt.Errorf("cannot determine home directory")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Count user turns
	turns := 0
	for _, msg := range history {
		if msg.Role == "user" {
			turns++
		}
	}

	session := Session{
		ID:        sessionID,
		CreatedAt: time.Now(), // will be overwritten on load if file exists
		UpdatedAt: time.Now(),
		ProjectID: projectID,
		Model:     model,
		Messages:  history,
		Usage:     usage,
		Turns:     turns,
	}

	// Preserve original creation time if session file exists
	existing, err := LoadSession(sessionID)
	if err == nil && existing != nil {
		session.CreatedAt = existing.CreatedAt
	}

	data, err := json.Marshal(session) // no indent — saves disk and load time
	if err != nil {
		return err
	}

	return os.WriteFile(sessionFilePath(sessionID), data, 0600)
}

// LoadSession loads a specific session by ID.
func LoadSession(id string) (*Session, error) {
	path := sessionFilePath(id)
	if path == "" {
		return nil, fmt.Errorf("cannot determine session path")
	}

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

// LoadLastSession loads the most recently updated session.
func LoadLastSession() (*Session, error) {
	sessions, err := ListSessions(1)
	if err != nil || len(sessions) == 0 {
		return nil, fmt.Errorf("no sessions found")
	}
	return LoadSession(sessions[0].ID)
}

// SessionSummary is a lightweight session descriptor for listing.
type SessionSummary struct {
	ID        string
	UpdatedAt time.Time
	Turns     int
	Tokens    int
	ProjectID int
}

// ListSessions returns recent sessions sorted by update time (newest first).
func ListSessions(limit int) ([]SessionSummary, error) {
	dir := sessionDirPath()
	if dir == "" {
		return nil, fmt.Errorf("cannot determine session directory")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var summaries []SessionSummary
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".json")
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}

		summaries = append(summaries, SessionSummary{
			ID:        id,
			UpdatedAt: s.UpdatedAt,
			Turns:     s.Turns,
			Tokens:    s.Usage.TotalTokens(),
			ProjectID: s.ProjectID,
		})
	}

	// Sort by update time, newest first
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})

	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}

	return summaries, nil
}

// CleanupOldSessions removes sessions older than sessionTTL.
// Called on startup to prevent unbounded disk growth.
func CleanupOldSessions() int {
	dir := sessionDirPath()
	if dir == "" {
		return 0
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	removed := 0
	cutoff := time.Now().Add(-sessionTTL)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			path := filepath.Join(dir, entry.Name())
			if os.Remove(path) == nil {
				removed++
			}
		}
	}

	return removed
}
