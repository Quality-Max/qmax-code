package api

import (
	"math"
	"testing"
)

// TestEstimatedCost locks in the standard per-MTok rates so the /cost and
// /status displays stay accurate. Rates per
// https://platform.claude.com/docs/en/about-claude/pricing — Opus 4.6+ is
// $5/$25 (not the retired Opus 4.1 $15/$75), Haiku 4.5 is $1/$5 (not the
// retired Haiku 3 $0.25/$1.25), Fable 5 is $10/$50, and Sonnet 4.6/5 is
// $3/$15. The 1M context window
// bills at these same rates, so model substring alone determines the rate.
func TestEstimatedCost(t *testing.T) {
	cases := []struct {
		model string
		want  float64 // cost of 1M input + 1M output tokens
	}{
		{"claude-fable-5", 10.0 + 50.0},
		{"claude-opus-4-8", 5.0 + 25.0},
		{"claude-opus-4-8[1m]", 5.0 + 25.0}, // 1M variant bills at standard rate
		{"claude-opus-4-7", 5.0 + 25.0},
		{"claude-sonnet-5", 3.0 + 15.0},
		{"claude-sonnet-4-6", 3.0 + 15.0},
		{"claude-haiku-4-5-20251001", 1.0 + 5.0},
		{"auto", 3.0 + 15.0}, // unknown → sonnet default
	}
	for _, c := range cases {
		u := TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
		if got := u.EstimatedCost(c.model); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("EstimatedCost(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}
