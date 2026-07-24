package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestToolCost_Free(t *testing.T) {
	freeTools := []string{"list_projects", "list_test_cases", "list_scripts", "read_file", "run_command", "edit_file", "write_file", "get_script"}
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

func TestEditFileReplacesExactBlock(t *testing.T) {
	dir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	path := filepath.Join(dir, "subject.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out := ExecuteTool("edit_file", map[string]interface{}{
		"path":     "subject.txt",
		"old_text": "beta\n",
		"new_text": "delta\n",
	}, &api.SessionContext{}, context.Background())
	if !strings.Contains(out, `"success": true`) {
		t.Fatalf("edit_file output = %s", out)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "alpha\ndelta\ngamma\n" {
		t.Fatalf("file content = %q", got)
	}
}

func TestEditFileRejectsAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	if err := os.WriteFile("subject.txt", []byte("same\nsame\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out := ExecuteTool("edit_file", map[string]interface{}{
		"path":     "subject.txt",
		"old_text": "same\n",
		"new_text": "other\n",
	}, &api.SessionContext{}, context.Background())
	if !strings.Contains(out, "matched 2 times") {
		t.Fatalf("edit_file output = %s", out)
	}
}

func TestWriteFileRejectsParentTraversal(t *testing.T) {
	dir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	out := ExecuteTool("write_file", map[string]interface{}{
		"path":    "../outside.txt",
		"content": "nope",
	}, &api.SessionContext{}, context.Background())
	if !strings.Contains(out, "restricted to the current directory") {
		t.Fatalf("write_file output = %s", out)
	}
}

// A symlink inside the workspace pointing outside it must not become an escape
// hatch for write_file: containment is checked after resolving symlinks, and
// the target file need not exist yet.
func TestWriteFileRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	out := ExecuteTool("write_file", map[string]interface{}{
		"path":    "escape/pwned.txt",
		"content": "nope",
	}, &api.SessionContext{}, context.Background())
	if !strings.Contains(out, "restricted to the current directory") {
		t.Fatalf("write_file output = %s", out)
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); err == nil {
		t.Fatal("write_file escaped the workspace through a symlink")
	}
}
