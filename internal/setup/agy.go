package setup

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"

	"github.com/qualitymax/qmax-code/internal/agent"
	"golang.org/x/term"
)

// agyGoogleLoginOffered is set after the first Google sign-in prompt in this
// process. AgyFileTokenPresent is a file-path probe only: macOS Keychain
// tokens are invisible, so a missing file must not keep re-asking on every
// /agy or /orch switch.
var agyGoogleLoginOffered atomic.Bool

func claimAgyGoogleLoginPrompt() bool {
	return agyGoogleLoginOffered.CompareAndSwap(false, true)
}

func resetAgyGoogleLoginPrompt() {
	agyGoogleLoginOffered.Store(false)
}

// PromptAgyGoogleLogin offers to launch Antigravity's Google OAuth flow.
// agy 1.1.x has no `auth login` subcommand: Google sign-in runs when the
// interactive CLI starts with no cached token. An AI Studio API key is not
// required. Skipped when stdin is not a TTY, when a file-backed OAuth token
// is already present, or when this process already offered the prompt.
func PromptAgyGoogleLogin(agyBin string) {
	if agyBin == "" {
		return
	}
	if agent.AgyFileTokenPresent() {
		return
	}
	if !claimAgyGoogleLoginPrompt() {
		return
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Println("  Sign in with Google first: run `agy`, complete the browser login, then exit.")
		return
	}

	fmt.Println()
	fmt.Println("  Antigravity authenticates with Google OAuth (browser sign-in).")
	fmt.Println("  Use the same Google account as Google AI Studio.")
	fmt.Println("  An AI Studio API key is not required.")
	fmt.Println()
	fmt.Print("  Launch Google sign-in now? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(line))
	if ans != "y" && ans != "yes" {
		fmt.Println("  Skipping. If a later turn fails with authentication required,")
		fmt.Println("  run `agy`, sign in with Google, then exit and retry /agy.")
		return
	}

	fmt.Println("  Opening Antigravity. Sign in with Google in the browser,")
	fmt.Println("  then exit the CLI (/exit or Ctrl+C) to return here.")
	cmd := exec.Command(agyBin)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("  Interactive `agy` exited (%v).\n", err)
		fmt.Println("  If you did not finish Google sign-in, run `agy` in a terminal and try again.")
	}
}
