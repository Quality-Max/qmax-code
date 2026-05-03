package main

import (
	"context"
	"time"
)

// cloudSessionTracker manages the lifecycle of a cloud-tracked agent session.
// Zero value is ready to use — no initialisation needed.
type cloudSessionTracker struct {
	cloudID string
}

// Start opens a cloud session the first time it is called with a valid API
// client and non-zero project ID. Subsequent calls are no-ops (idempotent).
func (t *cloudSessionTracker) Start(api *APIClient, projectID int, model string) {
	if api == nil || projectID == 0 || t.cloudID != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.cloudID = api.CreateAgentSession(ctx, projectID, model)
}

// Complete patches the cloud session as finished. No-op if Start was never
// called successfully (cloudID is empty) or api is nil.
func (t *cloudSessionTracker) Complete(api *APIClient, totalTokens int, summary string) {
	if api == nil || t.cloudID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	api.CompleteAgentSession(ctx, t.cloudID, totalTokens, summary)
}
