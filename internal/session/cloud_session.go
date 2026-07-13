package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
)

// PromptCloudSyncConsent asks the user once whether they want sessions synced
// to the QualityMax cloud. The answer is persisted in cfg so the prompt never
// appears again. Returns true if the user opted in.
//
// readLine must use the active readline instance (e.g. term.ReadConsent) so
// that it works correctly when readline already owns the terminal in raw mode.
func PromptCloudSyncConsent(cfg *api.Config, readLine func() (string, error)) bool {
	fmt.Println()
	fmt.Println("  ┌─ Cloud session sync ──────────────────────────────────────────┐")
	fmt.Println("  │  qmax-code can sync your sessions to the QualityMax cloud so  │")
	fmt.Println("  │  the agent remembers past conversations across restarts.       │")
	fmt.Println("  │                                                                │")
	fmt.Println("  │  You can change this any time with:  /set cloudsync true|false │")
	fmt.Println("  └────────────────────────────────────────────────────────────────┘")
	fmt.Println()
	fmt.Print("  Enable cloud session sync? [Y/n]: ")

	line, _ := readLine()

	enabled := ApplyCloudSyncChoice(cfg, line)
	if enabled {
		fmt.Println("  Cloud session sync enabled.")
	} else {
		fmt.Println("  Cloud session sync disabled.")
	}
	fmt.Println()
	return enabled
}

// ApplyCloudSyncChoice parses a raw answer line, updates cfg.CloudSync, and
// persists it. Extracted from PromptCloudSyncConsent so it can be unit-tested
// without touching stdin/stdout.
func ApplyCloudSyncChoice(cfg *api.Config, line string) bool {
	ans := strings.ToLower(strings.TrimSpace(line))
	enabled := ans == "" || ans == "y" || ans == "yes"
	v := enabled
	cfg.CloudSync = &v
	_ = cfg.Save()
	return enabled
}

// CloudSessionTracker manages the lifecycle of a cloud-tracked agent session.
// Zero value is ready to use — no initialisation needed.
type CloudSessionTracker struct {
	cloudID      string
	projectID    int
	historyStart int
	tokenStart   int
}

// Start opens a cloud session the first time it is called with a valid API
// client and non-zero project ID. Subsequent calls are no-ops (idempotent).
func (t *CloudSessionTracker) Start(client *api.APIClient, projectID int, model string) {
	t.StartWithHistory(client, projectID, model, 0, 0)
}

// StartWithHistory opens a cloud session and records the point in the local
// conversation at which it began. This lets a single local REPL conversation
// move between projects without copying the earlier project's messages into
// the next cloud session.
func (t *CloudSessionTracker) StartWithHistory(
	client *api.APIClient, projectID int, model string, historyLength int, totalTokens int,
) {
	if client == nil || projectID == 0 || t.cloudID != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.cloudID = client.CreateAgentSession(ctx, projectID, model)
	if t.cloudID != "" {
		t.projectID = projectID
		t.historyStart = historyLength
		t.tokenStart = totalTokens
	}
}

// SwitchProject finalizes the current cloud session and starts another one for
// projectID. Local history remains intact, but only messages entered after the
// switch belong to the new cloud session. It returns true when a tracked
// session was actually moved to a different project.
func (t *CloudSessionTracker) SwitchProject(
	client *api.APIClient, projectID int, model string, totalTokens int, messages []api.Message,
) bool {
	if client == nil {
		return false
	}
	if t.cloudID == "" {
		t.StartWithHistory(client, projectID, model, len(messages), totalTokens)
		return false
	}
	if t.projectID == projectID {
		return false
	}

	t.CompleteCurrent(client, totalTokens, messages)
	t.cloudID = ""
	t.projectID = 0
	t.historyStart = 0
	t.tokenStart = 0
	t.StartWithHistory(client, projectID, model, len(messages), totalTokens)
	return true
}

// CompleteCurrent finalizes the active cloud session using only the portion
// of the local history that belongs to its current project.
func (t *CloudSessionTracker) CompleteCurrent(client *api.APIClient, totalTokens int, messages []api.Message) {
	start := t.historyStart
	if start < 0 {
		start = 0
	}
	if start > len(messages) {
		start = len(messages)
	}
	projectMessages := messages[start:]
	projectTokens := totalTokens - t.tokenStart
	if projectTokens < 0 {
		projectTokens = 0
	}
	t.Complete(client, projectTokens, SummaryFor(projectMessages), projectMessages)
}

// Complete patches the cloud session as finished and uploads the full message
// history. No-op if Start was never called successfully (cloudID is empty) or
// client is nil.
func (t *CloudSessionTracker) Complete(client *api.APIClient, totalTokens int, summary string, messages []api.Message) {
	if client == nil || t.cloudID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	client.CompleteAgentSession(ctx, t.cloudID, totalTokens, summary)
	cancel()

	// Build the discriminated-union events the server expects, then trim to
	// fit the upload cap before sending. The api client doesn't need to know
	// what a Message is — only the event shape.
	events := make([]any, 0, len(messages))
	for _, m := range messages {
		events = append(events, map[string]interface{}{
			"type":    "message",
			"payload": m,
		})
	}
	events = api.TrimEventsToFit(events, api.MaxSessionUploadBytes)

	// Separate timeout for upload — payload can be large.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	client.UploadSessionEvents(ctx2, t.cloudID, events)
}
