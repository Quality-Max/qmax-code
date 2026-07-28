package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/exposure"
	"github.com/qualitymax/qmax-code/internal/httpx"
	"github.com/qualitymax/qmax-code/internal/security"
)

const (
	cloudMCPPath        = "/api/mcp/"
	maxCloudResponseLen = 16 << 20
)

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion,omitempty"`
	Capabilities    map[string]interface{} `json:"capabilities,omitempty"`
	ClientInfo      clientInfo             `json:"clientInfo"`
}

type cloudMCPClient struct {
	url        string
	apiKey     string
	http       *http.Client
	clientInfo clientInfo
	proxyName  string
}

func newCloudMCPClient(auth *api.AuthConfig, version string) *cloudMCPClient {
	if auth == nil || !auth.IsAuthenticated() {
		return nil
	}
	httpClient := httpx.NewClient(120 * time.Second)
	// A 307/308 redirect can replay a POST body. MCP tools/call may mutate
	// durable state, so redirects must never be followed implicitly.
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &cloudMCPClient{
		url:       auth.GetCloudURL() + cloudMCPPath,
		apiKey:    auth.APIKey,
		http:      httpClient,
		proxyName: "qmax-code/" + safeIdentityPart(version, "unknown"),
	}
}

func (c *cloudMCPClient) setClientInfo(raw json.RawMessage) (json.RawMessage, error) {
	params := make(map[string]interface{})
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, fmt.Errorf("decode initialize params: %w", err)
		}
	}

	var downstream clientInfo
	if value, ok := params["clientInfo"]; ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("decode downstream clientInfo: %w", err)
		}
		_ = json.Unmarshal(encoded, &downstream)
	}
	downstream = clientInfo{
		Name:    safeIdentityPart(downstream.Name, "unknown"),
		Version: safeIdentityPart(downstream.Version, "unknown"),
	}
	c.clientInfo = downstream
	params["clientInfo"] = downstream

	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode initialize params: %w", err)
	}
	return encoded, nil
}

func safeIdentityPart(value, fallback string) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if value == "" ||
		strings.Contains(lower, "bearer") ||
		strings.HasPrefix(lower, "qm-") ||
		strings.HasPrefix(lower, "sk-") ||
		looksLikeCredentialIdentity(value) ||
		looksLikeJWT(value) {
		return fallback
	}

	var out strings.Builder
	for _, r := range value {
		if out.Len() >= 64 {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._+-", r) {
			out.WriteRune(r)
		} else {
			out.WriteByte('_')
		}
	}
	safe := strings.Trim(out.String(), "_")
	if safe == "" || strings.Contains(security.RedactSensitive(safe), "[REDACTED]") {
		return fallback
	}
	return safe
}

func looksLikeCredentialIdentity(value string) bool {
	lower := strings.ToLower(value)
	for _, prefix := range []string{
		"ghp_",
		"github_pat_",
		"xoxb-",
		"xoxp-",
		"xoxa-",
		"xoxr-",
		"xoxs-",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if len(value) == 20 &&
		(strings.HasPrefix(value, "AKIA") || strings.HasPrefix(value, "ASIA")) {
		for _, r := range value[4:] {
			if !unicode.IsUpper(r) && !unicode.IsDigit(r) {
				return false
			}
		}
		return true
	}
	if len(value) < 32 {
		return false
	}
	for _, r := range value {
		if !unicode.IsLetter(r) &&
			!unicode.IsDigit(r) &&
			!strings.ContainsRune("_+-=.", r) {
			return false
		}
	}
	return true
}

func looksLikeJWT(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if len(part) < 8 {
			return false
		}
		for _, r := range part {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
				return false
			}
		}
	}
	return true
}

func (c *cloudMCPClient) call(ctx context.Context, req request) (response, bool, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return response{}, false, fmt.Errorf("encode cloud MCP request: %w", err)
	}
	ctx = httpx.WithCategory(ctx, exposure.CatMCPTraffic)
	httpReq, err := httpx.NewRequest(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return response{}, false, fmt.Errorf("build cloud MCP request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("User-Agent", c.userAgent())

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return response{}, false, fmt.Errorf("cloud MCP request failed: %s", security.RedactSensitive(err.Error()))
	}
	defer httpResp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(httpResp.Body, maxCloudResponseLen+1))
	if err != nil {
		return response{}, false, fmt.Errorf("read cloud MCP response: %w", err)
	}
	if len(data) > maxCloudResponseLen {
		return response{}, false, fmt.Errorf("cloud MCP response exceeds %d bytes", maxCloudResponseLen)
	}
	if httpResp.StatusCode >= 300 {
		return response{}, false, cloudHTTPError(httpResp.StatusCode, data)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return response{}, false, nil
	}

	var rpcResp response
	contentType := httpResp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		rpcResp, err = decodeSSEResponse(data, req.ID)
	} else {
		err = json.Unmarshal(data, &rpcResp)
	}
	if err != nil {
		return response{}, false, fmt.Errorf("decode cloud MCP response: %w", err)
	}
	return rpcResp, true, nil
}

func (c *cloudMCPClient) userAgent() string {
	if c.clientInfo.Name == "" {
		return c.proxyName
	}
	return fmt.Sprintf("%s downstream/%s/%s", c.proxyName, c.clientInfo.Name, c.clientInfo.Version)
}

func cloudHTTPError(status int, data []byte) error {
	var body struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		Detail           string `json:"detail"`
	}
	_ = json.Unmarshal(data, &body)
	message := body.ErrorDescription
	if message == "" {
		message = body.Detail
	}
	if message == "" {
		message = body.Error
	}
	message = security.RedactSensitive(message)
	if len(message) > 500 {
		message = message[:500]
	}
	if message == "" {
		return fmt.Errorf("cloud MCP returned HTTP %d", status)
	}
	return fmt.Errorf("cloud MCP returned HTTP %d: %s", status, message)
}

func decodeSSEResponse(data []byte, expectedID interface{}) (response, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), maxCloudResponseLen)
	var eventData strings.Builder
	decodeEvent := func() (response, bool) {
		if eventData.Len() == 0 {
			return response{}, false
		}
		var rpcResp response
		if err := json.Unmarshal([]byte(eventData.String()), &rpcResp); err != nil {
			eventData.Reset()
			return response{}, false
		}
		eventData.Reset()
		if rpcResp.ID == nil || !sameRPCID(rpcResp.ID, expectedID) {
			return response{}, false
		}
		return rpcResp, true
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			if eventData.Len() > 0 {
				eventData.WriteByte('\n')
			}
			eventData.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if line == "" {
			if rpcResp, ok := decodeEvent(); ok {
				return rpcResp, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return response{}, err
	}
	if rpcResp, ok := decodeEvent(); ok {
		return rpcResp, nil
	}
	return response{}, fmt.Errorf("SSE response contained no response for request id")
}

func sameRPCID(got, want interface{}) bool {
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)
	if gotErr != nil || wantErr != nil {
		return false
	}
	return bytes.Equal(gotJSON, wantJSON)
}
