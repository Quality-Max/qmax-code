package api

import "time"

// DefaultPlanWindowHours is the rolling usage-window length assumed for
// subscription coding plans (Claude Code, Codex, and OpenCode + Z.AI GLM).
// Those plans meter usage over a rolling window that opens on the first
// request and resets once the window elapses. Five hours matches the published
// behavior of all three at the time of writing; override it with the
// plan_window_hours config key for a plan whose window differs.
const DefaultPlanWindowHours = 5

// PlanWindow tracks the current rolling usage window of a subscription coding
// plan so the UI can show how much of it is spent and when it resets.
//
// It is a time-based estimate: qmax-code has no privileged access to the
// provider's real quota, so the window opens locally on the first orchestrated
// turn and rolls over once WindowLen elapses. When a provider does report an
// authoritative limit hit — e.g. a 429 carrying a reset/Retry-After header —
// MarkExhausted records it so the display reflects the real stop instead of the
// estimate.
//
// PlanWindow is not safe for concurrent use; the REPL drives it from the single
// turn loop, the same place it folds token usage into api.TokenUsage.
type PlanWindow struct {
	WindowLen  time.Duration // rolling window length (defaults to 5h)
	StartedAt  time.Time     // zero until the first turn opens a window
	Turns      int           // turns recorded in the current window
	InputToks  int           // input tokens recorded in the current window
	OutputToks int           // output tokens recorded in the current window

	// Exhausted marks that the provider refused a turn because the plan limit
	// was reached. ExhaustedReset is the provider-reported reset time when the
	// error carried one, otherwise the zero value.
	Exhausted      bool
	ExhaustedReset time.Time
}

// NewPlanWindow returns a tracker with the given window length. A non-positive
// length falls back to the 5-hour default.
func NewPlanWindow(windowLen time.Duration) *PlanWindow {
	if windowLen <= 0 {
		windowLen = DefaultPlanWindowHours * time.Hour
	}
	return &PlanWindow{WindowLen: windowLen}
}

// PlanWindowFromConfig builds a tracker honoring the plan_window_hours config
// value (0 or unset → the 5-hour default).
func PlanWindowFromConfig(cfg *Config) *PlanWindow {
	hours := 0
	if cfg != nil {
		hours = cfg.PlanWindowHours
	}
	return NewPlanWindow(time.Duration(hours) * time.Hour)
}

// Record folds one completed turn into the window. If no window is open, or the
// open one has reached its reset time, a fresh window starts at now. Token
// counts may be zero for backends that do not report usage — the time window is
// still tracked, so "when it resets" stays accurate even when "how full" is
// unknown.
//
// Rollover uses the window's effective reset time rather than WindowLen alone.
// When a provider reported an authoritative reset — a 429 carrying Retry-After,
// say — that time wins over the local estimate, so a turn the provider accepts
// after it opens a fresh window instead of landing in the exhausted one and
// leaving the UI showing a limit that no longer applies.
func (w *PlanWindow) Record(now time.Time, inputToks, outputToks int) {
	if w == nil {
		return
	}
	if w.StartedAt.IsZero() || !now.Before(w.ResetAt()) {
		w.reset(now)
	}
	w.Turns++
	w.InputToks += inputToks
	w.OutputToks += outputToks
}

// MarkExhausted records that the provider refused a turn because the plan limit
// was reached. reset is the provider-reported reset time, or the zero value if
// the error carried none (the display then falls back to the window estimate).
func (w *PlanWindow) MarkExhausted(now time.Time, reset time.Time) {
	if w == nil {
		return
	}
	// A limit can be hit on the very first turn (before Record opened a
	// window), so ensure one is considered open for the display.
	if w.StartedAt.IsZero() {
		w.StartedAt = now
	}
	w.Exhausted = true
	w.ExhaustedReset = reset
}

func (w *PlanWindow) reset(now time.Time) {
	w.StartedAt = now
	w.Turns = 0
	w.InputToks = 0
	w.OutputToks = 0
	w.Exhausted = false
	w.ExhaustedReset = time.Time{}
}

// Started reports whether any window has been opened yet.
func (w *PlanWindow) Started() bool {
	return w != nil && !w.StartedAt.IsZero()
}

// Active reports whether a window is currently open (started, and either not
// elapsed or flagged exhausted with time still on the provider's clock).
func (w *PlanWindow) Active(now time.Time) bool {
	if !w.Started() {
		return false
	}
	if w.Exhausted {
		return w.Remaining(now) > 0
	}
	return now.Sub(w.StartedAt) < w.WindowLen
}

// Elapsed is how long the current window has been open (0 if none), clamped to
// WindowLen.
func (w *PlanWindow) Elapsed(now time.Time) time.Duration {
	if !w.Started() {
		return 0
	}
	d := now.Sub(w.StartedAt)
	if d < 0 {
		return 0
	}
	if d > w.WindowLen {
		return w.WindowLen
	}
	return d
}

// Remaining is how long until the current window resets (0 if none or elapsed).
// A provider-reported reset time wins over the local estimate.
func (w *PlanWindow) Remaining(now time.Time) time.Duration {
	if !w.Started() {
		return 0
	}
	if w.Exhausted && !w.ExhaustedReset.IsZero() {
		if d := w.ExhaustedReset.Sub(now); d > 0 {
			return d
		}
		return 0
	}
	if d := w.WindowLen - now.Sub(w.StartedAt); d > 0 {
		return d
	}
	return 0
}

// ResetAt is the wall-clock time the current window resets (zero if none).
func (w *PlanWindow) ResetAt() time.Time {
	if !w.Started() {
		return time.Time{}
	}
	if w.Exhausted && !w.ExhaustedReset.IsZero() {
		return w.ExhaustedReset
	}
	return w.StartedAt.Add(w.WindowLen)
}

// TotalTokens returns the tokens recorded in the current window.
func (w *PlanWindow) TotalTokens() int {
	if w == nil {
		return 0
	}
	return w.InputToks + w.OutputToks
}
