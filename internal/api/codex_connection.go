package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/qualitymax/qmax-code/internal/httpx"
	"github.com/qualitymax/qmax-code/internal/security"
)

// CodexConnection is the non-sensitive connection state returned by QualityMax.
type CodexConnection struct {
	Connected    bool   `json:"connected"`
	Status       string `json:"status"`
	AccountLabel string `json:"account_label,omitempty"`
	LastRefresh  string `json:"last_refresh,omitempty"`
}

// HTTPStatusError preserves the response status so callers can recover from
// expired QualityMax authentication without inspecting an error string.
type HTTPStatusError struct {
	StatusCode int
	Message    string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

// ConnectCodex attaches Codex OAuth material to the authenticated QualityMax
// user. The server derives the target user from this client's bearer token.
func (c *APIClient) ConnectCodex(ctx context.Context, authJSON string) (*CodexConnection, error) {
	data, err := json.Marshal(map[string]string{"auth_json": authJSON})
	if err != nil {
		return nil, fmt.Errorf("encode Codex credentials: %w", err)
	}

	req, err := httpx.NewRequest(
		ctx,
		http.MethodPost,
		c.BaseURL+"/api/integrations/codex/connect",
		bytes.NewReader(data),
	)
	if err != nil {
		return nil, fmt.Errorf("create Codex connection request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect Codex: %s", security.RedactSensitive(err.Error()))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return nil, fmt.Errorf("read Codex connection response: %w", err)
	}
	if resp.StatusCode >= 400 {
		message := http.StatusText(resp.StatusCode)
		var envelope struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(body, &envelope) == nil && envelope.Detail != "" {
			message = envelope.Detail
		}
		return nil, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Message:    security.RedactSensitive(message),
		}
	}

	var connection CodexConnection
	if err := json.Unmarshal(body, &connection); err != nil {
		return nil, fmt.Errorf("decode Codex connection response: %w", err)
	}
	return &connection, nil
}
