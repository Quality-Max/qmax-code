package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestSummarizeToolResult(t *testing.T) {
	manyCases := make([]string, 0, 17)
	for i := 0; i < 17; i++ {
		manyCases = append(manyCases, fmt.Sprintf(`{"id":%d,"title":"case %d","category":"smoke","has_script":%v}`, i, i, i%2 == 0))
	}
	cases := []struct {
		name, tool, output string
		wants              []string
	}{
		{"error envelope", "anything", `{"error":"boom"}`, []string{"Error: boom"}},
		{"error envelope unparseable", "anything", `{"error" broken`, []string{`{"error" broken`}},
		{"detail envelope", "list_projects", `{"detail":"Not authenticated"}`, []string{"Error: Not authenticated"}},

		{"test cases array", "list_test_cases", "[" + strings.Join(manyCases, ",") + "]", []string{"17 test cases (9 automated, 8 manual)", "smoke(17)", "... and 2 more"}},
		{"test cases wrapped", "list_test_cases", `{"test_cases":[{"id":1,"title":"Login","is_automated":true}]}`, []string{"1 test cases (1 automated, 0 manual)", "#1 Login"}},
		{"test cases empty", "list_test_cases", `[]`, []string{"No test cases found."}},
		{"test cases bad wrapper", "list_test_cases", `{"items":[]}`, []string{`{"items":[]}`}},
		{"test cases bad shape", "list_test_cases", `{"test_cases":"nope"}`, []string{`{"test_cases":"nope"}`}},
		{"test cases not json", "list_test_cases", `nope`, []string{"nope"}},

		{"execution full", "run_test", `{"execution_id":"e1","status":"failed","result":"fail","duration":12,"passed":1,"failed":2,"message":"done","execution_time":3.5,"error":"assert","error_message":"timeout","test_errors":"line 4","test_output":"Error: locator.click timeout\n    at spec.ts:4","console_logs":[{"text":"Error: 500"},{"text":"ok"},"x"],"screenshot_paths":["a.png",1],"video_path":"v.webm"}`,
			[]string{"Execution: e1", "Status: failed", "Result: fail", "Duration: 12s", "Passed: 1", "Failed: 2", "Message: done", "Duration: 3.5s", "Error: assert", "Error: timeout", "Test errors: line 4", "Playwright error:", "Console: Error: 500", "Screenshots: 2 captured", "Video: v.webm"}},
		{"execution by id", "check_test_status", `{"id":"e2","duration_seconds":4}`, []string{"Execution: e2", "Duration: 4s"}},
		{"execution empty", "check_test_status", `{}`, []string{"{}"}},
		{"execution not json", "run_test", `nope`, []string{"nope"}},

		{"batch array", "run_tests_batch", `[{"execution_id":"a","status":"queued"},{"id":"b","status":"running"}]`, []string{"2 executions started:", "a — queued", "b — running"}},
		{"batch wrapped", "run_tests_batch", `{"executions":[{"execution_id":"c","status":"queued"}]}`, []string{"1 executions started:"}},
		{"batch single", "run_tests_batch", `{"execution_id":"d","status":"queued"}`, []string{"Execution: d"}},
		{"batch bad shape", "run_tests_batch", `{"executions":"x"}`, []string{`{"executions":"x"}`}},
		{"batch not json", "run_tests_batch", `nope`, []string{"nope"}},

		{"crawl status", "crawl_status", `{"crawl_id":"c1","status":"running","progress_percent":40}`, []string{"Crawl c1: running (40%)"}},
		{"crawl status job id", "crawl_status", `{"job_id":"j1","status":"queued"}`, []string{"Crawl j1: queued (0%)"}},
		{"crawl status incomplete", "crawl_status", `{"status":"queued"}`, []string{`{"status":"queued"}`}},

		{"crawl results nested", "crawl_results", `{"results":{"metrics":{"pages_crawled":5,"tests_generated":2},"test_case_id":7,"generated_tests":[{"name":"login","metadata":{"fanatical_quality_score":88}},{"name":"cart"}]}}`,
			[]string{"Crawl complete: 5 pages, 2 tests generated.", "Test case ID: 7.", "Scripts: login, cart", "Quality: 88/100."}},
		{"crawl results counted", "crawl_results", `{"generated_tests":[{"name":"a"},{"name":"b"},"x"]}`, []string{"0 pages, 3 tests generated.", "Scripts: a, b"}},
		{"crawl results not json", "crawl_results", `nope`, []string{"nope"}},

		{"crawl jobs total", "list_crawl_jobs", `{"total_count":9,"jobs":[{"id":1,"status":"done","url":"https://example.com/a/very/long/path/that/keeps/going/on","created_at":"today"},{"crawl_id":2,"status":"queued","url":"u"},"skip"]}`,
			[]string{"3 recent crawl jobs (of 9 total):", "1 — done — https://", "... (today)", "2 — queued — u"}},
		{"crawl jobs no total", "list_crawl_jobs", `{"jobs":[]}`, []string{"0 crawl jobs:"}},
		{"crawl jobs missing", "list_crawl_jobs", `{"items":[]}`, []string{`{"items":[]}`}},
		{"crawl jobs not json", "list_crawl_jobs", `nope`, []string{"nope"}},

		{"start crawl", "start_crawl", `{"crawl_id":"c9","estimated_seconds":30}`, []string{"Crawl started: c9. Estimated time: 30s"}},
		{"start crawl job", "start_crawl", `{"job_id":"j9"}`, []string{"Crawl started: j9"}},
		{"start crawl id", "start_crawl", `{"id":"i9","estimated_time":5}`, []string{"Crawl started: i9. Estimated time: 5s"}},
		{"start crawl unknown", "start_crawl", `{"queued":true}`, []string{`{"queued":true}`}},
		{"start crawl not json", "start_crawl", `nope`, []string{"nope"}},

		{"get script", "get_script", `{"id":3,"title":"Login","framework":"playwright","code":"line1\nline2"}`, []string{"Script #3 — Login [playwright] (2 lines)", "```javascript\nline1\nline2\n```"}},
		{"get script huge", "get_script", `{"id":3,"code":"` + strings.Repeat("x", 4100) + `"}`, []string{"truncated (4100 chars total)"}},
		{"get script no code", "get_script", `{"id":3,"name":"n"}`, []string{"Script #3 — n\n{"}},
		{"get script empty", "get_script", `{}`, []string{"{}"}},
		{"get script not json", "get_script", `nope`, []string{"nope"}},

		{"update script", "update_script", `{"id":3,"title":"Login","code":"a\nb\nc"}`, []string{"Script #3 updated — Login (3 lines)"}},
		{"update script bare", "update_script", `{"ok":true}`, []string{"Script updated"}},
		{"update script not json", "update_script", `nope`, []string{"nope"}},

		{"unknown pretty", "other", `{"a":` + strings.Repeat(" ", 100) + `1}`, []string{"{\n  \"a\": 1\n}"}},
		{"unknown passthrough", "other", `plain text`, []string{"plain text"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SummarizeToolResult(tc.tool, tc.output)
			for _, want := range tc.wants {
				if !strings.Contains(got, want) {
					t.Errorf("SummarizeToolResult(%q) missing %q\n%s", tc.tool, want, got)
				}
			}
		})
	}
}

func TestExtractPlaywrightError(t *testing.T) {
	if got := extractPlaywrightError("all good\n1 passed"); got != "" {
		t.Fatalf("clean output = %q", got)
	}
	var b strings.Builder
	b.WriteString("Running 1 test\n  Error: strict mode violation\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "  at frame%d (spec.ts:%d)\n", i, i)
	}
	got := extractPlaywrightError(b.String())
	if !strings.Contains(got, "strict mode violation") || strings.Count(got, "\n") > 25 {
		t.Fatalf("extract = %q", got)
	}
}

func TestToolCostAndArgHelpers(t *testing.T) {
	for tool, want := range map[string]string{
		"list_projects": "free", "generate_test_code": "low", "run_test": "medium",
		"start_crawl": "high", "create_pr": "medium", "set_review_preferences": "free", "mystery": "free",
	} {
		if got := ToolCost(tool); got != want {
			t.Errorf("ToolCost(%q) = %q, want %q", tool, got, want)
		}
	}
	in := map[string]interface{}{"n": 4}
	if intArg(in, "n", 1) != "4" || intArg(in, "x", 7) != "7" || intArg(in, "x", 0) != "0" {
		t.Error("intArg")
	}
}
