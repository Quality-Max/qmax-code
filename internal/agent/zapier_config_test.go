package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/pelletier/go-toml/v2"
)

func TestConcurrentAgentUpdateCannotUndoDisconnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := ConfigureCodexZapier(path, true); err != nil {
		t.Fatal(err)
	}
	read := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	agentDone := make(chan error, 1)
	go func() {
		agentDone <- updateCodexConfig(path, func(config string) (string, error) {
			close(read)
			<-release
			return upsertCodexMCPEntry(config, QmaxMCPCommand, nil)
		})
	}()
	select {
	case <-read:
	case err := <-agentDone:
		t.Fatalf("agent update ended before read: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("agent update did not start")
	}
	disconnected := make(chan error, 1)
	go func() { disconnected <- ConfigureCodexZapier(path, false) }()
	select {
	case err := <-disconnected:
		t.Fatalf("disconnect bypassed active config lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-agentDone; err != nil {
		t.Fatal(err)
	}
	if err := <-disconnected; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Servers map[string]map[string]any `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Servers["zapier"]["enabled"] != false || !CodexMCPEntryExists(string(data)) {
		t.Fatal("concurrent agent update lost disconnect or qmax entry")
	}
}

func TestZapierOptInPreservesCoexistingServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new-home", "config.toml")
	if err := ConfigureCodexZapier(path, true); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteCodexMCPEntry(path, map[string]string{api.LocalOnlyEnv: "1"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	other := "[mcp_servers.'9lives']\ncommand = '9l'\nargs = ['mcp']\n[projects.'/tmp/demo']\ntrust_level = 'trusted'\n"
	if _, err := f.WriteString("\n" + other); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, true, false, false, true} {
		if err := ConfigureCodexZapier(path, enabled); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteCodexMCPEntry(path, map[string]string{api.LocalOnlyEnv: "1"}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), other) {
			t.Fatal("unrelated config changed")
		}
		var doc struct {
			Servers map[string]map[string]any `toml:"mcp_servers"`
		}
		if err := toml.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Servers) != 3 || !managedZapierEntry(doc.Servers["zapier"]) || doc.Servers["zapier"]["enabled"] != enabled {
			t.Fatal("coexistence or read-only policy lost")
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("config permissions")
		}
	}
}

func TestZapierRefusesExistingCustomOrMalformedConfig(t *testing.T) {
	for _, config := range []string{
		"model = [",
		"[mcp_servers.zapier]\nurl = 'https://example.com/private'\n",
		"[mcp_servers.zapier]\nurl = '" + ZapierMCPURL + "'\nenabled=true\nenabled_tools=['execute_zapier_write_action']\n",
		"mcp_servers={zapier={url='" + ZapierMCPURL + "',enabled=true,enabled_tools=['inspect_zapier_actions','execute_zapier_read_action']}}\n",
	} {
		for _, enabled := range []bool{true, false} {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ConfigureCodexZapier(path, enabled); err == nil {
				t.Fatal("expected refusal")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != config {
				t.Fatal("refusal modified config")
			}
		}
	}
}

func TestZapierRefusesPlaintextOAuthStore(t *testing.T) {
	for _, original := range []string{"mcp_oauth_credentials_store='file'\n", "mcp_oauth_credentials_store='auto'\n", "[mcp_servers.other]\nurl='https://example.com/mcp'\n"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		if ConfigureCodexZapier(path, true) == nil {
			t.Fatal("existing credential storage policy changed")
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != original {
			t.Fatal("credential policy changed")
		}
	}
}

func TestCodexAgentUsesSelectedHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	selected := filepath.Join(t.TempDir(), "codex")
	t.Setenv("CODEX_HOME", selected)
	a := NewCodexAgent("codex", "", "high", false, "", &api.SessionContext{LocalOnly: true})
	if err := a.WriteMCPConfig(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(selected, "config.toml"))
	if err != nil || !CodexMCPEntryExists(string(data)) {
		t.Fatal("selected home not used")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatal("default Codex home touched")
	}
}
