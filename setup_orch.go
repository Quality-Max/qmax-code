package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

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

// SetupCodexIntegration writes the qmax MCP server into ~/.codex/config.json
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

	cfgPath := filepath.Join(codexDir, "config.json")
	return writeMCPEntry(cfgPath)
}

// writeMCPEntry merges the qmax MCP entry into a JSON config file.
// Existing keys are preserved; only the "qmax" entry under "mcpServers" is
// added or updated.
func writeMCPEntry(path string) (*OrchSetupResult, error) {
	self, err := os.Executable()
	if err != nil {
		self = "qmax-code"
	}

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
		"command": self,
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

// RunOrchSetup runs the one-time integration setup for the chosen backend,
// printing progress to the terminal.
func RunOrchSetup(backend string, term *Terminal) {
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
		cfgPath = filepath.Join(home, ".codex", "config.json")
	default:
		return true
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return false
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}

	mcpServers, _ := cfg["mcpServers"].(map[string]interface{})
	if mcpServers == nil {
		return false
	}
	_, exists := mcpServers["qmax"]
	return exists
}
