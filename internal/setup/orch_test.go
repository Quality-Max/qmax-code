package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestInstallCodexWritesConfigTOML(t *testing.T) {
	home := withTempHome(t)

	res, err := InstallCodex()
	if err != nil {
		t.Fatalf("InstallCodex: %v", err)
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
	if !IsOrchInstalled("codex") {
		t.Fatal("IsOrchInstalled(codex) should detect config.toml qmax entry")
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
