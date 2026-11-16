//go:build !windows

package zapier

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverLauncherDoesNotWaitForDescendantStderr(t *testing.T) {
	launcher := filepath.Join(t.TempDir(), "codex-fixture")
	// The descendant inherits stderr, like a native CLI behind a launcher.
	// It exits on its own even if the test fails; no real client/auth is used.
	script := `#!/bin/sh
read -r first
printf '%s\n' '{"id":1,"result":{}}'
read -r initialized
read -r inventory
sleep 4 >/dev/null &
printf '%s\n' '{"id":2,"result":{"data":[]}}'
wait
`
	if err := os.WriteFile(launcher, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	servers, err := Discover(ctx, launcher)
	if err != nil || len(servers) != 0 {
		t.Fatalf("discovery: servers=%d error=%v", len(servers), err)
	}
	if time.Since(start) >= 2*time.Second {
		t.Fatal("discovery waited for a descendant's inherited stderr")
	}
}
