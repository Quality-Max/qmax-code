package tui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
)

func writePNG(t *testing.T, path string, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 8), uint8(y * 8), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if path != "" {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// plainTerminalEnv makes rasterm fall back to half-block rendering.
func plainTerminalEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TERM_PROGRAM", "LC_TERMINAL", "KITTY_WINDOW_ID", "TERMINAL_EMULATOR", "KONSOLE_VERSION"} {
		t.Setenv(k, "")
	}
	t.Setenv("TERM", "dumb")
}

func TestDisplayImageHalfBlocks(t *testing.T) {
	plainTerminalEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	data := writePNG(t, path, 30, 9)

	out := captureTermOutput(t, func() {
		if err := DisplayImage(path, 10); err != nil {
			t.Errorf("DisplayImage: %v", err)
		}
		if err := DisplayImageFromBase64(base64.StdEncoding.EncodeToString(data), 0); err != nil {
			t.Errorf("DisplayImageFromBase64: %v", err)
		}
	})
	if !strings.Contains(out, "▀") {
		t.Fatalf("no half-block output: %q", out)
	}
	if err := DisplayImage(filepath.Join(dir, "missing.png"), 10); err == nil {
		t.Fatal("missing file should error")
	}
	notImage := filepath.Join(dir, "x.png")
	_ = os.WriteFile(notImage, []byte("nope"), 0o644)
	if err := DisplayImage(notImage, 10); err == nil {
		t.Fatal("non-image should error")
	}
	if err := DisplayImageFromBase64("%%%", 10); err == nil {
		t.Fatal("bad base64 should error")
	}
	if p := toPaletted(image.NewRGBA(image.Rect(0, 0, 2, 2))); p.Bounds().Dx() != 2 {
		t.Fatal("toPaletted bounds")
	}
	out = captureTermOutput(t, func() { RenderScreenshotCompact("Final state", "https://cdn.example/"+strings.Repeat("p", 60)) })
	if !strings.Contains(out, "📸 Final state") {
		t.Fatalf("compact screenshot = %q", out)
	}
}

func TestMediaHelpersWithStubbedTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX stubs")
	}
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "fixture.png")
	writePNG(t, pngPath, 4, 4)
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("screencapture", `cp "`+pngPath+`" "$2"`)
	stub("pngpaste", `cp "`+pngPath+`" "$1"`)
	stub("pbpaste", `printf 'clipboard text'`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")

	if img, err := CaptureScreenshot(); err != nil || img.MediaType != "image/png" {
		t.Fatalf("CaptureScreenshot = %+v, %v", img, err)
	}
	if img, err := PasteImageFromClipboard(); err != nil || img.FileName == "" {
		t.Fatalf("PasteImageFromClipboard = %+v, %v", img, err)
	}
	if text, err := PasteTextFromClipboard(); err != nil || text != "clipboard text" {
		t.Fatalf("PasteTextFromClipboard = %q, %v", text, err)
	}

	stub("screencapture", `exit 0`)
	if _, err := CaptureScreenshot(); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancelled screenshot err = %v", err)
	}
	stub("screencapture", `exit 1`)
	if _, err := CaptureScreenshot(); err == nil {
		t.Fatal("failing screencapture should error")
	}
	stub("pngpaste", `exit 0`)
	if _, err := PasteImageFromClipboard(); err == nil {
		t.Fatal("empty clipboard should error")
	}
	stub("pngpaste", `exit 1`)
	if _, err := PasteImageFromClipboard(); err == nil {
		t.Fatal("pngpaste failure should error")
	}
	stub("pbpaste", `exit 1`)
	if _, err := PasteTextFromClipboard(); err == nil {
		t.Fatal("pbpaste failure should error")
	}

	text, imgs := DetectAndLoadImages(`look at '` + pngPath + `' and ` + filepath.Join(dir, "missing.png"))
	if len(imgs) != 1 || !strings.Contains(text, "missing.png") || strings.Contains(text, "fixture.png") {
		t.Fatalf("DetectAndLoadImages = %q, %d images", text, len(imgs))
	}
	if _, err := LoadImageFromFile(filepath.Join(dir, "nope.gif")); err == nil {
		t.Fatal("missing file should error")
	}
}

func TestSaveThemeAndProgress(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := SaveTheme(nil, "x"); err == nil {
		t.Fatal("nil config should error")
	}
	cfg := api.DefaultConfig()
	if err := SaveTheme(cfg, "not-a-theme"); err == nil {
		t.Fatal("invalid theme should error")
	}
	if err := SaveTheme(cfg, ThemeNames()[0]); err != nil || cfg.Theme != ThemeNames()[0] {
		t.Fatalf("SaveTheme = %v (%q)", err, cfg.Theme)
	}
	if err := SaveTheme(cfg, ""); err != nil || cfg.Theme != "" {
		t.Fatalf("clear theme = %v", err)
	}
	t.Setenv("HOME", "/dev/null/nope")
	if err := SaveTheme(cfg, ThemeNames()[0]); err == nil || cfg.Theme != "" {
		t.Fatalf("unsavable config should roll back: %v %q", err, cfg.Theme)
	}

	out := captureTermOutput(t, func() {
		p := NewProgressBar("Running", 0)
		p.Update(40, "")
		p.Update(150, "almost there")
		p.Finish(false, "failed")
		p.Update(10, "ignored after finish")
		NewProgressBar("ok", 10).Finish(true, "done")
		ShowBrowserAnimation(0)
		ShowBrowserAnimation(3)
		ClearBrowserAnimation()
		AnimateMax(MoodHappy, "hi")
		AnimateMaxTransition(MoodNeutral, MoodHappy, "connected")
	})
	expectAll(t, out, "Running", "almost there", "failed", "done", "connected")
	for _, s := range []string{GetMaxGreeting(), GetMaxThought(), GetMaxSuccess(), GetMaxError(), GetMaxArt(MoodSad, "")} {
		if s == "" {
			t.Fatal("empty Max art")
		}
	}
}
