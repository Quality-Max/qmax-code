package mcp

import (
	"reflect"
	"testing"
)

func TestCompatibilityAliasTranslations(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]interface{}
		projectID int
		cloudName string
		want      map[string]interface{}
	}{
		{
			name:      "generate_test_code",
			args:      map[string]interface{}{"test_case_id": float64(7), "framework": "go_test", "force": true},
			cloudName: "generate_code_for_test_case",
			want:      map[string]interface{}{"test_case_id": float64(7), "framework": "go"},
		},
		{
			name:      "run_test",
			args:      map[string]interface{}{"script_id": float64(41), "headless": true, "browser": "webkit"},
			cloudName: "run_tests",
			want:      map[string]interface{}{"script_ids": []string{"41"}, "headless": true},
		},
		{
			name:      "run_tests_batch",
			args:      map[string]interface{}{"script_ids": "41, 42", "base_url": "https://example.test"},
			cloudName: "run_tests",
			want:      map[string]interface{}{"script_ids": []string{"41", "42"}, "base_url": "https://example.test"},
		},
		{
			name:      "check_test_status",
			args:      map[string]interface{}{"execution_id": "execution-1"},
			cloudName: "get_execution",
			want:      map[string]interface{}{"execution_id": "execution-1"},
		},
		{
			name:      "start_crawl",
			args:      map[string]interface{}{"url": "https://example.test", "pages": float64(5), "instructions": "checkout"},
			projectID: 73,
			cloudName: "start_ai_crawl",
			want: map[string]interface{}{
				"url":                 "https://example.test",
				"pages_limit":         float64(5),
				"custom_instructions": "checkout",
				"project_id":          73,
			},
		},
		{
			name:      "list_crawl_jobs",
			args:      map[string]interface{}{"limit": float64(10)},
			projectID: 73,
			cloudName: "list_ai_crawl_jobs",
			want:      map[string]interface{}{"limit": float64(10), "project_id": 73},
		},
		{
			name:      "list_repos",
			args:      map[string]interface{}{},
			projectID: 73,
			cloudName: "list_repositories",
			want:      map[string]interface{}{"project_id": 73},
		},
		{
			name:      "review_repo",
			args:      map[string]interface{}{"repo_id": float64(9)},
			cloudName: "ai_review",
			want:      map[string]interface{}{"repo_id": float64(9)},
		},
		{
			name: "import_repo",
			args: map[string]interface{}{
				"url":              "https://github.com/example/repo",
				"project_id":       float64(73),
				"base_url":         "https://example.test",
				"training_consent": "opt_out",
			},
			cloudName: "import_repository",
			want: map[string]interface{}{
				"repo_url":         "https://github.com/example/repo",
				"project_id":       float64(73),
				"training_consent": "opt_out",
			},
		},
		{
			name:      "import_document",
			args:      map[string]interface{}{"text": "requirements", "source_name": "PRD"},
			projectID: 73,
			cloudName: "import_test_cases_from_document",
			want:      map[string]interface{}{"text_content": "requirements", "source_name": "PRD", "project_id": 73},
		},
		{
			name:      "export_qtml",
			args:      map[string]interface{}{},
			projectID: 73,
			cloudName: "export_project_as_qtml",
			want:      map[string]interface{}{"project_id": 73},
		},
		{
			name:      "import_qtml",
			args:      map[string]interface{}{"content": "project: demo"},
			projectID: 73,
			cloudName: "import_qtml",
			want:      map[string]interface{}{"source": "project: demo", "project_id": 73},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotArgs, err := translateCloudCall(tt.name, tt.args, tt.projectID, nil)
			if err != nil {
				t.Fatalf("translateCloudCall() error = %v", err)
			}
			if gotName != tt.cloudName {
				t.Fatalf("cloud name = %q, want %q", gotName, tt.cloudName)
			}
			if !reflect.DeepEqual(gotArgs, tt.want) {
				t.Fatalf("translated args = %#v, want %#v", gotArgs, tt.want)
			}
		})
	}
}

func TestCompatibilityAliasTranslationRejectsInvalidScriptIDs(t *testing.T) {
	for _, tt := range []struct {
		name string
		args map[string]interface{}
	}{
		{name: "run_test", args: map[string]interface{}{"script_id": 4.5}},
		{name: "run_tests_batch", args: map[string]interface{}{"script_ids": "41, nope"}},
		{name: "run_tests_batch", args: map[string]interface{}{"script_ids": ""}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := translateCloudCall(tt.name, tt.args, 73, nil); err == nil {
				t.Fatal("translateCloudCall() unexpectedly accepted invalid script IDs")
			}
		})
	}
}

func TestCompatibilityAliasDefersToDiscoveredCloudTool(t *testing.T) {
	args := map[string]interface{}{"native": true}
	gotName, gotArgs, err := translateCloudCall(
		"run_test",
		args,
		73,
		map[string]bool{"run_test": true, "run_tests": true},
	)
	if err != nil {
		t.Fatalf("translateCloudCall() error = %v", err)
	}
	if gotName != "run_test" || !reflect.DeepEqual(gotArgs, args) {
		t.Fatalf("discovered cloud tool was translated: name=%q args=%#v", gotName, gotArgs)
	}
}
