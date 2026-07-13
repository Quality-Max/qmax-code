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
	}
}

// InstallSkills materializes the qmax QA skill catalog into the backend's
// native skills directory (~/.claude/skills or ~/.codex/skills). It is
// idempotent and refreshes the catalog on every call, so existing users pick up
// new or updated skills on upgrade.
//
// It is deliberately decoupled from RunOrch's one-time MCP install: the MCP
// entry is written once (guarded by IsOrchInstalled), but skills must keep
// syncing on every activation/upgrade — otherwise anyone who already had the
// MCP installed would never receive the skill catalog.
func InstallSkills(backend string) (*skills.MaterializeResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return skills.Materialize(skills.Backend(backend), home)
}

// InstallSkillsReport runs InstallSkills and reports the outcome to the
// terminal. Non-fatal: skills are additive, so a failure is surfaced but does
// not block backend activation.
func InstallSkillsReport(backend string, term *tui.Terminal) {
	res, err := InstallSkills(backend)
	if err != nil {
		term.PrintError(fmt.Sprintf("Skill install failed: %v", err))
		return
	}
	term.PrintSystem(fmt.Sprintf("Installed %d qmax QA skills into %s", len(res.Written), res.Dir))
}

// InstallSkillsAll materializes the catalog into every supported CLI backend
// and reports each. Used by the `/skills install` command.
func InstallSkillsAll(term *tui.Terminal) {
	InstallSkillsReport("cc", term)
	InstallSkillsReport("codex", term)
	InstallSkillsReport("opencode", term)
}

// PrintSkillsStatus lists the qmax QA skill catalog and shows, per skill,
// whether it is currently materialized into the Claude Code, Codex, and opencode
// skills directories. Backs the `/skills` command so the catalog is visible from
// inside qmax-code, even though the skills themselves run in the CLI backends.
func PrintSkillsStatus(term *tui.Terminal) {
	home, err := os.UserHomeDir()
	if err != nil {
		term.PrintError(fmt.Sprintf("Could not locate home dir: %v", err))
		return
	}
	ccDir, _ := skills.SkillsDir(skills.BackendCC, home)
	cxDir, _ := skills.SkillsDir(skills.BackendCodex, home)
	ocDir, _ := skills.SkillsDir(skills.BackendOpenCode, home)

	catalog := skills.SortedCatalog()
	term.PrintSystem(fmt.Sprintf("qmax QA skills (%d) — installed in:  cc = ~/.claude/skills · codex = ~/.codex/skills · oc = ~/.config/opencode/skills", len(catalog)))
	for _, sk := range catalog {
		cc := installMark(filepath.Join(ccDir, sk.Name, "SKILL.md"))
		cx := installMark(filepath.Join(cxDir, sk.Name, "SKILL.md"))
		oc := installMark(filepath.Join(ocDir, sk.Name, "SKILL.md"))
		desc := sk.ShortDescription
		if desc == "" {
			desc = sk.Description
		}
		fmt.Printf("  cc:%s codex:%s oc:%s  %s%-22s%s %s\n", cc, cx, oc, tui.ColorBold, sk.Name, tui.ColorReset, desc)
	}
	term.PrintSystem("Skills load inside Claude Code / Codex / opencode sessions — auto-invoked by description, or `$name` in Codex.")
	term.PrintSystem("Run /skills install to (re)install them into all backends now.")
}

// installMark returns a check or cross depending on whether path exists.
func installMark(path string) string {
	if _, err := os.Stat(path); err == nil {
		return tui.ColorGreen + "✓" + tui.ColorReset
	}
	return tui.ColorDim + "·" + tui.ColorReset
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
