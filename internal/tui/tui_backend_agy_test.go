package tui

import "testing"

func TestPickerIncludesAntigravityModels(t *testing.T) {
	m := newModelPickerModel("agy", "", "high", "", "", true, true, false, false, true, nil)
	found := map[string]bool{}
	for _, e := range m.allEntries {
		if e.backend == "agy" {
			found[e.modelID] = true
		}
	}
	if !found[""] {
		t.Fatal("picker missing Antigravity default row")
	}
	if !found["gemini-3.7-flash-high"] {
		t.Fatal("picker missing Gemini 3.7 Flash high")
	}
	if !found["gemini-3.1-pro-high"] {
		t.Fatal("picker missing Gemini 3.1 Pro high")
	}
}

func TestPickerAntigravityDefaultCursor(t *testing.T) {
	m := newModelPickerModel("agy", "", "high", "", "", true, true, false, false, true, nil)
	cur := m.allEntries[m.cursor]
	if cur.backend != "agy" || cur.modelID != "" {
		t.Errorf("cursor on %s/%s, want agy default", cur.backend, cur.modelID)
	}
}
