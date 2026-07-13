package main

import (
	"fmt"
	"os"
	"path/filepath"

	receipt "github.com/Quality-Max/qmax-receipt"
)

// initReceiptPaths points the shared receipt module at qmax-code's own state
// directory (~/.qmax-code) and stamps this build's version into every receipt.
// It must run before any receipt operation (session start or `receipt` command)
// so identity/store stay separate from the qmax CLI's ~/.qamax layout. Both
// binaries emit the same receipt_version schema; only BaseDir differs.
func initReceiptPaths() {
	if home, err := os.UserHomeDir(); err == nil {
		receipt.BaseDir = filepath.Join(home, ".qmax-code")
	}
	receipt.AgentVersion = Version
}

// beginSessionReceipt installs a process-global Exposure Receipt for this
// qmax-code run. Because the shared module routes any egress with no
// receipt-bearing context to the current run, every outbound request from this
// process — LLM prompts, cloud-API calls, integration connects — is captured
// without threading a context through every call site.
func beginSessionReceipt(kind string) *receipt.Receipt {
	return receipt.NewCurrent("session:" + kind)
}

// receiptKind derives a short run-kind label for the session receipt from the
// invocation's first argument, so a reviewer can tell an MCP-serve run from an
// interactive one at a glance in `qmax-code receipt list`.
func receiptKind(args []string) string {
	if len(args) > 1 {
		switch args[1] {
		case "serve":
			return "mcp"
		case "login":
			return "login"
		case "cc":
			return "cc"
		case "codex":
			return "codex"
		case "config":
			return "config"
		}
	}
	return "interactive"
}

// finalizeSessionReceipt signs and writes the session receipt at exit, but only
// when the session actually egressed — a no-egress run (--version, config show,
// help) leaves the receipts directory clean.
func finalizeSessionReceipt(r *receipt.Receipt) {
	if r == nil || r.EntryCount() == 0 {
		return
	}
	if path, err := r.Finalize(); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: failed to write exposure receipt: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "Exposure receipt: %s\n", path)
	}
}
