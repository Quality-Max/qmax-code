package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
)

// stubCCSeams swaps the three connectClaudeCode seams and restores them at
// test end. Returns the login stub so a test can record calls.
func stubCCSeams(t *testing.T, load func() *api.AuthConfig, login func() (*api.AuthConfig, error), upload func(context.Context, *api.AuthConfig, string) (*api.ClaudeCodeConnection, error)) {
	t.Helper()
	oldLoad := loadQualityMaxAuthForCC
	oldLogin := loginQualityMaxForCC
	oldUpload := uploadClaudeCodeAuth
	loadQualityMaxAuthForCC = load
	loginQualityMaxForCC = login
	uploadClaudeCodeAuth = upload
	t.Cleanup(func() {
		loadQualityMaxAuthForCC = oldLoad
		loginQualityMaxForCC = oldLogin
		uploadClaudeCodeAuth = oldUpload
	})
}

// writeClaudeCredentials writes a credentials file under the temp HOME so
// readClaudeCodeAuth's file fallback finds it. Combine with
// isolatePathWithFailingSecurity to keep the macOS keychain out of the picture.
func writeClaudeCredentials(t *testing.T, content string) {
	t.Helper()
	home := withTempHome(t)
	dir := filepath.Join(home, ".config", "claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHandleCCCommandUsage(t *testing.T) {
	cases := [][]string{
		nil,
		{},
		{"login"},
		{"connect", "extra"},
	}
	for _, args := range cases {
		err := handleCCCommand(args)
		if err == nil || !strings.Contains(err.Error(), "usage: qmax-code cc connect") {
			t.Errorf("handleCCCommand(%v) = %v, want usage error", args, err)
		}
	}
}

func TestHandleCCCommandConnectUploadsSession(t *testing.T) {
	isolatePathWithFailingSecurity(t)
	authJSON := `{"tokens":{"access_token":"access"}}`
	writeClaudeCredentials(t, authJSON)

	uploadRan := false
	stubCCSeams(t,
		func() *api.AuthConfig { return &api.AuthConfig{APIKey: "qm-user"} },
		func() (*api.AuthConfig, error) {
			t.Fatal("existing QualityMax auth should be reused")
			return nil, nil
		},
		func(ctx context.Context, auth *api.AuthConfig, gotJSON string) (*api.ClaudeCodeConnection, error) {
			uploadRan = true
			if gotJSON != authJSON {
				t.Fatalf("uploaded auth JSON = %q, want file content", gotJSON)
			}
			return &api.ClaudeCodeConnection{Connected: true, AccountLabel: "user@example.com"}, nil
		},
	)
	stdout, _ := captureOutput(t)

	if err := handleCCCommand([]string{"connect"}); err != nil {
		t.Fatalf("handleCCCommand(connect): %v", err)
	}
	if !uploadRan {
		t.Fatal("upload was never invoked")
	}
	if out := stdout(); !strings.Contains(out, "connected to QualityMax as user@example.com") {
		t.Fatalf("stdout = %q, want account-label confirmation", out)
	}
}

func TestConnectClaudeCodeRejectsNonJSONCredentials(t *testing.T) {
	isolatePathWithFailingSecurity(t)
	writeClaudeCredentials(t, "definitely not json")

	stubCCSeams(t,
		func() *api.AuthConfig { return &api.AuthConfig{APIKey: "qm-user"} },
		nil,
		func(context.Context, *api.AuthConfig, string) (*api.ClaudeCodeConnection, error) {
			t.Fatal("upload must not run for invalid credentials")
			return nil, nil
		},
	)

	err := connectClaudeCode(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not a valid JSON object") {
		t.Fatalf("connectClaudeCode error = %v, want JSON validation error", err)
	}
}

func TestConnectClaudeCodeErrorPropagatesThroughHandler(t *testing.T) {
	isolatePathWithFailingSecurity(t)
	writeClaudeCredentials(t, "definitely not json")

	stubCCSeams(t, func() *api.AuthConfig { return &api.AuthConfig{APIKey: "qm-user"} }, nil, nil)

	if err := handleCCCommand([]string{"connect"}); err == nil {
		t.Fatal("handleCCCommand should surface the credential error")
	}
}

func TestConnectClaudeCodeLogsInWhenUnauthenticated(t *testing.T) {
	isolatePathWithFailingSecurity(t)
	writeClaudeCredentials(t, `{"tokens":{"access_token":"access"}}`)

	loginCalls := 0
	stubCCSeams(t,
		func() *api.AuthConfig { return nil }, // no stored QualityMax auth
		func() (*api.AuthConfig, error) {
			loginCalls++
			return &api.AuthConfig{APIKey: "fresh-login"}, nil
		},
		func(ctx context.Context, auth *api.AuthConfig, authJSON string) (*api.ClaudeCodeConnection, error) {
			if auth.APIKey != "fresh-login" {
				t.Fatalf("upload used APIKey %q, want the freshly logged-in one", auth.APIKey)
			}
			return &api.ClaudeCodeConnection{Connected: true}, nil
		},
	)
	stdout, _ := captureOutput(t)

	if err := connectClaudeCode(context.Background()); err != nil {
		t.Fatalf("connectClaudeCode: %v", err)
	}
	if loginCalls != 1 {
		t.Fatalf("login calls = %d, want 1", loginCalls)
	}
	if out := stdout(); !strings.Contains(out, "Connect this computer to QualityMax") {
		t.Fatalf("stdout = %q, want login prompt message", out)
	}
	if out := stdout(); !strings.Contains(out, "Claude Code connected to QualityMax.") {
		t.Fatalf("stdout = %q, want unlabelled confirmation", out)
	}
}
