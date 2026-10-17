package main

import (
	"os"
	"path/filepath"
	"testing"
)

// exitSentinel is the panic value used by runWithStubbedExit to stop execution
// at the point where exitWithReceipt would terminate the process.
type exitSentinel struct{ code int }

// runWithStubbedExit runs fn with processExit replaced by a recorder. The stub
// panics with exitSentinel so code after the exit site does not run — mirroring
// the real os.Exit semantics — and the panic is recovered here. It returns the
// recorded exit codes (empty when fn returned without exiting).
func runWithStubbedExit(t *testing.T, fn func()) (codes []int) {
	t.Helper()

	exitMu.Lock()
	oldFinalize := finalizeBeforeExit
	oldExit := processExit
	finalizeBeforeExit = nil
	processExit = func(code int) {
		codes = append(codes, code)
		panic(exitSentinel{code: code})
	}
	exitMu.Unlock()
	t.Cleanup(func() {
		exitMu.Lock()
		processExit = oldExit
		finalizeBeforeExit = oldFinalize
		exitMu.Unlock()
	})

	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(exitSentinel); !ok {
				panic(r)
			}
		}
	}()
	fn()
	return codes
}

// captureOutput captures synchronous command output in files so assertions see
// every write without racing a pipe-draining goroutine. Callers must not run in
// parallel because stdout and stderr are process globals.
func captureOutput(t *testing.T) (stdout, stderr func() string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	capture := func(name string) (*os.File, func() string) {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return f, func() string {
			t.Helper()
			data, err := os.ReadFile(f.Name())
			if err != nil {
				t.Fatal(err)
			}
			return string(data)
		}
	}
	os.Stdout, stdout = capture("stdout")
	os.Stderr, stderr = capture("stderr")
	t.Cleanup(func() { os.Stdout, os.Stderr = oldOut, oldErr })
	return stdout, stderr
}

func withCapturedOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	out, errOut := captureOutput(t)
	fn()
	return out(), errOut()
}

// prependFakeBinaries writes tiny no-op executable scripts for each name into
// a fresh temp dir and prepends that dir to PATH, so exec.LookPath finds them.
// It returns the temp dir. Deterministic and cleaned up with the test.
func prependFakeBinaries(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	orig := os.Getenv("PATH")
	os.Setenv("PATH", dir+string(os.PathListSeparator)+orig)
	t.Cleanup(func() { os.Setenv("PATH", orig) })
	return dir
}

// isolatePathWithFailingSecurity replaces PATH with a temp dir whose only
// binary is a `security` stub that always fails. On darwin this makes macOS
// keychain lookups (used by loadClaudeCodeFromKeychain and api.LoadFromKeychain)
// miss deterministically instead of depending on the developer's real keychain;
// on other platforms `security` is never consulted anyway. Any other binary
// lookup through PATH fails, which also isolates the test from the machine.
func isolatePathWithFailingSecurity(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "security"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write security stub: %v", err)
	}
	orig := os.Getenv("PATH")
	os.Setenv("PATH", dir)
	t.Cleanup(func() { os.Setenv("PATH", orig) })
}

// setenv sets an environment variable for the duration of the test.
func setenv(t *testing.T, key, value string) {
	t.Helper()
	orig, had := os.LookupEnv(key)
	os.Setenv(key, value)
	t.Cleanup(func() {
		if had {
			os.Setenv(key, orig)
		} else {
			os.Unsetenv(key)
		}
	})
}
