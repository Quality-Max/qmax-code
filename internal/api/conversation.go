package api

// ConversationState retains the complete portable transcript separately from
// the model's compacted working history. Native cursors count transcript entries.
type ConversationState struct {
	Transcript []Message                     `json:"transcript"`
	Native     map[string]NativeConversation `json:"native,omitempty"`
}

// NativeConversation identifies a CLI conversation on the machine/workspace
// that created it. It contains no credentials or provider configuration.
type NativeConversation struct {
	ID          string `json:"id"`
	Model       string `json:"model,omitempty"`
	RolloutPath string `json:"rollout_path,omitempty"`
	Directory   string `json:"directory"`
	Cursor      int    `json:"cursor"`
}
