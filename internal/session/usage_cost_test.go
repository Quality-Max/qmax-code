package session

import (
	"math"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
)

func TestSessionPreservesRequestCostsOnResume(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var usage api.TokenUsage
	usage.RecordRequest(api.ModelHaiku55, api.APIUsage{InputTokens: 1, CacheReadInputTokens: 60_000, OutputTokens: 10})
	history := []api.Message{{Role: "user", Content: "hello"}}
	if err := SaveSession("cached", history, 0, usage, api.ModelHaiku55); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadSession("cached")
	if err != nil {
		t.Fatal(err)
	}
	saved.Usage.RecordRequest(api.ModelHaiku55, api.APIUsage{InputTokens: 1, CacheReadInputTokens: 60_000, OutputTokens: 10})
	if saved.Usage.TotalInputTokens() != 120_002 || saved.Usage.TotalTokens() != 120_022 || saved.Usage.Requests != 2 {
		t.Fatalf("resumed usage=%+v", saved.Usage)
	}
	if got := saved.Usage.EstimatedCost(api.ModelSonnet); math.Abs(got-0.0012102) > 1e-12 {
		t.Fatalf("resume changed request pricing: %v", got)
	}
}
