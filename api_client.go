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

	"github.com/qualitymax/qmax-code/internal/security"
	"github.com/qualitymax/qmax-code/internal/sysutil"
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

// allowedFrameworks mirrors the public framework values accepted by the
// QualityMax API. We validate client-side so bad values get rejected before
// hitting the wire and the agent can show a clearer error message.
var allowedFrameworks = map[string]bool{
	"":           true, // omitted → server auto-detects
	"playwright": true,
	"pytest":     true,
	"go":         true,
	"rust":       true,
	"go_test":    true,
	"rust_cargo": true,
	"cargo":      true,
}

// validateFramework returns an error string (JSON-encoded) if the value is
// outside the allow-list, or "" if OK. Callers can short-circuit before
// doing the HTTP POST.
func validateFramework(framework string) string {
	if allowedFrameworks[framework] {
		return ""
	}
	return jsonError(fmt.Sprintf(
		"Invalid framework %q. Allowed: playwright, pytest, go, rust, go_test, rust_cargo",
		framework,
	))
}

func (c *APIClient) GenerateTestCode(ctx context.Context, testCaseID int, force bool, framework string) string {
	if err := validateFramework(framework); err != "" {
		return err
	}
	body := map[string]interface{}{
		"test_case_id": testCaseID,
	}
	if force {
		body["force"] = true
	}
	// framework lets users request Rust/Go script generation instead of the
	// default Playwright path.
	// Empty string → server picks based on project settings + repo analysis.
	if framework != "" {
		body["framework"] = framework
	}
	return c.post(ctx, "/api/automation/generate", body)
}

// --- Execution operations ---

// RunTest dispatches a Playwright test to the QualityMax server. When
// useCloudSandbox is true, the server runs the script inside a QM Cloud
// Sandbox and the resulting status responses include a `live_browser_url`
// field that the REPL turns into a /browserfeed launch. The wire field
// is named `use_e2b` for backwards compatibility with the server's
// existing contract; we expose a clean name on the client side.
//
// We also send `live_feed_hold_seconds` so the server keeps the sandbox
// alive briefly after the run finishes — without that, the websockify
// port goes 502 by the time qmax-code's end-of-turn auto-launch fires.
func (c *APIClient) RunTest(ctx context.Context, scriptID int, headless bool, browser, baseURL string, useCloudSandbox bool) string {
	body := map[string]interface{}{
		"headless":         headless,
		"use_browserbase":  false,
		// Always request keepalive: the server may use E2B even when not
		// explicitly asked (e.g. per-script server-side policy). Without this,
		// the websockify port is gone before the end-of-turn auto-launch fires.
		"live_feed_hold_seconds": sysutil.LiveFeedHoldSeconds,
	}
	if useCloudSandbox {
		body["use_e2b"] = true
	}
	if browser != "" {
		body["browser"] = browser
	}
	if baseURL != "" {
		body["base_url"] = baseURL
	}
	return c.post(ctx, fmt.Sprintf("/api/playwright-execution/run/%d", scriptID), body)
}

// RunNativeTest executes a Rust (cargo test) / Go (go test -json) automation
// script through the QualityMax execution API. The backend dispatches by the
// script's framework field and returns a normalized result shape.
//
// Use this when the script is framework=rust_cargo or go_test. For
// Playwright scripts, keep using RunTest — the cloud runner path there
// handles video recording, which native runs skip.
func (c *APIClient) RunNativeTest(ctx context.Context, scriptID int, baseURL string) string {
	body := map[string]interface{}{
		"script_id": scriptID,
	}
	if baseURL != "" {
		body["custom_url"] = baseURL
	}
	return c.post(ctx, "/api/automation/execute", body)
}

// SetupCICD creates a GitHub Actions workflow PR on the linked repo.
// framework is optional — leave empty to let the server auto-detect from
// the repo's analyzed languages. For Rust repos the server auto-detects
// apt packages from Cargo.lock and injects them into the generated workflow.
func (c *APIClient) SetupCICD(ctx context.Context, repoID int, framework, targetBranch, baseURL string) string {
	if err := validateFramework(framework); err != "" {
		return err
	}
	body := map[string]interface{}{}
	if framework != "" {
		body["framework"] = framework
	}
	if targetBranch != "" {
		body["target_branch"] = targetBranch
	}
	if baseURL != "" {
		body["base_url"] = baseURL
	}
	return c.post(ctx, fmt.Sprintf("/api/repositories/%d/setup-cicd", repoID), body)
}

// RunTestsBatch fans a batch of test IDs out to the cloud runner. Same
// useCloudSandbox semantics as RunTest — when true, each entry runs in a
// QM Cloud Sandbox and surfaces a `live_browser_url`; the REPL only
// auto-launches the most recent one.
func (c *APIClient) RunTestsBatch(ctx context.Context, scriptIDs, baseURL string, useCloudSandbox bool) string {
	// Parse comma-separated string into integer array for JSON serialization
	var ids []int
	for _, s := range strings.Split(scriptIDs, ",") {
		s = strings.TrimSpace(s)
		if id, err := strconv.Atoi(s); err == nil {
			ids = append(ids, id)
		}
	}
	body := map[string]interface{}{
		"script_ids":             ids,
		"live_feed_hold_seconds": sysutil.LiveFeedHoldSeconds,
	}
	if useCloudSandbox {
		body["use_e2b"] = true
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

// StartCrawl kicks off an AI crawl. Same useCloudSandbox semantics as
// RunTest — when true, the crawl runs inside a QM Cloud Sandbox with a
// headed Chromium against Xvfb, and the status poll responses include
// `live_browser_url`.
func (c *APIClient) StartCrawl(ctx context.Context, projectID int, url string, depth, pages int, testType, instructions string, useCloudSandbox bool) string {
	body := map[string]interface{}{
		"project_id":             projectID,
		"url":                    url,
		"live_feed_hold_seconds": sysutil.LiveFeedHoldSeconds,
	}
	if useCloudSandbox {
		body["use_e2b"] = true
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

func (c *APIClient) GetReviewPreferences(ctx context.Context, repoID int) string {
	body := map[string]interface{}{}
	if repoID > 0 {
		body["repository_id"] = repoID
	}
	return c.post(ctx, "/api/mcp/tool/get_review_preferences", body)
}

func (c *APIClient) SetReviewPreferences(ctx context.Context, scope string, repoID int, preferences interface{}) string {
	body := map[string]interface{}{
		"scope":       scope,
		"preferences": preferences,
	}
	if repoID > 0 {
		body["repository_id"] = repoID
	}
	return c.post(ctx, "/api/mcp/tool/set_review_preferences", body)
}

func (c *APIClient) RepoCoverage(ctx context.Context, repoID int) string {
	return c.get(ctx, fmt.Sprintf("/api/repositories/%d/coverage", repoID))
}

func (c *APIClient) RepoQuality(ctx context.Context, repoID int) string {
	return c.get(ctx, fmt.Sprintf("/api/repositories/%d/quality", repoID))
}

// --- Import operations ---

func (c *APIClient) ImportRepo(ctx context.Context, url string, projectID int, createProject bool, projectName, baseURL, trainingConsent string) string {
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
	switch trainingConsent {
	case "opt_in", "opt_out":
		body["training_consent"] = trainingConsent
	case "":
		// Omit by default; consent should be explicit.
	default:
		return jsonError(`training_consent must be "opt_in" or "opt_out"`)
	}
	return c.post(ctx, "/api/repositories/import", body)
}

func (c *APIClient) ImportDocument(ctx context.Context, projectID int, text, sourceName string) string {
	body := map[string]interface{}{
		"project_id":   projectID,
		"text_content": text,
	}
	if sourceName != "" {
		body["source_name"] = sourceName
	}
	return c.post(ctx, "/api/import/document/text", body)
}

// --- PR operations ---

func (c *APIClient) CreatePR(ctx context.Context, repoID, projectID int) string {
	body := map[string]interface{}{
		"repo_id":    repoID,
		"project_id": projectID,
	}
	return c.post(ctx, "/api/repositories/create-pr", body)
}

func (c *APIClient) SecurityAuditPRCheck(ctx context.Context, repoSlug string, prNumber int, baseSHA, headSHA string) string {
	body := map[string]interface{}{
		"repo_slug": repoSlug,
		"pr_number": prNumber,
		"base_sha":  baseSHA,
		"head_sha":  headSHA,
	}
	return c.post(ctx, "/api/security-audit/pr-check", body)
}

// --- Agent session history ---

func (c *APIClient) ListAgentSessions(ctx context.Context, projectID, limit int) string {
	return c.get(ctx, fmt.Sprintf("/api/agent-sessions?project_id=%d&limit=%d", projectID, limit))
}

// CreateAgentSession opens a new cloud-tracked session and returns its UUID.
// Returns "" on failure or when the project ID is unknown.
func (c *APIClient) CreateAgentSession(ctx context.Context, projectID int, model string) string {
	body := map[string]interface{}{
		"project_id": projectID,
		"model":      model,
	}
	resp := c.post(ctx, "/api/agent-sessions", body)
	var r struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(resp), &r); err != nil {
		return ""
	}
	return r.SessionID
}

// CompleteAgentSession finalises a cloud session with status, token count, and summary.
// Errors are silently dropped — cloud sync failure must not block local operation.
func (c *APIClient) CompleteAgentSession(ctx context.Context, cloudID string, totalTokens int, summary string) {
	body := map[string]interface{}{
		"status":       "complete",
		"total_tokens": totalTokens,
		"ended_at":     time.Now().UTC().Format(time.RFC3339),
	}
	if summary != "" {
		body["summary"] = summary
	}
	c.patch(ctx, "/api/agent-sessions/"+cloudID, body)
}

// maxSessionUploadBytes caps the serialized message payload to avoid server
// rejection or excessive upload times on very long sessions.
const maxSessionUploadBytes = 4 * 1024 * 1024 // 4 MiB

// UploadSessionMessages uploads the full conversation history to a cloud session.
// Called alongside CompleteAgentSession so the cloud has complete context for
// cross-session recall. If the payload exceeds maxSessionUploadBytes, older
// messages are trimmed. Errors are silently dropped.
//
// The server exposes a generic /events endpoint with a discriminated-union body:
//
//	{"events": [{"type": "message", "payload": <Message>}, ...]}
//
// Valid event types per the server enum: file_edit, message, shell_cmd,
// test_result, tool_call — we only emit "message" here. The body must use the
// key "payload" (not "data"); other keys are silently dropped server-side and
// the event lands with payload={}.
func (c *APIClient) UploadSessionMessages(ctx context.Context, cloudID string, messages []Message) {
	if len(messages) == 0 {
		return
	}
	msgs := trimMessagesToFit(messages, maxSessionUploadBytes)
	events := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
		events = append(events, map[string]interface{}{
			"type":    "message",
			"payload": m,
		})
	}
	body := map[string]interface{}{"events": events}
	c.post(ctx, "/api/agent-sessions/"+cloudID+"/events", body)
}

// trimMessagesToFit drops oldest messages until the JSON-encoded payload fits
// within maxBytes. Returns the original slice if it already fits.
func trimMessagesToFit(messages []Message, maxBytes int) []Message {
	data, err := json.Marshal(messages)
	if err != nil || len(data) <= maxBytes {
		return messages
	}
	// Binary search for the largest suffix that fits.
	lo, hi := 0, len(messages)
	for lo < hi {
		mid := (lo + hi) / 2
		d, _ := json.Marshal(messages[mid:])
		if len(d) <= maxBytes {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo >= len(messages) {
		return messages[len(messages)-1:] // at minimum send the last message
	}
	return messages[lo:]
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

func (c *APIClient) patch(ctx context.Context, path string, body interface{}) string {
	var reqBody io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, "PATCH", c.BaseURL+path, reqBody)
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
		return jsonError(fmt.Sprintf("request failed: %s", security.RedactSensitive(err.Error())))
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		errMsg := ""
		// Try to extract error message from JSON response. The server may emit
		// either a plain FastAPI `{"detail": "..."}` envelope OR an MCP-style
		// `{"success": false, "error": "[CODE] ..."}` envelope where CODE is
		// NOT_FOUND / FORBIDDEN / BAD_REQUEST. Both are preserved verbatim so
		// an agent (or user) can parse the code when the HTTP status alone
		// isn't enough (e.g. when this method is called via the MCP transport
		// where there is no HTTP status in scope).
		var errResp map[string]interface{}
		if json.Unmarshal(data, &errResp) == nil {
			if detail, ok := errResp["detail"].(string); ok {
				errMsg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, detail)
			} else if errStr, ok := errResp["error"].(string); ok {
				// MCP-style envelope — keep any [CODE] prefix intact.
				errMsg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, errStr)
			}
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(data))
		}
		errMsg = security.RedactSensitive(errMsg)

		// Report server errors (5xx) and auth errors (401/403) to Bugsink
		if resp.StatusCode >= 500 || resp.StatusCode == 401 || resp.StatusCode == 403 {
			sysutil.CaptureError(fmt.Errorf("API error: %s", errMsg), map[string]interface{}{
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
	msg = security.RedactSensitive(msg)
	escaped, _ := json.Marshal(msg)
	return fmt.Sprintf(`{"error": %s}`, string(escaped))
}
