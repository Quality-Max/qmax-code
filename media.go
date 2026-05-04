package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const largePastedTextThreshold = 8 * 1024

// imageExtensions maps file extensions to MIME types.
var imageExtensions = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// IsImagePath returns true if the path looks like an image file.
func IsImagePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := imageExtensions[ext]
	return ok
}

// LoadImageFromFile reads an image file and returns an ImageAttachment.
func LoadImageFromFile(path string) (*ImageAttachment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read image: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	mediaType, ok := imageExtensions[ext]
	if !ok {
		return nil, fmt.Errorf("unsupported image format: %s", ext)
	}

	// Limit to 20MB (Claude API limit is ~20MB for images)
	if len(data) > 20*1024*1024 {
		return nil, fmt.Errorf("image too large (%d MB, max 20MB)", len(data)/(1024*1024))
	}

	return &ImageAttachment{
		MediaType: mediaType,
		Data:      base64.StdEncoding.EncodeToString(data),
		FileName:  filepath.Base(path),
	}, nil
}

// CaptureScreenshot takes a screenshot using macOS screencapture and returns it as an attachment.
func CaptureScreenshot() (*ImageAttachment, error) {
	tmpFile := filepath.Join(os.TempDir(), "qmax-screenshot.png")
	defer os.Remove(tmpFile)

	// Interactive screenshot selection on macOS
	cmd := exec.Command("screencapture", "-i", tmpFile)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("screenshot failed: %w", err)
	}

	// Check if file was created (user might have cancelled)
	if _, err := os.Stat(tmpFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("screenshot cancelled")
	}

	return LoadImageFromFile(tmpFile)
}

// PasteImageFromClipboard checks if the clipboard has an image and returns it.
func PasteImageFromClipboard() (*ImageAttachment, error) {
	tmpFile := filepath.Join(os.TempDir(), "qmax-clipboard.png")
	defer os.Remove(tmpFile)

	// Try pngpaste first (brew install pngpaste)
	cmd := exec.Command("pngpaste", tmpFile)
	if err := cmd.Run(); err != nil {
		// Fallback: use osascript to check clipboard
		return nil, fmt.Errorf("no image in clipboard (install pngpaste for image paste support)")
	}

	if _, err := os.Stat(tmpFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("no image in clipboard")
	}

	return LoadImageFromFile(tmpFile)
}

// PasteTextFromClipboard returns the text content of the clipboard.
func PasteTextFromClipboard() (string, error) {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return "", fmt.Errorf("clipboard read failed: %w", err)
	}
	return string(out), nil
}

func isLargePastedText(text string, pasted bool) bool {
	return pasted && len(text) >= largePastedTextThreshold
}

func savePastedTextFile(text string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, qmaxCodeConfigDir, "pastes")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("pasted_file_%s.txt", time.Now().UTC().Format("20060102T150405.000000000Z"))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		return "", err
	}
	return path, nil
}

func pastedFilePrompt(path string, size int) string {
	return fmt.Sprintf("A large text paste was saved as pasted_file. Read and use this local file: %s (%d bytes).", path, size)
}

// DetectAndLoadImages checks if the input contains file paths to images.
// Returns the cleaned text (without paths) and any loaded images.
func DetectAndLoadImages(input string) (string, []ImageAttachment) {
	words := strings.Fields(input)
	var cleanWords []string
	var images []ImageAttachment

	for _, word := range words {
		// Strip quotes that terminals add when dragging files
		clean := strings.Trim(word, "'\"")
		if IsImagePath(clean) {
			if img, err := LoadImageFromFile(clean); err == nil {
				images = append(images, *img)
				continue
			}
		}
		cleanWords = append(cleanWords, word)
	}

	cleanText := strings.Join(cleanWords, " ")
	return cleanText, images
}
