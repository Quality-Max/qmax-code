package repl

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
)

func TestHandleSetCommandCloudSyncDocumentedKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := api.DefaultConfig()
	ag := &agent.Agent{
		AppConfig: cfg,
		Cfg:       agent.AgentConfig{Context: &api.SessionContext{}},
	}

	handleSetCommand("/set cloud_sync true", ag, &tui.Terminal{})

	if cfg.CloudSync == nil || !*cfg.CloudSync {
		t.Fatalf("CloudSync = %v, want true", cfg.CloudSync)
	}
	if loaded := api.LoadQMaxCodeConfig(); loaded.CloudSync == nil || !*loaded.CloudSync {
		t.Fatalf("persisted CloudSync = %v, want true", loaded.CloudSync)
	}
}

func TestHandleSetCommandCloudSyncLegacyAlias(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := api.DefaultConfig()
	ag := &agent.Agent{
		AppConfig: cfg,
		Cfg:       agent.AgentConfig{Context: &api.SessionContext{}},
	}

	handleSetCommand("/set cloudsync false", ag, &tui.Terminal{})

	if cfg.CloudSync == nil || *cfg.CloudSync {
		t.Fatalf("CloudSync = %v, want false", cfg.CloudSync)
	}
}

func TestHandleSetCommandCloudSyncRejectsInvalidValue(t *testing.T) {
	cfg := api.DefaultConfig()
	ag := &agent.Agent{
		AppConfig: cfg,
		Cfg:       agent.AgentConfig{Context: &api.SessionContext{}},
	}

	handleSetCommand("/set cloud_sync maybe", ag, &tui.Terminal{})

	if cfg.CloudSync != nil {
		t.Fatalf("CloudSync = %v after invalid value, want nil", cfg.CloudSync)
	}
}
