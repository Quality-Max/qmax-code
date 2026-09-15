package repl

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/agent"
)

func TestDeactivateEmbeddedBackendsClearsCerebrasAndOllamaMode(t *testing.T) {
	ag := &agent.Agent{
		Mode:     agent.OllamaModeFull,
		Cerebras: &agent.CerebrasClient{Model: "gemma-4-31b"},
	}

	deactivateEmbeddedBackends(ag)

	if ag.Mode != agent.OllamaModeOff {
		t.Fatalf("Mode = %v, want off", ag.Mode)
	}
	if ag.Cerebras != nil {
		t.Fatalf("Cerebras client was not cleared")
	}
}

// TestEmbeddedInferenceAvailable guards the REPL turn pre-check: a skipped
// Anthropic key must be a supported state (turn refused with guidance, not a
// raw 401), while Ollama/Cerebras/key configurations must keep turns running.
func TestEmbeddedInferenceAvailable(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	tests := []struct {
		name string
		ag   *agent.Agent
		want bool
	}{
		{name: "nil agent", want: false},
		{name: "no backend, no key — skip state", ag: &agent.Agent{}, want: false},
		{name: "anthropic key in agent config", ag: &agent.Agent{Cfg: agent.AgentConfig{AnthropicKey: "sk-ant-x"}}, want: true},
		{name: "cerebras", ag: &agent.Agent{Cerebras: &agent.CerebrasClient{}}, want: true},
		{name: "ollama full mode", ag: &agent.Agent{Ollama: &agent.OllamaClient{}, Mode: agent.OllamaModeFull}, want: true},
		{name: "ollama chat mode", ag: &agent.Agent{Ollama: &agent.OllamaClient{}, Mode: agent.OllamaModeChat}, want: true},
		{name: "ollama configured but off", ag: &agent.Agent{Ollama: &agent.OllamaClient{}, Mode: agent.OllamaModeOff}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := embeddedInferenceAvailable(tt.ag); got != tt.want {
				t.Fatalf("embeddedInferenceAvailable() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("env key counts as configured", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-from-env")
		if !embeddedInferenceAvailable(&agent.Agent{}) {
			t.Fatal("ANTHROPIC_API_KEY env var must count as configured inference")
		}
	})
}
