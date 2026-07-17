package api

import "testing"

func TestResolveClaudeModelSonnetShorthandUsesSonnet5(t *testing.T) {
	if got := ResolveClaudeModel("sonnet"); got != ModelSonnet5 {
		t.Errorf("ResolveClaudeModel(sonnet) = %q, want %q", got, ModelSonnet5)
	}
}

func TestIsValidClaudeModelNameIncludesFableAndSonnet5(t *testing.T) {
	for _, model := range []string{"sonnet", ModelFable, ModelSonnet5, ModelOpus1M} {
		if !IsValidClaudeModelName(model) {
			t.Errorf("IsValidClaudeModelName(%q) = false, want true", model)
		}
	}
}

func TestContextWindow(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  int
	}{
		{ModelSonnet5, 1_000_000},
		{"claude-opus-4-6[1m]", 1_000_000},
		{"unknown-model", 200_000},
	} {
		if got := ContextWindow(tc.model); got != tc.want {
			t.Errorf("ContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}
