package tui

import (
	"strings"
	"testing"
)

func TestPickerIncludesCerebrasEntries(t *testing.T) {
	m := newModelPickerModel("", "", "high", "", "", true, true, false, false, nil)
	var got []string
	for _, e := range m.allEntries {
		if e.backend == "cerebras" {
			got = append(got, e.modelID)
		}
	}
	if len(got) != len(cerebrasModels) {
		t.Fatalf("expected %d cerebras entries, got %d (%v)", len(cerebrasModels), len(got), got)
	}
	if got[0] != "gpt-oss-120b" {
		t.Errorf("first cerebras model = %q, want gpt-oss-120b", got[0])
	}
}

func TestPickerCerebrasSectionRenders(t *testing.T) {
	// No key configured → status should advertise the inline prompt.
	m := newModelPickerModel("", "", "high", "", "", true, true, false, false, nil)
	view := m.View()
	if !strings.Contains(view, "Cerebras") {
		t.Error("picker view missing Cerebras section header")
	}
	if !strings.Contains(view, "no key") {
		t.Error("picker should show 'no key' status when CerebrasKeySet is false")
	}

	// Key configured → status should say so.
	m2 := newModelPickerModel("", "", "high", "", "", true, true, true, false, nil)
	if !strings.Contains(m2.View(), "key set") {
		t.Error("picker should show 'key set' status when CerebrasKeySet is true")
	}
}

func TestPickerCerebrasCursorOnCurrent(t *testing.T) {
	// When cerebras is the active backend, the cursor should land on the
	// matching model entry.
	m := newModelPickerModel("cerebras", "zai-glm-4.7", "high", "", "", true, true, true, false, nil)
	cur := m.allEntries[m.cursor]
	if cur.backend != "cerebras" || cur.modelID != "zai-glm-4.7" {
		t.Errorf("cursor on %s/%s, want cerebras/zai-glm-4.7", cur.backend, cur.modelID)
	}
}
