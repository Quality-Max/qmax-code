package main

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/color/palette"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/BourgeoisBear/rasterm"
)

// DisplayImage renders an image in the terminal.
// Tries iTerm2 → Kitty → Sixel → half-block fallback.
func DisplayImage(path string, maxWidth int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}

	// Try modern terminal protocols first
	if rasterm.IsItermCapable() {
		return rasterm.ItermWriteImage(os.Stdout, img)
	}
	if rasterm.IsKittyCapable() {
		return rasterm.KittyWriteImage(os.Stdout, img, rasterm.KittyImgOpts{})
	}
	if ok, _ := rasterm.IsSixelCapable(); ok {
		return rasterm.SixelWriteImage(os.Stdout, toPaletted(img))
	}

	// Fallback: half-block character rendering (works everywhere)
	return renderHalfBlock(img, maxWidth)
}

// DisplayImageFromBase64 renders a base64-encoded image.
func DisplayImageFromBase64(data string, maxWidth int) error {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return err
	}
	tmpFile := filepath.Join(os.TempDir(), "qmax-display.png")
	if err := os.WriteFile(tmpFile, raw, 0644); err != nil {
		return err
	}
	defer os.Remove(tmpFile)
	return DisplayImage(tmpFile, maxWidth)
}

// toPaletted converts any image to a paletted image for Sixel rendering.
func toPaletted(img image.Image) *image.Paletted {
	bounds := img.Bounds()
	paletted := image.NewPaletted(bounds, palette.Plan9)
	draw.FloydSteinberg.Draw(paletted, bounds, img, bounds.Min)
	return paletted
}

// renderHalfBlock renders an image using Unicode half-block characters (▀).
// Each character cell = 2 vertical pixels. Works in any terminal.
func renderHalfBlock(img image.Image, maxWidth int) error {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	if maxWidth <= 0 {
		maxWidth = 72
	}

	// Scale to fit
	scale := 1
	if w > maxWidth {
		scale = (w + maxWidth - 1) / maxWidth
	}
	sw := w / scale
	sh := h / scale

	fmt.Println()
	for y := 0; y < sh; y += 2 {
		fmt.Print("  ")
		for x := 0; x < sw; x++ {
			tr, tg, tb, _ := img.At(bounds.Min.X+x*scale, bounds.Min.Y+y*scale).RGBA()
			var br, bg, bb uint32
			if y+1 < sh {
				br, bg, bb, _ = img.At(bounds.Min.X+x*scale, bounds.Min.Y+(y+1)*scale).RGBA()
			} else {
				br, bg, bb = tr, tg, tb
			}
			fmt.Printf("\033[38;2;%d;%d;%dm\033[48;2;%d;%d;%dm▀\033[0m",
				tr>>8, tg>>8, tb>>8, br>>8, bg>>8, bb>>8)
		}
		fmt.Println()
	}
	fmt.Println()
	return nil
}

// RenderScreenshotCompact shows a framed screenshot label.
func RenderScreenshotCompact(label string, url string) {
	border := strings.Repeat("─", 44)
	fmt.Printf("  ┌%s┐\n", border)
	fmt.Printf("  │ 📸 %-42s│\n", truncateStr(label, 42))
	if url != "" {
		short := truncateStr(url, 42)
		fmt.Printf("  │ %-44s│\n", short)
	}
	fmt.Printf("  └%s┘\n", border)
}
