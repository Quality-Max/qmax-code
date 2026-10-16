package api

import (
	"strings"
	"testing"
)

// Additional models.go coverage: shorthand resolution, validation of
// unknown names, and the help string shown in error messages.

func TestResolveClaudeModelShorthandsAndPassthrough(t *testing.T) {
	cases := []struct{ in, want string }{
		{"OPUS", ModelOpus55}, // case-insensitive shorthand
		{"Haiku", ModelHaiku},
		{"sonnet", ModelSonnet5},
		{"fable", ModelFable51},
		{"auto", "auto"},                       // routing sentinel is preserved
		{"claude-opus-4-6", "claude-opus-4-6"}, // full IDs pass through lowercased
		{"Claude-Opus-5-5", "claude-opus-5-5"},
	}
	for _, tc := range cases {
		if got := ResolveClaudeModel(tc.in); got != tc.want {
			t.Errorf("ResolveClaudeModel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsValidClaudeModelNameRejectsUnknown(t *testing.T) {
	for _, bad := range []string{"", "gibberish", "claude-opus-9-9", "sonnet-xl"} {
		if IsValidClaudeModelName(bad) {
			t.Errorf("IsValidClaudeModelName(%q) = true, want false", bad)
		}
	}
	for _, good := range []string{ModelOpus47, ModelOpus, ModelSonnet} {
		if !IsValidClaudeModelName(good) {
			t.Errorf("IsValidClaudeModelName(%q) = false, want true", good)
		}
	}
}

func TestValidClaudeModelsHelpListsKnownModels(t *testing.T) {
	help := ValidClaudeModelsHelp()
	for _, want := range []string{"auto", "fable", "sonnet", "opus", "haiku", ModelFable51, ModelHaiku, ModelOpus1M} {
		if !strings.Contains(help, want) {
			t.Errorf("help string missing %q: %s", want, help)
		}
	}
}
