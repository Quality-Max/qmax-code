package repl

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	_ = w.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy stdout: %v", err)
	}
	return buf.String()
}

func TestMaybePrintSDKCreditBannerOncePerDay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	origCutover := sdkCreditCutover
	sdkCreditCutover = time.Now().Add(24 * time.Hour)
	t.Cleanup(func() { sdkCreditCutover = origCutover })

	first := captureStdout(t, maybePrintSDKCreditBanner)
	if !strings.Contains(first, "starting 2026-06-15") || !strings.Contains(first, "Agent SDK credit") {
		t.Fatalf("first banner missing expected disclosure: %q", first)
	}

	second := captureStdout(t, maybePrintSDKCreditBanner)
	if second != "" {
		t.Fatalf("second banner should be suppressed for same day, got %q", second)
	}

	markerPath := filepath.Join(os.Getenv("HOME"), ".qmax-code", "sdk_credit_banner_seen")
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("marker file was not written: %v", err)
	}
}

func TestMaybePrintSDKCreditBannerPostCutoverCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	origCutover := sdkCreditCutover
	sdkCreditCutover = time.Now().Add(-24 * time.Hour)
	t.Cleanup(func() { sdkCreditCutover = origCutover })

	out := captureStdout(t, maybePrintSDKCreditBanner)
	if !strings.Contains(out, "This session uses your Claude Agent SDK monthly credit") {
		t.Fatalf("post-cutover banner missing expected disclosure: %q", out)
	}
}
