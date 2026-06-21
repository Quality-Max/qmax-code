package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestReadClaudeCodeAuthFile(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".config", "claude")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "credentials.json"),
		[]byte(`{"tokens":{"access_token":"access"}}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	got, err := readClaudeCodeAuth()
	if err != nil {
		t.Fatalf("readClaudeCodeAuth: %v", err)
	}
	if got == "" {
		t.Fatal("expected auth JSON")
	}
}

func TestReadClaudeCodeAuthFileDot(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, ".credentials.json"),
		[]byte(`{"tokens":{"access_token":"access-dot"}}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	got, err := readClaudeCodeAuth()
	if err != nil {
		t.Fatalf("readClaudeCodeAuth: %v", err)
	}
	if got != `{"tokens":{"access_token":"access-dot"}}` {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestConnectClaudeCodeRunsAndUsesAuthenticatedUpload(t *testing.T) {
	oldLoad := loadQualityMaxAuthForCC
	oldLogin := loginQualityMaxForCC
	oldUpload := uploadClaudeCodeAuth
	t.Cleanup(func() {
		loadQualityMaxAuthForCC = oldLoad
		loginQualityMaxForCC = oldLogin
		uploadClaudeCodeAuth = oldUpload
	})

	home := withTempHome(t)
	dir := filepath.Join(home, ".config", "claude")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "credentials.json"),
		[]byte(`{"tokens":{"access_token":"fresh-access"}}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	uploadRan := false
	loadQualityMaxAuthForCC = func() *api.AuthConfig {
		return &api.AuthConfig{APIKey: "qm-user", Email: "user@example.com"}
	}
	loginQualityMaxForCC = func() (*api.AuthConfig, error) {
		t.Fatal("existing QualityMax auth should be reused")
		return nil, nil
	}
	uploadClaudeCodeAuth = func(ctx context.Context, auth *api.AuthConfig, authJSON string) (*api.ClaudeCodeConnection, error) {
		uploadRan = true
		if auth.APIKey != "qm-user" {
			t.Fatalf("APIKey = %q", auth.APIKey)
		}
		if authJSON == "" {
			t.Fatal("missing auth JSON")
		}
		return &api.ClaudeCodeConnection{Connected: true, Status: "connected"}, nil
	}

	if err := connectClaudeCode(context.Background()); err != nil {
		t.Fatalf("connectClaudeCode: %v", err)
	}
	if !uploadRan {
		t.Fatalf("uploadRan=%v", uploadRan)
	}
}

func TestConnectClaudeCodeReauthorizesQualityMaxAfterUnauthorized(t *testing.T) {
	oldLoad := loadQualityMaxAuthForCC
	oldLogin := loginQualityMaxForCC
	oldUpload := uploadClaudeCodeAuth
	t.Cleanup(func() {
		loadQualityMaxAuthForCC = oldLoad
		loginQualityMaxForCC = oldLogin
		uploadClaudeCodeAuth = oldUpload
	})

	home := withTempHome(t)
	dir := filepath.Join(home, ".config", "claude")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "credentials.json"),
		[]byte(`{"tokens":{"access_token":"access"}}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	loadQualityMaxAuthForCC = func() *api.AuthConfig { return &api.AuthConfig{APIKey: "expired"} }
	loginQualityMaxForCC = func() (*api.AuthConfig, error) {
		return &api.AuthConfig{APIKey: "fresh"}, nil
	}

	attempts := 0
	uploadClaudeCodeAuth = func(ctx context.Context, auth *api.AuthConfig, authJSON string) (*api.ClaudeCodeConnection, error) {
		attempts++
		if attempts == 1 {
			return nil, &api.HTTPStatusError{StatusCode: http.StatusUnauthorized, Message: "expired"}
		}
		if auth.APIKey != "fresh" {
			t.Fatalf("retry APIKey = %q", auth.APIKey)
		}
		return &api.ClaudeCodeConnection{Connected: true}, nil
	}

	if err := connectClaudeCode(context.Background()); err != nil {
		t.Fatalf("connectClaudeCode: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("upload attempts = %d", attempts)
	}
}
