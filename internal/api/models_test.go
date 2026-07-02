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
