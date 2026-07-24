package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/sysutil"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// withTempHome points $HOME at a fresh temp dir for the test and restores it
// in cleanup. Local copy of the helper used by other test files in the repo.
func withTempHome(t *testing.T) string {
	t.Helper()
	orig := os.Getenv("HOME")
	tmp := t.TempDir()
	os.Setenv("HOME", tmp)
	t.Cleanup(func() { os.Setenv("HOME", orig) })
	return tmp
}

func TestCodexRunRestoresMCPConfigBeforeExec(t *testing.T) {
	home := withTempHome(t)
	resetLiveURLFileForTest()
	codexBin := writeFakeCLI(t, "codex", `#!/bin/sh
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"codex ok"}}'
`)

	a := NewCodexAgent(codexBin, "", "high", "standard", false, &api.SessionContext{
		ProjectID: 88,
		LiveFeed:  true,
		LocalOnly: true,
	})

	got, err := a.Run("list projects", &tui.Terminal{})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got != "codex ok" {
		t.Fatalf("Run result = %q, want codex ok", got)
	}

	cfgPath := filepath.Join(home, ".codex", "config.toml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("expected Codex MCP config to be restored: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`[mcp_servers.qmax]`,
		`command = "qmax-code"`,
		`args = ["serve", "--mcp"]`,
		`"QMAX_PROJECT_ID" = "88"`,
		`"QMAX_LIVE_FEED" = "1"`,
		`"QMAX_LOCAL_ONLY" = "1"`,
		`"QMAX_LIVE_URL_FILE" = `,
		`"QMAX_EXEC_ID_FILE" = `,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in restored config:\n%s", want, text)
		}
	}
}

func TestCCRunRestoresDeletedMCPConfigBeforeExec(t *testing.T) {
	_ = withTempHome(t)
	claudeBin := writeFakeCLI(t, "claude", `#!/bin/sh
printf '%s\n' '{"type":"result","result":"cc ok"}'
`)

	a := NewCCAgent(claudeBin, "", "high", "standard", false, &api.SessionContext{ProjectID: 42, LocalOnly: true})
	if err := a.WriteMCPConfig(); err != nil {
		t.Fatalf("initial WriteMCPConfig: %v", err)
	}

	a.mu.Lock()
	oldPath := a.mcpConfigPath
	a.mu.Unlock()
	if oldPath == "" {
		t.Fatal("expected initial MCP config path")
	}
	if err := os.Remove(oldPath); err != nil {
		t.Fatalf("remove initial MCP config: %v", err)
	}

	got, err := a.Run("list projects", &tui.Terminal{})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got != "cc ok" {
		t.Fatalf("Run result = %q, want cc ok", got)
	}

	a.mu.Lock()
	newPath := a.mcpConfigPath
	a.mu.Unlock()
	if newPath == "" {
		t.Fatal("expected restored MCP config path")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected MCP config to exist after restore: %v", err)
	}
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("read restored MCP config: %v", err)
	}
	var restored struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("parse restored MCP config: %v", err)
	}
	if got := restored.MCPServers["qmax"].Env[api.LocalOnlyEnv]; got != "1" {
		t.Fatalf("restored MCP config %s = %q, want 1", api.LocalOnlyEnv, got)
	}
}

func writeFakeCLI(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	return path
}

func resetLiveURLFileForTest() {
	sysutil.ResetLiveURLFileForTest()
}

func resetExecIDFileForTest() {
	sysutil.ResetExecIDFileForTest()
}
