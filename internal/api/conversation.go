package api

// ConversationState retains the complete portable transcript separately from
// the model's compacted working history. Native cursors count transcript entries.
type ConversationState struct {
	Transcript []Message                     `json:"transcript"`
	Native     map[string]NativeConversation `json:"native,omitempty"`
	Handoff    *HandoffState                 `json:"handoff,omitempty"`
}

// HandoffState holds explicit user decisions independently of compacted history.
// A non-nil, empty checkpoint supersedes notes that the user has removed.
type HandoffState struct {
	RejectedApproaches []RejectedApproach `json:"rejected_approaches"`
}

// RejectedApproach records what was ruled out and the evidence supplied by the user.
type RejectedApproach struct {
	Approach string `json:"approach"`
	Evidence string `json:"evidence"`
}

// NativeConversation identifies a CLI conversation on the machine/workspace
// that created it. It contains no credentials or provider configuration.
type NativeConversation struct {
	ID          string `json:"id"`
	Model       string `json:"model,omitempty"`
	RolloutPath string `json:"rollout_path,omitempty"`
	Directory   string `json:"directory"`
	Cursor      int    `json:"cursor"`
	// Handoff is a digest of the checkpoint this native session last received,
	// so an unchanged checkpoint is not repeated into its history every turn.
	Handoff string `json:"handoff,omitempty"`
}
