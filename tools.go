package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ToolDef is a Claude API tool definition.
type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// BuildToolDefs returns all available tool definitions for the Claude API.
func BuildToolDefs() []ToolDef {
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
			Description: "Generate Playwright test code for a test case using AI. Returns a script ID that can be run.",
			InputSchema: obj(props(
				prop("test_case_id", "integer", "Test case ID to generate code for", true),
				prop("force", "boolean", "Regenerate even if code exists", false),
			)),
		},
		{
			Name:        "run_test",
			Description: "Execute a single Playwright test script on the cloud runner. IMPORTANT: Only works with playwright/cypress scripts. Check the framework first with list_scripts.",
			InputSchema: obj(props(
				prop("script_id", "integer", "Script ID to execute", true),
				prop("headless", "boolean", "Run headless (default true)", false),
				prop("browser", "string", "Browser: chromium, firefox, webkit", false),
				prop("base_url", "string", "Base URL override", false),
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
			Description: "Check the status of a running test execution.",
			InputSchema: obj(props(
				prop("execution_id", "string", "Execution ID to check", true),
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
			Description: "Start an AI-powered code review for a repository. Analyzes testing quality, security, and suggests improvements.",
			InputSchema: obj(props(
				prop("repo_id", "integer", "Repository ID", true),
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
	}
}

// ExecuteTool executes a tool and returns the output string.
func ExecuteTool(name string, rawInput interface{}, sctx *SessionContext, ctx context.Context) string {
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
		return runShell(ctx, "sh", "-c", fmt.Sprintf("%v", input["command"]))

	case "write_file":
		path := fmt.Sprintf("%v", input["path"])
		content := fmt.Sprintf("%v", input["content"])
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

		return updateScriptCode(sctx, ctx, scriptID, name, code)

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
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return `{"error": "cancelled"}`
		}
		if stderr.Len() > 0 {
			return fmt.Sprintf(`{"error": %q}`, stderr.String())
		}
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		output = strings.TrimSpace(stderr.String())
	}
	return output
}

// runShell executes a shell command with output limits.
func runShell(ctx context.Context, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return `{"error": "cancelled"}`
		}
		combined := stdout.String() + stderr.String()
		if combined != "" {
			return truncateOutput(combined, 8000)
		}
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}

	return truncateOutput(stdout.String(), 8000)
}

// truncateOutput limits output to maxLen characters.
func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... (truncated)"
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
	case "import_repo", "import_document", "create_pr", "update_script":
		return "medium"
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

	// Error message
	if errMsg, ok := data["error"]; ok && errMsg != nil && errMsg != "" {
		sb.WriteString(fmt.Sprintf("  Error: %v\n", errMsg))
	}
	if errMsg, ok := data["error_message"]; ok && errMsg != nil && errMsg != "" {
		sb.WriteString(fmt.Sprintf("  Error: %v\n", errMsg))
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
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode != 200 {
		return fmt.Sprintf(`{"error": "HTTP %d: %s"}`, resp.StatusCode, string(body))
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
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode != 200 {
		return fmt.Sprintf(`{"error": "HTTP %d: %s"}`, resp.StatusCode, string(body))
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

	// Include the full output so the LLM can read the code
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
