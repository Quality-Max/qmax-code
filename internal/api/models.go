package api

import "strings"

// ResolveClaudeModel expands user-facing shorthand names to concrete
// Anthropic model IDs. "auto" is preserved as qmax-code's smart-routing
// sentinel.
func ResolveClaudeModel(m string) string {
	switch strings.ToLower(m) {
	case "sonnet":
		return ModelSonnet
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
	case "auto", ModelSonnet, ModelOpus, ModelHaiku:
		return true
	default:
		return false
	}
}

func ValidClaudeModelsHelp() string {
	return "auto, sonnet, opus, haiku, " + ModelSonnet + ", " + ModelOpus + ", " + ModelHaiku
}
