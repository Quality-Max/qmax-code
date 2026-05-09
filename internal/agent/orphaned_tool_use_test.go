package agent

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

// Regression for the "messages.13.content: Input should be a valid list"
// crash that hit the session after a `run_native_test` error — when the
// user typed a new prompt mid-tool-loop, the dangling tool_use broke
// Anthropic's invariant that every tool_use must be followed by a
// matching tool_result list.

func TestStripOrphanedToolUse_NoOp_WhenPaired(t *testing.T) {
	msgs := []api.Message{
		{Role: "user", Content: "run foo"},
		{Role: "assistant", Content: []api.ContentBlock{
			{Type: "text", Text: "calling foo"},
			{Type: "tool_use", ID: "t1", Name: "foo", Input: map[string]interface{}{}},
		}},
		{Role: "user", Content: []api.ContentBlock{
			{Type: "tool_result", ToolUseID: "t1", Content: "ok"},
		}},
	}
	out := stripOrphanedToolUse(msgs)
	// The assistant message must still have its tool_use intact.
	blocks, ok := out[1].Content.([]api.ContentBlock)
	if !ok {
		t.Fatalf("expected []api.ContentBlock, got %T", out[1].Content)
	}
	if len(blocks) != 2 || blocks[1].Type != "tool_use" {
		t.Errorf("expected tool_use preserved when paired; got %+v", blocks)
	}
}

func TestStripOrphanedToolUse_Drops_WhenFollowedByFreshUserPrompt(t *testing.T) {
	// User interrupted mid-tool-loop with a new prompt — tool_use has no
	// matching tool_result. Must be stripped or Anthropic rejects.
	msgs := []api.Message{
		{Role: "user", Content: "run foo"},
		{Role: "assistant", Content: []api.ContentBlock{
			{Type: "text", Text: "calling foo"},
			{Type: "tool_use", ID: "t1", Name: "foo"},
		}},
		{Role: "user", Content: "forget that, do something else"},
	}
	out := stripOrphanedToolUse(msgs)
	blocks, ok := out[1].Content.([]api.ContentBlock)
	if !ok {
		t.Fatalf("expected []api.ContentBlock, got %T", out[1].Content)
	}
	for _, b := range blocks {
		if b.Type == "tool_use" {
			t.Errorf("orphaned tool_use should have been stripped, got %+v", blocks)
		}
	}
	// Original text block should remain.
	if len(blocks) < 1 || blocks[0].Type != "text" {
		t.Errorf("expected text block to remain, got %+v", blocks)
	}
}

func TestStripOrphanedToolUse_InsertsPlaceholder_WhenMessageWouldBeEmpty(t *testing.T) {
	// Assistant emitted only tool_use blocks (no text). After stripping
	// them the message would be empty, which Anthropic also rejects.
	// Ensure a placeholder text block is inserted.
	msgs := []api.Message{
		{Role: "user", Content: "run foo"},
		{Role: "assistant", Content: []api.ContentBlock{
			{Type: "tool_use", ID: "t1", Name: "foo"},
		}},
		{Role: "user", Content: "do something else"},
	}
	out := stripOrphanedToolUse(msgs)
	blocks := out[1].Content.([]api.ContentBlock)
	if len(blocks) == 0 {
		t.Fatal("expected placeholder block, got empty content")
	}
	if blocks[0].Type != "text" {
		t.Errorf("expected text placeholder, got %q", blocks[0].Type)
	}
}

func TestStripOrphanedToolUse_HandlesInterfaceContent(t *testing.T) {
	// After JSON deserialization (session load), typed []api.ContentBlock
	// becomes []interface{} with map[string]interface{} items. The
	// pruner must handle that shape too.
	msgs := []api.Message{
		{Role: "user", Content: "x"},
		{Role: "assistant", Content: []interface{}{
			map[string]interface{}{"type": "text", "text": "calling"},
			map[string]interface{}{"type": "tool_use", "id": "t1", "name": "foo"},
		}},
		{Role: "user", Content: "interrupt"},
	}
	out := stripOrphanedToolUse(msgs)
	blocks, ok := out[1].Content.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{} (preserved), got %T", out[1].Content)
	}
	for _, raw := range blocks {
		block := raw.(map[string]interface{})
		if block["type"] == "tool_use" {
			t.Errorf("orphaned tool_use should have been stripped from []interface{}: %+v", blocks)
		}
	}
}

func TestStripOrphanedToolUse_PartialMatch_TreatedAsOrphaned(t *testing.T) {
	// Assistant called two tools but tool_result only covers one. Anthropic
	// requires ALL tool_uses to be answered, so partial match = strip.
	msgs := []api.Message{
		{Role: "user", Content: "multi"},
		{Role: "assistant", Content: []api.ContentBlock{
			{Type: "tool_use", ID: "t1", Name: "foo"},
			{Type: "tool_use", ID: "t2", Name: "bar"},
		}},
		{Role: "user", Content: []api.ContentBlock{
			{Type: "tool_result", ToolUseID: "t1", Content: "ok"},
			// t2 missing!
		}},
	}
	out := stripOrphanedToolUse(msgs)
	blocks := out[1].Content.([]api.ContentBlock)
	for _, b := range blocks {
		if b.Type == "tool_use" {
			t.Errorf("partially-answered tool_use set should be stripped; got %+v", blocks)
		}
	}
}

func TestStripOrphanedToolUse_LastMessageIsAssistantToolUse(t *testing.T) {
	// Edge: assistant ended on tool_use but no user message follows yet
	// (in-flight call). Strip — the API won't accept it in this shape.
	msgs := []api.Message{
		{Role: "user", Content: "x"},
		{Role: "assistant", Content: []api.ContentBlock{
			{Type: "tool_use", ID: "t1", Name: "foo"},
		}},
	}
	out := stripOrphanedToolUse(msgs)
	blocks := out[1].Content.([]api.ContentBlock)
	for _, b := range blocks {
		if b.Type == "tool_use" {
			t.Errorf("trailing unanswered tool_use should be stripped; got %+v", blocks)
		}
	}
}

func TestStripOrphanedToolUse_EmptyHistory(t *testing.T) {
	// Defensive: empty history is a valid input (fresh session, first turn).
	// Must not panic, must return empty slice.
	out := stripOrphanedToolUse([]api.Message{})
	if len(out) != 0 {
		t.Errorf("empty input should return empty output, got %v", out)
	}
	// Also nil.
	out2 := stripOrphanedToolUse(nil)
	if len(out2) != 0 {
		t.Errorf("nil input should return empty output, got %v", out2)
	}
}

func TestStripOrphanedToolUse_MultipleSeparateToolLoops(t *testing.T) {
	// Two independent tool loops in history. Each is paired internally.
	// The function must not conflate tool_result from loop A with tool_use
	// from loop B — even though matching is ID-exact, this guards against
	// future regressions in the collector/matcher logic.
	msgs := []api.Message{
		{Role: "user", Content: "run A"},
		{Role: "assistant", Content: []api.ContentBlock{{Type: "tool_use", ID: "A1", Name: "a"}}},
		{Role: "user", Content: []api.ContentBlock{{Type: "tool_result", ToolUseID: "A1", Content: "ok"}}},
		{Role: "assistant", Content: []api.ContentBlock{{Type: "text", Text: "done"}}},
		{Role: "user", Content: "run B"},
		{Role: "assistant", Content: []api.ContentBlock{{Type: "tool_use", ID: "B1", Name: "b"}}},
		{Role: "user", Content: []api.ContentBlock{{Type: "tool_result", ToolUseID: "B1", Content: "ok"}}},
	}
	out := stripOrphanedToolUse(msgs)
	// Both assistant messages with tool_use should keep them intact.
	for _, i := range []int{1, 5} {
		blocks := out[i].Content.([]api.ContentBlock)
		hasToolUse := false
		for _, b := range blocks {
			if b.Type == "tool_use" {
				hasToolUse = true
			}
		}
		if !hasToolUse {
			t.Errorf("paired tool_use at msg %d was wrongly stripped: %+v", i, blocks)
		}
	}
}

func TestStripOrphanedToolUse_OrphanedToolResultLeftAlone(t *testing.T) {
	// Reverse orphan: a user message with tool_result blocks but NO
	// preceding assistant tool_use. This shouldn't happen naturally but
	// can appear after history compression drops the assistant. The
	// function's job is only to strip orphaned tool_USE — orphaned
	// tool_RESULT is the session sanitizer's job (session.go). Make
	// sure we don't touch those.
	msgs := []api.Message{
		{Role: "user", Content: "start"},
		{Role: "assistant", Content: []api.ContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "user", Content: []api.ContentBlock{
			{Type: "tool_result", ToolUseID: "orphan-1", Content: "ghost"},
		}},
	}
	out := stripOrphanedToolUse(msgs)
	// User message at index 2 must still have its tool_result block —
	// the Anthropic server will reject it eventually, but that's not
	// this function's concern.
	blocks, ok := out[2].Content.([]api.ContentBlock)
	if !ok {
		t.Fatalf("expected []api.ContentBlock at msg 2, got %T", out[2].Content)
	}
	if len(blocks) != 1 || blocks[0].Type != "tool_result" {
		t.Errorf("orphaned tool_result was unexpectedly modified: %+v", blocks)
	}
}

func TestStripOrphanedToolUse_NilContent(t *testing.T) {
	// After session save/load there have been cases where Content ends
	// up nil (e.g. corrupted row). Must not panic.
	msgs := []api.Message{
		{Role: "user", Content: nil},
		{Role: "assistant", Content: nil},
	}
	_ = stripOrphanedToolUse(msgs) // no crash → pass
}
