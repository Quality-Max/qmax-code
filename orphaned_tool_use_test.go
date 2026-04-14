package main

import (
	"testing"
)

// Regression for the "messages.13.content: Input should be a valid list"
// crash that hit the session after a `run_native_test` error — when the
// user typed a new prompt mid-tool-loop, the dangling tool_use broke
// Anthropic's invariant that every tool_use must be followed by a
// matching tool_result list.

func TestStripOrphanedToolUse_NoOp_WhenPaired(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "run foo"},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "text", Text: "calling foo"},
			{Type: "tool_use", ID: "t1", Name: "foo", Input: map[string]interface{}{}},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "t1", Content: "ok"},
		}},
	}
	out := stripOrphanedToolUse(msgs)
	// The assistant message must still have its tool_use intact.
	blocks, ok := out[1].Content.([]ContentBlock)
	if !ok {
		t.Fatalf("expected []ContentBlock, got %T", out[1].Content)
	}
	if len(blocks) != 2 || blocks[1].Type != "tool_use" {
		t.Errorf("expected tool_use preserved when paired; got %+v", blocks)
	}
}

func TestStripOrphanedToolUse_Drops_WhenFollowedByFreshUserPrompt(t *testing.T) {
	// User interrupted mid-tool-loop with a new prompt — tool_use has no
	// matching tool_result. Must be stripped or Anthropic rejects.
	msgs := []Message{
		{Role: "user", Content: "run foo"},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "text", Text: "calling foo"},
			{Type: "tool_use", ID: "t1", Name: "foo"},
		}},
		{Role: "user", Content: "forget that, do something else"},
	}
	out := stripOrphanedToolUse(msgs)
	blocks, ok := out[1].Content.([]ContentBlock)
	if !ok {
		t.Fatalf("expected []ContentBlock, got %T", out[1].Content)
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
	msgs := []Message{
		{Role: "user", Content: "run foo"},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ID: "t1", Name: "foo"},
		}},
		{Role: "user", Content: "do something else"},
	}
	out := stripOrphanedToolUse(msgs)
	blocks := out[1].Content.([]ContentBlock)
	if len(blocks) == 0 {
		t.Fatal("expected placeholder block, got empty content")
	}
	if blocks[0].Type != "text" {
		t.Errorf("expected text placeholder, got %q", blocks[0].Type)
	}
}

func TestStripOrphanedToolUse_HandlesInterfaceContent(t *testing.T) {
	// After JSON deserialization (session load), typed []ContentBlock
	// becomes []interface{} with map[string]interface{} items. The
	// pruner must handle that shape too.
	msgs := []Message{
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
	msgs := []Message{
		{Role: "user", Content: "multi"},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ID: "t1", Name: "foo"},
			{Type: "tool_use", ID: "t2", Name: "bar"},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "t1", Content: "ok"},
			// t2 missing!
		}},
	}
	out := stripOrphanedToolUse(msgs)
	blocks := out[1].Content.([]ContentBlock)
	for _, b := range blocks {
		if b.Type == "tool_use" {
			t.Errorf("partially-answered tool_use set should be stripped; got %+v", blocks)
		}
	}
}

func TestStripOrphanedToolUse_LastMessageIsAssistantToolUse(t *testing.T) {
	// Edge: assistant ended on tool_use but no user message follows yet
	// (in-flight call). Strip — the API won't accept it in this shape.
	msgs := []Message{
		{Role: "user", Content: "x"},
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ID: "t1", Name: "foo"},
		}},
	}
	out := stripOrphanedToolUse(msgs)
	blocks := out[1].Content.([]ContentBlock)
	for _, b := range blocks {
		if b.Type == "tool_use" {
			t.Errorf("trailing unanswered tool_use should be stripped; got %+v", blocks)
		}
	}
}
