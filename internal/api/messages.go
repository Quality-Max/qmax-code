package api

// Anthropic Messages API wire types. Kept in this package because they're
// the on-the-wire shape of /v1/messages requests and responses, and because
// session persistence + agent + cloud upload all need to round-trip them.

// Message represents a conversation message.
type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []ContentBlock
}

// ContentBlock is a typed content block in a message.
type ContentBlock struct {
	Type      string       `json:"type"`
	Text      string       `json:"text,omitempty"`
	ID        string       `json:"id,omitempty"`
	Name      string       `json:"name,omitempty"`
	Input     interface{}  `json:"input,omitempty"`
	ToolUseID string       `json:"tool_use_id,omitempty"`
	Content   string       `json:"content,omitempty"`
	Source    *ImageSource `json:"source,omitempty"` // for type="image"
}

// ImageSource is the source data for an image content block.
type ImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // "image/png", "image/jpeg", "image/gif", "image/webp"
	Data      string `json:"data"`       // base64-encoded image data
}

// APIRequest is the Claude API request body.
type APIRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system"`
	Messages  []Message `json:"messages"`
	Tools     []ToolDef `json:"tools"`
	Stream    bool      `json:"stream"`
}

// APIResponse is the Claude API response (non-streaming).
type APIResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      APIUsage       `json:"usage"`
}

// APIUsage tracks token usage on a single request/response.
type APIUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ToolDef is a Claude API tool definition.
type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}
