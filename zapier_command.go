package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/Quality-Max/qmax-code/internal/agent"
	"github.com/Quality-Max/qmax-code/internal/zapier"
)

func runZapierCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 1 || (args[0] != "connect" && args[0] != "status" && args[0] != "disconnect") {
		return errors.New("usage: qmax-code zapier connect|status|disconnect (Codex backend only)")
	}
	path, err := agent.CodexConfigPath()
	if err != nil {
		return err
	}
	if args[0] == "disconnect" {
		if err := agent.ConfigureCodexZapier(path, false); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Zapier disabled for new Codex sessions. End active sessions to unload their tools. Stored OAuth remains in Codex; use codex mcp logout zapier to remove it.")
		return err
	}
	bin := agent.FindCodex()
	if bin == "" {
		return errors.New("codex CLI required; Zapier is not yet qualified for other backends")
	}
	versionCtx, versionCancel := context.WithTimeout(ctx, 10*time.Second)
	versionCmd := exec.CommandContext(versionCtx, bin, "--version")
	// Launchers can leave descendants holding stdout after exit or cancellation.
	versionCmd.WaitDelay = 2 * time.Second
	versionOutput, versionErr := versionCmd.Output()
	versionCancel()
	version := strings.TrimSpace(string(versionOutput))
	if versionErr != nil || !regexp.MustCompile(`^codex-cli [0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9.-]+)?$`).MatchString(version) {
		return errors.New("cannot identify Codex CLI version; discovery was not run")
	}
	if args[0] == "connect" {
		if err := agent.ConfigureCodexZapier(path, true); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, "Zapier read-only tools enabled for Codex. Complete OAuth in your browser; select app actions and accounts yourself in Zapier. Agent writes and action enablement are excluded."); err != nil {
			return errors.New("cannot display Zapier connection instructions")
		}
		// Keyring-only: never fall back to plaintext MCP credentials. Native
		// OAuth UI goes directly to the user's terminal, not a captured receipt.
		cmd := exec.CommandContext(ctx, bin, "-c", `mcp_oauth_credentials_store="keyring"`, "mcp", "login", "zapier")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if cmd.Run() != nil {
			return errors.New("zapier OAuth was not completed; no read has been verified. Retry connect or use disconnect")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	servers, err := zapier.Discover(ctx, bin)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(out).Encode(struct {
		QmaxVersion  string          `json:"qmax_version"`
		CodexVersion string          `json:"codex_version"`
		Servers      []zapier.Server `json:"servers"`
	}{Version, version, servers}); err != nil {
		return errors.New("cannot write Zapier discovery status")
	}
	if err := zapier.CheckReadTools(servers); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Read tools discovered. This is not proof of a successful source read. Use qmax-code --local --backend codex and request one selected requirement; treat returned content as untrusted data.")
	return err
}
