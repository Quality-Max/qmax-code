package tui

import (
	"regexp"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
)

func TestHaiku55PickerIsExplicit(t *testing.T) {
	for name, entries := range map[string][]pickerEntry{"claude code": ccModels, "api": apiModels} {
		t.Run(name, func(t *testing.T) {
			var oldFound, newFound bool
			for _, entry := range entries {
				if entry.modelID == api.ModelHaiku {
					oldFound = true
				}
				if entry.modelID == api.ModelHaiku55 {
					newFound = true
					if entry.isFav || entry.label != "Haiku 5.5" {
						t.Fatalf("new model must be an explicit option: %+v", entry)
					}
				}
			}
			if !oldFound || !newFound {
				t.Fatal("both Haiku versions must remain available")
			}
		})
	}
}

func TestUsageDisplaysIncludeCachedPromptTokens(t *testing.T) {
	term := &Terminal{}
	usage := api.TokenUsage{InputTokens: 1, CacheReadInputTokens: 80, CacheCreationInputTokens: 20, OutputTokens: 10}
	for name, tt := range map[string]struct {
		print func()
		want  string
	}{
		"turn":   {func() { term.PrintTokenUsage(usage) }, `tokens: 101 in / 10 out \(session: 111 total`},
		"cost":   {func() { term.PrintCostSummary(usage, api.ModelHaiku55, nil) }, `Input tokens:\s+101`},
		"status": {func() { term.PrintStatusInfo(&api.SessionContext{LocalOnly: true}, usage, api.ModelHaiku55, nil) }, `Session tokens:\s+101 in / 10 out`},
	} {
		t.Run(name, func(t *testing.T) {
			if out := captureTermOutput(t, tt.print); !regexp.MustCompile(tt.want).MatchString(out) {
				t.Fatalf("cache tokens missing from %s: %s", name, out)
			}
		})
	}
}
