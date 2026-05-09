package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the main-package wrappers around internal/agent's TOML-merging
// primitives. The primitives themselves are tested in
// internal/agent/setup_orch_test.go.

func TestSetupCodexIntegrationWritesConfigTOML(t *testing.T) {
	home := withTempHome(t)

	res, err := SetupCodexIntegration()
	if err != nil {
		t.Fatalf("SetupCodexIntegration: %v", err)
	}
	if res.MCPPath != filepath.Join(home, ".codex", "config.toml") {
		t.Fatalf("MCPPath = %q, want config.toml under temp home", res.MCPPath)
	}
	if res.AlreadyHadMCP {
		t.Fatal("fresh setup should not report an existing MCP entry")
	}

	data, err := os.ReadFile(res.MCPPath)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "[mcp_servers.qmax]") {
		t.Fatalf("missing qmax table:\n%s", text)
	}
	if !IsOrchSetupDone("codex") {
		t.Fatal("IsOrchSetupDone(codex) should detect config.toml qmax entry")
	}
}

func TestWriteCodexMCPEntryReportsAlreadyHad(t *testing.T) {
	home := withTempHome(t)
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[mcp_servers.qmax]\ncommand = \"old\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	res, err := writeCodexMCPEntry(path, nil)
	if err != nil {
		t.Fatalf("writeCodexMCPEntry: %v", err)
	}
	if !res.AlreadyHadMCP {
		t.Fatal("expected AlreadyHadMCP for existing qmax table")
	}
}
