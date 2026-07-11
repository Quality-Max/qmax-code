package tui

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestPickerIncludesClaudeCodeFableAndSonnet5(t *testing.T) {
	m := newModelPickerModel("cc", "", "high", "", "", true, true, false)

	seen := map[string]pickerEntry{}
	for _, e := range m.allEntries {
		if e.backend == "cc" {
			seen[e.modelID] = e
		}
	}

	fable, ok := seen[api.ModelFable]
	if !ok {
		t.Fatalf("Claude Code picker missing %s", api.ModelFable)
	}
	if fable.label != "Fable 5" {
		t.Errorf("Fable label = %q, want Fable 5", fable.label)
	}
	if fable.subLabel != "1M ctx · long agents" {
		t.Errorf("Fable subLabel = %q, want 1M ctx · long agents", fable.subLabel)
	}

	sonnet, ok := seen[api.ModelSonnet5]
	if !ok {
		t.Fatalf("Claude Code picker missing %s", api.ModelSonnet5)
	}
	if sonnet.label != "Sonnet 5" {
		t.Errorf("Sonnet label = %q, want Sonnet 5", sonnet.label)
	}
	if !sonnet.isFav {
		t.Error("Sonnet 5 should be the default Claude Code picker row")
	}
}

func TestPickerClaudeCodeDefaultCursorOnSonnet5(t *testing.T) {
	m := newModelPickerModel("cc", "", "high", "", "", true, true, false)
	cur := m.allEntries[m.cursor]
	if cur.backend != "cc" || cur.modelID != api.ModelSonnet5 {
		t.Errorf("cursor on %s/%s, want cc/%s", cur.backend, cur.modelID, api.ModelSonnet5)
	}
}

func TestPickerIncludesCodexGPT56Variants(t *testing.T) {
	m := newModelPickerModel("codex", "", "high", "", "", true, true, false)

	seen := map[string]pickerEntry{}
	for _, e := range m.allEntries {
		if e.backend == "codex" {
			seen[e.modelID] = e
		}
	}

	for id, wantLabel := range map[string]string{
		"gpt-5.6-terra": "GPT-5.6 Terra",
		"gpt-5.6-sol":   "GPT-5.6 Sol",
		"gpt-5.6-luna":  "GPT-5.6 Luna",
	} {
		e, ok := seen[id]
		if !ok {
			t.Fatalf("Codex picker missing %s", id)
		}
		if e.label != wantLabel {
			t.Errorf("%s label = %q, want %q", id, e.label, wantLabel)
		}
		if !e.isNew {
			t.Errorf("%s should be flagged isNew", id)
		}
	}

	// o4-mini remains the default Codex model — adding new rows above it
	// must not silently change which model launches when none is specified.
	if fav := seen["o4-mini"]; !fav.isFav {
		t.Error("o4-mini should remain the default Codex picker row")
	}
}
