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
	// Every tool in the standalone catalog must be advertised to the Ollama
	// model; otherwise it cannot reach a tool it is allowed to call (e.g.
	// update_plan for multi-step work).
	for want := range localOnlyToolNames {
		if !strings.Contains(instructions, want) {
			t.Errorf("standalone Ollama instructions missing local tool %q", want)
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

func TestCLIQASystemPromptSwitchesOnLocalOnly(t *testing.T) {
	const connected = "CONNECTED-MARKER"
	if got := cliQASystemPrompt(&api.SessionContext{LocalOnly: true}, connected); got != localCLISystemPrompt {
		t.Errorf("local-only should return localCLISystemPrompt, got %q", got)
	}
	if strings.Contains(localCLISystemPrompt, "project ID") == false {
		// Guard the standalone contract: the local CLI prompt must forbid asking
		// for a QualityMax project, so cloud workflows can't leak into --local.
		t.Error("localCLISystemPrompt should address QualityMax project unavailability")
	}
	for _, sctx := range []*api.SessionContext{nil, {LocalOnly: false}} {
		if got := cliQASystemPrompt(sctx, connected); got != connected {
			t.Errorf("connected mode should return the passed prompt, got %q", got)
		}
	}
}

func TestOllamaToolInstructionsConnectedBranch(t *testing.T) {
	connected := NewAgent(AgentConfig{Context: &api.SessionContext{LocalOnly: false}})
	got := connected.ollamaToolInstructions()
	if got != ollamaToolPrompt {
		t.Fatalf("connected Ollama instructions should be ollamaToolPrompt")
	}
	// Connected mode must advertise cloud actions that standalone hides.
	if !strings.Contains(got, "list_projects") || !strings.Contains(got, "start_crawl") {
		t.Error("connected Ollama instructions missing cloud actions")
	}
	if strings.Contains(got, "QualityMax projects and cloud actions are unavailable") {
		t.Error("connected Ollama instructions leaked the standalone restriction notice")
	}
}

func TestBuildLocalSystemPromptBranches(t *testing.T) {
	// Git context branch.
	withGit := NewAgent(AgentConfig{Context: &api.SessionContext{
		LocalOnly: true,
		GitInfo:   &api.GitInfo{Branch: "feature/x", RemoteURL: "git@example.com:o/r.git", ChangedFiles: []string{"a.go", "b.go"}},
	}})
	gitPrompt := withGit.buildLocalSystemPrompt()
	for _, want := range []string{"Git context:", "feature/x", "git@example.com:o/r.git", "Changed files: 2"} {
		if !strings.Contains(gitPrompt, want) {
			t.Errorf("git-context prompt missing %q", want)
		}
	}

	// No-git prompt omits the git section.
	plain := NewAgent(AgentConfig{Context: &api.SessionContext{LocalOnly: true}}).buildLocalSystemPrompt()
	if strings.Contains(plain, "Git context:") {
		t.Error("prompt without GitInfo should not emit a Git context section")
	}

	// Cerebras Gemma 4 image-support branch.
	gemma := NewAgent(AgentConfig{Context: &api.SessionContext{LocalOnly: true}})
	gemma.Cerebras = &CerebrasClient{Model: api.CerebrasGemma4Model}
	if !strings.Contains(gemma.buildLocalSystemPrompt(), "inspect attached images") {
		t.Error("Gemma 4 standalone prompt should include image-inspection guidance")
	}
	if strings.Contains(plain, "inspect attached images") {
		t.Error("non-Cerebras standalone prompt should not mention image inspection")
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
