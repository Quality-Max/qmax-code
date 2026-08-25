package repl

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
)

func TestResetCLIConversationUsesBackendContinuityHook(t *testing.T) {
	spy := &continuityResetSpy{}
	resetCLIConversation(spy)
	if spy.resets != 1 {
		t.Fatal("clear did not reset CLI continuity")
	}
}

func TestPersistOrchModelSelectionPreservesPreferenceForCodex(t *testing.T) {
	cfg := &api.Config{ModelOverride: "saved-preference"}

	persistOrchModelSelection(cfg, "codex", "")
	if cfg.ModelOverride != "saved-preference" {
		t.Fatal("selecting Codex erased another backend's model preference")
	}

	persistOrchModelSelection(cfg, "cc", "replacement-preference")
	if cfg.ModelOverride != "replacement-preference" {
		t.Fatal("non-Codex model selection was not persisted")
	}
}

type continuityResetSpy struct {
	resets int
}

func (*continuityResetSpy) Run(string, *tui.Terminal) (string, error) { return "", nil }
func (*continuityResetSpy) Cancel()                                   {}
func (*continuityResetSpy) SetOutputVerbose(bool)                     {}
func (*continuityResetSpy) Cleanup()                                  {}
func (spy *continuityResetSpy) ResetConversation()                    { spy.resets++ }
