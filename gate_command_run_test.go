package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initMinimalGitRepo creates a tiny committed Go module inside a temp dir:
// one passing test, one commit. The gate running against `--base HEAD` sees an
// empty diff and a passing `go test ./...` / `go vet ./...`, so its verdict is
// PASS without touching the real repository.
func initMinimalGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("go.mod", "module gatetest\n\ngo 1.25\n")
	write("gate_test.go", "package gatetest\n\nimport \"testing\"\n\nfunc TestNoop(t *testing.T) {}\n")

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Ignore the developer's global/system git config so identity,
		// signing, and hooks settings cannot affect the fixture.
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_CONFIG_COUNT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", "-A")
	git("-c", "user.name=Gate Test", "-c", "user.email=gate@test.example",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	return dir
}

func TestRunGateCommandPassesOnCleanMinimalRepo(t *testing.T) {
	t.Chdir(initMinimalGitRepo(t))

	var stdout, stderr bytes.Buffer
	code := runGateCommand(context.Background(), []string{"--base", "HEAD"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (PASS)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Base: HEAD") {
		t.Errorf("stdout missing base line:\n%s", out)
	}
	if !strings.Contains(out, "Verdict: PASS") {
		t.Errorf("stdout missing PASS verdict:\n%s", out)
	}
	if !strings.Contains(out, "go test ./...") || !strings.Contains(out, "go vet ./...") {
		t.Errorf("stdout missing go checks:\n%s", out)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunGateCommandIncompleteOutsideARepo(t *testing.T) {
	t.Chdir(t.TempDir()) // empty dir: no git repo, no go.mod

	var stdout, stderr bytes.Buffer
	code := runGateCommand(context.Background(), nil, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (INCOMPLETE)", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "Verdict: INCOMPLETE") {
		t.Errorf("stdout missing INCOMPLETE verdict:\n%s", out)
	}
	if !strings.Contains(out, "cannot resolve base") {
		t.Errorf("stdout missing explanation:\n%s", out)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}
