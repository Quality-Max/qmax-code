package api

import "strings"

// Cerebras inference (OpenAI-compatible). The cerebras backend drives the
// native qmax agent loop — full tool set, native function-calling — through
// Cerebras's /v1/chat/completions endpoint, which is fast and low-cost.
//
// Auth is a Bearer token (CEREBRAS_API_KEY), not the x-api-key header the
// Anthropic Messages API uses. Base URL and model are overridable via config
// or the CEREBRAS_API_BASE / CEREBRAS_MODEL env vars.
const (
	// CerebrasAPIBase is the default OpenAI-compatible base URL.
	CerebrasAPIBase = "https://api.cerebras.ai/v1"
	// CerebrasDefaultModel is a fast, capable general/coding model on Cerebras.
	CerebrasDefaultModel = "gpt-oss-120b"
	// CerebrasGemma4Model is Google DeepMind's Gemma 4 31B as hosted on
	// Cerebras. It is multimodal (text + image input, text output) and exposes
	// reasoning via the top-level reasoning_effort parameter (off by default).
	// Model ID per the Cerebras + Gemma 4 hackathon docs.
	CerebrasGemma4Model = "gemma-4-31b"
)

// ResolveCerebrasModel expands shorthand aliases to full Cerebras model IDs.
// Unknown values pass through unchanged so users can set raw model IDs.
//
//	""            → CerebrasDefaultModel (gpt-oss-120b)
//	"gemma"       → CerebrasGemma4Model (gemma-4-31b)
//	"gemma-4-31b" → CerebrasGemma4Model
func ResolveCerebrasModel(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "default":
		return CerebrasDefaultModel
	case "gemma", "gemma4", "gemma-4", "gemma-4-31b", "gemma4-31b", "gemma-4-31":
		return CerebrasGemma4Model
	}
	return name
}

// IsCerebrasGemma4Model reports whether modelID is the Gemma 4 31B variant.
// Used to gate Gemma-specific behavior (reasoning, multimodal nudges).
func IsCerebrasGemma4Model(modelID string) bool {
	return ResolveCerebrasModel(modelID) == CerebrasGemma4Model
}

// ValidCerebrasReasoningEffort reports whether s is an accepted
// reasoning_effort value. Empty and "none" keep Gemma 4 thinking off (the
// Cerebras default); "low", "medium", and "high" turn it on.
func ValidCerebrasReasoningEffort(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none", "low", "medium", "high":
		return true
	}
	return false
}

// NormalizeCerebrasReasoningEffort canonicalizes a reasoning_effort value.
// Invalid input maps to "" (omit from the request → Cerebras default = off).
func NormalizeCerebrasReasoningEffort(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "none", "low", "medium", "high":
		return s
	}
	return ""
}
