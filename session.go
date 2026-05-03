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

	// Sanitize before saving to prevent persisting corruption
	sanitizeSessionMessages(history)

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

	// Sanitize loaded messages — fix corrupted tool_use blocks
	sanitizeSessionMessages(session.Messages)

	return &session, nil
}

// sanitizeSessionMessages fixes common corruption issues in saved sessions:
// - tool_use blocks missing Input field (causes Anthropic API 400 errors)
// - text blocks with extra Input field (causes "Extra inputs are not permitted")
// - tool_result blocks with nil Content
func sanitizeSessionMessages(messages []Message) {
	for i := range messages {
		blocks, ok := messages[i].Content.([]interface{})
		if !ok {
			continue
		}
		for j, raw := range blocks {
			block, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)

			// tool_use must have input
			if blockType == "tool_use" && block["input"] == nil {
				block["input"] = map[string]interface{}{}
				blocks[j] = block
			}

			// text blocks must NOT have input, id, name, tool_use_id
			if blockType == "text" {
				delete(block, "input")
				delete(block, "id")
				delete(block, "name")
				delete(block, "tool_use_id")
				blocks[j] = block
			}

			// tool_result must have content
			if blockType == "tool_result" {
				if block["content"] == nil || block["content"] == "" {
					block["content"] = "{}"
					blocks[j] = block
				}
				// tool_result must NOT have input
				delete(block, "input")
				delete(block, "name")
				blocks[j] = block
			}
		}
		messages[i].Content = blocks
	}
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
	Model     string
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
			Model:     s.Model,
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

// sessionSummary builds a short human-readable summary from the conversation
// history to upload with cloud sessions. It takes the first user message as the
// topic and appends the turn count.
func sessionSummary(history []Message) string {
	if len(history) == 0 {
		return ""
	}
	var firstUser string
	turns := 0
	for _, m := range history {
		if m.Role == "user" {
			turns++
			if firstUser == "" {
				switch v := m.Content.(type) {
				case string:
					firstUser = v
				case []interface{}:
					for _, block := range v {
						if b, ok := block.(map[string]interface{}); ok && b["type"] == "text" {
							if t, ok := b["text"].(string); ok {
								firstUser = t
								break
							}
						}
					}
				}
			}
		}
	}
	if len(firstUser) > 200 {
		firstUser = firstUser[:200] + "…"
	}
	if firstUser == "" {
		return fmt.Sprintf("%d turns", turns)
	}
	return fmt.Sprintf("%s  [%d turns]", firstUser, turns)
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
