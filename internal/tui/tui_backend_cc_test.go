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
