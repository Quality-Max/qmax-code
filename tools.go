package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
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
			Description: "List automation scripts (Playwright tests) for a project.",
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
			Description: "Execute a single Playwright test script. Returns execution ID and waits for completion.",
			InputSchema: obj(props(
				prop("script_id", "integer", "Script ID to execute", true),
				prop("headless", "boolean", "Run headless (default true)", false),
				prop("browser", "string", "Browser: chromium, firefox, webkit", false),
				prop("base_url", "string", "Base URL override", false),
			)),
		},
		{
			Name:        "run_tests_batch",
			Description: "Execute multiple test scripts in batch.",
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
	}
}

// ExecuteTool executes a tool and returns the output string.
func ExecuteTool(name string, rawInput interface{}, ctx *SessionContext) string {
	// Parse input
	input := make(map[string]interface{})
	switch v := rawInput.(type) {
	case map[string]interface{}:
		input = v
	default:
		data, _ := json.Marshal(rawInput)
		json.Unmarshal(data, &input)
	}

	switch name {
	// --- qmax CLI wrappers ---
	case "list_projects":
		return runQMax("projects")

	case "list_test_cases":
		args := []string{"test", "cases", "--json", "--project-id", intArg(input, "project_id", ctx.ProjectID)}
		if v, ok := input["limit"]; ok {
			args = append(args, "--limit", fmt.Sprintf("%v", v))
		}
		if v, ok := input["search"]; ok && v != "" {
			args = append(args, "--search", fmt.Sprintf("%v", v))
		}
		return runQMax(args...)

	case "list_scripts":
		args := []string{"test", "scripts", "--json", "--project-id", intArg(input, "project_id", ctx.ProjectID)}
		if v, ok := input["limit"]; ok {
			args = append(args, "--limit", fmt.Sprintf("%v", v))
		}
		return runQMax(args...)

	case "generate_test_code":
		args := []string{"test", "generate", "--json", "--test-case-id", fmt.Sprintf("%v", input["test_case_id"])}
		if v, ok := input["force"]; ok && v == true {
			args = append(args, "--force")
		}
		return runQMax(args...)

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
		return runQMax(args...)

	case "run_tests_batch":
		args := []string{"test", "run", "--json", "--script-ids", fmt.Sprintf("%v", input["script_ids"])}
		if v, ok := input["base_url"]; ok && v != "" {
			args = append(args, "--base-url", fmt.Sprintf("%v", v))
		}
		return runQMax(args...)

	case "check_test_status":
		return runQMax("test", "status", "--json", "--execution-id", fmt.Sprintf("%v", input["execution_id"]))

	case "start_crawl":
		args := []string{"crawl", "start", "--json", "--wait",
			"--project-id", intArg(input, "project_id", ctx.ProjectID),
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
		return runQMax(args...)

	case "crawl_status":
		return runQMax("crawl", "status", "--json", "--crawl-id", fmt.Sprintf("%v", input["crawl_id"]))

	case "crawl_results":
		return runQMax("crawl", "results", "--json", "--crawl-id", fmt.Sprintf("%v", input["crawl_id"]))

	case "list_crawl_jobs":
		args := []string{"crawl", "jobs", "--json"}
		if v, ok := input["limit"]; ok {
			args = append(args, "--limit", fmt.Sprintf("%v", v))
		}
		return runQMax(args...)

	case "list_repos":
		return runQMax("repo", "list", "--json", "--project-id", intArg(input, "project_id", ctx.ProjectID))

	case "review_repo":
		return runQMax("repo", "review", "--json", "--wait", "--repo-id", fmt.Sprintf("%v", input["repo_id"]))

	case "repo_coverage":
		return runQMax("repo", "coverage", "--json", "--repo-id", fmt.Sprintf("%v", input["repo_id"]))

	case "repo_quality":
		return runQMax("repo", "quality", "--json", "--repo-id", fmt.Sprintf("%v", input["repo_id"]))

	case "import_repo":
		args := []string{"import", "repo", "--json", "--url", fmt.Sprintf("%v", input["url"])}
		pid := intArg(input, "project_id", ctx.ProjectID)
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
		return runQMax(args...)

	case "import_document":
		args := []string{"import", "doc", "--json",
			"--project-id", intArg(input, "project_id", ctx.ProjectID),
			"--text", fmt.Sprintf("%v", input["text"])}
		if v, ok := input["source_name"]; ok && v != "" {
			args = append(args, "--source", fmt.Sprintf("%v", v))
		}
		return runQMax(args...)

	case "create_pr":
		return runQMax("pr", "create", "--json",
			"--repo-id", fmt.Sprintf("%v", input["repo_id"]),
			"--project-id", intArg(input, "project_id", ctx.ProjectID))

	// --- Local operations ---
	case "read_file":
		return runShell("cat", fmt.Sprintf("%v", input["path"]))

	case "run_command":
		return runShell("sh", "-c", fmt.Sprintf("%v", input["command"]))

	default:
		return fmt.Sprintf(`{"error": "Unknown tool: %s"}`, name)
	}
}

// runQMax executes a qmax CLI command and returns stdout.
func runQMax(args ...string) string {
	binary := "qmax"
	if _, err := exec.LookPath(binary); err != nil {
		return `{"error": "qmax CLI not found on PATH. Install it first: see https://docs.qualitymax.io/cli"}`
	}
	cmd := exec.Command(binary, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
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
func runShell(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
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
