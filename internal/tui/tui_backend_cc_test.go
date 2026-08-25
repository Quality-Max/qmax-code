package tui

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestPickerIncludesClaudeCodeFableAndSonnet5(t *testing.T) {
	m := newModelPickerModel("cc", "", "high", "", "", true, true, false, false, nil)

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
	m := newModelPickerModel("cc", "", "high", "", "", true, true, false, false, nil)
	cur := m.allEntries[m.cursor]
	if cur.backend != "cc" || cur.modelID != api.ModelSonnet5 {
		t.Errorf("cursor on %s/%s, want cc/%s", cur.backend, cur.modelID, api.ModelSonnet5)
	}
}

func TestPickerUsesCodexConfigurationInsteadOfClaimingModelControl(t *testing.T) {
	// A legacy saved model must still put the cursor on the terminal-neutral
	// Codex entry; the runner no longer puts model IDs on the command line.
	m := newModelPickerModel("codex", "legacy-model", "high", "", "", true, true, false, false, nil)

	var entries []pickerEntry
	for _, e := range m.allEntries {
		if e.backend == "codex" {
			entries = append(entries, e)
		}
	}
	if len(entries) != 1 {
		t.Fatalf("Codex picker entries = %d, want 1", len(entries))
	}
	if entries[0].modelID != "" || entries[0].subLabel != "uses Codex config" || !entries[0].isFav {
		t.Fatal("Codex picker must defer model selection to Codex configuration")
	}
	if got := m.allEntries[m.cursor].backend; got != "codex" {
		t.Fatalf("cursor backend = %q, want codex", got)
	}
}
