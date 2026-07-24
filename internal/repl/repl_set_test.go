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

func TestHandleSetCommandLocalOnlyPersistsForRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := api.DefaultConfig()
	ctx := &api.SessionContext{}
	ag := &agent.Agent{
		AppConfig: cfg,
		Cfg:       agent.AgentConfig{Context: ctx},
	}

	handleSetCommand("/set local_only true", ag, &tui.Terminal{})

	if !cfg.LocalOnly {
		t.Fatal("LocalOnly = false, want true")
	}
	if ctx.LocalOnly {
		t.Fatal("active context changed without restart")
	}
	if loaded := api.LoadQMaxCodeConfig(); !loaded.LocalOnly {
		t.Fatal("persisted LocalOnly = false, want true")
	}
}

func TestHandleSetCommandBlocksCloudSettingsInActiveStandaloneMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := api.DefaultConfig()
	ctx := &api.SessionContext{LocalOnly: true}
	ag := &agent.Agent{
		AppConfig: cfg,
		Cfg:       agent.AgentConfig{Context: ctx},
	}

	handleSetCommand("/set project 42", ag, &tui.Terminal{})
	handleSetCommand("/set cloud_sync true", ag, &tui.Terminal{})
	handleSetCommand("/set live_feed true", ag, &tui.Terminal{})

	if cfg.DefaultProject != 0 || ctx.ProjectID != 0 {
		t.Fatalf("standalone project changed: config=%d context=%d", cfg.DefaultProject, ctx.ProjectID)
	}
	if cfg.CloudSync != nil {
		t.Fatalf("standalone CloudSync changed: %v", cfg.CloudSync)
	}
	if cfg.LiveFeed || ctx.LiveFeed {
		t.Fatalf("standalone LiveFeed changed: config=%v context=%v", cfg.LiveFeed, ctx.LiveFeed)
	}
}
