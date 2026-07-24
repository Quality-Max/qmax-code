package api

import (
	"testing"
	"time"
)

func TestPlanWindowOpensAndAccumulates(t *testing.T) {
	w := NewPlanWindow(5 * time.Hour)
	base := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)

	if w.Started() {
		t.Fatal("window should not be started before first Record")
	}

	w.Record(base, 100, 20)
	w.Record(base.Add(10*time.Minute), 50, 10)

	if w.Turns != 2 {
		t.Errorf("Turns = %d, want 2", w.Turns)
	}
	if w.InputToks != 150 || w.OutputToks != 30 {
		t.Errorf("tokens = %d/%d, want 150/30", w.InputToks, w.OutputToks)
	}
	if got := w.TotalTokens(); got != 180 {
		t.Errorf("TotalTokens = %d, want 180", got)
	}
	if !w.Active(base.Add(10 * time.Minute)) {
		t.Error("window should be active 10m in")
	}
	if got, want := w.ResetAt(), base.Add(5*time.Hour); !got.Equal(want) {
		t.Errorf("ResetAt = %v, want %v", got, want)
	}
	if got := w.Remaining(base.Add(1 * time.Hour)); got != 4*time.Hour {
		t.Errorf("Remaining after 1h = %v, want 4h", got)
	}
}

func TestPlanWindowRollsOverAfterWindowLen(t *testing.T) {
	w := NewPlanWindow(5 * time.Hour)
	base := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)

	w.Record(base, 100, 20)
	w.Record(base.Add(2*time.Hour), 100, 20) // same window
	if w.Turns != 2 {
		t.Fatalf("Turns before rollover = %d, want 2", w.Turns)
	}

	// A turn past the 5h mark opens a fresh window.
	after := base.Add(5*time.Hour + time.Minute)
	w.Record(after, 7, 3)
	if w.Turns != 1 {
		t.Errorf("Turns after rollover = %d, want 1", w.Turns)
	}
	if w.InputToks != 7 || w.OutputToks != 3 {
		t.Errorf("tokens after rollover = %d/%d, want 7/3", w.InputToks, w.OutputToks)
	}
	if got, want := w.ResetAt(), after.Add(5*time.Hour); !got.Equal(want) {
		t.Errorf("ResetAt after rollover = %v, want %v", got, want)
	}
}

func TestPlanWindowNotActiveAfterExpiry(t *testing.T) {
	w := NewPlanWindow(5 * time.Hour)
	base := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	w.Record(base, 1, 1)

	if w.Active(base.Add(5*time.Hour + time.Second)) {
		t.Error("window should be inactive past its length")
	}
	if got := w.Remaining(base.Add(6 * time.Hour)); got != 0 {
		t.Errorf("Remaining past expiry = %v, want 0", got)
	}
	if got := w.Elapsed(base.Add(6 * time.Hour)); got != 5*time.Hour {
		t.Errorf("Elapsed clamps to WindowLen, got %v", got)
	}
}

func TestPlanWindowExhaustedUsesProviderReset(t *testing.T) {
	w := NewPlanWindow(5 * time.Hour)
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	w.Record(now, 10, 5)

	providerReset := now.Add(90 * time.Minute)
	w.MarkExhausted(now, providerReset)

	if !w.Exhausted {
		t.Fatal("window should be flagged exhausted")
	}
	if got := w.Remaining(now); got != 90*time.Minute {
		t.Errorf("Remaining uses provider reset = %v, want 90m", got)
	}
	if got := w.ResetAt(); !got.Equal(providerReset) {
		t.Errorf("ResetAt = %v, want provider reset %v", got, providerReset)
	}
	if !w.Active(now.Add(time.Hour)) {
		t.Error("exhausted window with time left should stay active")
	}
}

func TestPlanWindowExhaustedOnFirstTurnWithoutReset(t *testing.T) {
	w := NewPlanWindow(5 * time.Hour)
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)

	// Limit hit before any successful turn opened a window, and no reset header.
	w.MarkExhausted(now, time.Time{})
	if !w.Started() {
		t.Fatal("MarkExhausted should open a window for display")
	}
	// Falls back to the 5h estimate.
	if got := w.Remaining(now); got != 5*time.Hour {
		t.Errorf("Remaining falls back to estimate = %v, want 5h", got)
	}
}

func TestPlanWindowFromConfigDefault(t *testing.T) {
	if got := PlanWindowFromConfig(nil).WindowLen; got != DefaultPlanWindowHours*time.Hour {
		t.Errorf("nil config window = %v, want %v", got, DefaultPlanWindowHours*time.Hour)
	}
	if got := PlanWindowFromConfig(&Config{}).WindowLen; got != DefaultPlanWindowHours*time.Hour {
		t.Errorf("zero-value config window = %v, want default", got)
	}
	if got := PlanWindowFromConfig(&Config{PlanWindowHours: 3}).WindowLen; got != 3*time.Hour {
		t.Errorf("configured window = %v, want 3h", got)
	}
}
