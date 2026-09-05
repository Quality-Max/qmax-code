package agent

import (
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestCodexAgentUsesNativeContinuityAndClearStartsFresh(t *testing.T) {
	_ = withTempHome(t)
	codexBin := writeFakeCLI(t, "codex-continuity", `#!/bin/sh
thread_id='abcdef12-3456-4abc-8def-1234567890ab'
if [ "$#" -eq 3 ] && [ "$1" = 'exec' ] && [ "$2" = '--json' ] && [ "$3" = '-' ]; then
  :
elif [ "$#" -eq 5 ] && [ "$1" = 'exec' ] && [ "$2" = 'resume' ] && [ "$3" = '--json' ] && [ "$4" = "$thread_id" ] && [ "$5" = '-' ]; then
  :
else
  exit 64
fi
printf '%s\n' "{\"type\":\"thread.started\",\"thread_id\":\"$thread_id\"}"
`)
	a := NewCodexAgent(codexBin, "", "high", false, &api.SessionContext{})

	if _, err := a.Run(strings.Repeat(t.Name(), 1), nil); err != nil {
		t.Fatal("initial adapter turn failed")
	}
	if a.continuity.Checkpoint().ThreadID != firstAdapterThreadID {
		t.Fatal("adapter did not retain the exact thread ID")
	}
	if _, err := a.Run(strings.Repeat(t.Name(), 2), nil); err != nil {
		t.Fatal("adapter resume turn failed")
	}

	a.ClearHistory()
	if _, err := a.Run(strings.Repeat(t.Name(), 3), nil); err != nil {
		t.Fatal("adapter did not start fresh after clear")
	}
}

func TestCodexAgentAddsQAScaffoldOnlyToInitialTurn(t *testing.T) {
	a := NewCodexAgent("codex", "", "high", false, &api.SessionContext{})

	initial := a.buildPrompt(strings.Repeat(t.Name(), 1), true)
	if !strings.Contains(initial, codexQASystemPrompt) {
		t.Fatal("initial turn is missing the QA scaffold")
	}

	followUp := a.buildPrompt(strings.Repeat(t.Name(), 2), false)
	if strings.Contains(followUp, codexQASystemPrompt) {
		t.Fatal("follow-up turn repeated the QA scaffold")
	}
}

const firstAdapterThreadID = "abcdef12-3456-4abc-8def-1234567890ab"

func TestCodexAgentAstraSurvivesResumeAndClear(t *testing.T) {
	_ = withTempHome(t)
	codexBin := writeFakeCLI(t, "codex-astra", `#!/bin/sh
thread_id='abcdef12-3456-4abc-8def-1234567890ab'
if [ "$#" -eq 5 ] && [ "$1" = 'exec' ] && [ "$2" = '--model' ] && [ "$3" = 'gpt-6-astra' ] && [ "$4" = '--json' ] && [ "$5" = '-' ]; then
  :
elif [ "$#" -eq 7 ] && [ "$1" = 'exec' ] && [ "$2" = 'resume' ] && [ "$3" = '--model' ] && [ "$4" = 'gpt-6-astra' ] && [ "$5" = '--json' ] && [ "$6" = "$thread_id" ] && [ "$7" = '-' ]; then
  :
else
  exit 64
fi
printf '%s\n' "{\"type\":\"thread.started\",\"thread_id\":\"$thread_id\"}"
`)
	a := NewCodexAgent(codexBin, "gpt-6-astra", "high", false, &api.SessionContext{})
	for i := 0; i < 3; i++ {
		if i == 2 {
			a.ClearHistory()
			if a.continuity.Checkpoint().ThreadID != "" {
				t.Fatal("clear retained the previous thread")
			}
		}
		if _, err := a.Run("Check the repository", nil); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
		if got := a.continuity.Checkpoint(); got.Model != "gpt-6-astra" || got.ThreadID != firstAdapterThreadID {
			t.Fatalf("turn %d did not preserve the exact model and thread", i)
		}
	}
}
