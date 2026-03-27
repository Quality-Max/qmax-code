package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIClient calls QualityMax REST API directly (no qmax CLI needed).
type APIClient struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// NewAPIClient creates a new API client from auth config.
func NewAPIClient(auth *AuthConfig) *APIClient {
	if auth == nil {
		return nil
	}
	return &APIClient{
		BaseURL: auth.GetCloudURL(),
		APIKey:  auth.APIKey,
		HTTP:    &http.Client{Timeout: 120 * time.Second},
	}
}

// --- Project operations ---

func (c *APIClient) ListProjects(ctx context.Context) string {
	return c.get(ctx, "/api/projects")
}

// --- Test case operations ---

func (c *APIClient) ListTestCases(ctx context.Context, projectID, limit int, search string) string {
	path := fmt.Sprintf("/api/test-cases?project_id=%d", projectID)
	if limit > 0 {
		path += fmt.Sprintf("&limit=%d", limit)
	}
	if search != "" {
		path += "&search=" + search
	}
	return c.get(ctx, path)
}

func (c *APIClient) ListScripts(ctx context.Context, projectID, limit int) string {
	path := fmt.Sprintf("/api/automation/scripts?project_id=%d", projectID)
	if limit > 0 {
		path += fmt.Sprintf("&limit=%d", limit)
	}
	return c.get(ctx, path)
}

func (c *APIClient) GenerateTestCode(ctx context.Context, testCaseID int, force bool) string {
	body := map[string]interface{}{
		"test_case_id": testCaseID,
	}
	if force {
		body["force"] = true
	}
	return c.post(ctx, "/api/automation/generate", body)
}

// --- Execution operations ---

func (c *APIClient) RunTest(ctx context.Context, scriptID int, headless bool, browser, baseURL string) string {
	body := map[string]interface{}{
		"script_id": scriptID,
		"headless":  headless,
	}
	if browser != "" {
		body["browser"] = browser
	}
	if baseURL != "" {
		body["base_url"] = baseURL
	}
	return c.post(ctx, "/api/automation/run", body)
}

func (c *APIClient) RunTestsBatch(ctx context.Context, scriptIDs, baseURL string) string {
	body := map[string]interface{}{
		"script_ids": scriptIDs,
	}
	if baseURL != "" {
		body["base_url"] = baseURL
	}
	return c.post(ctx, "/api/automation/run-batch", body)
}

func (c *APIClient) CheckTestStatus(ctx context.Context, executionID string) string {
	return c.get(ctx, "/api/automation/status/"+executionID)
}

// --- Crawl operations ---

func (c *APIClient) StartCrawl(ctx context.Context, projectID int, url string, depth, pages int, testType, instructions string) string {
	body := map[string]interface{}{
		"project_id": projectID,
		"url":        url,
	}
	if depth > 0 {
		body["depth"] = depth
	}
	if pages > 0 {
		body["pages_limit"] = pages
	}
	if testType != "" {
		body["test_type"] = testType
	}
	if instructions != "" {
		body["custom_instructions"] = instructions
	}
	return c.post(ctx, "/api/ai-crawl/start", body)
}

func (c *APIClient) CrawlStatus(ctx context.Context, crawlID string) string {
	return c.get(ctx, "/api/ai-crawl/status/"+crawlID)
}

func (c *APIClient) CrawlResults(ctx context.Context, crawlID string) string {
	return c.get(ctx, "/api/ai-crawl/results/"+crawlID)
}

func (c *APIClient) ListCrawlJobs(ctx context.Context, limit int) string {
	path := "/api/ai-crawl/jobs"
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	return c.get(ctx, path)
}

// --- Repository operations ---

func (c *APIClient) ListRepos(ctx context.Context, projectID int) string {
	return c.get(ctx, fmt.Sprintf("/api/repositories?project_id=%d", projectID))
}

func (c *APIClient) ReviewRepo(ctx context.Context, repoID int) string {
	return c.post(ctx, fmt.Sprintf("/api/repositories/%d/review", repoID), nil)
}

func (c *APIClient) RepoCoverage(ctx context.Context, repoID int) string {
	return c.get(ctx, fmt.Sprintf("/api/repositories/%d/coverage", repoID))
}

func (c *APIClient) RepoQuality(ctx context.Context, repoID int) string {
	return c.get(ctx, fmt.Sprintf("/api/repositories/%d/quality", repoID))
}

// --- Import operations ---

func (c *APIClient) ImportRepo(ctx context.Context, url string, projectID int, createProject bool, projectName, baseURL string) string {
	body := map[string]interface{}{
		"repo_url": url,
	}
	if projectID > 0 {
		body["project_id"] = projectID
	}
	if createProject {
		body["create_new_project"] = true
		if projectName != "" {
			body["new_project_name"] = projectName
		}
	}
	if baseURL != "" {
		body["base_url"] = baseURL
	}
	body["training_consent"] = true
	return c.post(ctx, "/api/repositories/import", body)
}

func (c *APIClient) ImportDocument(ctx context.Context, projectID int, text, sourceName string) string {
	body := map[string]interface{}{
		"project_id": projectID,
		"content":    text,
	}
	if sourceName != "" {
		body["source_name"] = sourceName
	}
	return c.post(ctx, "/api/test-cases/import-from-document", body)
}

// --- PR operations ---

func (c *APIClient) CreatePR(ctx context.Context, repoID, projectID int) string {
	body := map[string]interface{}{
		"repo_id":    repoID,
		"project_id": projectID,
	}
	return c.post(ctx, "/api/repositories/create-pr", body)
}

// --- Script operations ---

func (c *APIClient) GetScript(ctx context.Context, scriptID int) string {
	return c.get(ctx, fmt.Sprintf("/api/automation/scripts/%d", scriptID))
}

func (c *APIClient) UpdateScript(ctx context.Context, scriptID int, name, code string) string {
	body := map[string]interface{}{
		"name": name,
		"code": code,
	}
	return c.put(ctx, fmt.Sprintf("/api/automation/scripts/%d", scriptID), body)
}

// --- HTTP helpers ---

func (c *APIClient) get(ctx context.Context, path string) string {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+path, nil)
	if err != nil {
		return jsonError(err.Error())
	}
	return c.doRequest(req)
}

func (c *APIClient) post(ctx context.Context, path string, body interface{}) string {
	var reqBody io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+path, reqBody)
	if err != nil {
		return jsonError(err.Error())
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.doRequest(req)
}

func (c *APIClient) put(ctx context.Context, path string, body interface{}) string {
	var reqBody io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", c.BaseURL+path, reqBody)
	if err != nil {
		return jsonError(err.Error())
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.doRequest(req)
}

func (c *APIClient) doRequest(req *http.Request) string {
	// Auth: use API key as Bearer token (strip qm- prefix if present)
	token := c.APIKey
	if strings.HasPrefix(token, "qm-") {
		token = token[3:]
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return jsonError(fmt.Sprintf("request failed: %s", err))
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		// Try to extract error message from JSON response
		var errResp map[string]interface{}
		if json.Unmarshal(data, &errResp) == nil {
			if detail, ok := errResp["detail"].(string); ok {
				return jsonError(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, detail))
			}
		}
		return jsonError(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(data)))
	}

	return string(data)
}

func jsonError(msg string) string {
	escaped, _ := json.Marshal(msg)
	return fmt.Sprintf(`{"error": %s}`, string(escaped))
}
