package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertCodexMCPEntryAddsQMaxTable(t *testing.T) {
	input := `model = "gpt-5.5"

[projects."/tmp/app"]
trust_level = "trusted"
`

	got := upsertCodexMCPEntry(input, "qmax-code", nil)

	for _, want := range []string{
		`model = "gpt-5.5"`,
		`[projects."/tmp/app"]`,
		`[mcp_servers.qmax]`,
		`command = "qmax-code"`,
		`args = ["serve", "--mcp"]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestUpsertCodexMCPEntryReplacesExistingQMaxOnly(t *testing.T) {
	input := `model = "gpt-5.5"

[mcp_servers.qualitymax]
url = "https://app.qualitymax.io/api/mcp/"
enabled = true

[mcp_servers.qmax]
command = "/old/qmax-code"
args = ["serve"]
env = { "QMAX_PROJECT_ID" = "7" }

[plugins."browser-use@openai-bundled"]
enabled = true
`

	got := upsertCodexMCPEntry(input, "qmax-code", map[string]string{"QMAX_PROJECT_ID": "42"})

	if strings.Contains(got, "/old/qmax-code") || strings.Contains(got, `args = ["serve"]`) {
		t.Fatalf("stale qmax entry was not replaced:\n%s", got)
	}
	for _, want := range []string{
		`[mcp_servers.qualitymax]`,
		`url = "https://app.qualitymax.io/api/mcp/"`,
		`[plugins."browser-use@openai-bundled"]`,
		`command = "qmax-code"`,
		`args = ["serve", "--mcp"]`,
		`env = { "QMAX_PROJECT_ID" = "42" }`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Count(got, "[mcp_servers.qmax]") != 1 {
		t.Fatalf("expected one qmax table, got:\n%s", got)
	}
}

func TestCodexMCPEntryExists(t *testing.T) {
	if !codexMCPEntryExists("\n[mcp_servers.qmax]\ncommand = \"qmax-code\"\n") {
		t.Fatal("expected qmax MCP entry to be detected")
	}
	if codexMCPEntryExists("\n[mcp_servers.qualitymax]\nenabled = true\n") {
		t.Fatal("qualitymax remote entry should not satisfy qmax local entry")
	}
}

func TestRenderCodexMCPEntryRejectsUnsafeCommand(t *testing.T) {
	lines := renderCodexMCPEntry("/tmp/qmax-code\" \n[mcp_servers.evil]\ncommand = \"sh\"", nil)
	got := strings.Join(lines, "\n")

	if !strings.Contains(got, `command = "qmax-code"`) {
		t.Fatalf("unsafe command should fall back to qmax-code:\n%s", got)
	}
	if strings.Contains(got, "evil") || strings.Contains(got, "/tmp/qmax-code") {
		t.Fatalf("unsafe command leaked into TOML:\n%s", got)
	}
}

func TestValidateCodexMCPCommand(t *testing.T) {
	if got, err := validateCodexMCPCommand("qmax-code"); err != nil || got != "qmax-code" {
		t.Fatalf("validateCodexMCPCommand(qmax-code) = %q, %v", got, err)
	}

	for _, command := range []string{"", "sh", "/tmp/not-qmax", "/usr/local/bin/qmax-code", "/tmp/qmax-code;rm", "/tmp/qmax-code/.."} {
		if got, err := validateCodexMCPCommand(command); err == nil {
			t.Fatalf("validateCodexMCPCommand(%q) = %q, nil; want error", command, got)
		}
	}
}

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
