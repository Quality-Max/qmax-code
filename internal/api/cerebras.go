package api

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
)
