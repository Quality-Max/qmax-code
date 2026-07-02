package api

import (
	"strings"
	"time"
)

// sonnet5StandardPricingStart is when Claude Sonnet 5 intro pricing ($2/$10
// MTok) ends and standard pricing ($3/$15) begins.
var sonnet5StandardPricingStart = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// ResolveClaudeModel expands user-facing shorthand names to concrete
// Anthropic model IDs. "auto" is preserved as qmax-code's smart-routing
// sentinel.
func ResolveClaudeModel(m string) string {
	switch strings.ToLower(m) {
	case "sonnet":
		return ModelSonnet5
	case "opus":
		return ModelOpus
	case "haiku":
		return ModelHaiku
	default:
		return strings.ToLower(m)
	}
}

// IsValidClaudeModelName reports whether m is a model name qmax-code knows
// how to route. Validation is intentionally strict so typos fail locally
// instead of being forwarded to the Anthropic API or Claude Code.
func IsValidClaudeModelName(m string) bool {
	switch ResolveClaudeModel(m) {
	case "auto", ModelFable, ModelSonnet5, ModelSonnet, ModelOpus, ModelOpus1M, ModelOpus47, ModelHaiku:
		return true
	default:
		return false
	}
}

func ValidClaudeModelsHelp() string {
	return "auto, sonnet, opus, haiku, " + ModelFable + ", " + ModelSonnet5 + ", " + ModelSonnet + ", " + ModelOpus + ", " + ModelOpus1M + ", " + ModelOpus47 + ", " + ModelHaiku
}
