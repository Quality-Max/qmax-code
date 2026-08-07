package agent

import (
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/tui"
)

// ocSuccessStream is a real stream captured from
// `opencode run --format json --model opencode/deepseek-v4-flash-free`
// (opencode 1.0.105): a step-start, the text answer, then a benign trailing
// UnknownError with no statusCode that opencode emits even on a successful
// turn. The parser must return the answer, ignore the noise, and not invent
// token usage the stream never carried.
const ocSuccessStream = `{"type":"step_start","timestamp":1784907423206,"sessionID":"ses_06b3a6901ffeO62YWpcEfJj7ds","part":{"id":"prt_a","type":"step-start"}}
{"type":"text","timestamp":1784907424583,"sessionID":"ses_06b3a6901ffeO62YWpcEfJj7ds","part":{"id":"prt_b","type":"text","text":"pong"}}
{"type":"error","timestamp":1784907424833,"sessionID":"ses_06b3a6901ffeO62YWpcEfJj7ds","error":{"name":"UnknownError","data":{"message":"Invalid input"}}}`

func TestOpenCodeParseSuccessStream(t *testing.T) {
	a := &OpenCodeAgent{}
	result := a.parseStream(strings.NewReader(ocSuccessStream), &tui.Terminal{})

	if result != "pong" {
		t.Errorf("result = %q, want %q", result, "pong")
	}
	if _, hit := a.LastPlanLimit(); hit {
		t.Error("a benign UnknownError (no status code) must not be treated as a plan limit")
	}
	if _, _, ok := a.LastTurnStats(); ok {
		t.Error("stream carried no usage event; LastTurnStats ok should be false")
	}
	if a.sessionID != "ses_06b3a6901ffeO62YWpcEfJj7ds" {
		t.Errorf("sessionID = %q, want it captured from the stream", a.sessionID)
	}
}

// ocLimitStream models a coding-plan usage-limit refusal: a 429 error event
// carrying a Retry-After header. This is the case that previously produced no
// user-visible output at all.
const ocLimitStream = `{"type":"error","timestamp":1784907306260,"sessionID":"ses_limit001","error":{"name":"APIError","data":{"message":"Too Many Requests","statusCode":429,"responseHeaders":{"retry-after":"3600"}}}}`

func TestOpenCodeParseLimitStream(t *testing.T) {
	a := &OpenCodeAgent{}
	a.parseStream(strings.NewReader(ocLimitStream), &tui.Terminal{})

	reset, hit := a.LastPlanLimit()
	if !hit {
		t.Fatal("a 429 error should be detected as a plan-limit hit")
	}
	if reset.IsZero() {
		t.Error("retry-after header should have produced a reset time")
	}
}

// ocSubscriptionStream is the real 403 captured when requesting a model that
// needs a paid subscription. It is a genuine provider error (has a status
// code) but NOT a usage-limit hit, so it should surface without flagging the
// window exhausted.
const ocSubscriptionStream = `{"type":"error","timestamp":1784907306260,"sessionID":"ses_sub0001","error":{"name":"APIError","data":{"message":"Forbidden","statusCode":403,"responseBody":"this model requires a subscription, upgrade for access"}}}`

func TestOpenCodeParseSubscriptionErrorIsNotLimit(t *testing.T) {
	a := &OpenCodeAgent{}
	a.parseStream(strings.NewReader(ocSubscriptionStream), &tui.Terminal{})
	if _, hit := a.LastPlanLimit(); hit {
		t.Error("a 403 subscription error is not a usage-limit hit")
	}
}

// TestOpenCodeParseUsageTokens is forward-compatible coverage: when an opencode
// version does emit token counts on a completion event — at the top level or
// on the part, under either the input/output or prompt/completion names — the
// parser folds them into the turn stats.
func TestOpenCodeParseUsageTokens(t *testing.T) {
	cases := []struct {
		name         string
		line         string
		wantIn, want int
	}{
		{
			name:   "top-level tokens input/output",
			line:   `{"type":"step_finish","sessionID":"ses_usage01","tokens":{"input":1200,"output":345}}`,
			wantIn: 1200, want: 345,
		},
		{
			name:   "part usage prompt/completion",
			line:   `{"type":"step_finish","sessionID":"ses_usage02","part":{"type":"step-finish","usage":{"prompt":50,"completion":10}}}`,
			wantIn: 50, want: 10,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &OpenCodeAgent{}
			a.parseStream(strings.NewReader(tc.line), &tui.Terminal{})
			in, out, ok := a.LastTurnStats()
			if !ok || in != tc.wantIn || out != tc.want {
				t.Errorf("LastTurnStats = %d,%d,%v; want %d,%d,true", in, out, ok, tc.wantIn, tc.want)
			}
		})
	}
}

// ocMultiStepStream models a turn that calls a tool: opencode emits usage on
// each step-finish, so the turn's real cost is the sum across steps. Keeping
// only the last step — as the parser previously did by overwriting — reports
// 30/6 for a turn that actually cost 150/45.
const ocMultiStepStream = `{"type":"step_start","timestamp":1784907423206,"sessionID":"ses_multi","part":{"id":"prt_s1","type":"step-start"}}
{"type":"tool","timestamp":1784907423500,"sessionID":"ses_multi","part":{"id":"prt_t1","type":"tool","tool":"read"}}
{"type":"step_finish","timestamp":1784907423900,"sessionID":"ses_multi","part":{"id":"prt_s1","type":"step-finish"},"tokens":{"input":120,"output":39}}
{"type":"text","timestamp":1784907424583,"sessionID":"ses_multi","part":{"id":"prt_b","type":"text","text":"done"}}
{"type":"step_finish","timestamp":1784907424900,"sessionID":"ses_multi","part":{"id":"prt_s2","type":"step-finish"},"tokens":{"input":30,"output":6}}`

func TestOpenCodeAccumulatesUsageAcrossSteps(t *testing.T) {
	a := &OpenCodeAgent{}
	result := a.parseStream(strings.NewReader(ocMultiStepStream), &tui.Terminal{})

	if result != "done" {
		t.Errorf("result = %q, want %q", result, "done")
	}
	in, out, ok := a.LastTurnStats()
	if !ok {
		t.Fatal("a stream carrying usage must report stats")
	}
	if in != 150 || out != 45 {
		t.Fatalf("LastTurnStats = %d/%d, want 150/45 summed across both steps", in, out)
	}
}

// ocRepeatedStepStream re-emits one step-finish, which opencode does for
// snapshot-style events. The same step must not be counted twice.
const ocRepeatedStepStream = `{"type":"step_finish","timestamp":1784907423900,"sessionID":"ses_rep","part":{"id":"prt_s1","type":"step-finish"},"tokens":{"input":100,"output":20}}
{"type":"step_finish","timestamp":1784907423900,"sessionID":"ses_rep","part":{"id":"prt_s1","type":"step-finish"},"tokens":{"input":100,"output":20}}`

func TestOpenCodeDoesNotDoubleCountARepeatedStep(t *testing.T) {
	a := &OpenCodeAgent{}
	a.parseStream(strings.NewReader(ocRepeatedStepStream), &tui.Terminal{})

	in, out, ok := a.LastTurnStats()
	if !ok {
		t.Fatal("expected usage to be reported")
	}
	if in != 100 || out != 20 {
		t.Fatalf("LastTurnStats = %d/%d, want 100/20 counted once", in, out)
	}
}

// ocDualShapeStream carries the same usage in two locations, which different
// opencode/provider versions do. They are one payload, not two counts.
const ocDualShapeStream = `{"type":"step_finish","timestamp":1784907423900,"sessionID":"ses_dual","part":{"id":"prt_s1","type":"step-finish","tokens":{"input":80,"output":10}},"tokens":{"input":80,"output":10}}`

func TestOpenCodeCountsOneCanonicalPayloadPerEvent(t *testing.T) {
	a := &OpenCodeAgent{}
	a.parseStream(strings.NewReader(ocDualShapeStream), &tui.Terminal{})

	in, out, ok := a.LastTurnStats()
	if !ok {
		t.Fatal("expected usage to be reported")
	}
	if in != 80 || out != 10 {
		t.Fatalf("LastTurnStats = %d/%d, want 80/10 — the shapes are one payload", in, out)
	}
}
