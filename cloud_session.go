package main

import (
	"context"
	"fmt"
	"github.com/qualitymax/qmax-code/internal/api"
	"strings"
	"time"
)

// promptCloudSyncConsent asks the user once whether they want sessions synced
// to the QualityMax cloud. The answer is persisted in cfg so the prompt never
// appears again. Returns true if the user opted in.
//
// readLine must use the active readline instance (e.g. term.ReadConsent) so
// that it works correctly when readline already owns the terminal in raw mode.
func promptCloudSyncConsent(cfg *api.Config, readLine func() (string, error)) bool {
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

	enabled := applyCloudSyncChoice(cfg, line)
	if enabled {
		fmt.Println("  Cloud session sync enabled.")
	} else {
		fmt.Println("  Cloud session sync disabled.")
	}
	fmt.Println()
	return enabled
}

// applyCloudSyncChoice parses a raw answer line, updates cfg.CloudSync, and
// persists it. Extracted from promptCloudSyncConsent so it can be unit-tested
// without touching stdin/stdout.
func applyCloudSyncChoice(cfg *api.Config, line string) bool {
	ans := strings.ToLower(strings.TrimSpace(line))
	enabled := ans == "" || ans == "y" || ans == "yes"
	v := enabled
	cfg.CloudSync = &v
	_ = cfg.Save()
	return enabled
}

// cloudSessionTracker manages the lifecycle of a cloud-tracked agent session.
// Zero value is ready to use — no initialisation needed.
type cloudSessionTracker struct {
	cloudID string
}

// Start opens a cloud session the first time it is called with a valid API
// client and non-zero project ID. Subsequent calls are no-ops (idempotent).
func (t *cloudSessionTracker) Start(client *api.APIClient, projectID int, model string) {
	if client == nil || projectID == 0 || t.cloudID != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.cloudID = client.CreateAgentSession(ctx, projectID, model)
}

// Complete patches the cloud session as finished and uploads the full message
// history. No-op if Start was never called successfully (cloudID is empty) or
// client is nil.
func (t *cloudSessionTracker) Complete(client *api.APIClient, totalTokens int, summary string, messages []Message) {
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
