package sysutil

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLoggerWritesConcurrentStructuredEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	l := NewLogger("fixture")
	defer l.Close()
	if trace, err := hex.DecodeString(l.TraceID()); err != nil || len(trace) != 8 {
		t.Fatal("trace ID must contain eight random bytes")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); l.Info("test", "entry", map[string]interface{}{"index": i}) }(i)
	}
	wg.Wait()
	l.Error("test", "failure")
	l.Info("test", "complete")
	f, err := os.Open(filepath.Join(home, ".qmax-code", "logs", "session-fixture.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	seen := make(map[int]bool)
	count := 0
	for scanner.Scan() {
		var entry map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("concurrent log writes produced invalid JSON: %v", err)
		}
		if _, err := time.Parse(time.RFC3339Nano, entry["ts"].(string)); err != nil || entry["trace_id"] != l.TraceID() || entry["component"] != "test" {
			t.Fatal("structured log metadata missing")
		}
		if n, ok := entry["index"].(float64); ok {
			seen[int(n)] = true
		}
		if entry["msg"] == "failure" && entry["level"] != "error" {
			t.Fatal("Error did not set error level")
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 22 || len(seen) != 20 {
		t.Fatalf("log contains %d entries and %d unique concurrent entries", count, len(seen))
	}
}

func TestLoggerDegradesWhenHomeUnavailable(t *testing.T) {
	for _, home := range []string{"", filepath.Join(t.TempDir(), "blocked")} {
		t.Run(home, func(t *testing.T) {
			if home != "" {
				if err := os.WriteFile(home, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("HOME", home)
			l := NewLogger("fixture")
			l.Info("test", "discard")
			l.Error("test", "discard")
			l.Close()
			if l.file != nil || l.TraceID() == "" {
				t.Fatal("unavailable log directory must produce a usable no-op logger")
			}
		})
	}
}

func TestMaskURLHidesPasswordAndPreservesAddress(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"https://alice:fixture-password@example.test:8443/path?mode=1#top", "https://alice:%2A%2A%2A%2A@example.test:8443/path?mode=1#top"},
		{"https://example.test/path", "https://example.test/path"},
		{"http://%invalid", "http://%invalid"},
	} {
		if got := MaskURL(tc.input); got != tc.want {
			t.Fatalf("masked URL = %q, want %q", got, tc.want)
		}
	}
}
