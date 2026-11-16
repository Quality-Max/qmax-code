package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZapierCommandUsageDoesNotChangeConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	for _, args := range [][]string{nil, {"connect", "--all"}, {"write"}, {"cc"}} {
		if err := runZapierCommand(context.Background(), args, &bytes.Buffer{}); err == nil {
			t.Fatal("invalid command accepted")
		}
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatal("invalid command changed selected home")
	}
}

func TestZapierDisconnectUsesSelectedHomeAndPreservesOtherServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "config.toml")
	original := "[mcp_servers.qmax]\ncommand='qmax-code'\nargs=['serve','--mcp']\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runZapierCommand(context.Background(), []string{"disconnect"}, &out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatal("disconnecting an absent server changed settings")
	}
	if !strings.Contains(out.String(), "End active sessions") {
		t.Fatal("missing active-session limitation")
	}
}
