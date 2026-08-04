package repl

import (
	"errors"
	"testing"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
)

// limitReporter is a CLI agent stub that reports a plan-limit hit.
type limitReporter struct {
	reset time.Time
	hit   bool
}

func (l *limitReporter) LastPlanLimit() (time.Time, bool) { return l.reset, l.hit }

// plainAgent implements no optional reporter interfaces.
type plainAgent struct{}

func TestRecordPlanTurnMarksExhaustedWhenTheRunFailed(t *testing.T) {
	// The failure this guards: a 429 that produces no assistant output makes
	// the CLI subprocess exit non-zero, so the limit arrives with runErr != nil.
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	reset := now.Add(90 * time.Minute)
	w := api.NewPlanWindow(5 * time.Hour)

	recordPlanTurn(w, now, errors.New("opencode exited with status 1"), 0, 0, &limitReporter{reset: reset, hit: true})

	if !w.Exhausted {
		t.Fatal("a limit hit reported alongside a run error must still exhaust the window")
	}
	if !w.ExhaustedReset.Equal(reset) {
		t.Fatalf("ExhaustedReset = %v, want %v", w.ExhaustedReset, reset)
	}
	if !w.Started() {
		t.Fatal("a limit on the first turn must still open a window for the display")
	}
	if w.Turns != 0 {
		t.Fatalf("a failed turn must not be counted, got Turns = %d", w.Turns)
	}
}

func TestRecordPlanTurnRecordsOnlySuccessfulTurns(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	w := api.NewPlanWindow(5 * time.Hour)

	recordPlanTurn(w, now, nil, 120, 45, plainAgent{})
	if w.Turns != 1 || w.InputToks != 120 || w.OutputToks != 45 {
		t.Fatalf("successful turn not recorded: turns=%d in=%d out=%d", w.Turns, w.InputToks, w.OutputToks)
	}
	if w.Exhausted {
		t.Fatal("an agent that reports no limit must not exhaust the window")
	}

	recordPlanTurn(w, now.Add(time.Minute), errors.New("boom"), 999, 999, plainAgent{})
	if w.Turns != 1 || w.InputToks != 120 {
		t.Fatalf("failed turn must not advance the window: turns=%d in=%d", w.Turns, w.InputToks)
	}
}

func TestRecordPlanTurnToleratesNilWindow(t *testing.T) {
	recordPlanTurn(nil, time.Now(), nil, 1, 1, &limitReporter{hit: true})
}

func TestPlanWindowKeySeparatesQuotas(t *testing.T) {
	// /cc, /codex, and /opencode draw on independent subscriptions, and
	// OpenCode's providers are metered separately from each other.
	cases := []struct {
		name    string
		backend string
		cfg     *api.Config
		want    string
	}{
		{"claude code", "cc", &api.Config{}, "cc"},
		{"codex", "codex", &api.Config{}, "codex"},
		{"opencode without a model override", "opencode", &api.Config{}, "opencode"},
		{"opencode on zai", "opencode", &api.Config{ModelOverride: "zai/glm-4.6"}, "opencode/zai"},
		{"opencode on groq", "opencode", &api.Config{ModelOverride: "groq/llama-3.3-70b"}, "opencode/groq"},
		{"opencode with a bare model name", "opencode", &api.Config{ModelOverride: "glm-4.6"}, "opencode"},
		{"nil config", "opencode", nil, "opencode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := planWindowKey(tc.backend, tc.cfg); got != tc.want {
				t.Fatalf("planWindowKey(%q) = %q, want %q", tc.backend, got, tc.want)
			}
		})
	}

	zai := planWindowKey("opencode", &api.Config{ModelOverride: "zai/glm-4.6"})
	groq := planWindowKey("opencode", &api.Config{ModelOverride: "groq/llama-3.3-70b"})
	if zai == groq {
		t.Fatal("two OpenCode providers must not share one usage window")
	}
	if planWindowKey("cc", &api.Config{}) == planWindowKey("codex", &api.Config{}) {
		t.Fatal("Claude Code and Codex must not share one usage window")
	}
}
