//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/agent"
)

// Only this fixture CLI runs: it never authenticates or contacts a provider.
func zapierFixture(t *testing.T, version, login, inventory string) string {
	t.Helper()
	home := t.TempDir()
	bin := t.TempDir()
	marker := filepath.Join(home, "login-attempted")
	script := "#!/bin/sh\n" + `if [ "$1" = "--version" ]; then
` + version + `
 exit
fi
if [ "$3" = "mcp" ]; then
 : > "$CODEX_HOME/login-attempted"
` + login + `
 exit
fi
read -r initialize
printf '%s\n' '{"id":1,"result":{}}'
read -r initialized
read -r inventory
` + inventory + "\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

const fixtureVersion = "printf '%s\\n' 'codex-cli 0.144.5'"
const fixtureInventory = `printf '%s\n' '{"id":2,"result":{"data":[{"name":"zapier","authStatus":"oAuth","tools":{"inspect_zapier_actions":{},"execute_zapier_read_action":{}}}]}}'`

func TestZapierConnectAfterFreshDisconnect(t *testing.T) {
	marker := zapierFixture(t, fixtureVersion, "exit 0", fixtureInventory)
	path, err := agent.CodexConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := runZapierCommand(context.Background(), []string{"disconnect"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("disconnect created a config in a fresh home")
	}
	var out bytes.Buffer
	if err := runZapierCommand(context.Background(), []string{"connect"}, &out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `mcp_oauth_credentials_store = "keyring"`) || !strings.Contains(string(data), "enabled = true") {
		t.Fatal("connect did not establish keyring-only opt-in")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("native login was not called")
	}
	if !strings.Contains(out.String(), "Read tools discovered") {
		t.Fatal("successful native discovery not reported")
	}
	if err := runZapierCommand(context.Background(), []string{"disconnect"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "enabled = false") {
		t.Fatal("existing managed integration was not disabled")
	}
}

func TestZapierStatusReportsNativeInventoryWithoutLogin(t *testing.T) {
	marker := zapierFixture(t, fixtureVersion, "exit 0", fixtureInventory)
	var out bytes.Buffer
	if err := runZapierCommand(context.Background(), []string{"status"}, &out); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Qmax    string `json:"qmax_version"`
		Codex   string `json:"codex_version"`
		Servers []struct {
			Name  string   `json:"name"`
			Tools []string `json:"tools"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(bytes.SplitN(out.Bytes(), []byte("\n"), 2)[0], &status); err != nil {
		t.Fatal(err)
	}
	if status.Qmax != Version || status.Codex != "codex-cli 0.144.5" || len(status.Servers) != 1 || status.Servers[0].Name != "zapier" || len(status.Servers[0].Tools) != 2 {
		t.Fatal("missing version or structural tool evidence")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("status attempted login")
	}
	path, err := agent.CodexConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("status mutated configuration")
	}
}

func TestZapierCommandFailuresStayScoped(t *testing.T) {
	tests := []struct{ name, command, version, login, inventory, config, want string }{
		{name: "invalid version", command: "connect", version: "printf '%s\\n' 'unexpected native diagnostic'", want: "cannot identify Codex CLI version"},
		{name: "version exit", command: "status", version: "exit 1", want: "cannot identify Codex CLI version"},
		{name: "login failure", command: "connect", version: fixtureVersion, login: "exit 1", want: "OAuth was not completed"},
		{name: "unsafe credential policy", command: "connect", version: fixtureVersion, config: "mcp_oauth_credentials_store='file'\n", want: "existing credential-storage policy was not changed"},
		{name: "missing auth", command: "status", version: fixtureVersion, inventory: `printf '%s\n' '{"id":2,"result":{"data":[{"name":"zapier","authStatus":"notLoggedIn","tools":{}}]}}'`, want: "authentication required or expired"},
		// Native Codex 0.144.5 retains oAuth after provider revocation but
		// returns an empty tool inventory. The failure must explain recovery.
		{name: "revoked auth with cached OAuth status", command: "status", version: fixtureVersion, inventory: `printf '%s\n' '{"id":2,"result":{"data":[{"name":"zapier","authStatus":"oAuth","tools":{}}]}}'`, want: "if access was revoked or expired, run qmax-code zapier connect"},
		{name: "missing tool", command: "status", version: fixtureVersion, inventory: `printf '%s\n' '{"id":2,"result":{"data":[{"name":"zapier","authStatus":"oAuth","tools":{"inspect_zapier_actions":{}}}]}}'`, want: "read tools unavailable"},
		{name: "broken discovery", command: "status", version: fixtureVersion, inventory: "exit 1", want: "discovery failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marker := zapierFixture(t, tt.version, tt.login, tt.inventory)
			path, err := agent.CodexConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			if tt.config != "" {
				if err := os.WriteFile(path, []byte(tt.config), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			err = runZapierCommand(context.Background(), []string{tt.command}, &out)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("wanted %q, got %v", tt.want, err)
			}
			if strings.Contains(err.Error()+out.String(), "unexpected native diagnostic") {
				t.Fatal("raw diagnostic leaked")
			}
			if tt.name != "login failure" {
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("failure expanded into login")
				}
			}
			if tt.config != "" {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != tt.config {
					t.Fatal("refusal modified configuration")
				}
			}
		})
	}
}

type zapierFailWriter struct{ writes, failAt int }

func (w *zapierFailWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes >= w.failAt {
		return 0, errors.New("fixture write failed")
	}
	return len(p), nil
}

func TestZapierCommandOutputFailures(t *testing.T) {
	for _, tt := range []struct {
		command string
		failAt  int
		want    string
	}{
		{"connect", 1, "cannot display Zapier connection instructions"},
		{"status", 1, "cannot write Zapier discovery status"},
		{"status", 2, "fixture write failed"},
	} {
		t.Run(tt.command+tt.want, func(t *testing.T) {
			zapierFixture(t, fixtureVersion, "exit 0", fixtureInventory)
			err := runZapierCommand(context.Background(), []string{tt.command}, &zapierFailWriter{failAt: tt.failAt})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("wanted %q, got %v", tt.want, err)
			}
		})
	}
}

func TestZapierVersionProbeBoundsInheritedPipes(t *testing.T) {
	for _, version := range []string{"sleep 8 &\nwait", "sleep 8 &\nexit 0"} {
		t.Run(version, func(t *testing.T) {
			marker := zapierFixture(t, version, "exit 0", fixtureInventory)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			err := runZapierCommand(ctx, []string{"connect"}, &bytes.Buffer{})
			elapsed := time.Since(start)
			if err == nil || !strings.Contains(err.Error(), "cannot identify Codex CLI version") {
				t.Fatalf("unexpected result: %v", err)
			}
			if elapsed > 5*time.Second {
				t.Fatalf("inherited pipe defeated deadline: %s", elapsed)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("timed-out version probe attempted OAuth")
			}
		})
	}
}
