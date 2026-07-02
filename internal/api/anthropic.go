package api

// Anthropic defaults for smart routing and direct API calls.
const (
	AnthropicMessagesURL = "https://api.anthropic.com/v1/messages"
	AnthropicVersion     = "2023-06-01"
	ModelFable           = "claude-fable-5"
	ModelHaiku           = "claude-haiku-4-5-20251001"
	ModelSonnet5         = "claude-sonnet-5"
	ModelSonnet          = "claude-sonnet-4-6"
	ModelOpus            = "claude-opus-4-8" // latest Opus; the "opus" shorthand resolves here
	ModelOpus1M          = ModelOpus + "[1m]" // Claude Code 1M-context selector
	ModelOpus47          = "claude-opus-4-7" // prior Opus, still selectable by full ID
)
