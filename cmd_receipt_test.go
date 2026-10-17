package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	receipt "github.com/Quality-Max/qmax-receipt"
)

// useTempReceipts points the receipt store at a temp HOME and returns the
// BaseDir it installed. receipt.BaseDir is process-global, so it is restored
// when the test ends.
func useTempReceipts(t *testing.T) string {
	t.Helper()
	home := withTempHome(t)
	oldBase := receipt.BaseDir
	t.Cleanup(func() { receipt.BaseDir = oldBase })
	initReceiptPaths()
	base := filepath.Join(home, ".qmax-code")
	if receipt.BaseDir != base {
		t.Fatalf("initReceiptPaths: BaseDir = %q, want %q", receipt.BaseDir, base)
	}
	return base
}

// finalizeFixtureReceipt creates a signed receipt with one entry per host and
// writes it to the store, returning the in-memory receipt.
func finalizeFixtureReceipt(t *testing.T, kind string, hosts ...string) *receipt.Receipt {
	t.Helper()
	r := receipt.NewCurrent(kind)
	for _, host := range hosts {
		r.Record(receipt.Entry{
			Method:   "POST",
			Host:     host,
			Path:     "/v1/chat",
			Category: "llm",
			ReqBytes: 128,
			Allowed:  true,
		})
	}
	if _, err := r.Finalize(); err != nil {
		t.Fatalf("fixture Finalize: %v", err)
	}
	return r
}

func TestArgAt(t *testing.T) {
	if got := argAt([]string{"list", "show"}, 0); got != "list" {
		t.Errorf("argAt(..., 0) = %q, want list", got)
	}
	if got := argAt([]string{"list", "show"}, 1); got != "show" {
		t.Errorf("argAt(..., 1) = %q, want show", got)
	}
	if got := argAt([]string{"list"}, 1); got != "" {
		t.Errorf("argAt(..., 1) out of range = %q, want empty", got)
	}
	if got := argAt(nil, 0); got != "" {
		t.Errorf("argAt(nil, 0) = %q, want empty", got)
	}
}

func TestReceiptListEmpty(t *testing.T) {
	useTempReceipts(t)

	out, _ := withCapturedOutput(t, receiptList)

	if !strings.Contains(out, "No exposure receipts found.") {
		t.Fatalf("receiptList output = %q, want empty-store message", out)
	}
}

func TestReceiptListShowsRuns(t *testing.T) {
	useTempReceipts(t)
	r := finalizeFixtureReceipt(t, "session:interactive", "api.anthropic.com")

	out, _ := withCapturedOutput(t, receiptList)

	if !strings.Contains(out, "RUN ID") || !strings.Contains(out, "KIND") {
		t.Fatalf("receiptList missing header:\n%s", out)
	}
	if !strings.Contains(out, r.RunID) {
		t.Fatalf("receiptList missing run %s:\n%s", r.RunID, out)
	}
	if !strings.Contains(out, "session:interactive") {
		t.Fatalf("receiptList missing kind:\n%s", out)
	}
	if !strings.Contains(out, "api.anthropic.com") {
		t.Fatalf("receiptList missing destination:\n%s", out)
	}
}

func TestReceiptListSkipsUnreadableFiles(t *testing.T) {
	base := useTempReceipts(t)
	r := finalizeFixtureReceipt(t, "session:mcp", "api.cerebras.ai")
	dir := filepath.Join(base, "receipts")
	if err := os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _ := withCapturedOutput(t, receiptList)

	if !strings.Contains(out, r.RunID) {
		t.Fatalf("receiptList should still list the valid receipt:\n%s", out)
	}
	if strings.Contains(out, "corrupt") {
		t.Fatalf("receiptList should silently skip unreadable files:\n%s", out)
	}
}

func TestReceiptListReportsStoreErrors(t *testing.T) {
	useTempReceipts(t)
	receipt.BaseDir = "" // forces receipt.List to fail

	var codes []int
	_, stderr := withCapturedOutput(t, func() {
		codes = runWithStubbedExit(t, receiptList)
	})

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("exit codes = %v, want [1]", codes)
	}
	if !strings.Contains(stderr, "Error:") {
		t.Fatalf("stderr = %q, want error message", stderr)
	}
}

func TestReceiptShowLatest(t *testing.T) {
	useTempReceipts(t)
	r := finalizeFixtureReceipt(t, "session:interactive", "api.anthropic.com")

	out, _ := withCapturedOutput(t, func() { receiptShow("") })

	if !strings.Contains(out, `"run_id": "`+r.RunID+`"`) {
		t.Fatalf("receiptShow(\"\") output missing run_id:\n%s", out)
	}
	if !strings.Contains(out, `"run_kind": "session:interactive"`) {
		t.Fatalf("receiptShow(\"\") output missing run_kind:\n%s", out)
	}
}

func TestReceiptShowLatestKeywordAndID(t *testing.T) {
	useTempReceipts(t)
	r := finalizeFixtureReceipt(t, "session:cc", "api.anthropic.com")

	for _, id := range []string{"latest", r.RunID} {
		out, _ := withCapturedOutput(t, func() { receiptShow(id) })
		if !strings.Contains(out, r.RunID) {
			t.Fatalf("receiptShow(%q) output missing run id:\n%s", id, out)
		}
	}
}

func TestReceiptShowUnknownIDExits(t *testing.T) {
	useTempReceipts(t)
	finalizeFixtureReceipt(t, "session:interactive", "api.anthropic.com")

	var codes []int
	_, stderr := withCapturedOutput(t, func() {
		codes = runWithStubbedExit(t, func() { receiptShow("deadbeef") })
	})

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("exit codes = %v, want [1]", codes)
	}
	if !strings.Contains(stderr, "Error:") {
		t.Fatalf("stderr = %q, want error message", stderr)
	}
}

func TestReceiptShowNoReceiptsExits(t *testing.T) {
	useTempReceipts(t)

	var codes []int
	_, stderr := withCapturedOutput(t, func() {
		codes = runWithStubbedExit(t, func() { receiptShow("latest") })
	})

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("exit codes = %v, want [1]", codes)
	}
	if !strings.Contains(stderr, "No exposure receipts found.") {
		t.Fatalf("stderr = %q, want no-receipts message", stderr)
	}
}

func TestReceiptVerifyValid(t *testing.T) {
	useTempReceipts(t)
	r := finalizeFixtureReceipt(t, "session:codex", "api.openai.com", "api.anthropic.com")

	out, _ := withCapturedOutput(t, func() { receiptVerify(r.RunID) })

	if !strings.Contains(out, "✓ VALID") {
		t.Fatalf("receiptVerify output = %q, want VALID marker", out)
	}
	if !strings.Contains(out, r.RunID) {
		t.Fatalf("receiptVerify output missing run id:\n%s", out)
	}
	if !strings.Contains(out, "2 request(s)") {
		t.Fatalf("receiptVerify output missing request count:\n%s", out)
	}
	if !strings.Contains(out, "provenance") {
		t.Fatalf("receiptVerify output missing provenance caveat:\n%s", out)
	}
}

func TestReceiptVerifyTamperedReceiptExits(t *testing.T) {
	base := useTempReceipts(t)
	r := finalizeFixtureReceipt(t, "session:interactive", "api.anthropic.com")

	// Rewrite the stored receipt with a different run_id (renaming the file to
	// match) but keep the original signature, so verification must fail.
	path := filepath.Join(base, "receipts", r.RunID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fakeID := "0123456789abcdef0123456789abcdef"
	tampered := []byte(strings.ReplaceAll(string(data), r.RunID, fakeID))
	if err := os.WriteFile(filepath.Join(base, "receipts", fakeID+".json"), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	var codes []int
	_, stderr := withCapturedOutput(t, func() {
		codes = runWithStubbedExit(t, func() { receiptVerify(fakeID) })
	})

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("exit codes = %v, want [1]", codes)
	}
	if !strings.Contains(stderr, "✗ INVALID") {
		t.Fatalf("stderr = %q, want INVALID marker", stderr)
	}
}

func TestMustResolveReceiptBadStoreExits(t *testing.T) {
	useTempReceipts(t)
	receipt.BaseDir = "" // forces receipt.ReceiptsDir to fail

	var codes []int
	_, stderr := withCapturedOutput(t, func() {
		codes = runWithStubbedExit(t, func() { mustResolveReceipt("whatever") })
	})

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("exit codes = %v, want [1]", codes)
	}
	if !strings.Contains(stderr, "Error:") {
		t.Fatalf("stderr = %q, want error message", stderr)
	}
}

func TestHandleReceiptCommandDispatch(t *testing.T) {
	useTempReceipts(t)
	r := finalizeFixtureReceipt(t, "session:interactive", "api.anthropic.com")

	t.Run("list is the default", func(t *testing.T) {
		out, _ := withCapturedOutput(t, func() { handleReceiptCommand(nil) })
		if !strings.Contains(out, r.RunID) {
			t.Fatalf("default subcommand should list receipts:\n%s", out)
		}
	})

	t.Run("show explicit", func(t *testing.T) {
		out, _ := withCapturedOutput(t, func() { handleReceiptCommand([]string{"show", r.RunID}) })
		if !strings.Contains(out, `"run_id": "`+r.RunID+`"`) {
			t.Fatalf("show output missing run_id:\n%s", out)
		}
	})

	t.Run("verify explicit", func(t *testing.T) {
		out, _ := withCapturedOutput(t, func() { handleReceiptCommand([]string{"verify", r.RunID}) })
		if !strings.Contains(out, "✓ VALID") {
			t.Fatalf("verify output = %q, want VALID marker", out)
		}
	})
}

func TestHandleReceiptCommandUnknownSubcommand(t *testing.T) {
	useTempReceipts(t)

	var codes []int
	_, stderr := withCapturedOutput(t, func() {
		codes = runWithStubbedExit(t, func() { handleReceiptCommand([]string{"bogus"}) })
	})

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("exit codes = %v, want [1]", codes)
	}
	if !strings.Contains(stderr, "Usage: qmax-code receipt <list|show|verify>") {
		t.Fatalf("stderr = %q, want usage message", stderr)
	}
}

func TestHandleReceiptCommandVerifyNoReceiptsExits(t *testing.T) {
	useTempReceipts(t)

	var codes []int
	_, stderr := withCapturedOutput(t, func() {
		codes = runWithStubbedExit(t, func() { handleReceiptCommand([]string{"verify"}) })
	})

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("exit codes = %v, want [1]", codes)
	}
	if !strings.Contains(stderr, "No exposure receipts found.") {
		t.Fatalf("stderr = %q, want no-receipts message", stderr)
	}
}
