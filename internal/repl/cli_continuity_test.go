package repl

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
)

func TestAPIModelSelectionUpdatesRoutingAndStartupPreference(t *testing.T) {
	ag := &agent.Agent{AppConfig: &api.Config{}}
	applyAPIModelSelection(ag, api.ModelFable51)
	if ag.Cfg.AutoRoute || ag.Cfg.Model != api.ModelFable51 || ag.Cfg.ChatModel != api.ModelFable51 || ag.AppConfig.DefaultModel != api.ModelFable51 {
		t.Fatal("Fable picker selection did not reach both API routes and saved preference")
	}
	applyAPIModelSelection(ag, "auto")
	if !ag.Cfg.AutoRoute || ag.Cfg.Model != api.ModelSonnet || ag.Cfg.ChatModel != api.ModelHaiku || ag.AppConfig.DefaultModel != "auto" {
		t.Fatal("auto picker selection did not restore smart routing")
	}
}

func TestResetCLIConversationUsesBackendContinuityHook(t *testing.T) {
	spy := &continuityResetSpy{}
	resetCLIConversation(spy)
	if spy.resets != 1 {
		t.Fatal("clear did not reset CLI continuity")
	}
}

func TestPersistOrchModelSelectionPreservesPreferenceForCodex(t *testing.T) {
	cfg := &api.Config{ModelOverride: "saved-preference"}

	persistOrchModelSelection(cfg, "codex", "gpt-6-astra")
	if cfg.CodexModel != "gpt-6-astra" {
		t.Fatal("Codex model was not persisted")
	}
	if cfg.ModelOverride != "saved-preference" {
		t.Fatal("selecting Codex erased another backend's model preference")
	}

	persistOrchModelSelection(cfg, "cc", "replacement-preference")
	if cfg.ModelOverride != "replacement-preference" {
		t.Fatal("non-Codex model selection was not persisted")
	}
	if cfg.CodexModel != "gpt-6-astra" {
		t.Fatal("Claude selection erased Codex model")
	}
	persistOrchModelSelection(cfg, "codex", "")
	if cfg.CodexModel != "" {
		t.Fatal("Codex default did not clear explicit model")
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
