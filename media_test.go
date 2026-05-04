package main

import (
	"os"
	"strings"
	"testing"
)

func TestLargePastedTextDetectionRequiresPaste(t *testing.T) {
	large := strings.Repeat("x", largePastedTextThreshold)
	if !isLargePastedText(large, true) {
		t.Fatal("large bracketed paste should be treated as pasted_file")
	}
	if isLargePastedText(large, false) {
		t.Fatal("large typed input should not be treated as pasted_file")
	}
	if isLargePastedText("small paste", true) {
		t.Fatal("small paste should remain inline")
	}
}

func TestSavePastedTextFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := savePastedTextFile("sensitive pasted body")
	if err != nil {
		t.Fatalf("savePastedTextFile: %v", err)
	}
	if !strings.Contains(path, "pasted_file_") {
		t.Fatalf("path %q does not use pasted_file naming", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pasted file: %v", err)
	}
	if string(data) != "sensitive pasted body" {
		t.Fatalf("file content = %q", string(data))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat pasted file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("file mode = %o, want 0600", got)
	}
}
