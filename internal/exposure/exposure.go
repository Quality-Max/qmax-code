// Package exposure holds qmax-code's traffic taxonomy — the category constants
// and the Classify function that maps a (host, path) pair to an exposure
// category for the Exposure Receipt.
//
// It is deliberately agent-specific. The shared receipt schema
// (github.com/Quality-Max/qmax-receipt) treats Entry.Category as a free-form
// string and never enumerates categories; each agent supplies its own. The
// qmax CLI has its own taxonomy (test-plan / behavioral-snapshot / …); this is
// qmax-code's, which is dominated by LLM inference and QualityMax cloud API
// traffic rather than crawl/test artifacts.
//
// Path templatization is shared, so Classify delegates to receipt.Templatize.
package exposure

import (
	"strings"

	receipt "github.com/Quality-Max/qmax-receipt"
)

// Category constants — qmax-code's taxonomy (QUA-1316).
//
// The receipt records one Entry per *outbound* request, so LLM traffic is
// classified from the prompt side: a request to an inference endpoint carries
// the prompt (CatLLMPrompt), and the model's answer comes back as that entry's
// response bytes. CatLLMCompletion is reserved for any future response-side
// accounting (and keeps the taxonomy symmetric with how humans describe LLM
// traffic); Classify does not emit it today. CatMCPTraffic is reserved for
// outbound MCP-over-HTTP egress — qmax-code's MCP server is stdio-only today,
// so no request is classified as MCP yet, but the constant documents the slot.
const (
	CatLLMPrompt     = "llm-prompt"     // outbound inference request carrying a prompt
	CatLLMCompletion = "llm-completion" // reserved: response-side LLM accounting
	CatCloudAPI      = "cloud-api"      // QualityMax cloud REST API (projects, scripts, integrations)
	CatMCPTraffic    = "mcp-traffic"    // reserved: outbound MCP-over-HTTP egress
	CatTelemetry     = "telemetry"      // opt-in error-reporting envelope
	CatVNCControl    = "vnc-control"    // noVNC WebSocket handshake/control channel
	CatControl       = "control"        // metadata only: auth check, login poll, health/reachability probes
	CatUncategorized = "uncategorized"  // unknown host/path — flagged so it can't hide
)

// Classify maps a (host, path) pair to an exposure category. It takes plain
// strings so this package never imports net/http. path should be the raw URL
// path; classification is done against the templatized form.
//
// LLM inference endpoints are matched by path shape (not host) so both the
// hosted providers (Anthropic /v1/messages, Cerebras /chat/completions) and a
// local or remote Ollama endpoint (/v1/chat/completions) land in CatLLMPrompt.
func Classify(host, path string) string {
	p := receipt.Templatize(path)
	switch {
	// LLM inference — the request carries the prompt.
	case strings.HasSuffix(p, "/v1/messages"),
		strings.HasSuffix(p, "/chat/completions"),
		strings.HasSuffix(p, "/api/chat"),
		strings.HasSuffix(p, "/api/generate"):
		return CatLLMPrompt

	// Control plane — metadata-only calls that carry no source/prompt content.
	case strings.HasSuffix(p, "/api/me"), // auth/identity check
		strings.Contains(p, "/api/auth/cli-login"),
		strings.Contains(p, "/api/auth/cli-poll"),
		strings.HasSuffix(p, "/api/tags"), // Ollama reachability probe
		strings.Contains(p, "/api/job-health/"):
		return CatControl

	// Sentry-compatible telemetry is opt-in. The request body is still hashed
	// by httpx, but it is categorized separately from QualityMax cloud REST.
	case strings.Contains(p, "/envelope"):
		return CatTelemetry

	// QualityMax cloud REST API — projects, scripts, crawl, integrations, etc.
	case strings.Contains(p, "/api/"):
		return CatCloudAPI

	default:
		return CatUncategorized
	}
}
