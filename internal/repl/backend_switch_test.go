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
