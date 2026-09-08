package session

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/security"
)

// Session represents a saved conversation session.
type Session struct {
	ID           string                `json:"id"`
	Conversation api.ConversationState `json:"conversation,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
	UpdatedAt    time.Time             `json:"updated_at"`
	ProjectID    int                   `json:"project_id,omitempty"`
	Model        string                `json:"model,omitempty"`
	Messages     []api.Message         `json:"messages"`
	Usage        api.TokenUsage        `json:"usage"`
	Turns        int                   `json:"turns"`
}

const sessionsSubDir = "sessions"
const sessionTTL = 7 * 24 * time.Hour // 7 days

// durableSessionTTL is the grace period for sessions carrying a portable
// transcript. They are far more valuable than a legacy session (they are what
// --resume and provider switching replay) and far larger, so they live much
// longer, but they are still reclaimed eventually.
const durableSessionTTL = 90 * 24 * time.Hour // 90 days

// GenerateSessionID creates a short random hex ID like Claude Code uses.
func GenerateSessionID() string {
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

// sessionFilePath returns the path for a specific session, or "" if id is
// invalid. id must be a non-empty string of [a-zA-Z0-9_-] only — anything
// else (path separators, "..", spaces, control chars) is rejected so user
// input from /resume <id> can't traverse out of the sessions directory.
func sessionFilePath(id string) string {
	if !isValidSessionID(id) {
		return ""
	}
	dir := sessionDirPath()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, id+".json")
}

// IsValidSessionID is the same validator used internally to gate filesystem
// access. Exported so callers (e.g. REPL /resume handlers) can validate user
// input *at the call site* and produce a clear error before LoadSession is
// reached. Validation is duplicated rather than relying on LoadSession's
// internal check so taint-tracking SAST tools see sanitization on the path
// from user input to LoadSession.
func IsValidSessionID(id string) bool { return isValidSessionID(id) }

func isValidSessionID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// SaveSession persists the current conversation to disk.
// Called after every message exchange for crash safety.
func SaveSession(sessionID string, history []api.Message, projectID int, usage api.TokenUsage, model string, states ...api.ConversationState) error {
	if !isValidSessionID(sessionID) {
		return fmt.Errorf("invalid session ID")
	}
	dir := sessionDirPath()
	if dir == "" {
		return fmt.Errorf("cannot determine home directory")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Work on a detached copy so saving cannot corrupt live tool blocks.
	historyData, err := json.Marshal(history)
	if err != nil {
		return err
	}
	var savedHistory []api.Message
	if err := json.Unmarshal(historyData, &savedHistory); err != nil {
		return err
	}
	SanitizeSessionMessages(savedHistory)
	history = savedHistory

	// Count against the retained transcript so compaction does not reset turns.
	turnHistory := history
	if len(states) > 0 && states[0].Transcript != nil {
		turnHistory = states[0].Transcript
	}

	session := Session{
		ID:        sessionID,
		CreatedAt: time.Now(), // will be overwritten on load if file exists
		UpdatedAt: time.Now(),
		ProjectID: projectID,
		Model:     model,
		Messages:  history,
		Usage:     usage,
		Turns:     CountTurns(turnHistory),
	}

	if len(states) > 0 {
		session.Conversation = states[0]
	}
	// The transcript is a superset of the working history, and RestoreConversation
	// rebuilds History from it, so storing both doubles the file for nothing.
	// LoadSession rehydrates Messages for callers that still read it.
	if session.Conversation.Transcript != nil {
		session.Messages = nil
	}

	// Preserve original creation time if session file exists
	existing, err := LoadSession(sessionID)
	if err == nil && existing != nil {
		session.CreatedAt = existing.CreatedAt
	}

	data, err := MarshalRedacted(session)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), sessionFilePath(sessionID))
}

// LoadSession loads a specific session by ID.
func LoadSession(id string) (*Session, error) {
	if !isValidSessionID(id) {
		return nil, fmt.Errorf("invalid session ID %q (must be alphanumeric, _ or -, ≤64 chars)", id)
	}
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

	// Saves that carry a transcript omit the redundant working history.
	if len(session.Messages) == 0 && session.Conversation.Transcript != nil {
		session.Messages = session.Conversation.Transcript
	}

	// Sanitize loaded messages — fix corrupted tool_use blocks
	SanitizeSessionMessages(session.Messages)

	return &session, nil
}

// CountTurns counts real user prompts. Tool results travel as user-role
// messages in the Anthropic wire format, and CLI backends contribute one entry
// per exposed tool call, so counting every user-role message reports a turn
// count several times the number of things the user actually typed.
func CountTurns(history []api.Message) int {
	turns := 0
	for _, msg := range history {
		if msg.Role == "user" && !isToolResultOnly(msg.Content) {
			turns++
		}
	}
	return turns
}

// isToolResultOnly reports whether content consists solely of tool_result
// blocks. It accepts both live ([]api.ContentBlock) and deserialized
// ([]interface{}) shapes, since sessions round-trip through JSON.
func isToolResultOnly(content interface{}) bool {
	switch v := content.(type) {
	case []api.ContentBlock:
		if len(v) == 0 {
			return false
		}
		for _, block := range v {
			if block.Type != "tool_result" {
				return false
			}
		}
		return true
	case []interface{}:
		if len(v) == 0 {
			return false
		}
		for _, raw := range v {
			block, ok := raw.(map[string]interface{})
			if !ok || block["type"] != "tool_result" {
				return false
			}
		}
		return true
	}
	return false
}

// SanitizeSessionMessages fixes common corruption issues in saved sessions:
// - tool_use blocks missing Input field (causes Anthropic API 400 errors)
// - text blocks with extra Input field (causes "Extra inputs are not permitted")
// - tool_result blocks with nil Content
func SanitizeSessionMessages(messages []api.Message) {
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
func SummaryFor(history []api.Message) string {
	if len(history) == 0 {
		return ""
	}
	var firstUser string
	turns := CountTurns(history)
	for _, m := range history {
		if m.Role == "user" && !isToolResultOnly(m.Content) {
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

// CleanupOldSessions removes expired sessions to prevent unbounded disk
// growth. Portable transcripts get a much longer grace period than legacy
// sessions, but they still expire: a lossless transcript is the largest thing
// this tool writes, and "retain forever" is not a bounded policy.
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
	now := time.Now()
	cutoff := now.Add(-sessionTTL)
	durableCutoff := now.Add(-durableSessionTTL)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue // within the legacy window, so within every window
		}

		path := filepath.Join(dir, entry.Name())
		expiry := cutoff
		if hasDurableTranscript(path) {
			expiry = durableCutoff
		}
		if info.ModTime().Before(expiry) {
			if os.Remove(path) == nil {
				removed++
			}
		}
	}

	return removed
}

// hasDurableTranscript reports whether a session file carries a portable
// transcript by reading only the head of the file. Session files can be
// megabytes; startup cleanup must not JSON-parse every expired one just to
// decide which cutoff applies. Session marshals ID then Conversation, so the
// transcript key lands within the first few dozen bytes.
func hasDurableTranscript(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	head := make([]byte, 512)
	n, err := file.Read(head)
	if n <= 0 || (err != nil && err != io.EOF) {
		return false
	}
	head = head[:n]
	return bytes.Contains(head, []byte(`"transcript":`)) &&
		!bytes.Contains(head, []byte(`"transcript":null`))
}

func redactSessionValue(value any) any {
	switch v := value.(type) {
	case string:
		// CLI tool content can itself be encoded JSON. Redact its fields too.
		var nested any
		if (strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[")) && json.Unmarshal([]byte(v), &nested) == nil {
			if data, err := json.Marshal(redactSessionValue(nested)); err == nil {
				return string(data)
			}
		}
		return security.RedactRetained(v)
	case []any:
		for i := range v {
			v[i] = redactSessionValue(v[i])
		}
	case map[string]any:
		for key := range v {
			switch strings.ToLower(strings.ReplaceAll(key, "-", "_")) {
			case "password", "passwd", "api_key", "apikey", "access_token", "refresh_token", "token", "secret", "client_secret", "private_key", "service_role_key", "authorization", "database_url", "redis_url", "webhook_secret", "signing_key", "encrypted_payload":
				v[key] = "[REDACTED]"
			default:
				v[key] = redactSessionValue(v[key])
			}
		}
	}
	return value
}

// MarshalRedacted serializes portable content without mutating live history.
// Structured credential fields and recognized secrets in prose are removed.
func MarshalRedacted(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var detached any
	if err := json.Unmarshal(data, &detached); err != nil {
		return nil, err
	}
	return json.Marshal(redactSessionValue(detached))
}
