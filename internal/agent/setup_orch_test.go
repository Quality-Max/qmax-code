package agent

import (
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

// Tests for the main-package wrappers SetupCodexIntegration / IsOrchSetupDone /
// writeCodexMCPEntry live in setup_orch_test.go in the main package, since
// those wrappers compose this package's primitives.
