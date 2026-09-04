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

	fable, ok := seen[api.ModelFable51]
	if !ok {
		t.Fatalf("Claude Code picker missing %s", api.ModelFable51)
	}
	if fable.label != "Fable 5.1" {
		t.Errorf("Fable label = %q, want Fable 5.1", fable.label)
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

func TestPickerCodexDefaultAndExplicitModel(t *testing.T) {
	for _, model := range []string{"", "legacy-model", "gpt-6-astra"} {
		m := newModelPickerModel("codex", model, "high", "", "", true, true, false, false, nil)
		want := model
		if model == "legacy-model" {
			want = ""
		}
		cur := m.allEntries[m.cursor]
		if cur.backend != "codex" || cur.modelID != want {
			t.Fatalf("selection %q: cursor on %s/%s, want codex/%s", model, cur.backend, cur.modelID, want)
		}
	}
}

func TestPickerIncludesFable51ForDirectAPI(t *testing.T) {
	m := newModelPickerModel("", api.ModelFable51, "high", "", "", false, false, false, false, nil)
	cur := m.allEntries[m.cursor]
	if cur.backend != "" || cur.modelID != api.ModelFable51 {
		t.Fatal("direct API Fable 5.1 selection is missing")
	}
}
