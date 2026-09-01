package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunGateCommandHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runGateCommand(context.Background(), []string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage: qmax-code gate") {
		t.Fatalf("stdout = %q, want gate usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunGateCommandUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runGateCommand(context.Background(), []string{"unexpected"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unexpected gate argument") {
		t.Fatalf("stderr = %q, want actionable usage error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
