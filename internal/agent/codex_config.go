package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// QmaxMCPCommand is the binary name written into the Codex MCP config so the
// CLI can spawn the qmax MCP server. Centralised here because both runtime
// (codex_agent.go writing per-session config) and setup orchestration
// (cmd/qmax setup) need the same value.
const QmaxMCPCommand = "qmax-code"

// CodexConfigPath follows the same home selection as the Codex subprocess.
func CodexConfigPath() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Abs(filepath.Join(home, "config.toml"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot locate Codex home")
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

// WriteCodexMCPEntry merges the qmax MCP entry into Codex's TOML config at
// path. The env map is rendered into the entry's `env = { ... }` block. The
// returned alreadyHad flag tells the caller whether an entry was already
// present (used by setup orchestration to phrase its progress message).
func WriteCodexMCPEntry(path string, env map[string]string) (alreadyHad bool, err error) {
	err = updateCodexConfig(path, func(config string) (string, error) {
		alreadyHad = CodexMCPEntryExists(config)
		return upsertCodexMCPEntry(config, QmaxMCPCommand, env)
	})
	return alreadyHad, err
}

func updateCodexConfig(path string, update func(string) (string, error)) error {
	// Preserve a user-managed symlink rather than replacing the link itself.
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			return errors.New("cannot resolve Codex config symlink")
		}
		path = resolved
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("cannot create Codex config directory")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return errors.New("cannot resolve Codex config directory")
	}
	path = filepath.Join(parent, filepath.Base(path))
	// Keep the sidecar inode: unlinking it could let another process acquire
	// a different lock while this one is still held. OS locks release on exit.
	lock, err := os.OpenFile(path+".qmax.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return errors.New("cannot open Codex config lock")
	}
	defer func() { _ = lock.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		acquired, lockErr := tryCodexConfigLock(lock)
		if lockErr != nil {
			return errors.New("cannot lock Codex config")
		}
		if acquired {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("codex config is busy; retry the command")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer unlockCodexConfig(lock)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot read Codex config; existing settings were not changed")
	}
	updated, err := update(string(data))
	if err != nil {
		return err
	}
	// Replace atomically so a failed write cannot truncate other MCP entries.
	f, err := os.CreateTemp(filepath.Dir(path), ".qmax-codex-*")
	if err != nil {
		return errors.New("cannot prepare Codex config update")
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.WriteString(updated); err != nil {
		_ = f.Close()
		return errors.New("cannot write Codex config update")
	}
	if err = f.Close(); err != nil {
		return errors.New("cannot close Codex config update")
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return errors.New("cannot replace Codex config")
	}
	return nil
}

// CodexMCPEntryExists reports whether valid TOML defines a qmax MCP entry.
func CodexMCPEntryExists(config string) bool {
	var doc struct {
		Servers map[string]any `toml:"mcp_servers"`
	}
	if toml.Unmarshal([]byte(config), &doc) != nil {
		return false
	}
	_, ok := doc.Servers["qmax"]
	return ok
}

func upsertCodexMCPEntry(config, command string, env map[string]string) (string, error) {
	return replaceCodexMCPTable(config, "qmax", strings.Join(renderCodexMCPEntry(command, env), "\n")+"\n")
}

func replaceCodexMCPTable(config, name, entry string) (string, error) {
	data := []byte(config)
	var doc map[string]any
	// Parser diagnostics can contain configuration values. Never return them.
	if toml.Unmarshal(data, &doc) != nil {
		return "", errors.New("invalid Codex TOML; existing settings were not changed")
	}
	var parser unstable.Parser
	parser.Reset(data)
	var out bytes.Buffer
	start, skip := 0, false
	for parser.NextExpression() {
		node := parser.Expression()
		if node.Kind != unstable.Table && node.Kind != unstable.ArrayTable {
			continue
		}
		keys := node.Key()
		var path []string
		offset := 0
		for keys.Next() {
			key := keys.Node()
			if len(path) == 0 {
				offset = int(key.Raw.Offset)
			}
			path = append(path, string(key.Data))
		}
		// The key's source offset handles quoted names and headers with comments;
		// unlike line matching it cannot mistake multiline string data for a table.
		offset = bytes.LastIndexByte(data[:offset], '\n') + 1
		if !skip {
			out.Write(data[start:offset])
		}
		start = offset
		skip = len(path) >= 2 && path[0] == "mcp_servers" && path[1] == name
	}
	if parser.Error() != nil {
		return "", errors.New("cannot parse Codex config")
	}
	if !skip {
		out.Write(data[start:])
	}
	if out.Len() > 0 && out.Bytes()[out.Len()-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteString(entry)
	updated := out.String()
	// Inline/dotted qmax definitions cannot be safely removed as table ranges.
	// Refuse the update rather than duplicate or rewrite unrelated settings.
	if toml.Unmarshal([]byte(updated), &doc) != nil {
		return "", errors.New("unsupported MCP TOML layout; use separate server tables")
	}
	return updated, nil
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
