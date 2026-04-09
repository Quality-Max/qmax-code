package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
	return c.get(ctx, "/api/projects?limit=200")
}

func (c *APIClient) GetProjectBySlug(ctx context.Context, slug string) string {
	// Try exact slug first
	result := c.get(ctx, "/api/projects/by-slug/"+slug)
	if !strings.Contains(result, "not found") && !strings.Contains(result, "404") {
		return result
	}

	// Slug not found — search by name/key in all projects
	listResult := c.get(ctx, "/api/projects?limit=200")

	// Parse the projects list and find a fuzzy match
	var listResp struct {
		Projects []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Key  string `json:"key"`
			Slug string `json:"slug"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(listResult), &listResp); err != nil {
		return result // Return original 404
	}

	query := strings.ToLower(slug)
	for _, p := range listResp.Projects {
		if strings.ToLower(p.Name) == query || strings.ToLower(p.Key) == query ||
			strings.Contains(strings.ToLower(p.Name), query) {
			return c.get(ctx, fmt.Sprintf("/api/projects/by-slug/%s", p.Slug))
		}
	}

	return result // No match found, return original 404
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
		"headless":         headless,
		"use_browserbase":  false, // Use QualityMax server runner, not BrowserBase
	}
	if browser != "" {
		body["browser"] = browser
	}
	if baseURL != "" {
		body["base_url"] = baseURL
	}
	return c.post(ctx, fmt.Sprintf("/api/playwright-execution/run/%d", scriptID), body)
}

func (c *APIClient) RunTestsBatch(ctx context.Context, scriptIDs, baseURL string) string {
	// Parse comma-separated string into integer array for JSON serialization
	var ids []int
	for _, s := range strings.Split(scriptIDs, ",") {
		s = strings.TrimSpace(s)
		if id, err := strconv.Atoi(s); err == nil {
			ids = append(ids, id)
		}
	}
	body := map[string]interface{}{
		"script_ids": ids,
	}
	if baseURL != "" {
		body["base_url"] = baseURL
	}
	return c.post(ctx, "/api/automation/execute-batch", body)
}

func (c *APIClient) CheckTestStatus(ctx context.Context, executionID string) string {
	return c.get(ctx, "/api/playwright-execution/status/"+executionID)
}

func (c *APIClient) GetExecutionArtifact(ctx context.Context, executionID, artifactType string) string {
	return c.get(ctx, fmt.Sprintf("/api/playwright-execution/artifacts/%s/%s", executionID, artifactType))
}

func (c *APIClient) ReportLocalResult(ctx context.Context, scriptID int, status, output, framework string, duration float64) string {
	body := map[string]interface{}{
		"script_id": scriptID,
		"status":    status,
		"output":    output,
		"framework": framework,
		"duration":  duration,
	}
	return c.post(ctx, "/api/playwright-execution/report-local", body)
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
	return c.get(ctx, fmt.Sprintf("/api/repositories/project/%d", projectID))
}

func (c *APIClient) ReviewRepo(ctx context.Context, repoID int) string {
	return c.post(ctx, fmt.Sprintf("/api/repositories/%d/ai-review", repoID), map[string]interface{}{})
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
	body["training_consent"] = "opt_in"
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

// --- k6 Performance Testing ---

func (c *APIClient) K6ListScripts(ctx context.Context, projectID int) string {
	return c.get(ctx, fmt.Sprintf("/api/k6/scripts?project_id=%d", projectID))
}

func (c *APIClient) K6CreateScript(ctx context.Context, projectID int, name, testType, targetURL, code string) string {
	body := map[string]interface{}{
		"project_id": projectID,
		"name":       name,
		"test_type":  testType,
		"target_url": targetURL,
	}
	if code != "" {
		body["code"] = code
	}
	return c.post(ctx, "/api/k6/scripts", body)
}

func (c *APIClient) K6GetScript(ctx context.Context, scriptID int) string {
	return c.get(ctx, fmt.Sprintf("/api/k6/scripts/%d", scriptID))
}

func (c *APIClient) K6RunTest(ctx context.Context, scriptID, vus int, duration string) string {
	body := map[string]interface{}{
		"script_id": scriptID,
	}
	if vus > 0 {
		body["vus"] = vus
	}
	if duration != "" {
		body["duration"] = duration
	}
	return c.post(ctx, fmt.Sprintf("/api/k6/run/%d", scriptID), body)
}

func (c *APIClient) K6CheckStatus(ctx context.Context, executionID string) string {
	return c.get(ctx, "/api/k6/status/"+executionID)
}

func (c *APIClient) K6Report(ctx context.Context, executionID string) string {
	return c.get(ctx, "/api/k6/executions/"+executionID+"/report")
}

func (c *APIClient) K6Generate(ctx context.Context, projectID int, targetURL, testType string, endpoints string) string {
	body := map[string]interface{}{
		"project_id": projectID,
		"target_url": targetURL,
		"test_type":  testType,
	}
	if endpoints != "" {
		body["endpoints"] = endpoints
	}
	return c.post(ctx, "/api/k6/generate", body)
}

func (c *APIClient) K6Convert(ctx context.Context, sourceCode, sourceFramework, testType string) string {
	body := map[string]interface{}{
		"source_code":      sourceCode,
		"source_framework": sourceFramework,
		"test_type":        testType,
	}
	return c.post(ctx, "/api/k6/convert", body)
}

// --- Test Case CRUD ---

func (c *APIClient) CreateTestCase(ctx context.Context, projectID int, title, description, category, priority string) string {
	body := map[string]interface{}{
		"project_id":  projectID,
		"title":       title,
		"description": description,
	}
	if category != "" {
		body["category"] = category
	}
	if priority != "" {
		body["priority"] = priority
	}
	return c.post(ctx, "/api/test-cases/", body)
}

func (c *APIClient) UpdateTestCase(ctx context.Context, testCaseID int, title, description, category, priority, status string) string {
	body := map[string]interface{}{}
	if title != "" {
		body["title"] = title
	}
	if description != "" {
		body["description"] = description
	}
	if category != "" {
		body["category"] = category
	}
	if priority != "" {
		body["priority"] = priority
	}
	if status != "" {
		body["status"] = status
	}
	return c.put(ctx, fmt.Sprintf("/api/test-cases/%d", testCaseID), body)
}

func (c *APIClient) DeleteTestCase(ctx context.Context, testCaseID int) string {
	return c.delete(ctx, fmt.Sprintf("/api/test-cases/%d", testCaseID))
}

// --- Project CRUD ---

func (c *APIClient) CreateProject(ctx context.Context, name, description, baseURL string) string {
	// Auto-generate project key from name (uppercase, alphanumeric, max 10 chars)
	key := ""
	for _, ch := range strings.ToUpper(name) {
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			key += string(ch)
		}
		if len(key) >= 10 {
			break
		}
	}
	if key == "" {
		key = "PROJ"
	}
	body := map[string]interface{}{
		"name": name,
		"key":  key,
	}
	if description != "" {
		body["description"] = description
	}
	if baseURL != "" {
		body["main_url"] = baseURL
	}
	return c.post(ctx, "/api/projects", body)
}

func (c *APIClient) UpdateProject(ctx context.Context, projectID int, name, description, baseURL string) string {
	body := map[string]interface{}{}
	if name != "" {
		body["name"] = name
	}
	if description != "" {
		body["description"] = description
	}
	if baseURL != "" {
		body["base_url"] = baseURL
	}
	return c.put(ctx, fmt.Sprintf("/api/projects/%d", projectID), body)
}

func (c *APIClient) DeleteProject(ctx context.Context, projectID int) string {
	return c.delete(ctx, fmt.Sprintf("/api/projects/%d", projectID))
}

func (c *APIClient) GetProjectSummary(ctx context.Context, projectID int) string {
	return c.get(ctx, fmt.Sprintf("/api/projects/%d", projectID))
}

// --- Framework Operations ---

func (c *APIClient) TriggerFrameworkRun(ctx context.Context, projectID int, frameworkType string) string {
	body := map[string]interface{}{}
	if frameworkType != "" {
		body["framework_type"] = frameworkType
	}
	return c.post(ctx, fmt.Sprintf("/api/frameworks/%d/run", projectID), body)
}

func (c *APIClient) AddScriptToFramework(ctx context.Context, projectID, scriptID int) string {
	body := map[string]interface{}{
		"script_id": scriptID,
	}
	return c.post(ctx, fmt.Sprintf("/api/frameworks/%d/add-script", projectID), body)
}

func (c *APIClient) ExportFramework(ctx context.Context, projectID int, framework string) string {
	path := fmt.Sprintf("/api/frameworks/%d/export", projectID)
	if framework != "" {
		path += "?framework=" + framework
	}
	return c.get(ctx, path)
}

func (c *APIClient) GetInstallCommand(ctx context.Context, projectID int) string {
	return c.get(ctx, fmt.Sprintf("/api/frameworks/%d/install-command", projectID))
}

// --- AI-Powered Tools ---

func (c *APIClient) EnhanceTestCase(ctx context.Context, testCaseID int) string {
	return c.post(ctx, fmt.Sprintf("/api/test-cases/%d/enhance", testCaseID), nil)
}

func (c *APIClient) GenerateGapTests(ctx context.Context, repoID int) string {
	return c.post(ctx, fmt.Sprintf("/api/repositories/%d/generate-gap-tests", repoID), map[string]interface{}{})
}

func (c *APIClient) StartCrawlFromTestCase(ctx context.Context, testCaseID int) string {
	return c.post(ctx, fmt.Sprintf("/api/ai-crawl/start-from-test-case/%d", testCaseID), nil)
}

// --- QTML ---

func (c *APIClient) ExportQTML(ctx context.Context, projectID int) string {
	return c.get(ctx, fmt.Sprintf("/api/qtml/export?project_id=%d", projectID))
}

func (c *APIClient) ImportQTML(ctx context.Context, projectID int, qtmlContent string) string {
	body := map[string]interface{}{
		"project_id": projectID,
		"content":    qtmlContent,
	}
	return c.post(ctx, "/api/qtml/import", body)
}

func (c *APIClient) ConvertQTMLToPlaywright(ctx context.Context, qtmlContent string) string {
	body := map[string]interface{}{
		"content": qtmlContent,
	}
	return c.post(ctx, "/api/qtml/convert-to-playwright", body)
}

// --- Deployment Testing ---

func (c *APIClient) TestDeployedEnvironment(ctx context.Context, projectID int, url string) string {
	body := map[string]interface{}{
		"project_id": projectID,
		"url":        url,
	}
	return c.post(ctx, "/api/automation/test-deployed", body)
}

// --- Background Job Status ---

func (c *APIClient) CheckBackgroundJob(ctx context.Context, jobID string) string {
	return c.get(ctx, "/api/job-health/background/"+jobID)
}

// --- Delete helper ---

func (c *APIClient) delete(ctx context.Context, path string) string {
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.BaseURL+path, nil)
	if err != nil {
		return jsonError(err.Error())
	}
	return c.doRequest(req)
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
	// Auth: send full API key as Bearer token (backend handles qm- prefix)
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return jsonError(fmt.Sprintf("request failed: %s", err))
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		errMsg := ""
		// Try to extract error message from JSON response
		var errResp map[string]interface{}
		if json.Unmarshal(data, &errResp) == nil {
			if detail, ok := errResp["detail"].(string); ok {
				errMsg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, detail)
			}
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(data))
		}

		// Report server errors (5xx) and auth errors (401/403) to Bugsink
		if resp.StatusCode >= 500 || resp.StatusCode == 401 || resp.StatusCode == 403 {
			CaptureError(fmt.Errorf("API error: %s", errMsg), map[string]interface{}{
				"method":      req.Method,
				"path":        req.URL.Path,
				"status_code": fmt.Sprintf("%d", resp.StatusCode),
			})
		}

		return jsonError(errMsg)
	}

	return string(data)
}

func jsonError(msg string) string {
	escaped, _ := json.Marshal(msg)
	return fmt.Sprintf(`{"error": %s}`, string(escaped))
}
