package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestReadCodexAuthFileRequiresOAuthTokens(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "auth.json"),
		[]byte(`{"tokens":{"access_token":"access","refresh_token":"refresh"}}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	got, err := readCodexAuthFile()
	if err != nil {
		t.Fatalf("readCodexAuthFile: %v", err)
	}
	if got == "" {
		t.Fatal("expected auth JSON")
	}
}

func TestConnectCodexRunsFreshLoginAndUsesAuthenticatedUpload(t *testing.T) {
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
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	loginRan := false
	uploadRan := false
	findCodexForConnect = func() string { return "/usr/local/bin/codex" }
	runCodexLogin = func(bin string) error {
		loginRan = true
		return os.WriteFile(
			filepath.Join(dir, "auth.json"),
			[]byte(`{"tokens":{"access_token":"fresh-access","refresh_token":"fresh-refresh"}}`),
			0600,
		)
	}
	loadQualityMaxAuth = func() *api.AuthConfig {
		return &api.AuthConfig{APIKey: "qm-user", Email: "user@example.com"}
	}
	loginQualityMax = func() (*api.AuthConfig, error) {
		t.Fatal("existing QualityMax auth should be reused")
		return nil, nil
	}
	uploadCodexAuth = func(ctx context.Context, auth *api.AuthConfig, authJSON string) (*api.CodexConnection, error) {
		uploadRan = true
		if auth.APIKey != "qm-user" {
			t.Fatalf("APIKey = %q", auth.APIKey)
		}
		if authJSON == "" {
			t.Fatal("missing auth JSON")
		}
		return &api.CodexConnection{Connected: true, Status: "connected"}, nil
	}

	if err := connectCodex(context.Background()); err != nil {
		t.Fatalf("connectCodex: %v", err)
	}
	if !loginRan || !uploadRan {
		t.Fatalf("loginRan=%v uploadRan=%v", loginRan, uploadRan)
	}
}

func TestConnectCodexStopsWhenLoginFails(t *testing.T) {
	oldFind := findCodexForConnect
	oldRun := runCodexLogin
	t.Cleanup(func() {
		findCodexForConnect = oldFind
		runCodexLogin = oldRun
	})

	findCodexForConnect = func() string { return "/usr/local/bin/codex" }
	runCodexLogin = func(string) error { return errors.New("cancelled") }

	if err := connectCodex(context.Background()); err == nil {
		t.Fatal("expected login failure")
	}
}

func TestConnectCodexReauthorizesQualityMaxAfterUnauthorized(t *testing.T) {
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
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "auth.json"),
		[]byte(`{"tokens":{"access_token":"access","refresh_token":"refresh"}}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	findCodexForConnect = func() string { return "/usr/local/bin/codex" }
	runCodexLogin = func(string) error { return nil }
	loadQualityMaxAuth = func() *api.AuthConfig { return &api.AuthConfig{APIKey: "expired"} }
	loginQualityMax = func() (*api.AuthConfig, error) {
		return &api.AuthConfig{APIKey: "fresh"}, nil
	}

	attempts := 0
	uploadCodexAuth = func(ctx context.Context, auth *api.AuthConfig, authJSON string) (*api.CodexConnection, error) {
		attempts++
		if attempts == 1 {
			return nil, &api.HTTPStatusError{StatusCode: http.StatusUnauthorized, Message: "expired"}
		}
		if auth.APIKey != "fresh" {
			t.Fatalf("retry APIKey = %q", auth.APIKey)
		}
		return &api.CodexConnection{Connected: true}, nil
	}

	if err := connectCodex(context.Background()); err != nil {
		t.Fatalf("connectCodex: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("upload attempts = %d", attempts)
	}
}
