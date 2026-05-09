package main

import (
	"encoding/json"
	"fmt"
	"github.com/qualitymax/qmax-code/internal/tui"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const qmaxMCPCommand = "qmax-code"

// OrchSetupResult describes what autosetup did so the caller can display it.
type OrchSetupResult struct {
	MCPWritten    bool
	MCPPath       string
	AlreadyHadMCP bool
}

// SetupCCIntegration writes the qmax MCP server into ~/.claude/settings.json
// so Claude Code picks up qmax tools in EVERY session — not just when spawned
// by qmax-code. This gives CC full bidirectional capability inheritance:
//
//   - CC gets all qmax QA tools (list, run, generate, crawl, review…)
//   - When qmax-code is the host, CC's own tools (bash, file ops, web) are
//     also fully active and the system prompt instructs CC to use both.
func SetupCCIntegration() (*OrchSetupResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0700); err != nil {
		return nil, err
	}

	settingsPath := filepath.Join(claudeDir, "settings.json")
	return writeMCPEntry(settingsPath)
}

// SetupCodexIntegration writes the qmax MCP server into ~/.codex/config.toml
// so Codex picks up qmax tools whenever the user invokes `codex` directly,
// in addition to when qmax-code spawns it.
func SetupCodexIntegration() (*OrchSetupResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		return nil, err
	}

	cfgPath := filepath.Join(codexDir, "config.toml")
	return writeCodexMCPEntry(cfgPath, nil)
}

// writeMCPEntry merges the qmax MCP entry into a JSON config file.
// Existing keys are preserved; only the "qmax" entry under "mcpServers" is
// added or updated.
func writeMCPEntry(path string) (*OrchSetupResult, error) {
	// Load existing config (may not exist yet).
	cfg := map[string]interface{}{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}

	// Navigate / create the mcpServers map.
	mcpServers, _ := cfg["mcpServers"].(map[string]interface{})
	if mcpServers == nil {
		mcpServers = map[string]interface{}{}
	}

	alreadyHad := false
	if existing, ok := mcpServers["qmax"]; ok && existing != nil {
		alreadyHad = true
	}

	mcpServers["qmax"] = map[string]interface{}{
		"command": qmaxMCPCommand,
		"args":    []string{"serve", "--mcp"},
	}
	cfg["mcpServers"] = mcpServers

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, err
	}

	return &OrchSetupResult{
		MCPWritten:    true,
		MCPPath:       path,
		AlreadyHadMCP: alreadyHad,
	}, nil
}

// writeCodexMCPEntry merges the qmax MCP entry into Codex's TOML config.
func writeCodexMCPEntry(path string, env map[string]string) (*OrchSetupResult, error) {
	data, _ := os.ReadFile(path)
	alreadyHad := codexMCPEntryExists(string(data))
	updated := upsertCodexMCPEntry(string(data), qmaxMCPCommand, env)

	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		return nil, err
	}

	return &OrchSetupResult{
		MCPWritten:    true,
		MCPPath:       path,
		AlreadyHadMCP: alreadyHad,
	}, nil
}

func codexMCPEntryExists(config string) bool {
	for _, line := range strings.Split(config, "\n") {
		if strings.TrimSpace(line) == "[mcp_servers.qmax]" {
			return true
		}
	}
	return false
}

func upsertCodexMCPEntry(config, command string, env map[string]string) string {
	lines := strings.Split(strings.ReplaceAll(config, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines)+8)

	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "[mcp_servers.qmax]" {
			out = append(out, lines[i])
			continue
		}
		for i+1 < len(lines) && !isTOMLTableHeader(lines[i+1]) {
			i++
		}
	}

	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) > 0 {
		out = append(out, "")
	}
	out = append(out, renderCodexMCPEntry(command, env)...)
	return strings.Join(out, "\n") + "\n"
}

func isTOMLTableHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")
}

func renderCodexMCPEntry(command string, env map[string]string) []string {
	safeCommand, err := validateCodexMCPCommand(command)
	if err != nil {
		safeCommand = "qmax-code"
	}

	lines := []string{
		"[mcp_servers.qmax]",
		"command = " + tomlQuote(safeCommand),
		`args = ["serve", "--mcp"]`,
	}
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, tomlQuote(k)+" = "+tomlQuote(env[k]))
		}
		lines = append(lines, "env = { "+strings.Join(parts, ", ")+" }")
	}
	return lines
}

func validateCodexMCPCommand(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == qmaxMCPCommand {
		return command, nil
	}
	return "", fmt.Errorf("command must be %s", qmaxMCPCommand)
}

func tomlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(fmt.Sprintf(`\u%04x`, r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// RunOrchSetup runs the one-time integration setup for the chosen backend,
// printing progress to the terminal.
func RunOrchSetup(backend string, term *tui.Terminal) {
	switch backend {
	case "cc":
		term.PrintSystem("Setting up Claude Code integration…")
		res, err := SetupCCIntegration()
		if err != nil {
			term.PrintError(fmt.Sprintf("CC setup failed: %v", err))
			return
		}
		if res.AlreadyHadMCP {
			term.PrintSystem(fmt.Sprintf("qmax MCP entry updated in %s", res.MCPPath))
		} else {
			term.PrintSystem(fmt.Sprintf("qmax MCP entry added to %s", res.MCPPath))
		}
		term.PrintSystem("Claude Code now has qmax tools in EVERY session (not just when spawned by qmax-code).")
		term.PrintSystem("Run `claude` directly and qmax QA tools will be available automatically.")

	case "codex":
		term.PrintSystem("Setting up Codex integration…")
		res, err := SetupCodexIntegration()
		if err != nil {
			term.PrintError(fmt.Sprintf("Codex setup failed: %v", err))
			return
		}
		if res.AlreadyHadMCP {
			term.PrintSystem(fmt.Sprintf("qmax MCP entry updated in %s", res.MCPPath))
		} else {
			term.PrintSystem(fmt.Sprintf("qmax MCP entry added to %s", res.MCPPath))
		}
		term.PrintSystem("Codex now has qmax tools in every session.")
	}
}

// IsOrchSetupDone returns true if the qmax MCP entry already exists in the
// CLI's config file. Used to skip the setup banner on subsequent launches.
func IsOrchSetupDone(backend string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	var cfgPath string
	switch backend {
	case "cc":
		cfgPath = filepath.Join(home, ".claude", "settings.json")
	case "codex":
		cfgPath = filepath.Join(home, ".codex", "config.toml")
	default:
		return true
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return false
	}

	if backend == "codex" {
		return codexMCPEntryExists(string(data))
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}
	mcpServers, _ := cfg["mcpServers"].(map[string]interface{})
	_, exists := mcpServers["qmax"]
	return exists
}
