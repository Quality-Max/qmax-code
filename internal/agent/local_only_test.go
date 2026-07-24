package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestStandaloneToolCatalogsContainOnlyLocalTools(t *testing.T) {
	nativeWant := map[string]bool{
		"update_plan": true,
		"read_file":   true,
		"run_command": true,
		"edit_file":   true,
		"write_file":  true,
	}
	mcpWant := map[string]bool{
		"read_file":   true,
		"run_command": true,
		"edit_file":   true,
		"write_file":  true,
	}

	assertExactToolSet(t, BuildToolDefsForMode(true), nativeWant)
	assertExactToolSet(t, BuildMCPToolDefsForMode(true), mcpWant)
}

func TestNewAgentUsesStandaloneToolCatalog(t *testing.T) {
	a := NewAgent(AgentConfig{Context: &api.SessionContext{LocalOnly: true}})
	if got, want := len(a.tools), len(localOnlyToolNames); got != want {
		t.Fatalf("standalone agent has %d tools, want %d", got, want)
	}
	for _, tool := range a.tools {
		if !localOnlyToolNames[tool.Name] {
			t.Fatalf("standalone agent exposed non-local tool %q", tool.Name)
		}
	}
}

func TestStandaloneExecutionBlocksCloudToolByName(t *testing.T) {
	out := ExecuteTool(
		"list_projects",
		map[string]interface{}{},
		&api.SessionContext{LocalOnly: true},
		context.Background(),
	)
	if !strings.Contains(out, "unavailable in standalone mode") {
		t.Fatalf("cloud tool was not blocked: %s", out)
	}
}

func TestStandaloneSystemPromptMatchesToolBoundary(t *testing.T) {
	a := NewAgent(AgentConfig{
		Context:      &api.SessionContext{LocalOnly: true},
		Professional: true,
	})
	prompt := a.buildSystemPrompt()

	for _, want := range []string{"standalone local-only mode", "read_file", "run_command", "edit_file", "write_file"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("standalone prompt missing %q", want)
		}
	}
	for _, forbidden := range []string{"list_projects", "run_test", "generate_test_code"} {
		if strings.Contains(prompt, forbidden) {
			t.Errorf("standalone prompt advertises unavailable tool %q", forbidden)
		}
	}
}

func TestStandaloneOllamaUsesLocalActionsAndExecutionGuard(t *testing.T) {
	a := NewAgent(AgentConfig{Context: &api.SessionContext{LocalOnly: true}})
	instructions := a.ollamaToolInstructions()
	for _, want := range []string{"read_file", "run_command", "edit_file", "write_file"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("standalone Ollama instructions missing %q", want)
		}
	}
	for _, forbidden := range []string{"list_projects", "run_test", "start_crawl"} {
		if strings.Contains(instructions, forbidden) {
			t.Errorf("standalone Ollama instructions advertise cloud action %q", forbidden)
		}
	}

	out := a.executeOllamaAction("list_projects", map[string]interface{}{}, context.Background())
	if !strings.Contains(out, "unavailable in standalone mode") {
		t.Fatalf("standalone Ollama bypassed execution guard: %s", out)
	}
}

func assertExactToolSet(t *testing.T, defs []api.ToolDef, want map[string]bool) {
	t.Helper()
	if len(defs) != len(want) {
		t.Fatalf("tool count = %d, want %d", len(defs), len(want))
	}
	for _, def := range defs {
		if !want[def.Name] {
			t.Errorf("unexpected tool %q", def.Name)
		}
	}
}
