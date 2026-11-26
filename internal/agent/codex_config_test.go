package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestCodexMCPPreservesOtherSettings(t *testing.T) {
	for _, header := range []string{"[mcp_servers.qmax]", `[mcp_servers."qmax"] # local`, `[ 'mcp_servers' . 'qmax' ]`} {
		t.Run(header, func(t *testing.T) {
			prefix := "model = 'gpt-6-astra'\napproval_policy = 'on-request'\n"
			other := `[mcp_servers.remote] # user-owned
url = "https://mcp.example.com/connect"
enabled = false
default_tools_approval_mode = "prompt"
[mcp_servers.local]
command = "other-mcp"
args = ["serve"]
[projects."/tmp/project"]
trust_level = "trusted"
notes = '''
[mcp_servers.qmax]
this is text, not a table
'''
`
			input := prefix + header + "\ncommand = 'old'\n[mcp_servers.qmax.env]\nQMAX_PROJECT_ID = '7'\n" + other
			got, err := upsertCodexMCPEntry(input, "qmax-code", map[string]string{"QMAX_PROJECT_ID": "42"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(got, prefix) || !strings.Contains(got, other) {
				t.Fatal("unrelated config bytes changed")
			}
			var before, after map[string]any
			if err := toml.Unmarshal([]byte(input), &before); err != nil {
				t.Fatal(err)
			}
			if err := toml.Unmarshal([]byte(got), &after); err != nil {
				t.Fatal(err)
			}
			delete(before["mcp_servers"].(map[string]any), "qmax")
			servers := after["mcp_servers"].(map[string]any)
			qmax := servers["qmax"].(map[string]any)
			if qmax["command"] != "qmax-code" || qmax["env"].(map[string]any)["QMAX_PROJECT_ID"] != "42" {
				t.Fatal("qmax replacement incorrect")
			}
			delete(servers, "qmax")
			if !reflect.DeepEqual(before, after) {
				t.Fatal("unrelated settings changed")
			}
			if !CodexMCPEntryExists(input) {
				t.Fatal("quoted qmax table not detected")
			}
		})
	}
}

func TestWriteCodexMCPEntryRejectsInvalidOrUnsupportedConfig(t *testing.T) {
	for _, input := range []string{"invalid = [", `mcp_servers = { qmax = { command = "old" } }`} {
		t.Run(input, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := WriteCodexMCPEntry(path, nil); err == nil {
				t.Fatal("expected failure")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != input {
				t.Fatal("failed update changed original")
			}
		})
	}
}

// Pre-v1.40.2 writers replaced only the exact [mcp_servers.qmax] header, so a
// separate [mcp_servers.qmax.env] table survived elsewhere in the file. The next
// inline `env = { ... }` then defined env twice and Codex refused to load.
func TestCodexMCPRemovesNonAdjacentEnvTable(t *testing.T) {
	head := "[mcp_servers.hf]\nurl = \"https://hf.example.com/mcp\"\n\n"
	stale := "[mcp_servers.qmax.env]\nQMAX_EXEC_ID_FILE = \"/tmp/.exec-id-1\"\n\n"
	middle := "[mcp_servers.cloudflare]\nurl = \"https://cf.example.com/mcp\"\n\n[projects.\"/tmp/p\"]\ntrust_level = \"trusted\"\n\n"
	input := head + stale + middle + "[mcp_servers.qmax]\ncommand = \"qmax-code\"\nargs = [\"serve\", \"--mcp\"]\n"

	got, err := upsertCodexMCPEntry(input, "qmax-code", map[string]string{"QMAX_EXEC_ID_FILE": "/tmp/.exec-id-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, head+middle) {
		t.Fatalf("unrelated config bytes changed:\n%s", got)
	}
	if strings.Contains(got, "exec-id-1") || strings.Count(got, "[mcp_servers.qmax") != 1 {
		t.Fatalf("stale qmax env table survived:\n%s", got)
	}
	var doc map[string]any
	if err := toml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("output is not valid TOML: %v", err)
	}
	env := doc["mcp_servers"].(map[string]any)["qmax"].(map[string]any)["env"].(map[string]any)
	if env["QMAX_EXEC_ID_FILE"] != "/tmp/.exec-id-2" {
		t.Fatal("qmax env not replaced")
	}
	again, err := upsertCodexMCPEntry(got, "qmax-code", map[string]string{"QMAX_EXEC_ID_FILE": "/tmp/.exec-id-2"})
	if err != nil || again != got {
		t.Fatal("second write was not idempotent")
	}
}

// A file the old writer already broke (env defined as a table and inline) must
// be left untouched, not rewritten into something else.
func TestWriteCodexMCPEntryLeavesDuplicateEnvConfigUnchanged(t *testing.T) {
	input := "[mcp_servers.qmax.env]\nQMAX_EXEC_ID_FILE = \"/tmp/.exec-id-1\"\n\n[mcp_servers.other]\nurl = \"https://mcp.example.com\"\n\n" +
		"[mcp_servers.qmax]\ncommand = \"qmax-code\"\nenv = { \"QMAX_EXEC_ID_FILE\" = \"/tmp/.exec-id-2\" }\n"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteCodexMCPEntry(path, map[string]string{"QMAX_EXEC_ID_FILE": "/tmp/.exec-id-3"}); err == nil {
		t.Fatal("expected invalid config to be refused")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != input {
		t.Fatal("refused update changed the original")
	}
}

func TestWriteCodexMCPEntryReadFailure(t *testing.T) {
	// A directory deterministically fails ReadFile even when tests run as root.
	path := t.TempDir()
	if _, err := WriteCodexMCPEntry(path, nil); err == nil {
		t.Fatal("read failure ignored")
	}
}

func TestWriteCodexMCPEntryAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("model = 'gpt-6-astra'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		had, err := WriteCodexMCPEntry(path, nil)
		if err != nil || had != (i == 1) {
			t.Fatalf("update %d: had=%v err=%v", i, had, err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("config is not owner-only")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatal("temporary file left behind")
	}
	for _, entry := range entries {
		if entry.Name() != "config.toml" && entry.Name() != "config.toml.qmax.lock" {
			t.Fatal("unexpected temporary file left behind")
		}
	}
}

func TestWriteCodexMCPEntryPreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "managed.toml")
	link := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(target, []byte("model = 'gpt-6-astra'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := WriteCodexMCPEntry(link, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced")
	}
	got, err := os.ReadFile(target)
	if err != nil || !CodexMCPEntryExists(string(got)) {
		t.Fatal("target was not updated")
	}
}

func TestCodexMCPUpdateIsIdempotent(t *testing.T) {
	first, err := upsertCodexMCPEntry("model = 'gpt-6-astra'\n", "qmax-code", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := upsertCodexMCPEntry(first, "qmax-code", nil)
	if err != nil || first != second {
		t.Fatal("repeated turns change configuration")
	}
}
