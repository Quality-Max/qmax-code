package main

import (
	"strings"
	"testing"
)

func TestToolCost_Free(t *testing.T) {
	freeTools := []string{"list_projects", "list_test_cases", "list_scripts", "read_file", "run_command", "write_file", "get_script"}
	for _, tool := range freeTools {
		if cost := ToolCost(tool); cost != "free" {
			t.Errorf("ToolCost(%q) = %q, want free", tool, cost)
		}
	}
}

func TestToolCost_Medium(t *testing.T) {
	mediumTools := []string{"run_test", "run_tests_batch", "update_script", "import_repo", "create_pr"}
	for _, tool := range mediumTools {
		if cost := ToolCost(tool); cost != "medium" {
			t.Errorf("ToolCost(%q) = %q, want medium", tool, cost)
		}
	}
}

func TestToolCost_High(t *testing.T) {
	highTools := []string{"start_crawl", "review_repo"}
	for _, tool := range highTools {
		if cost := ToolCost(tool); cost != "high" {
			t.Errorf("ToolCost(%q) = %q, want high", tool, cost)
		}
	}
}

func TestSummarizeToolResult_Error(t *testing.T) {
	result := SummarizeToolResult("list_projects", `{"error": "not authenticated"}`)
	if result != "Error: not authenticated" {
		t.Errorf("Expected error summary, got: %s", result)
	}
}

func TestSummarizeToolResult_Projects(t *testing.T) {
	result := SummarizeToolResult("list_projects", `{"projects":[{"id":1,"name":"Test","slug":"abc"}]}`)
	if !strings.Contains(result, "1 projects") || !strings.Contains(result, "Test") {
		t.Errorf("Expected project summary, got: %s", result)
	}
}

func TestSummarizeToolResult_Scripts_Framework(t *testing.T) {
	result := SummarizeToolResult("list_scripts", `{"scripts":[
        {"id":1,"name":"pw test","framework":"playwright"},
        {"id":2,"name":"py test","framework":"pytest"}
    ]}`)
	if !strings.Contains(result, "Playwright") || !strings.Contains(result, "Pytest") {
		t.Errorf("Expected framework grouping, got: %s", result)
	}
	if !strings.Contains(result, "cloud") || !strings.Contains(result, "local") {
		t.Errorf("Expected cloud/local labels, got: %s", result)
	}
}

func TestSummarizeToolResult_CrawlStatus(t *testing.T) {
	result := SummarizeToolResult("crawl_status", `{"id":"abc","status":"crawling","progress":50}`)
	if !strings.Contains(result, "crawling") || !strings.Contains(result, "50") {
		t.Errorf("Expected crawl status, got: %s", result)
	}
}
