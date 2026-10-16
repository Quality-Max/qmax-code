package api

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestHaiku55ExplicitSelectionPreservesDefaults(t *testing.T) {
	if !IsValidClaudeModelName(ModelHaiku55) || ResolveClaudeModel(ModelHaiku55) != ModelHaiku55 {
		t.Fatal("Haiku 5.5 must be selectable by its full ID")
	}
	if ResolveClaudeModel("haiku") != "claude-haiku-4-5-20251001" || ModelHaiku != "claude-haiku-4-5-20251001" {
		t.Fatal("the Haiku shorthand must stay on the verified default")
	}
	if !strings.Contains(ValidClaudeModelsHelp(), ModelHaiku55) {
		t.Fatal("model help is missing Haiku 5.5")
	}
}

func TestHaiku55RequestSettings(t *testing.T) {
	for _, model := range []string{ModelHaiku55, ModelHaiku, ModelSonnet} {
		t.Run(model, func(t *testing.T) {
			data, err := json.Marshal(APIRequest{Model: model, Thinking: DefaultThinking(model)})
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if model == ModelHaiku55 {
				if string(fields["thinking"]) != `{"type":"disabled"}` {
					t.Fatalf("thinking = %s", fields["thinking"])
				}
			} else if _, ok := fields["thinking"]; ok {
				t.Fatal("existing models must retain their request settings")
			}
			for _, key := range []string{"temperature", "top_p", "top_k"} {
				if _, ok := fields[key]; ok {
					t.Fatalf("unsupported sampling parameter %s", key)
				}
			}
		})
	}
}

func TestHaiku55CostTiersAndCache(t *testing.T) {
	for _, tt := range []struct {
		name  string
		usage TokenUsage
		want  float64
	}{
		{"at short limit", TokenUsage{InputTokens: 100_000, OutputTokens: 1_000}, 0.0105},
		{"above short limit", TokenUsage{InputTokens: 100_001, OutputTokens: 1_000}, 0.0525005},
		{"cached short prompt", TokenUsage{InputTokens: 1, CacheReadInputTokens: 99_999, OutputTokens: 10}, 0.00100509},
		{"cache read crosses limit", TokenUsage{InputTokens: 1, CacheReadInputTokens: 100_000, OutputTokens: 10}, 0.0050255},
		{"cache creation crosses limit", TokenUsage{InputTokens: 1, CacheCreationInputTokens: 100_000, OutputTokens: 10}, 0.0625255},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.usage.EstimatedCost(ModelHaiku55); math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("cost = %.12f, want %.12f", got, tt.want)
			}
		})
	}
}

func TestRecordRequestPreservesTiersModelsAndSnapshots(t *testing.T) {
	var usage TokenUsage
	usage.RecordRequest(ModelHaiku55, APIUsage{InputTokens: 60_000})
	snapshot := usage
	usage.RecordRequest(ModelHaiku55, APIUsage{InputTokens: 60_000})
	if got := usage.EstimatedCost(ModelHaiku55); math.Abs(got-0.012) > 1e-12 {
		t.Fatalf("two short requests must not become one long request: %v", got)
	}
	usage.RecordRequest(ModelSonnet, APIUsage{InputTokens: 1_000, OutputTokens: 1_000})
	if got := usage.EstimatedCost(ModelHaiku); math.Abs(got-0.030) > 1e-12 {
		t.Fatalf("switching the display model repriced earlier requests: %v", got)
	}
	if snapshot.EstimatedCost(ModelSonnet) != 0.006 || snapshot.Requests != 1 {
		t.Fatal("a later request mutated the saved usage snapshot")
	}
}

func TestTokenUsageLegacyAndRecordedJSON(t *testing.T) {
	var usage TokenUsage
	if err := json.Unmarshal([]byte(`{"InputTokens":1000,"OutputTokens":100,"Requests":1}`), &usage); err != nil {
		t.Fatal(err)
	}
	if usage.CostUSD != nil || math.Abs(usage.EstimatedCost(ModelHaiku)-0.0015) > 1e-12 {
		t.Fatal("legacy usage should retain its aggregate estimate")
	}
	usage.RecordRequest(ModelHaiku, APIUsage{InputTokens: 20, OutputTokens: 10, CacheReadInputTokens: 30, CacheCreationInputTokens: 40})
	data, err := json.Marshal(usage)
	if err != nil {
		t.Fatal(err)
	}
	var restored TokenUsage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Requests != 2 || restored.TotalTokens() != 1200 || restored.CostUSD == nil || math.Abs(restored.EstimatedCost(ModelSonnet)-0.001623) > 1e-12 {
		t.Fatalf("usage did not round-trip: %+v", restored)
	}
}
