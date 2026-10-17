package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
)

func TestHandleCodexCommandUsage(t *testing.T) {
	cases := [][]string{
		nil,
		{},
		{"login"},
		{"connect", "extra"},
	}
	for _, args := range cases {
		err := handleCodexCommand(args)
		if err == nil || !strings.Contains(err.Error(), "usage: qmax-code codex connect") {
			t.Errorf("handleCodexCommand(%v) = %v, want usage error", args, err)
		}
	}
}

func TestHandleCodexCommandConnectRequiresCodexBinary(t *testing.T) {
	oldFind := findCodexForConnect
	findCodexForConnect = func() string { return "" }
	t.Cleanup(func() { findCodexForConnect = oldFind })

	err := handleCodexCommand([]string{"connect"})
	if err == nil || !strings.Contains(err.Error(), "Codex CLI not found") {
		t.Fatalf("handleCodexCommand error = %v, want binary-not-found", err)
	}
}

func TestHandleCodexCommandConnectUploadsSession(t *testing.T) {
	oldFind := findCodexForConnect
	oldRun := runCodexLogin
	oldLoad := loadQualityMaxAuth
	oldLogin := loginQualityMax
	oldUpload := uploadCodexAuth
	t.Cleanup(func() {
		findCodexForConnect = oldFind
		runCodexLogin = oldRun
		loadQualityMaxAuth = oldLoad
		loginQualityMax = oldLogin
		uploadCodexAuth = oldUpload
	})

	home := withTempHome(t)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	authJSON := `{"tokens":{"access_token":"access","refresh_token":"refresh"}}`
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	loginRan := false
	uploadRan := false
	findCodexForConnect = func() string { return "/usr/local/bin/codex" }
	runCodexLogin = func(string) error { loginRan = true; return nil }
	loadQualityMaxAuth = func() *api.AuthConfig { return &api.AuthConfig{APIKey: "qm-user"} }
	loginQualityMax = func() (*api.AuthConfig, error) {
		t.Fatal("existing QualityMax auth should be reused")
		return nil, nil
	}
	uploadCodexAuth = func(ctx context.Context, auth *api.AuthConfig, gotJSON string) (*api.CodexConnection, error) {
		uploadRan = true
		if gotJSON != authJSON {
			t.Fatalf("uploaded auth JSON = %q, want file content", gotJSON)
		}
		return &api.CodexConnection{Connected: true, AccountLabel: "user@example.com"}, nil
	}
	stdout, _ := captureOutput(t)

	if err := handleCodexCommand([]string{"connect"}); err != nil {
		t.Fatalf("handleCodexCommand(connect): %v", err)
	}
	if !loginRan || !uploadRan {
		t.Fatalf("loginRan=%v uploadRan=%v, want both", loginRan, uploadRan)
	}
	if out := stdout(); !strings.Contains(out, "connected to QualityMax as user@example.com") {
		t.Fatalf("stdout = %q, want account-label confirmation", out)
	}
}

func writeCodexAuth(t *testing.T, content string) {
	t.Helper()
	home := withTempHome(t)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadCodexAuthFileMissingFile(t *testing.T) {
	withTempHome(t)

	if _, err := readCodexAuthFile(); err == nil {
		t.Fatal("expected error when ~/.codex/auth.json does not exist")
	}
}

func TestReadCodexAuthFileInvalidJSON(t *testing.T) {
	writeCodexAuth(t, "{not json")

	_, err := readCodexAuthFile()
	if err == nil || !strings.Contains(err.Error(), "is not valid JSON") {
		t.Fatalf("readCodexAuthFile error = %v, want JSON parse error", err)
	}
}

func TestReadCodexAuthFileMissingRefreshToken(t *testing.T) {
	writeCodexAuth(t, `{"tokens":{"access_token":"access"}}`)

	_, err := readCodexAuthFile()
	if err == nil || !strings.Contains(err.Error(), "refresh_token") {
		t.Fatalf("readCodexAuthFile error = %v, want missing refresh_token error", err)
	}
}

func TestReadCodexAuthFileMissingAccessToken(t *testing.T) {
	writeCodexAuth(t, `{"tokens":{"refresh_token":"refresh"}}`)

	_, err := readCodexAuthFile()
	if err == nil || !strings.Contains(err.Error(), "access_token") {
		t.Fatalf("readCodexAuthFile error = %v, want missing access_token error", err)
	}
}

func TestReadCodexAuthFileRejectsOversizedFile(t *testing.T) {
	writeCodexAuth(t, strings.Repeat("x", maxCodexAuthBytes+1))

	_, err := readCodexAuthFile()
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("readCodexAuthFile error = %v, want size-limit error", err)
	}
}
