package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// limitWriter is an io.Writer that silently discards bytes once the cap is hit.
// Use it as cmd.Stdout/Stderr to prevent unbounded memory growth from large
// subprocess outputs. The cap is enforced at write time so the underlying
// buffer never exceeds maxBytes.
type limitWriter struct {
	b      strings.Builder
	n      int
	maxN   int
	capped bool
}

func newLimitWriter(maxBytes int) *limitWriter { return &limitWriter{maxN: maxBytes} }

func (lw *limitWriter) Write(p []byte) (int, error) {
	remaining := lw.maxN - lw.n
	if remaining <= 0 {
		lw.capped = true
		return len(p), nil // discard; tell caller we consumed it
	}
	if len(p) > remaining {
		p = p[:remaining]
		lw.capped = true
	}
	n, err := lw.b.Write(p)
	lw.n += n
	return n, err
}

func (lw *limitWriter) String() string {
	s := lw.b.String()
	if lw.capped {
		s += "\n... (output capped)"
	}
	return s
}

// --- input parsing helpers ---

func parseInput(rawInput interface{}) map[string]interface{} {
	input := make(map[string]interface{})
	switch v := rawInput.(type) {
	case map[string]interface{}:
		input = v
	default:
		data, _ := json.Marshal(rawInput)
		_ = json.Unmarshal(data, &input)
	}
	return input
}

func strVal(input map[string]interface{}, key string) string {
	if v, ok := input[key]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func intVal(input map[string]interface{}, key string, fallback int) int {
	if v, ok := input[key]; ok && v != nil {
		switch n := v.(type) {
		case float64:
			return int(math.Round(n))
		case int:
			return n
		case json.Number:
			i, _ := n.Int64()
			return int(i)
		}
	}
	return fallback
}

func boolVal(input map[string]interface{}, key string) bool {
	if v, ok := input[key]; ok && v == true {
		return true
	}
	return false
}

// ToolDef is a Claude API tool definition.
type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// experimentalToolNames lists tools that are gated behind QMAX_EXPERIMENTAL=1.
// These surfaces work but lack public docs / support guarantees, per
// OPEN_SOURCE_SCOPE.md Phase 2. Set QMAX_EXPERIMENTAL=1 to expose them to the
// agent and the MCP server.
var experimentalToolNames = map[string]bool{
	// k6 load testing
	"k6_list_scripts":  true,
	"k6_create_script": true,
	"k6_get_script":    true,
	"k6_run_test":      true,
	"k6_check_status":  true,
	"k6_report":        true,
	"k6_generate":      true,
	"k6_convert":       true,
	// QTML import/export
	"export_qtml": true,
	"import_qtml": true,
	// Framework export / install / trigger
	"export_framework":      true,
	"get_install_command":   true,
	"trigger_framework_run": true,
	// Operational health (private/remove per scope doc)
	"check_job_status": true,
}

// BuildToolDefs returns the public tool definitions exposed to the LLM agent
// and via the MCP server. Experimental tools are filtered out unless
// QMAX_EXPERIMENTAL=1 is set.
func BuildToolDefs() []ToolDef {
	all := buildAllToolDefs()
	if envEnabled("QMAX_EXPERIMENTAL") {
		return all
	}
	out := make([]ToolDef, 0, len(all))
	for _, d := range all {
		if !experimentalToolNames[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

// buildAllToolDefs returns every tool definition, including experimental ones.
// Used internally; public callers go through BuildToolDefs which applies the
// QMAX_EXPERIMENTAL gate.
func buildAllToolDefs() []ToolDef {
	return []ToolDef{
		// --- Project operations ---
		{
			Name:        "list_projects",
			Description: "List all QualityMax projects. Always call this first if the user hasn't specified a project.",
			InputSchema: obj(props()),
		},

		// --- Test case operations ---
		{
			Name:        "list_test_cases",
			Description: "List test cases for a project. Shows title, category, priority, status, and whether automated.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
				prop("limit", "integer", "Max results (default 50)", false),
				prop("search", "string", "Search in title/description", false),
			)),
		},
		{
			Name:        "list_scripts",
			Description: "List automation scripts for a project. IMPORTANT: Check the 'framework' field — only 'playwright' scripts can run on the cloud runner. pytest scripts need local execution.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
				prop("limit", "integer", "Max results (default 50)", false),
			)),
		},
		{
			Name:        "generate_test_code",
			Description: "Generate test code for a test case using AI. Returns a script ID that can be run. Default framework is playwright; pass framework=pytest / rust_cargo / go_test to generate native scripts for those toolchains.",
			InputSchema: obj(props(
				prop("test_case_id", "integer", "Test case ID to generate code for", true),
				prop("force", "boolean", "Regenerate even if code exists", false),
				prop("framework", "string", "Override target framework: playwright (default), pytest, rust_cargo, go_test. Omit to let the server pick based on project settings + repo analysis.", false),
			)),
		},
		{
			Name:        "run_test",
			Description: "Execute a Playwright test on the cloud runner. Shows live progress with browser animation. Returns full execution trace: status, test_errors, console_logs, screenshot_paths, video_path. When failed, always show the test_errors and console_logs to the user.",
			InputSchema: obj(props(
				prop("script_id", "integer", "Script ID to execute", true),
				prop("headless", "boolean", "Run headless (default true)", false),
				prop("browser", "string", "Browser: chromium, firefox, webkit", false),
				prop("base_url", "string", "Base URL override", false),
			)),
		},
		{
			Name:        "run_native_test",
			Description: "Execute a Rust (cargo test) or Go (go test -json) script on the QualityMax server runner. Returns the normalized result shape (status, passed_tests, failed_tests, total_tests, console_logs, test_output, test_errors). Use this for scripts where framework is rust_cargo or go_test. For Playwright scripts, use run_test. When failed, always show test_errors + console_logs to the user.",
			InputSchema: obj(props(
				prop("script_id", "integer", "Script ID to execute (must be a rust_cargo / go_test script)", true),
				prop("base_url", "string", "Base URL exported as BASE_URL to the test subprocess (for integration tests that hit a staging endpoint)", false),
			)),
		},
		{
			Name:        "setup_cicd",
			Description: "Create a Pull Request on the linked GitHub repo that adds a GitHub Actions workflow file running the project's test suite. Auto-detects the framework from the repo's analyzed languages (playwright / pytest / go / rust) when omitted, and for Rust auto-detects apt packages from Cargo.lock (glib-sys→libglib2.0-dev, openssl-sys→libssl-dev, libxdo-sys→libxdo-dev, etc.). Requires the GitHub App to be installed on the target repo. Returns PR URL, PR number, workflow file path, detected framework, and injected apt packages.",
			InputSchema: obj(props(
				prop("repo_id", "integer", "Code repository ID (from list_repositories)", true),
				prop("framework", "string", "Optional override: playwright / pytest / go / rust / rust_cargo / go_test. Omit to auto-detect.", false),
				prop("target_branch", "string", "Branch the workflow triggers on. Defaults to the repo's default branch.", false),
				prop("base_url", "string", "Optional BASE_URL baked into the workflow.", false),
			)),
		},
		{
			Name:        "run_tests_batch",
			Description: "Execute multiple Playwright test scripts in batch. IMPORTANT: Filter script IDs to only include playwright/cypress scripts. pytest scripts will fail.",
			InputSchema: obj(props(
				prop("script_ids", "string", "Comma-separated script IDs", true),
				prop("base_url", "string", "Base URL override", false),
			)),
		},
		{
			Name:        "check_test_status",
			Description: "Check the status of a test execution. Returns status, progress, message, test_errors, console_logs, screenshot_paths, video_path. When status is 'failed', always report the test_errors and message to the user.",
			InputSchema: obj(props(
				prop("execution_id", "string", "Execution ID to check", true),
			)),
		},

		{
			Name:        "check_job_status",
			Description: "Check the status of a background job (AI review, gap analysis, etc). Use this for any job_id that starts with 'repo_ai_review_' or 'gap_analysis_'. NOT for test executions — use check_test_status for those.",
			InputSchema: obj(props(
				prop("job_id", "string", "Background job ID", true),
			)),
		},

		// --- Local test execution (pytest, etc.) ---
		{
			Name:        "run_local_test",
			Description: "Run a test script locally using the user's environment (pytest, node, etc). Downloads the script from QualityMax, runs it, and reports results back. Works with any framework including pytest.",
			InputSchema: obj(props(
				prop("script_id", "integer", "Script ID to run", true),
				prop("base_url", "string", "Base URL for the app under test", false),
			)),
		},

		// --- k6 Performance Testing ---
		{
			Name:        "k6_list_scripts",
			Description: "List k6 performance test scripts for a project.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
			)),
		},
		{
			Name:        "k6_create_script",
			Description: "Create a k6 performance test script. Supports load, stress, spike, soak, smoke, breakpoint, API, security, and browser test types.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
				prop("name", "string", "Script name", true),
				prop("test_type", "string", "Test type: load, stress, spike, soak, smoke, breakpoint, api, security, browser", true),
				prop("target_url", "string", "Target URL to test", true),
				prop("code", "string", "k6 script code (optional — auto-generated if empty)", false),
			)),
		},
		{
			Name:        "k6_get_script",
			Description: "Get a k6 script by ID including its code.",
			InputSchema: obj(props(
				prop("script_id", "integer", "k6 script ID", true),
			)),
		},
		{
			Name:        "k6_run_test",
			Description: "Execute a k6 performance test. Returns an execution ID for polling status.",
			InputSchema: obj(props(
				prop("script_id", "integer", "k6 script ID to execute", true),
				prop("vus", "integer", "Number of virtual users (override)", false),
				prop("duration", "string", "Test duration override (e.g. '30s', '5m')", false),
			)),
		},
		{
			Name:        "k6_check_status",
			Description: "Check the status and results of a k6 test execution.",
			InputSchema: obj(props(
				prop("execution_id", "string", "k6 execution ID", true),
			)),
		},
		{
			Name:        "k6_report",
			Description: "Get the full report of a k6 test execution with metrics, thresholds, and HTTP stats.",
			InputSchema: obj(props(
				prop("execution_id", "string", "k6 execution ID", true),
			)),
		},
		{
			Name:        "k6_generate",
			Description: "Generate a k6 performance test script from a target URL. Auto-creates load profiles with thresholds.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
				prop("target_url", "string", "URL to generate test for", true),
				prop("test_type", "string", "Test type: load, stress, spike, soak, smoke", false),
				prop("endpoints", "string", "Comma-separated endpoint paths to test", false),
			)),
		},
		{
			Name:        "k6_convert",
			Description: "Convert performance test scripts from JMeter, Gatling, Locust, or Playwright to k6.",
			InputSchema: obj(props(
				prop("source_code", "string", "Source test script code", true),
				prop("source_framework", "string", "Source framework: jmeter, gatling, locust, playwright", true),
				prop("test_type", "string", "Target k6 test type (default: load)", false),
			)),
		},

		// --- Test Case CRUD ---
		{
			Name:        "create_test_case",
			Description: "Create a new test case in a project.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
				prop("title", "string", "Test case title", true),
				prop("description", "string", "Test case description/steps", true),
				prop("category", "string", "Category: functional, api, ui, security, performance, accessibility", false),
				prop("priority", "string", "Priority: critical, high, medium, low", false),
			)),
		},
		{
			Name:        "update_test_case",
			Description: "Update an existing test case.",
			InputSchema: obj(props(
				prop("test_case_id", "integer", "Test case ID", true),
				prop("title", "string", "New title", false),
				prop("description", "string", "New description", false),
				prop("category", "string", "New category", false),
				prop("priority", "string", "New priority", false),
				prop("status", "string", "New status: active, draft, deprecated", false),
			)),
		},
		{
			Name:        "delete_test_case",
			Description: "Delete a test case. This is irreversible.",
			InputSchema: obj(props(
				prop("test_case_id", "integer", "Test case ID to delete", true),
			)),
		},

		// --- Project CRUD ---
		{
			Name:        "create_project",
			Description: "Create a new QualityMax project.",
			InputSchema: obj(props(
				prop("name", "string", "Project name", true),
				prop("description", "string", "Project description", false),
				prop("base_url", "string", "Base URL of the app under test", false),
			)),
		},
		{
			Name:        "update_project",
			Description: "Update a project's name, description, or base URL.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
				prop("name", "string", "New name", false),
				prop("description", "string", "New description", false),
				prop("base_url", "string", "New base URL", false),
			)),
		},
		{
			Name:        "delete_project",
			Description: "Delete a project and all its test cases and scripts. This is irreversible.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID to delete", true),
			)),
		},
		{
			Name:        "get_project_by_slug",
			Description: "Look up a project by its URL slug (e.g., 'amber-panda', 'oak-mango'). Use this when the user provides a project URL or slug instead of an ID.",
			InputSchema: obj(props(
				prop("slug", "string", "Project slug from URL (e.g., amber-panda)", true),
			)),
		},
		{
			Name:        "get_project_summary",
			Description: "Get a project's summary including test case count, script count, and recent activity.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
			)),
		},

		// --- Framework Operations ---
		{
			Name:        "trigger_framework_run",
			Description: "Trigger a CI test run for a project's framework (runs all tests in the GitHub Action).",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
				prop("framework_type", "string", "Framework: playwright or pytest", false),
			)),
		},
		{
			Name:        "export_framework",
			Description: "Export a project's test framework as a downloadable zip with all scripts and config.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
				prop("framework", "string", "Framework type: playwright (default) or pytest", false),
			)),
		},
		{
			Name:        "get_install_command",
			Description: "Get the one-liner install command for a project's test framework (for CI/CD setup).",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
			)),
		},

		// --- AI-Powered ---
		{
			Name:        "enhance_test_case",
			Description: "Use AI to enhance a test case — adds detailed steps, edge cases, and assertions.",
			InputSchema: obj(props(
				prop("test_case_id", "integer", "Test case ID to enhance", true),
			)),
		},
		{
			Name:        "generate_gap_tests",
			Description: "Analyze a repository and generate test cases for untested code paths.",
			InputSchema: obj(props(
				prop("repo_id", "integer", "Repository ID", true),
			)),
		},
		{
			Name:        "start_crawl_from_test_case",
			Description: "Start an AI crawl that targets a specific test case — generates automation for that test scenario.",
			InputSchema: obj(props(
				prop("test_case_id", "integer", "Test case ID", true),
			)),
		},

		// --- Page Analysis (for test healing) ---
		{
			Name:        "analyze_screenshot",
			Description: "Analyze a test execution screenshot to understand what's visible on the page. Use this after a test fails to see what the page actually looks like — helps identify correct selectors, missing elements, and page state. Returns a description of visible elements, text, and layout.",
			InputSchema: obj(props(
				prop("execution_id", "string", "Execution ID from a test run", true),
			)),
		},
		{
			Name:        "get_page_elements",
			Description: "Get a list of visible interactive elements on the page from a test execution screenshot. Returns element roles, text, and suggested Playwright selectors. Use this to find the correct selectors when healing a broken test.",
			InputSchema: obj(props(
				prop("execution_id", "string", "Execution ID from a test run", true),
			)),
		},

		// --- QTML ---
		{
			Name:        "export_qtml",
			Description: "Export a project's test cases as QTML (QualityMax Test Markup Language) for portability.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID to export", true),
			)),
		},
		{
			Name:        "import_qtml",
			Description: "Import test cases from QTML format into a project.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Target project ID", true),
				prop("content", "string", "QTML content to import", true),
			)),
		},

		// --- Deployment Testing ---
		{
			Name:        "test_deployed_environment",
			Description: "Run a smoke test against a deployed environment URL.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
				prop("url", "string", "Deployment URL to test", true),
			)),
		},

		// --- Crawl operations ---
		{
			Name:        "start_crawl",
			Description: "Start an AI-powered crawl to discover pages and generate tests. The crawl navigates the site, captures screenshots, and generates Playwright test scripts.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
				prop("url", "string", "URL to crawl", true),
				prop("depth", "integer", "Max crawl depth (default 3)", false),
				prop("pages", "integer", "Max pages to crawl (default 10)", false),
				prop("test_type", "string", "Test type: e2e, functional, ui, integration", false),
				prop("instructions", "string", "Custom instructions for the AI crawler", false),
			)),
		},
		{
			Name:        "crawl_status",
			Description: "Check the status and progress of a crawl job.",
			InputSchema: obj(props(
				prop("crawl_id", "string", "Crawl job ID", true),
			)),
		},
		{
			Name:        "crawl_results",
			Description: "Get the results of a completed crawl — generated test cases and scripts.",
			InputSchema: obj(props(
				prop("crawl_id", "string", "Crawl job ID", true),
			)),
		},
		{
			Name:        "list_crawl_jobs",
			Description: "List recent crawl jobs.",
			InputSchema: obj(props(
				prop("limit", "integer", "Max jobs to return (default 20)", false),
			)),
		},

		// --- Repository operations ---
		{
			Name:        "list_repos",
			Description: "List imported repositories for a project.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
			)),
		},
		{
			Name:        "review_repo",
			Description: "Start an AI-powered code review for a repository. Analyzes testing quality, security, and suggests improvements. Respects the user's saved review preferences (which categories to check/skip).",
			InputSchema: obj(props(
				prop("repo_id", "integer", "Repository ID", true),
			)),
		},
		{
			Name:        "get_review_preferences",
			Description: "Get the user's AI code-review preferences (which categories to check, which to skip). Returns effective preferences: per-repo overrides merged over global defaults. Call without repository_id for global defaults only.",
			InputSchema: obj(props(
				prop("repository_id", "integer", "Repository ID. Omit to get global defaults only.", false),
			)),
		},
		{
			Name:        "set_review_preferences",
			Description: "Set or update AI code-review preferences. Categories: security, performance, test_coverage, type_safety, accessibility, style, secrets_scanning, ai_safety_for_agents (booleans). Plus optional custom_focus (string, max 500 chars). Use scope='global' for defaults, scope='repo' with repository_id for per-repo overrides.",
			InputSchema: obj(props(
				prop("scope", "string", "global or repo", true),
				prop("repository_id", "integer", "Required when scope is repo", false),
				prop("preferences", "object", "Key-value map of review categories (boolean) and optional custom_focus (string)", true),
			)),
		},
		{
			Name:        "repo_coverage",
			Description: "Get test coverage analysis for a repository.",
			InputSchema: obj(props(
				prop("repo_id", "integer", "Repository ID", true),
			)),
		},
		{
			Name:        "repo_quality",
			Description: "Get quality signal snapshot for a repository.",
			InputSchema: obj(props(
				prop("repo_id", "integer", "Repository ID", true),
			)),
		},

		// --- Import operations ---
		{
			Name:        "import_repo",
			Description: "Import a GitHub or GitLab repository for analysis and test generation.",
			InputSchema: obj(props(
				prop("url", "string", "Repository URL (e.g., https://github.com/user/repo)", true),
				prop("project_id", "integer", "Project ID to associate with", false),
				prop("create_project", "boolean", "Create a new project for this repo", false),
				prop("project_name", "string", "Name for the new project", false),
				prop("base_url", "string", "Base URL for testing", false),
				prop("training_consent", "string", "Optional explicit consent value: opt_in or opt_out. Omit if the user has not chosen.", false),
			)),
		},
		{
			Name:        "import_document",
			Description: "Import test cases from text content — requirements, specs, user stories, PRDs. The AI extracts structured test cases.",
			InputSchema: obj(props(
				prop("project_id", "integer", "Project ID", true),
				prop("text", "string", "Text content containing requirements or test descriptions", true),
				prop("source_name", "string", "Name for the import source", false),
			)),
		},

		// --- PR operations ---
		{
			Name:        "create_pr",
			Description: "Create a GitHub pull request with the generated test suite for a repository.",
			InputSchema: obj(props(
				prop("repo_id", "integer", "Repository ID", true),
				prop("project_id", "integer", "Project ID", true),
			)),
		},

		// --- Local operations ---
		{
			Name:        "read_file",
			Description: "Read a local file. Use for examining test files, configs, source code, etc.",
			InputSchema: obj(props(
				prop("path", "string", "File path to read", true),
			)),
		},
		{
			Name:        "run_command",
			Description: "Run a shell command locally. Use for git operations, npm commands, checking project structure, etc.",
			InputSchema: obj(props(
				prop("command", "string", "Shell command to execute", true),
			)),
		},
		{
			Name:        "write_file",
			Description: "Write content to a local file. Use for creating test files, configs, etc.",
			InputSchema: obj(props(
				prop("path", "string", "File path to write", true),
				prop("content", "string", "File content to write", true),
			)),
		},

		// --- Test healing operations ---
		{
			Name:        "get_script",
			Description: "Get the full code of an automation script by ID. Use this to read a script before healing or modifying it.",
			InputSchema: obj(props(
				prop("script_id", "integer", "Script ID to fetch", true),
			)),
		},
		{
			Name:        "update_script",
			Description: "Update the code of an existing automation script. SECURITY: Code is scanned before saving. Only use for healing/fixing broken tests. Always get_script first, fix the issue, then update.",
			InputSchema: obj(props(
				prop("script_id", "integer", "Script ID to update", true),
				prop("name", "string", "Script name", true),
				prop("code", "string", "New script code (must pass security scan)", true),
			)),
		},
		{
			Name:        "rollback_script",
			Description: "Rollback a script to its previous version (before the last heal). Use if healing made things worse.",
			InputSchema: obj(props(
				prop("script_id", "integer", "Script ID to rollback", true),
			)),
		},
	}
}

// ExecuteTool executes a tool and returns the output string.
// Uses the API client for QualityMax operations (no qmax CLI needed).
// Falls back to qmax CLI if API client is not available.
func ExecuteTool(name string, rawInput interface{}, sctx *SessionContext, ctx context.Context) string {
	// Use API client if available (standalone mode)
	if sctx.API != nil {
		result := executeToolViaAPI(name, rawInput, sctx, ctx)
		if result != "" {
			return result
		}
		// Fall through to qmax CLI for unhandled tools
	}
	return executeToolViaQMax(name, rawInput, sctx, ctx)
}

// executeToolViaAPI handles tool execution through the QualityMax REST API.
func executeToolViaAPI(name string, rawInput interface{}, sctx *SessionContext, ctx context.Context) string {
	input := parseInput(rawInput)
	api := sctx.API

	switch name {
	case "list_projects":
		return api.ListProjects(ctx)

	case "list_test_cases":
		return api.ListTestCases(ctx, intVal(input, "project_id", sctx.ProjectID), intVal(input, "limit", 0), strVal(input, "search"))

	case "list_scripts":
		return api.ListScripts(ctx, intVal(input, "project_id", sctx.ProjectID), intVal(input, "limit", 0))

	case "generate_test_code":
		// If the caller didn't pass a framework, fall back to the one the
		// wizard detected in the cwd (Cargo.toml → rust_cargo, go.mod →
		// go_test, etc.). Lets Rust/Go users run `qmax-code generate` and
		// get native scripts without having to spell out the framework
		// on every call.
		//
		// Intentional: omitted-field and empty-string are treated the same
		// here. Users who want to FORCE server-side auto-detect (bypassing
		// DefaultFramework) should remove DefaultFramework from their config
		// rather than trying to pass framework="". The alternative (using
		// map key presence to distinguish) would leak JSON-parsing quirks
		// into the agent loop and trips up nearly every LLM.
		fw := strVal(input, "framework")
		if fw == "" {
			if cfg := LoadQMaxCodeConfig(); cfg != nil {
				fw = cfg.DefaultFramework
			}
		}
		return api.GenerateTestCode(ctx, intVal(input, "test_case_id", 0), boolVal(input, "force"), fw)

	case "run_test":
		return runTestWithProgress(ctx, api, intVal(input, "script_id", 0), boolVal(input, "headless"), strVal(input, "browser"), strVal(input, "base_url"))

	case "run_native_test":
		return api.RunNativeTest(ctx, intVal(input, "script_id", 0), strVal(input, "base_url"))

	case "setup_cicd":
		return api.SetupCICD(ctx, intVal(input, "repo_id", 0), strVal(input, "framework"), strVal(input, "target_branch"), strVal(input, "base_url"))

	case "run_tests_batch":
		return api.RunTestsBatch(ctx, strVal(input, "script_ids"), strVal(input, "base_url"))

	case "check_test_status":
		return api.CheckTestStatus(ctx, strVal(input, "execution_id"))

	case "start_crawl":
		return api.StartCrawl(ctx, intVal(input, "project_id", sctx.ProjectID), strVal(input, "url"),
			intVal(input, "depth", 0), intVal(input, "pages", 0), strVal(input, "test_type"), strVal(input, "instructions"))

	case "crawl_status":
		return api.CrawlStatus(ctx, strVal(input, "crawl_id"))

	case "crawl_results":
		return api.CrawlResults(ctx, strVal(input, "crawl_id"))

	case "list_crawl_jobs":
		return api.ListCrawlJobs(ctx, intVal(input, "limit", 0))

	case "list_repos":
		return api.ListRepos(ctx, intVal(input, "project_id", sctx.ProjectID))

	case "review_repo":
		return api.ReviewRepo(ctx, intVal(input, "repo_id", 0))

	case "get_review_preferences":
		return api.GetReviewPreferences(ctx, intVal(input, "repository_id", 0))

	case "set_review_preferences":
		return api.SetReviewPreferences(ctx, strVal(input, "scope"), intVal(input, "repository_id", 0), input["preferences"])

	case "repo_coverage":
		return api.RepoCoverage(ctx, intVal(input, "repo_id", 0))

	case "repo_quality":
		return api.RepoQuality(ctx, intVal(input, "repo_id", 0))

	case "import_repo":
		return api.ImportRepo(ctx, strVal(input, "url"), intVal(input, "project_id", sctx.ProjectID),
			boolVal(input, "create_project"), strVal(input, "project_name"), strVal(input, "base_url"), strVal(input, "training_consent"))

	case "import_document":
		return api.ImportDocument(ctx, intVal(input, "project_id", sctx.ProjectID), strVal(input, "text"), strVal(input, "source_name"))

	case "create_pr":
		return api.CreatePR(ctx, intVal(input, "repo_id", 0), intVal(input, "project_id", sctx.ProjectID))

	case "get_script":
		return api.GetScript(ctx, intVal(input, "script_id", 0))

	case "update_script":
		code := strVal(input, "code")
		if code == "" {
			code = strVal(input, "content")
		}
		if violations := scanCodeSecurity(code); len(violations) > 0 {
			return fmt.Sprintf(`{"error": "Security scan failed", "violations": %q}`, strings.Join(violations, "; "))
		}
		scriptID := intVal(input, "script_id", 0)
		backup := api.GetScript(ctx, scriptID)
		if !strings.HasPrefix(backup, `{"error"`) {
			saveScriptBackup(fmt.Sprintf("%d", scriptID), backup)
		}
		return api.UpdateScript(ctx, scriptID, strVal(input, "name"), code)

	case "check_job_status":
		return api.CheckBackgroundJob(ctx, strVal(input, "job_id"))

	// --- k6 Performance Testing ---
	case "k6_list_scripts":
		return api.K6ListScripts(ctx, intVal(input, "project_id", sctx.ProjectID))
	case "k6_create_script":
		return api.K6CreateScript(ctx, intVal(input, "project_id", sctx.ProjectID), strVal(input, "name"), strVal(input, "test_type"), strVal(input, "target_url"), strVal(input, "code"))
	case "k6_get_script":
		return api.K6GetScript(ctx, intVal(input, "script_id", 0))
	case "k6_run_test":
		return api.K6RunTest(ctx, intVal(input, "script_id", 0), intVal(input, "vus", 0), strVal(input, "duration"))
	case "k6_check_status":
		return api.K6CheckStatus(ctx, strVal(input, "execution_id"))
	case "k6_report":
		return api.K6Report(ctx, strVal(input, "execution_id"))
	case "k6_generate":
		return api.K6Generate(ctx, intVal(input, "project_id", sctx.ProjectID), strVal(input, "target_url"), strVal(input, "test_type"), strVal(input, "endpoints"))
	case "k6_convert":
		return api.K6Convert(ctx, strVal(input, "source_code"), strVal(input, "source_framework"), strVal(input, "test_type"))

	// --- Test Case CRUD ---
	case "create_test_case":
		return api.CreateTestCase(ctx, intVal(input, "project_id", sctx.ProjectID), strVal(input, "title"), strVal(input, "description"), strVal(input, "category"), strVal(input, "priority"))
	case "update_test_case":
		return api.UpdateTestCase(ctx, intVal(input, "test_case_id", 0), strVal(input, "title"), strVal(input, "description"), strVal(input, "category"), strVal(input, "priority"), strVal(input, "status"))
	case "delete_test_case":
		return api.DeleteTestCase(ctx, intVal(input, "test_case_id", 0))

	// --- Project CRUD ---
	case "create_project":
		return api.CreateProject(ctx, strVal(input, "name"), strVal(input, "description"), strVal(input, "base_url"))
	case "update_project":
		return api.UpdateProject(ctx, intVal(input, "project_id", sctx.ProjectID), strVal(input, "name"), strVal(input, "description"), strVal(input, "base_url"))
	case "delete_project":
		return api.DeleteProject(ctx, intVal(input, "project_id", 0))
	case "get_project_by_slug":
		return api.GetProjectBySlug(ctx, strVal(input, "slug"))
	case "get_project_summary":
		return api.GetProjectSummary(ctx, intVal(input, "project_id", sctx.ProjectID))

	// --- Framework Operations ---
	case "trigger_framework_run":
		return api.TriggerFrameworkRun(ctx, intVal(input, "project_id", sctx.ProjectID), strVal(input, "framework_type"))
	case "export_framework":
		return api.ExportFramework(ctx, intVal(input, "project_id", sctx.ProjectID), strVal(input, "framework"))
	case "get_install_command":
		return api.GetInstallCommand(ctx, intVal(input, "project_id", sctx.ProjectID))

	// --- AI-Powered ---
	case "enhance_test_case":
		return api.EnhanceTestCase(ctx, intVal(input, "test_case_id", 0))
	case "generate_gap_tests":
		return api.GenerateGapTests(ctx, intVal(input, "repo_id", 0))
	case "start_crawl_from_test_case":
		return api.StartCrawlFromTestCase(ctx, intVal(input, "test_case_id", 0))

	case "analyze_screenshot":
		return analyzeScreenshot(ctx, api, strVal(input, "execution_id"), sctx)
	case "get_page_elements":
		return getPageElements(ctx, api, strVal(input, "execution_id"), sctx)

	// --- QTML ---
	case "export_qtml":
		return api.ExportQTML(ctx, intVal(input, "project_id", sctx.ProjectID))
	case "import_qtml":
		return api.ImportQTML(ctx, intVal(input, "project_id", sctx.ProjectID), strVal(input, "content"))

	// --- Deployment Testing ---
	case "test_deployed_environment":
		return api.TestDeployedEnvironment(ctx, intVal(input, "project_id", sctx.ProjectID), strVal(input, "url"))

	case "run_local_test":
		return runLocalTest(ctx, sctx, intVal(input, "script_id", 0), strVal(input, "base_url"))
	}

	// Not handled by API — return empty to fall through to qmax CLI
	return ""
}

// runTestWithProgress starts a cloud test and polls with a live progress bar + browser animation.
func runTestWithProgress(ctx context.Context, api *APIClient, scriptID int, headless bool, browser, baseURL string) string {
	// Start the test
	raw := api.RunTest(ctx, scriptID, headless, browser, baseURL)
	if strings.HasPrefix(raw, `{"error"`) {
		return raw
	}

	var startResp map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &startResp); err != nil {
		return raw // return as-is if can't parse
	}

	execID, _ := startResp["execution_id"].(string)
	if execID == "" {
		return raw
	}

	// Show browser animation + progress bar
	fmt.Println()
	ShowBrowserAnimation(0)
	progress := NewProgressBar("Running test...", 30)

	// Poll until done
	frame := 1
	for i := 0; i < 120; i++ { // max 4 minutes (120 * 2s)
		time.Sleep(2 * time.Second)

		statusRaw := api.CheckTestStatus(ctx, execID)
		var status map[string]interface{}
		if err := json.Unmarshal([]byte(statusRaw), &status); err != nil {
			continue
		}

		st, _ := status["status"].(string)
		msg, _ := status["message"].(string)
		pct := 0
		if p, ok := status["progress"].(float64); ok {
			pct = int(p)
		}

		// Animate browser
		ShowBrowserAnimation(frame)
		frame++

		// Update progress
		if st == "running" || st == "queued" {
			if pct == 0 {
				pct = min(10+i*3, 90) // fake progress if backend doesn't report
			}
			progress.Update(pct, msg)
		}

		if st == "passed" || st == "failed" || st == "completed" {
			ClearBrowserAnimation()
			progress.Finish(st == "passed", msg)
			return statusRaw
		}
	}

	ClearBrowserAnimation()
	progress.Finish(false, "Timed out")
	return api.CheckTestStatus(ctx, execID) // final check
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// runLocalTest downloads a script from QualityMax and runs it. Execution
// location depends on the script's framework:
//
//	pytest / playwright → executed LOCALLY (pytest/npx on the user's machine)
//	rust_cargo / go_test → delegated to the QualityMax execution API because
//	  scaffolding a Cargo.toml / go.mod and running cargo/go locally is
//	  toolchain-heavy.
//
// The function name reads as "local" but for native toolchains it transparently
// falls back to remote. This is intentional — the agent's tool-call surface
// doesn't need to know which path runs; users get "tests ran" either way.
func runLocalTest(ctx context.Context, sctx *SessionContext, scriptID int, baseURL string) string {
	if sctx.API == nil {
		return jsonError("Not connected to QualityMax. Run /connect first.")
	}

	// 1. Fetch script details
	raw := sctx.API.GetScript(ctx, scriptID)
	if strings.HasPrefix(raw, `{"error"`) {
		return raw
	}

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return jsonError("Failed to parse script: " + err.Error())
	}

	// API returns {"success": true, "script": {...}} — unwrap
	script, _ := resp["script"].(map[string]interface{})
	if script == nil {
		script = resp // fallback: maybe the response IS the script
	}

	code := ""
	if c, ok := script["code"].(string); ok {
		code = c
	}
	if code == "" {
		return jsonError("Script has no code")
	}

	framework := ""
	if f, ok := script["framework"].(string); ok {
		framework = f
	}
	name := ""
	if n, ok := script["name"].(string); ok {
		name = n
	}

	// 2. Write to temp file
	tmpDir, err := os.MkdirTemp("", "qmax-test-*")
	if err != nil {
		return jsonError("Failed to create temp dir: " + err.Error())
	}
	defer os.RemoveAll(tmpDir)

	var fileName string
	var cmd *exec.Cmd

	switch framework {
	case "pytest", "unittest":
		fileName = filepath.Join(tmpDir, "test_script.py")
		if err := os.WriteFile(fileName, []byte(code), 0644); err != nil {
			return jsonError("Failed to write test file: " + err.Error())
		}
		junitFile := filepath.Join(tmpDir, "results.xml")
		args := []string{"-m", "pytest", fileName, "-v", "--tb=short", "--junitxml=" + junitFile}
		if baseURL != "" {
			args = append(args, "--base-url="+baseURL)
		}
		cmd = exec.CommandContext(ctx, "python3", args...)

	case "playwright":
		fileName = filepath.Join(tmpDir, "test_script.spec.js")
		if err := os.WriteFile(fileName, []byte(code), 0644); err != nil {
			return jsonError("Failed to write test file: " + err.Error())
		}
		args := []string{"playwright", "test", fileName}
		if baseURL != "" {
			args = append(args, "--base-url="+baseURL)
		}
		cmd = exec.CommandContext(ctx, "npx", args...)

	case "rust_cargo", "rust", "cargo", "go_test", "go":
		// Native (Rust/Go) scripts are toolchain-heavy and need a scaffolded
		// Cargo.toml / go.mod to build. Rather than duplicate that logic
		// client-side, delegate to the QualityMax execution API.
		return sctx.API.RunNativeTest(ctx, scriptID, baseURL)

	default:
		return jsonError(fmt.Sprintf(
			"Framework '%s' not supported for local execution. "+
				"Supported locally: pytest, playwright. "+
				"For rust_cargo/go_test scripts, use the run_native_test tool — "+
				"it runs through the QualityMax execution API. "+
				"(Local execution of those frameworks would require us to "+
				"scaffold a Cargo.toml / go.mod per run, plus download a few "+
				"hundred MB of dependencies on your machine — running on the "+
				"managed runner is faster and avoids polluting your local $GOPATH / "+
				"$CARGO_HOME.)",
			framework,
		))
	}

	// 3. Run with timeout
	// Cap buffers at the pipe level so a verbose test suite on a large repo
	// cannot inflate our process memory before we get to truncate.
	stdout := newLimitWriter(3 * 1024 * 1024) // 3 MB hard cap
	stderr := newLimitWriter(512 * 1024)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Dir = tmpDir

	if baseURL != "" {
		cmd.Env = append(os.Environ(), "BASE_URL="+baseURL)
	}

	startTime := time.Now()
	runErr := cmd.Run()
	duration := time.Since(startTime)

	// 4. Build result
	passed := runErr == nil
	output := redactSensitive(stdout.String())
	if stderr.n > 0 {
		output += "\n--- stderr ---\n" + redactSensitive(stderr.String())
	}

	// Trim output if too long (display budget)
	if len(output) > 5000 {
		output = output[:2000] + "\n...\n" + output[len(output)-2000:]
	}

	// 5. Report results back to QualityMax
	statusStr := "failed"
	if passed {
		statusStr = "passed"
	}
	reportResp := sctx.API.ReportLocalResult(ctx, scriptID, statusStr, output, framework, duration.Seconds())

	// Parse report response for execution_id
	var reportData map[string]interface{}
	reportExecID := ""
	if err := json.Unmarshal([]byte(reportResp), &reportData); err == nil {
		reportExecID, _ = reportData["execution_id"].(string)
	}

	result := map[string]interface{}{
		"script_id":    scriptID,
		"name":         name,
		"framework":    framework,
		"passed":       passed,
		"duration":     fmt.Sprintf("%.1fs", duration.Seconds()),
		"output":       output,
		"reported":     !strings.HasPrefix(reportResp, `{"error"`),
		"execution_id": reportExecID,
	}

	data, _ := json.Marshal(result)
	return string(data)
}

// executeToolViaQMax handles tool execution through the qmax CLI binary (legacy).
func executeToolViaQMax(name string, rawInput interface{}, sctx *SessionContext, ctx context.Context) string {
	// Parse input
	input := make(map[string]interface{})
	switch v := rawInput.(type) {
	case map[string]interface{}:
		input = v
	default:
		data, _ := json.Marshal(rawInput)
		_ = json.Unmarshal(data, &input)
	}

	switch name {
	// --- qmax CLI wrappers ---
	case "list_projects":
		return runQMax(sctx, ctx, "projects", "--json")

	case "list_test_cases":
		args := []string{"test", "cases", "--json", "--project-id", intArg(input, "project_id", sctx.ProjectID)}
		if v, ok := input["limit"]; ok {
			args = append(args, "--limit", fmt.Sprintf("%v", v))
		}
		if v, ok := input["search"]; ok && v != "" {
			args = append(args, "--search", fmt.Sprintf("%v", v))
		}
		return runQMax(sctx, ctx, args...)

	case "list_scripts":
		args := []string{"test", "scripts", "--json", "--project-id", intArg(input, "project_id", sctx.ProjectID)}
		if v, ok := input["limit"]; ok {
			args = append(args, "--limit", fmt.Sprintf("%v", v))
		}
		return runQMax(sctx, ctx, args...)

	case "generate_test_code":
		args := []string{"test", "generate", "--json", "--test-case-id", fmt.Sprintf("%v", input["test_case_id"])}
		if v, ok := input["force"]; ok && v == true {
			args = append(args, "--force")
		}
		return runQMax(sctx, ctx, args...)

	case "run_test":
		args := []string{"test", "run", "--json", "--wait", "--script-id", fmt.Sprintf("%v", input["script_id"])}
		if v, ok := input["headless"]; ok {
			args = append(args, "--headless", fmt.Sprintf("%v", v))
		}
		if v, ok := input["browser"]; ok && v != "" {
			args = append(args, "--browser", fmt.Sprintf("%v", v))
		}
		if v, ok := input["base_url"]; ok && v != "" {
			args = append(args, "--base-url", fmt.Sprintf("%v", v))
		}
		return runQMax(sctx, ctx, args...)

	case "run_tests_batch":
		args := []string{"test", "run", "--json", "--script-ids", fmt.Sprintf("%v", input["script_ids"])}
		if v, ok := input["base_url"]; ok && v != "" {
			args = append(args, "--base-url", fmt.Sprintf("%v", v))
		}
		return runQMax(sctx, ctx, args...)

	case "check_test_status":
		return runQMax(sctx, ctx, "test", "status", "--json", "--execution-id", fmt.Sprintf("%v", input["execution_id"]))

	case "start_crawl":
		args := []string{"crawl", "start", "--json", "--wait",
			"--project-id", intArg(input, "project_id", sctx.ProjectID),
			"--url", fmt.Sprintf("%v", input["url"])}
		if v, ok := input["depth"]; ok {
			args = append(args, "--depth", fmt.Sprintf("%v", v))
		}
		if v, ok := input["pages"]; ok {
			args = append(args, "--pages", fmt.Sprintf("%v", v))
		}
		if v, ok := input["test_type"]; ok && v != "" {
			args = append(args, "--test-type", fmt.Sprintf("%v", v))
		}
		if v, ok := input["instructions"]; ok && v != "" {
			args = append(args, "--instructions", fmt.Sprintf("%v", v))
		}
		return runQMax(sctx, ctx, args...)

	case "crawl_status":
		return runQMax(sctx, ctx, "crawl", "status", "--json", "--crawl-id", fmt.Sprintf("%v", input["crawl_id"]))

	case "crawl_results":
		return runQMax(sctx, ctx, "crawl", "results", "--json", "--crawl-id", fmt.Sprintf("%v", input["crawl_id"]))

	case "list_crawl_jobs":
		args := []string{"crawl", "jobs", "--json"}
		if v, ok := input["limit"]; ok {
			args = append(args, "--limit", fmt.Sprintf("%v", v))
		}
		return runQMax(sctx, ctx, args...)

	case "list_repos":
		return runQMax(sctx, ctx, "repo", "list", "--json", "--project-id", intArg(input, "project_id", sctx.ProjectID))

	case "review_repo":
		return runQMax(sctx, ctx, "repo", "review", "--json", "--wait", "--repo-id", fmt.Sprintf("%v", input["repo_id"]))

	case "repo_coverage":
		return runQMax(sctx, ctx, "repo", "coverage", "--json", "--repo-id", fmt.Sprintf("%v", input["repo_id"]))

	case "repo_quality":
		return runQMax(sctx, ctx, "repo", "quality", "--json", "--repo-id", fmt.Sprintf("%v", input["repo_id"]))

	case "import_repo":
		args := []string{"import", "repo", "--json", "--url", fmt.Sprintf("%v", input["url"])}
		pid := intArg(input, "project_id", sctx.ProjectID)
		if pid != "0" {
			args = append(args, "--project-id", pid)
		}
		if v, ok := input["create_project"]; ok && v == true {
			args = append(args, "--create-project")
			if name, ok := input["project_name"]; ok && name != "" {
				args = append(args, "--project-name", fmt.Sprintf("%v", name))
			}
		}
		if v, ok := input["base_url"]; ok && v != "" {
			args = append(args, "--base-url", fmt.Sprintf("%v", v))
		}
		return runQMax(sctx, ctx, args...)

	case "import_document":
		args := []string{"import", "doc", "--json",
			"--project-id", intArg(input, "project_id", sctx.ProjectID),
			"--text", fmt.Sprintf("%v", input["text"])}
		if v, ok := input["source_name"]; ok && v != "" {
			args = append(args, "--source", fmt.Sprintf("%v", v))
		}
		return runQMax(sctx, ctx, args...)

	case "create_pr":
		return runQMax(sctx, ctx, "pr", "create", "--json",
			"--repo-id", fmt.Sprintf("%v", input["repo_id"]),
			"--project-id", intArg(input, "project_id", sctx.ProjectID))

	// --- Local operations ---
	case "read_file":
		return runShell(ctx, "cat", fmt.Sprintf("%v", input["path"]))

	case "run_command":
		cmd := fmt.Sprintf("%v", input["command"])
		if violation := validateCommand(cmd); violation != "" {
			return fmt.Sprintf(`{"error": "Command blocked: %s"}`, violation)
		}
		return runShell(ctx, "sh", "-c", cmd)

	case "write_file":
		path := fmt.Sprintf("%v", input["path"])
		content := fmt.Sprintf("%v", input["content"])

		// Security: restrict to current directory
		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Sprintf(`{"error": %q}`, err.Error())
		}
		cwd, _ := os.Getwd()
		if !strings.HasPrefix(absPath, cwd) {
			return `{"error": "write_file restricted to current directory for security"}`
		}

		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Sprintf(`{"error": %q}`, err.Error())
		}
		return fmt.Sprintf(`{"success": true, "path": %q, "bytes": %d}`, path, len(content))

	// --- Test healing operations ---
	case "get_script":
		scriptID := fmt.Sprintf("%v", input["script_id"])
		return fetchScriptCode(sctx, ctx, scriptID)

	case "update_script":
		scriptID := fmt.Sprintf("%v", input["script_id"])
		name := fmt.Sprintf("%v", input["name"])
		code := fmt.Sprintf("%v", input["content"])
		if code == "" || code == "<nil>" {
			code = fmt.Sprintf("%v", input["code"])
		}

		// Security scan before saving
		if violations := scanCodeSecurity(code); len(violations) > 0 {
			return fmt.Sprintf(`{"error": "Security scan failed", "violations": %q}`, strings.Join(violations, "; "))
		}

		// Backup current version before overwriting
		backup := fetchScriptCode(sctx, ctx, scriptID)
		if !strings.HasPrefix(backup, `{"error"`) {
			saveScriptBackup(scriptID, backup)
		}

		return updateScriptCode(sctx, ctx, scriptID, name, code)

	case "rollback_script":
		scriptID := fmt.Sprintf("%v", input["script_id"])
		return rollbackScript(sctx, ctx, scriptID)

	default:
		return fmt.Sprintf(`{"error": "Unknown tool: %s"}`, name)
	}
}

// runQMax executes a qmax CLI command and returns stdout.
func runQMax(sctx *SessionContext, ctx context.Context, args ...string) string {
	binary := sctx.QMaxBin
	if binary == "" {
		return fmt.Sprintf(`{"error": "qmax CLI not found. %s"}`, formatQMaxInstallHint())
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	stdout := newLimitWriter(512 * 1024) // 512 KB — qmax JSON responses are small
	stderr := newLimitWriter(64 * 1024)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return `{"error": "cancelled"}`
		}
		if stderr.n > 0 {
			return fmt.Sprintf(`{"error": %q}`, redactSensitive(stderr.String()))
		}
		return fmt.Sprintf(`{"error": %q}`, redactSensitive(err.Error()))
	}

	output := strings.TrimSpace(redactSensitive(stdout.String()))
	if output == "" {
		output = strings.TrimSpace(redactSensitive(stderr.String()))
	}
	return output
}

// runShell executes a shell command with output limits.
func runShell(ctx context.Context, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout := newLimitWriter(8000) // match the display budget
	stderr := newLimitWriter(4000)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return `{"error": "cancelled"}`
		}
		combined := stdout.String() + stderr.String()
		if combined != "" {
			return redactSensitive(combined)
		}
		return fmt.Sprintf(`{"error": %q}`, redactSensitive(err.Error()))
	}

	return redactSensitive(stdout.String())
}

// extractPlaywrightError parses Playwright test output and extracts the actual error
// with line numbers, locator info, and expected/received values.
func extractPlaywrightError(output string) string {
	lines := strings.Split(output, "\n")
	var errLines []string
	capturing := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Start capturing on error indicators
		if strings.Contains(trimmed, "Error:") ||
			strings.Contains(trimmed, "error:") ||
			strings.Contains(trimmed, "locator.") ||
			strings.Contains(trimmed, "expect(") ||
			strings.Contains(trimmed, "Timeout") ||
			strings.Contains(trimmed, "waiting for") ||
			strings.Contains(trimmed, "strict mode violation") {
			capturing = true
		}
		if capturing {
			errLines = append(errLines, "    "+trimmed)
			// Stop after enough context
			if len(errLines) > 15 {
				break
			}
		}
		// Also capture "at" lines for stack trace
		if strings.HasPrefix(trimmed, "at ") && len(errLines) > 0 && len(errLines) < 20 {
			errLines = append(errLines, "    "+trimmed)
		}
	}

	if len(errLines) == 0 {
		return ""
	}
	return strings.Join(errLines, "\n")
}

// intArg extracts an integer argument, falling back to a default.
func intArg(input map[string]interface{}, key string, fallback int) string {
	if v, ok := input[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	if fallback > 0 {
		return fmt.Sprintf("%d", fallback)
	}
	return "0"
}

// ToolCost classifies tools by cost impact.
func ToolCost(name string) string {
	switch name {
	case "list_projects", "list_test_cases", "list_scripts", "check_test_status",
		"crawl_status", "crawl_results", "list_crawl_jobs", "list_repos",
		"repo_coverage", "repo_quality", "read_file", "run_command", "write_file",
		"get_script":
		return "free" // read-only or local, no cost
	case "generate_test_code":
		return "low" // AI generation, small cost
	case "run_test", "run_tests_batch":
		return "medium" // execution credits
	case "start_crawl", "review_repo":
		return "high" // significant AI + execution cost
	case "import_repo", "import_document", "create_pr", "update_script", "rollback_script":
		return "medium"
	case "get_review_preferences", "set_review_preferences":
		return "free"
	default:
		return "free"
	}
}

// SummarizeToolResult parses common JSON responses and returns human-readable summaries.
// The summary is shown in the terminal AND sent to the LLM to save tokens.
func SummarizeToolResult(name, output string) string {
	// Check for error responses first
	if strings.HasPrefix(output, `{"error"`) {
		var errData map[string]interface{}
		if err := json.Unmarshal([]byte(output), &errData); err == nil {
			if msg, ok := errData["error"]; ok {
				return fmt.Sprintf("Error: %v", msg)
			}
		}
		return output
	}

	// Try to extract "detail" field (common API error format)
	var singleObj map[string]interface{}
	if err := json.Unmarshal([]byte(output), &singleObj); err == nil {
		if detail, ok := singleObj["detail"]; ok {
			return fmt.Sprintf("Error: %v", detail)
		}
	}

	switch name {
	case "list_projects":
		return summarizeProjects(output)
	case "list_test_cases":
		return summarizeTestCases(output)
	case "list_scripts":
		return summarizeScripts(output)
	case "run_test", "check_test_status":
		return summarizeExecution(output)
	case "run_tests_batch":
		return summarizeBatchExecution(output)
	case "crawl_status":
		return summarizeCrawlStatus(output)
	case "crawl_results":
		return summarizeCrawlResults(output)
	case "list_crawl_jobs":
		return summarizeCrawlJobs(output)
	case "start_crawl":
		return summarizeStartCrawl(output)
	case "get_script":
		return summarizeGetScript(output)
	case "update_script":
		return summarizeUpdateScript(output)
	default:
		// For unknown tools, try to pretty-print JSON; otherwise return as-is
		var data interface{}
		if err := json.Unmarshal([]byte(output), &data); err == nil {
			pretty, err := json.MarshalIndent(data, "", "  ")
			if err == nil && len(pretty) < len(output) {
				return string(pretty)
			}
		}
		return output
	}
}

func summarizeProjects(output string) string {
	var projects []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &projects); err != nil {
		// Try wrapped format: {"projects": [...]}
		var wrapped map[string]interface{}
		if err2 := json.Unmarshal([]byte(output), &wrapped); err2 != nil {
			return output
		}
		if arr, ok := wrapped["projects"]; ok {
			data, _ := json.Marshal(arr)
			if err := json.Unmarshal(data, &projects); err != nil {
				return output
			}
		} else {
			return output
		}
	}

	if len(projects) == 0 {
		return "No projects found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d projects:\n", len(projects)))
	// Show up to 20 projects
	limit := len(projects)
	if limit > 20 {
		limit = 20
	}
	for i := 0; i < limit; i++ {
		p := projects[i]
		id := p["id"]
		name := p["name"]
		slug, _ := p["slug"].(string)
		if slug != "" {
			sb.WriteString(fmt.Sprintf("  #%v %v (slug: %s)\n", id, name, slug))
		} else {
			sb.WriteString(fmt.Sprintf("  #%v %v\n", id, name))
		}
	}
	if len(projects) > 20 {
		sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(projects)-20))
	}
	return sb.String()
}

func summarizeTestCases(output string) string {
	var cases []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &cases); err != nil {
		var wrapped map[string]interface{}
		if err2 := json.Unmarshal([]byte(output), &wrapped); err2 != nil {
			return output
		}
		if arr, ok := wrapped["test_cases"]; ok {
			data, _ := json.Marshal(arr)
			if err := json.Unmarshal(data, &cases); err != nil {
				return output
			}
		} else {
			return output
		}
	}

	if len(cases) == 0 {
		return "No test cases found."
	}

	automated := 0
	manual := 0
	categories := map[string]int{}
	for _, tc := range cases {
		if hasScript, ok := tc["has_script"]; ok && hasScript == true {
			automated++
		} else if isAutomated, ok := tc["is_automated"]; ok && isAutomated == true {
			automated++
		} else {
			manual++
		}
		if cat, ok := tc["category"]; ok && cat != nil && cat != "" {
			categories[fmt.Sprintf("%v", cat)]++
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d test cases (%d automated, %d manual)\n", len(cases), automated, manual))

	if len(categories) > 0 {
		sb.WriteString("  Categories: ")
		first := true
		for cat, count := range categories {
			if !first {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s(%d)", cat, count))
			first = false
		}
		sb.WriteString("\n")
	}

	// List up to 15 test cases
	limit := len(cases)
	if limit > 15 {
		limit = 15
	}
	for i := 0; i < limit; i++ {
		tc := cases[i]
		id := tc["id"]
		title := tc["title"]
		sb.WriteString(fmt.Sprintf("  #%v %v\n", id, title))
	}
	if len(cases) > 15 {
		sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(cases)-15))
	}
	return sb.String()
}

func summarizeScripts(output string) string {
	var scripts []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &scripts); err != nil {
		var wrapped map[string]interface{}
		if err2 := json.Unmarshal([]byte(output), &wrapped); err2 != nil {
			return output
		}
		if arr, ok := wrapped["scripts"]; ok {
			data, _ := json.Marshal(arr)
			if err := json.Unmarshal(data, &scripts); err != nil {
				return output
			}
		} else {
			return output
		}
	}

	if len(scripts) == 0 {
		return "No scripts found."
	}

	// Group by framework
	groups := map[string][]map[string]interface{}{}
	for _, s := range scripts {
		fw := "unknown"
		if f, ok := s["framework"]; ok && f != nil && f != "" {
			fw = strings.ToLower(fmt.Sprintf("%v", f))
		}
		groups[fw] = append(groups[fw], s)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d scripts:\n", len(scripts)))

	for fw, items := range groups {
		canRun := ""
		switch fw {
		case "playwright", "cypress":
			canRun = " — can run on cloud"
		case "pytest", "unittest":
			canRun = " — local execution only"
		}

		ids := make([]string, 0, len(items))
		for _, s := range items {
			ids = append(ids, fmt.Sprintf("#%v", s["id"]))
		}
		fwLabel := strings.ToUpper(fw[:1]) + fw[1:]
		sb.WriteString(fmt.Sprintf("  %s (%d): %s%s\n", fwLabel, len(items), strings.Join(ids, ", "), canRun))
	}

	// List scripts with titles
	sb.WriteString("\n")
	for _, s := range scripts {
		id := s["id"]
		title := s["title"]
		if title == nil || title == "" {
			title = s["name"]
		}
		fw := ""
		if f, ok := s["framework"]; ok && f != nil {
			fw = fmt.Sprintf(" [%v]", f)
		}
		sb.WriteString(fmt.Sprintf("  #%v %v%s\n", id, title, fw))
	}
	return sb.String()
}

func summarizeExecution(output string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return output
	}

	var sb strings.Builder

	// Extract execution info
	if execID, ok := data["execution_id"]; ok {
		sb.WriteString(fmt.Sprintf("Execution: %v\n", execID))
	} else if id, ok := data["id"]; ok {
		sb.WriteString(fmt.Sprintf("Execution: %v\n", id))
	}

	if status, ok := data["status"]; ok {
		sb.WriteString(fmt.Sprintf("  Status: %v\n", status))
	}

	if result, ok := data["result"]; ok {
		sb.WriteString(fmt.Sprintf("  Result: %v\n", result))
	}

	if duration, ok := data["duration"]; ok {
		sb.WriteString(fmt.Sprintf("  Duration: %vs\n", duration))
	} else if dur, ok := data["duration_seconds"]; ok {
		sb.WriteString(fmt.Sprintf("  Duration: %vs\n", dur))
	}

	// Pass/fail counts
	if passed, ok := data["passed"]; ok {
		sb.WriteString(fmt.Sprintf("  Passed: %v\n", passed))
	}
	if failed, ok := data["failed"]; ok {
		sb.WriteString(fmt.Sprintf("  Failed: %v\n", failed))
	}

	// Message
	if msg, ok := data["message"].(string); ok && msg != "" {
		sb.WriteString(fmt.Sprintf("  Message: %s\n", msg))
	}

	// Execution time
	if dur, ok := data["execution_time"].(float64); ok && dur > 0 {
		sb.WriteString(fmt.Sprintf("  Duration: %.1fs\n", dur))
	}

	// Error message
	if errMsg, ok := data["error"]; ok && errMsg != nil && errMsg != "" {
		sb.WriteString(fmt.Sprintf("  Error: %v\n", errMsg))
	}
	if errMsg, ok := data["error_message"]; ok && errMsg != nil && errMsg != "" {
		sb.WriteString(fmt.Sprintf("  Error: %v\n", errMsg))
	}
	if errMsg, ok := data["test_errors"].(string); ok && errMsg != "" {
		sb.WriteString(fmt.Sprintf("  Test errors: %s\n", truncateStr(errMsg, 1000)))
	}

	// test_output — extract Playwright error lines (locator failures, timeouts, assertion errors)
	if testOut, ok := data["test_output"].(string); ok && testOut != "" {
		// Extract the most useful error info from Playwright output
		errLines := extractPlaywrightError(testOut)
		if errLines != "" {
			sb.WriteString(fmt.Sprintf("  Playwright error:\n%s\n", errLines))
		}
	}

	// Console logs — extract error lines
	if logs, ok := data["console_logs"].([]interface{}); ok && len(logs) > 0 {
		for _, l := range logs {
			if entry, ok := l.(map[string]interface{}); ok {
				text, _ := entry["text"].(string)
				if strings.Contains(text, "Error") || strings.Contains(text, "failed") || strings.Contains(text, "✗") {
					sb.WriteString(fmt.Sprintf("  Console: %s\n", truncateStr(text, 200)))
				}
			}
		}
	}

	// Screenshots
	if screenshots, ok := data["screenshot_paths"].([]interface{}); ok && len(screenshots) > 0 {
		sb.WriteString(fmt.Sprintf("  Screenshots: %d captured\n", len(screenshots)))
		for _, s := range screenshots {
			if url, ok := s.(string); ok {
				sb.WriteString(fmt.Sprintf("    %s\n", url))
			}
		}
	}

	// Video
	if video, ok := data["video_path"].(string); ok && video != "" {
		sb.WriteString(fmt.Sprintf("  Video: %s\n", video))
	}

	if sb.Len() == 0 {
		return output
	}
	return sb.String()
}

func summarizeBatchExecution(output string) string {
	// Could be an array of executions or a wrapper object
	var executions []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &executions); err != nil {
		var wrapped map[string]interface{}
		if err2 := json.Unmarshal([]byte(output), &wrapped); err2 != nil {
			return output
		}
		if arr, ok := wrapped["executions"]; ok {
			data, _ := json.Marshal(arr)
			if err := json.Unmarshal(data, &executions); err != nil {
				return output
			}
		} else {
			// Single execution object
			return summarizeExecution(output)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d executions started:\n", len(executions)))
	for _, exec := range executions {
		id := exec["execution_id"]
		if id == nil {
			id = exec["id"]
		}
		status := exec["status"]
		sb.WriteString(fmt.Sprintf("  %v — %v\n", id, status))
	}
	return sb.String()
}

func summarizeCrawlStatus(output string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return output
	}

	id := data["id"]
	if id == nil {
		id = data["crawl_id"]
	}
	if id == nil {
		id = data["job_id"]
	}
	status := data["status"]
	progress := data["progress"]
	if progress == nil {
		progress = data["progress_percent"]
	}
	if progress == nil {
		progress = 0
	}

	if id != nil && status != nil {
		return fmt.Sprintf("Crawl %v: %v (%v%%)", id, status, progress)
	}
	return output
}

func summarizeCrawlResults(output string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return output
	}

	// Results are nested under "results" key
	results := data
	if r, ok := data["results"].(map[string]interface{}); ok {
		results = r
	}

	// Extract metrics (nested under results.metrics)
	pages := 0
	testCount := 0
	if metrics, ok := results["metrics"].(map[string]interface{}); ok {
		if p, ok := metrics["pages_crawled"].(float64); ok {
			pages = int(p)
		}
		if t, ok := metrics["tests_generated"].(float64); ok {
			testCount = int(t)
		}
	}

	// Fallback: count generated_tests array
	var testNames []string
	if tests, ok := results["generated_tests"].([]interface{}); ok {
		if testCount == 0 {
			testCount = len(tests)
		}
		for _, t := range tests {
			if m, ok := t.(map[string]interface{}); ok {
				if name, ok := m["name"].(string); ok {
					testNames = append(testNames, name)
				}
			}
		}
	}

	// Test case ID
	testCaseID := results["test_case_id"]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Crawl complete: %d pages, %d tests generated.", pages, testCount))
	if testCaseID != nil {
		sb.WriteString(fmt.Sprintf(" Test case ID: %v.", testCaseID))
	}
	if len(testNames) > 0 {
		sb.WriteString(" Scripts: " + strings.Join(testNames, ", "))
	}

	// Quality score
	if tests, ok := results["generated_tests"].([]interface{}); ok {
		for _, t := range tests {
			if m, ok := t.(map[string]interface{}); ok {
				if meta, ok := m["metadata"].(map[string]interface{}); ok {
					if score, ok := meta["fanatical_quality_score"].(float64); ok {
						sb.WriteString(fmt.Sprintf(" Quality: %d/100.", int(score)))
					}
				}
			}
		}
	}

	return sb.String()
}

func summarizeCrawlJobs(output string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return output
	}

	jobs, ok := data["jobs"].([]interface{})
	if !ok {
		return output
	}

	count := len(jobs)
	total := data["total_count"]

	var sb strings.Builder
	if total != nil {
		sb.WriteString(fmt.Sprintf("%d recent crawl jobs (of %v total):\n", count, total))
	} else {
		sb.WriteString(fmt.Sprintf("%d crawl jobs:\n", count))
	}

	for _, j := range jobs {
		job, ok := j.(map[string]interface{})
		if !ok {
			continue
		}
		id := job["id"]
		if id == nil {
			id = job["crawl_id"]
		}
		status := job["status"]
		url := job["url"]
		created := job["created_at"]

		// Truncate URL
		urlStr := fmt.Sprintf("%v", url)
		if len(urlStr) > 40 {
			urlStr = urlStr[:37] + "..."
		}

		sb.WriteString(fmt.Sprintf("  %v — %v — %v (%v)\n", id, status, urlStr, created))
	}

	return sb.String()
}

func summarizeStartCrawl(output string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return output
	}

	crawlID := data["crawl_id"]
	if crawlID == nil {
		crawlID = data["id"]
	}
	if crawlID == nil {
		crawlID = data["job_id"]
	}

	estTime := data["estimated_time"]
	if estTime == nil {
		estTime = data["estimated_seconds"]
	}

	if crawlID != nil {
		result := fmt.Sprintf("Crawl started: %v", crawlID)
		if estTime != nil {
			result += fmt.Sprintf(". Estimated time: %vs", estTime)
		}
		return result
	}
	return output
}

// =============================================================================
// API helpers — direct HTTP calls for operations not in the qmax CLI
// =============================================================================

// fetchScriptCode retrieves a script's full details via the QualityMax API.
func fetchScriptCode(sctx *SessionContext, ctx context.Context, scriptID string) string {
	token := sctx.QMaxCfg.Token
	apiURL := sctx.QMaxCfg.CloudURL
	if token == "" || apiURL == "" {
		return `{"error": "not authenticated — run 'qmax login' first"}`
	}

	url := fmt.Sprintf("%s/api/automation/scripts/%s", apiURL, scriptID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, redactSensitive(err.Error()))
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, redactSensitive(err.Error()))
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode != 200 {
		return fmt.Sprintf(`{"error": "HTTP %d: %s"}`, resp.StatusCode, redactSensitive(string(body)))
	}
	return string(body)
}

// updateScriptCode updates a script's code via the QualityMax API.
func updateScriptCode(sctx *SessionContext, ctx context.Context, scriptID, name, code string) string {
	token := sctx.QMaxCfg.Token
	apiURL := sctx.QMaxCfg.CloudURL
	if token == "" || apiURL == "" {
		return `{"error": "not authenticated — run 'qmax login' first"}`
	}

	url := fmt.Sprintf("%s/api/automation/scripts/%s", apiURL, scriptID)
	payload, _ := json.Marshal(map[string]string{
		"name": name,
		"code": code,
	})

	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, redactSensitive(err.Error()))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, redactSensitive(err.Error()))
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode != 200 {
		return fmt.Sprintf(`{"error": "HTTP %d: %s"}`, resp.StatusCode, redactSensitive(string(body)))
	}
	return string(body)
}

// =============================================================================
// Summarizers for test healing tools
// =============================================================================

func summarizeGetScript(output string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return output
	}

	var sb strings.Builder

	id := data["id"]
	name := data["name"]
	if name == nil {
		name = data["title"]
	}
	framework := data["framework"]
	code, _ := data["code"].(string)

	if id != nil {
		sb.WriteString(fmt.Sprintf("Script #%v", id))
	}
	if name != nil {
		sb.WriteString(fmt.Sprintf(" — %v", name))
	}
	if framework != nil {
		sb.WriteString(fmt.Sprintf(" [%v]", framework))
	}
	if code != "" {
		lines := strings.Count(code, "\n") + 1
		sb.WriteString(fmt.Sprintf(" (%d lines)", lines))
	}

	if sb.Len() == 0 {
		return output
	}

	// Include the code but cap at 4000 chars to avoid context bloat.
	// The LLM needs the code to analyze/edit it, but full API JSON is wasteful.
	if code != "" {
		if len(code) > 4000 {
			sb.WriteString("\n```javascript\n" + code[:4000] + "\n// ... truncated (" + fmt.Sprintf("%d", len(code)) + " chars total)\n```")
		} else {
			sb.WriteString("\n```javascript\n" + code + "\n```")
		}
		return sb.String()
	}

	return sb.String() + "\n" + output
}

func summarizeUpdateScript(output string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return output
	}

	id := data["id"]
	name := data["name"]
	if name == nil {
		name = data["title"]
	}
	code, _ := data["code"].(string)

	var sb strings.Builder
	if id != nil {
		sb.WriteString(fmt.Sprintf("Script #%v updated", id))
	} else {
		sb.WriteString("Script updated")
	}
	if name != nil {
		sb.WriteString(fmt.Sprintf(" — %v", name))
	}
	if code != "" {
		lines := strings.Count(code, "\n") + 1
		sb.WriteString(fmt.Sprintf(" (%d lines)", lines))
	}

	return sb.String()
}

// =============================================================================
// Script backup and rollback
// =============================================================================

// saveScriptBackup saves the current script code before healing overwrites it.
func saveScriptBackup(scriptID, content string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".qmax-code", "script-backups")
	_ = os.MkdirAll(dir, 0700)

	// Save with timestamp
	filename := fmt.Sprintf("%s_%s.json", scriptID, time.Now().Format("20060102-150405"))
	_ = os.WriteFile(filepath.Join(dir, filename), []byte(content), 0600)
}

// rollbackScript restores a script to its most recent backup.
func rollbackScript(sctx *SessionContext, ctx context.Context, scriptID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return `{"error": "cannot determine home directory"}`
	}

	dir := filepath.Join(home, ".qmax-code", "script-backups")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return `{"error": "no backups found"}`
	}

	// Find the most recent backup for this script
	var latestFile string
	var latestTime time.Time
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), scriptID+"_") {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(latestTime) {
				latestTime = info.ModTime()
				latestFile = entry.Name()
			}
		}
	}

	if latestFile == "" {
		return fmt.Sprintf(`{"error": "no backup found for script %s"}`, scriptID)
	}

	// Load backup
	data, err := os.ReadFile(filepath.Join(dir, latestFile))
	if err != nil {
		return fmt.Sprintf(`{"error": "failed to read backup: %v"}`, err)
	}

	// Parse the backup to get name and code
	var backupData map[string]interface{}
	if err := json.Unmarshal(data, &backupData); err != nil {
		return fmt.Sprintf(`{"error": "failed to parse backup: %v"}`, err)
	}

	script, ok := backupData["script"].(map[string]interface{})
	if !ok {
		// Try top-level keys (the backup may be the script object itself)
		script = backupData
	}

	name := fmt.Sprintf("%v", script["name"])
	code := fmt.Sprintf("%v", script["code"])

	result := updateScriptCode(sctx, ctx, scriptID, name, code)

	// Delete the used backup
	os.Remove(filepath.Join(dir, latestFile))

	return result
}

// =============================================================================
// Schema helpers — build Claude API tool input_schema objects concisely
// =============================================================================

type propDef struct {
	name     string
	typ      string
	desc     string
	required bool
}

func prop(name, typ, desc string, required bool) propDef {
	return propDef{name: name, typ: typ, desc: desc, required: required}
}

func props(pp ...propDef) []propDef {
	return pp
}

func obj(pp []propDef) map[string]interface{} {
	properties := map[string]interface{}{}
	var required []string
	for _, p := range pp {
		properties[p.name] = map[string]string{
			"type":        p.typ,
			"description": p.desc,
		}
		if p.required {
			required = append(required, p.name)
		}
	}
	schema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// =============================================================================
// Screenshot analysis + page elements (Vision-based tools)
// =============================================================================

// analyzeScreenshot fetches the screenshot from a test execution and asks Claude Vision
// to describe what's visible on the page.
func analyzeScreenshot(ctx context.Context, api *APIClient, executionID string, sctx *SessionContext) string {
	if executionID == "" {
		return jsonError("execution_id is required")
	}

	screenshotURL, err := getScreenshotURL(api, executionID, sctx)
	if err != "" {
		return jsonError(err)
	}

	return callVisionAnalysis(sctx, screenshotURL,
		"Analyze this web page screenshot from a Playwright test execution. Describe:\n"+
			"1. What page is this? (URL, title, app name)\n"+
			"2. What interactive elements are visible? (buttons, links, inputs, forms)\n"+
			"3. What content/text is displayed?\n"+
			"4. Any error messages, modals, or overlays?\n"+
			"5. Is there a cookie consent banner or login form?\n"+
			"Be specific about element positions and text content — this helps fix broken test selectors.")
}

// getPageElements fetches the screenshot and asks Claude Vision to extract
// interactive elements with suggested Playwright selectors.
func getPageElements(ctx context.Context, api *APIClient, executionID string, sctx *SessionContext) string {
	if executionID == "" {
		return jsonError("execution_id is required")
	}

	screenshotURL, err := getScreenshotURL(api, executionID, sctx)
	if err != "" {
		return jsonError(err)
	}

	return callVisionAnalysis(sctx, screenshotURL,
		"Extract ALL interactive elements visible on this web page screenshot. For each element, provide:\n"+
			"- Role (button, link, input, checkbox, select, etc.)\n"+
			"- Visible text or label\n"+
			"- Suggested Playwright selector (prefer getByRole, getByText, getByLabel)\n"+
			"- Position on page (header, sidebar, main content, footer, modal)\n\n"+
			"Format as a structured list. Be exhaustive — include every clickable, fillable, or assertable element.\n"+
			"This will be used to fix broken Playwright test selectors.")
}

// getScreenshotURL fetches the screenshot URL from a test execution result.
func getScreenshotURL(api *APIClient, executionID string, sctx *SessionContext) (string, string) {
	raw := api.CheckTestStatus(context.Background(), executionID)

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return "", "Failed to parse execution status"
	}

	screenshots, _ := data["screenshot_paths"].([]interface{})
	if len(screenshots) == 0 {
		return "", "No screenshots available for this execution. Run the test first."
	}

	url, _ := screenshots[len(screenshots)-1].(string) // Last screenshot = final state
	if url == "" {
		return "", "Screenshot path is empty"
	}

	// Build full URL if relative
	if !strings.HasPrefix(url, "http") {
		cloudURL := sctx.QMaxCfg.CloudURL
		if cloudURL == "" {
			cloudURL = "https://app.qualitymax.io"
		}
		url = cloudURL + "/static/test-executions/" + url
	}

	return url, ""
}

// callVisionAnalysis sends a screenshot URL to Claude Vision API for analysis.
func callVisionAnalysis(sctx *SessionContext, imageURL, prompt string) string {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		// Fall back to QualityMax backend for vision analysis
		return callBackendVisionAnalysis(sctx, imageURL, prompt)
	}

	// Direct Claude Vision call
	reqBody := map[string]interface{}{
		"model":      ModelHaiku,
		"max_tokens": 2000,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type": "image",
						"source": map[string]interface{}{
							"type": "url",
							"url":  imageURL,
						},
					},
					{
						"type": "text",
						"text": prompt,
					},
				},
			},
		},
	}

	data, _ := json.Marshal(reqBody)
	req, err := http.NewRequest("POST", AnthropicMessagesURL, bytes.NewReader(data))
	if err != nil {
		return jsonError("Failed to create vision request: " + err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", AnthropicVersion)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return jsonError("Vision API request failed: " + err.Error())
	}
	defer resp.Body.Close()

	respData, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return jsonError(fmt.Sprintf("Vision API error %d: %s", resp.StatusCode, redactSensitive(string(respData))))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respData, &result); err != nil {
		return jsonError("Failed to parse vision response")
	}

	// Extract text from response
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		return jsonError("Empty vision response")
	}
	block, _ := content[0].(map[string]interface{})
	text, _ := block["text"].(string)

	return text
}

// callBackendVisionAnalysis falls back to QualityMax backend for vision analysis
// when no local Anthropic key is available.
func callBackendVisionAnalysis(sctx *SessionContext, imageURL, prompt string) string {
	return fmt.Sprintf("Screenshot URL: %s\n\n"+
		"[Vision analysis requires ANTHROPIC_API_KEY env var. "+
		"Set it with: export ANTHROPIC_API_KEY=sk-ant-...]\n\n"+
		"You can view the screenshot at the URL above to manually inspect the page.", imageURL)
}
