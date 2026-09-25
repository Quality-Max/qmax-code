package api

// Anthropic defaults for smart routing and direct API calls.
const (
	AnthropicMessagesURL = "https://api.anthropic.com/v1/messages"
	AnthropicVersion     = "2023-06-01"
	ModelFable51         = "claude-fable-5-1"
	ModelFable           = "claude-fable-5"
	ModelHaiku           = "claude-haiku-4-5-20251001"
	ModelSonnet5         = "claude-sonnet-5"
	ModelSonnet          = "claude-sonnet-4-6"
	ModelOpus55          = "claude-opus-5-5"    // latest Opus; the "opus" shorthand resolves here
	ModelOpus            = "claude-opus-4-8"    // prior Opus, still selectable by full ID
	ModelOpus1M          = ModelOpus + "[1m]"   // Claude Code 1M-context selector (Opus 4.8)
	ModelOpus47          = "claude-opus-4-7"    // older Opus, still selectable by full ID
)
