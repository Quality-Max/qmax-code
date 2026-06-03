package api

// Anthropic defaults for smart routing and direct API calls.
const (
	AnthropicMessagesURL = "https://api.anthropic.com/v1/messages"
	AnthropicVersion     = "2023-06-01"
	ModelHaiku           = "claude-haiku-4-5-20251001"
	ModelSonnet          = "claude-sonnet-4-6"
	ModelOpus            = "claude-opus-4-8" // latest Opus; the "opus" shorthand resolves here
	ModelOpus47          = "claude-opus-4-7" // prior Opus, still selectable by full ID
)
