package agent

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// QmaxMCPCommand is the binary name written into the Codex MCP config so the
// CLI can spawn the qmax MCP server. Centralised here because both runtime
// (codex_agent.go writing per-session config) and setup orchestration
// (cmd/qmax setup) need the same value.
const QmaxMCPCommand = "qmax-code"

// WriteCodexMCPEntry merges the qmax MCP entry into Codex's TOML config at
// path. The env map is rendered into the entry's `env = { ... }` block. The
// returned alreadyHad flag tells the caller whether an entry was already
// present (used by setup orchestration to phrase its progress message).
func WriteCodexMCPEntry(path string, env map[string]string) (alreadyHad bool, err error) {
	data, _ := os.ReadFile(path)
	alreadyHad = codexMCPEntryExists(string(data))
	updated := upsertCodexMCPEntry(string(data), QmaxMCPCommand, env)

	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		return alreadyHad, err
	}
	return alreadyHad, nil
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
	if command == QmaxMCPCommand {
		return command, nil
	}
	return "", fmt.Errorf("command must be %s", QmaxMCPCommand)
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
