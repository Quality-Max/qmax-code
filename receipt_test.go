package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	receipt "github.com/Quality-Max/qmax-receipt"
)

func TestReceiptKind(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, "interactive"},
		{[]string{"qmax-code"}, "interactive"},
		{[]string{"qmax-code", "serve"}, "mcp"},
		{[]string{"qmax-code", "login"}, "login"},
		{[]string{"qmax-code", "cc"}, "cc"},
		{[]string{"qmax-code", "codex"}, "codex"},
		{[]string{"qmax-code", "agy"}, "agy"},
		{[]string{"qmax-code", "config"}, "config"},
		{[]string{"qmax-code", "gate"}, "interactive"},
		{[]string{"qmax-code", "receipt"}, "interactive"},
		{[]string{"qmax-code", "-p", "hi"}, "interactive"},
	}
	for _, tc := range cases {
		if got := receiptKind(tc.args); got != tc.want {
			t.Errorf("receiptKind(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestInitReceiptPathsPointsAtQMaxCodeState(t *testing.T) {
	home := withTempHome(t)
	oldBase := receipt.BaseDir
	oldVersion := receipt.AgentVersion
	t.Cleanup(func() {
		receipt.BaseDir = oldBase
		receipt.AgentVersion = oldVersion
	})

	initReceiptPaths()

	if receipt.BaseDir != filepath.Join(home, ".qmax-code") {
		t.Fatalf("BaseDir = %q, want %q", receipt.BaseDir, filepath.Join(home, ".qmax-code"))
	}
	if receipt.AgentVersion != Version {
		t.Fatalf("AgentVersion = %q, want %q", receipt.AgentVersion, Version)
	}
}

func TestBeginSessionReceipt(t *testing.T) {
	useTempReceipts(t)

	r := beginSessionReceipt("mcp")

	if r == nil {
		t.Fatal("beginSessionReceipt returned nil")
	}
	if r.RunKind != "session:mcp" {
		t.Fatalf("RunKind = %q, want session:mcp", r.RunKind)
	}
	if r.RunID == "" {
		t.Fatal("RunID is empty")
	}
	if r.EntryCount() != 0 {
		t.Fatalf("EntryCount = %d, want 0 for a fresh session", r.EntryCount())
	}
}

func TestFinalizeSessionReceiptSkipsNoEgressRuns(t *testing.T) {
	base := useTempReceipts(t)

	// A nil receipt and a zero-entry run must both be no-ops that leave the
	// receipts directory clean (no file, no directory created).
	_, _ = withCapturedOutput(t, func() {
		finalizeSessionReceipt(nil)
		finalizeSessionReceipt(beginSessionReceipt("interactive"))
	})

	if _, err := os.Stat(filepath.Join(base, "receipts")); !os.IsNotExist(err) {
		t.Fatalf("receipts dir created for a no-egress run (stat err = %v)", err)
	}
}

func TestFinalizeSessionReceiptWritesSignedFile(t *testing.T) {
	base := useTempReceipts(t)
	r := beginSessionReceipt("interactive")
	r.Record(receipt.Entry{Method: "POST", Host: "api.anthropic.com", Category: "llm", Allowed: true})

	_, stderr := withCapturedOutput(t, func() { finalizeSessionReceipt(r) })

	wantPath := filepath.Join(base, "receipts", r.RunID+".json")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("receipt file not written: %v", err)
	}
	if !strings.Contains(stderr, "Exposure receipt: "+wantPath) {
		t.Fatalf("stderr = %q, want receipt path announcement", stderr)
	}
}

func TestFinalizeSessionReceiptWarnsWhenWriteFails(t *testing.T) {
	base := useTempReceipts(t)
	// Occupy the receipts directory path with a regular file so Finalize's
	// MkdirAll fails.
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "receipts"), []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := beginSessionReceipt("cc")
	r.Record(receipt.Entry{Method: "POST", Host: "api.anthropic.com", Category: "llm", Allowed: true})

	_, stderr := withCapturedOutput(t, func() { finalizeSessionReceipt(r) })

	if !strings.Contains(stderr, "WARN: failed to write exposure receipt") {
		t.Fatalf("stderr = %q, want write-failure warning", stderr)
	}
}
