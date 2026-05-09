package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/qualitymax/qmax-code/internal/api"
)

// orchConsentResult is what activateBackend gets back from the consent prompt.
type orchConsentResult struct {
	Proceed        bool   // user said yes; false means cancel activation
	PermissionMode string // "standard" | "unattended"
	GlobalInstall  bool   // ok to write into ~/.claude/settings.json or ~/.codex/config.toml
}

// promptOrchConsent shows the autonomy + global-install dialog the first time
// a CLI backend is activated. If the user already chose persistently
// (cfg.OrchPermissionMode / cfg.OrchGlobalInstall) we skip the prompt.
//
// Conductor's safety model: the user is the principal. We never sneak privilege
// escalation behind a backend switch. Mode choices are explicit and persisted.
func promptOrchConsent(cfg *api.Config, backend string) orchConsentResult {
	cliName := "Claude Code"
	globalConfigPath := "~/.claude/settings.json"
	if backend == "codex" {
		cliName = "Codex"
		globalConfigPath = "~/.codex/config.toml"
	}

	res := orchConsentResult{
		PermissionMode: cfg.OrchPermissionMode,
		GlobalInstall:  cfg.OrchGlobalInstall,
	}

	reader := bufio.NewReader(os.Stdin)

	// First prompt: autonomy level. Skip if previously chosen.
	if res.PermissionMode == "" {
		fmt.Println()
		fmt.Printf("  Activating %s backend.\n", cliName)
		fmt.Println()
		fmt.Println("  Pick how much autonomy to grant:")
		fmt.Println()
		fmt.Println("   [1] Standard   — auto-approves reads, search, git status/diff, common")
		fmt.Println("                    test runners (go test, pytest, npm test, cargo test…)")
		fmt.Println("                    and qmax tools. Won't edit files or run destructive")
		fmt.Println("                    shell commands without asking.   (recommended)")
		fmt.Println()
		fmt.Println("   [2] Unattended — full --dangerously-skip-permissions. Anything goes:")
		fmt.Println("                    edit files, run any shell command, push commits.")
		fmt.Println("                    Use only on trusted projects.")
		fmt.Println()
		fmt.Println("   [N] Cancel     — keep current backend.")
		fmt.Println()
		fmt.Print("  Choice [1/2/N, default 1]: ")
		line, _ := reader.ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		switch ans {
		case "", "1", "standard", "s":
			res.PermissionMode = "standard"
		case "2", "unattended", "u", "y", "yes":
			res.PermissionMode = "unattended"
			fmt.Println()
			fmt.Print("  ⚠ Unattended grants full shell + file-edit access. Confirm? [y/N]: ")
			line, _ := reader.ReadString('\n')
			ans2 := strings.ToLower(strings.TrimSpace(line))
			if ans2 != "y" && ans2 != "yes" {
				fmt.Println("  Cancelled.")
				return orchConsentResult{Proceed: false}
			}
		default:
			fmt.Println("  Cancelled.")
			return orchConsentResult{Proceed: false}
		}
	}

	// Second prompt: global install. Only ask if not previously consented and not already done.
	if !res.GlobalInstall && !IsOrchSetupDone(backend) {
		fmt.Println()
		fmt.Printf("  Optional: register qmax tools globally in %s\n", globalConfigPath)
		fmt.Printf("  so they appear in every `%s` session, not just qmax-code.\n", strings.ToLower(cliName))
		fmt.Println()
		fmt.Print("  Install globally? [y/N]: ")
		line, _ := reader.ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		res.GlobalInstall = (ans == "y" || ans == "yes")
	}

	res.Proceed = true
	return res
}
