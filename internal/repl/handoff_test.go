package repl

import (
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/tui"
)

func TestHandoffCommandLifecycle(t *testing.T) {
	ag := &agent.Agent{}
	term := &tui.Terminal{}
	if handleHandoffCommand("/handoff", ag, term) {
		t.Fatal("listing notes changed state")
	}
	if !handleHandoffCommand("/handoff reject Increase timeout | TestReconnect still fails | see test output", ag, term) {
		t.Fatal("valid note was not recorded")
	}
	note := ag.Conversation.Handoff.RejectedApproaches[0]
	if note.Approach != "Increase timeout" || note.Evidence != "TestReconnect still fails | see test output" {
		t.Fatal("command did not preserve the approach and evidence")
	}
	if handleHandoffCommand("/handoff", ag, term) || len(ag.History) != 1 {
		t.Fatal("inspecting checkpoint modified history")
	}
	if !handleHandoffCommand("/handoff forget 1", ag, term) || !strings.Contains(ag.HandoffContext(), "No active rejected approaches") {
		t.Fatal("forget did not remove the entry")
	}
}

func TestHandoffCommandRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{
		"/handoff reject", "/handoff reject timeout", "/handoff reject | failure",
		"/handoff reject timeout |", "/handoff forget", "/handoff forget one",
		"/handoff forget 0", "/handoff forget 1", "/handoff unexpected",
	} {
		t.Run(input, func(t *testing.T) {
			ag := &agent.Agent{}
			if handleHandoffCommand(input, ag, &tui.Terminal{}) {
				t.Fatal("malformed command reported a mutation")
			}
			if ag.Conversation.Handoff != nil || len(ag.History) != 0 {
				t.Fatal("malformed command changed the session")
			}
		})
	}
}

func TestHandoffEvidenceDoesNotEnterRecallHistory(t *testing.T) {
	for _, input := range []string{
		"/handoff reject timeout | pasted diagnostic output",
		"/handoff  reject timeout | pasted diagnostic output",
		"/handoff\treject timeout | pasted diagnostic output",
	} {
		if got := redactSecretInput(input); got != "/handoff" {
			t.Fatal("handoff evidence entered recall history")
		}
	}
	if got := redactSecretInput("/handoff forget 1"); got != "/handoff forget 1" {
		t.Fatal("forget command should remain recallable")
	}
}
