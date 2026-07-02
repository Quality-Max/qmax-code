package api

import (
	"math"
	"testing"
	"time"
)

// TestEstimatedCost locks in the standard per-MTok rates so the /cost and
// /status displays stay accurate. Rates per
// https://platform.claude.com/docs/en/about-claude/pricing — Opus 4.6+ is
// $5/$25 (not the retired Opus 4.1 $15/$75), Haiku 4.5 is $1/$5 (not the
// retired Haiku 3 $0.25/$1.25), Fable 5 is $10/$50, Sonnet 4.6 is $3/$15,
// and Sonnet 5 is $2/$10 intro through Aug 31 2026 then $3/$15. The 1M
// context window bills at these same rates, so model substring alone
// determines the rate.
func TestEstimatedCost(t *testing.T) {
	introSonnet5 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	standardSonnet5 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		model string
		now   time.Time
		want  float64 // cost of 1M input + 1M output tokens
	}{
		{"claude-fable-5", introSonnet5, 10.0 + 50.0},
		{"claude-opus-4-8", introSonnet5, 5.0 + 25.0},
		{"claude-opus-4-8[1m]", introSonnet5, 5.0 + 25.0}, // 1M variant bills at standard rate
		{"claude-opus-4-7", introSonnet5, 5.0 + 25.0},
		{"claude-sonnet-5", introSonnet5, 2.0 + 10.0},
		{"claude-sonnet-5", standardSonnet5, 3.0 + 15.0},
		{"claude-sonnet-4-6", introSonnet5, 3.0 + 15.0},
		{"claude-haiku-4-5-20251001", introSonnet5, 1.0 + 5.0},
		{"auto", introSonnet5, 3.0 + 15.0}, // unknown → sonnet default
	}
	for _, c := range cases {
		u := TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
		if got := u.estimatedCostAt(c.model, c.now); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("estimatedCostAt(%q, %v) = %v, want %v", c.model, c.now, got, c.want)
		}
	}
}
