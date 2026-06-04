package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/skills"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// InstallResult describes what autosetup did so the caller can display it.
type InstallResult struct {
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
func InstallCC() (*InstallResult, error) {
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
func InstallCodex() (*InstallResult, error) {
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
func writeMCPEntry(path string) (*InstallResult, error) {
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
		"command": agent.QmaxMCPCommand,
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

	return &InstallResult{
		MCPWritten:    true,
		MCPPath:       path,
		AlreadyHadMCP: alreadyHad,
	}, nil
}

// writeCodexMCPEntry wraps agent.WriteCodexMCPEntry, packaging the
// already-had flag into an InstallResult.
func writeCodexMCPEntry(path string, env map[string]string) (*InstallResult, error) {
	alreadyHad, err := agent.WriteCodexMCPEntry(path, env)
	if err != nil {
		return nil, err
	}
	return &InstallResult{
		MCPWritten:    true,
		MCPPath:       path,
		AlreadyHadMCP: alreadyHad,
	}, nil
}

// RunOrchSetup runs the one-time integration setup for the chosen backend,
// printing progress to the terminal.
func RunOrch(backend string, term *tui.Terminal) {
	switch backend {
	case "cc":
		term.PrintSystem("Setting up Claude Code integration…")
		res, err := InstallCC()
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
		materializeSkills(skills.BackendCC, term)

	case "codex":
		term.PrintSystem("Setting up Codex integration…")
		res, err := InstallCodex()
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
		materializeSkills(skills.BackendCodex, term)
	}
}

// materializeSkills writes the qmax skill catalog into the backend's native
// skills directory so the QA skills auto-load in every CLI session, the same
// way the MCP tools do. Failures are non-fatal — the MCP integration is the
// hard requirement; skills are an additive convenience.
func materializeSkills(backend skills.Backend, term *tui.Terminal) {
	home, err := os.UserHomeDir()
	if err != nil {
		term.PrintError(fmt.Sprintf("Could not locate home dir for skill install: %v", err))
		return
	}
	res, err := skills.Materialize(backend, home)
	if err != nil {
		term.PrintError(fmt.Sprintf("Skill install failed: %v", err))
		return
	}
	term.PrintSystem(fmt.Sprintf("Installed %d qmax QA skills into %s", len(res.Written), res.Dir))
}

// IsOrchSetupDone returns true if the qmax MCP entry already exists in the
// CLI's config file. Used to skip the setup banner on subsequent launches.
func IsOrchInstalled(backend string) bool {
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
		// Inline check — avoid exporting codexMCPEntryExists since this is
		// the only caller outside the agent package.
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "[mcp_servers.qmax]" {
				return true
			}
		}
		return false
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}
	mcpServers, _ := cfg["mcpServers"].(map[string]interface{})
	_, exists := mcpServers["qmax"]
	return exists
}
