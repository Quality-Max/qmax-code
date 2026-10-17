package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for auth.go: the LoadAuth priority chain (env var → auth.json →
// legacy ~/.qamax/config.json), SaveAuth, LoginWithAPIKey, and helpers.
// These tests mutate HOME and env vars — they must not run in parallel.

// isolateAuthEnv points HOME at a fresh temp dir and clears the QualityMax
// env overrides so auth tests never touch the real user config.
func isolateAuthEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("QUALITYMAX_API_KEY", "")
	t.Setenv("QUALITYMAX_URL", "")
	return home
}

func writeAuthFile(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, QmaxCodeConfigDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, authFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".qamax")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ---- LoadAuth priority chain ----

func TestLoadAuth_FromEnvVar(t *testing.T) {
	isolateAuthEnv(t)
	t.Setenv("QUALITYMAX_API_KEY", "qm-env-key")

	cfg := LoadAuth()
	if cfg == nil {
		t.Fatal("expected config from env var")
	}
	if cfg.APIKey != "qm-env-key" {
		t.Errorf("APIKey = %q, want qm-env-key", cfg.APIKey)
	}
	if cfg.CloudURL != DefaultCloudURL {
		t.Errorf("CloudURL = %q, want %q", cfg.CloudURL, DefaultCloudURL)
	}
	if !cfg.IsAuthenticated() {
		t.Error("IsAuthenticated should be true with env key")
	}
}

func TestLoadAuth_FromEnvVarWithCustomURL(t *testing.T) {
	isolateAuthEnv(t)
	t.Setenv("QUALITYMAX_API_KEY", "qm-env-key")
	t.Setenv("QUALITYMAX_URL", "http://cloud.example.com/")

	cfg := LoadAuth()
	if cfg.CloudURL != "http://cloud.example.com/" {
		t.Errorf("CloudURL = %q, want the env value verbatim", cfg.CloudURL)
	}
	if got := cfg.GetCloudURL(); got != "http://cloud.example.com" {
		t.Errorf("GetCloudURL() = %q, want trailing slash trimmed", got)
	}
}

func TestLoadAuth_EnvVarBeatsAuthFile(t *testing.T) {
	home := isolateAuthEnv(t)
	writeAuthFile(t, home, `{"api_key":"file-key"}`)
	t.Setenv("QUALITYMAX_API_KEY", "qm-env-key")

	cfg := LoadAuth()
	if cfg.APIKey != "qm-env-key" {
		t.Errorf("APIKey = %q, want env var to win over auth.json", cfg.APIKey)
	}
}

func TestLoadAuth_FromAuthFile(t *testing.T) {
	home := isolateAuthEnv(t)
	writeAuthFile(t, home, `{"api_key":"file-key","email":"file@example.com","user_id":"u1","cloud_url":"http://file.example.com"}`)

	cfg := LoadAuth()
	if cfg == nil {
		t.Fatal("expected config from auth.json")
	}
	if cfg.APIKey != "file-key" {
		t.Errorf("APIKey = %q, want file-key", cfg.APIKey)
	}
	if cfg.Email != "file@example.com" {
		t.Errorf("Email = %q, want file@example.com", cfg.Email)
	}
	if cfg.UserID != "u1" {
		t.Errorf("UserID = %q, want u1", cfg.UserID)
	}
	if cfg.CloudURL != "http://file.example.com" {
		t.Errorf("CloudURL = %q", cfg.CloudURL)
	}
}

func TestLoadAuth_EmptyAuthFileKeyFallsBackToLegacy(t *testing.T) {
	home := isolateAuthEnv(t)
	writeAuthFile(t, home, `{"api_key":""}`)
	writeLegacyConfig(t, home, `{"token":"legacy-token","email":"leg@example.com"}`)

	cfg := LoadAuth()
	if cfg == nil || cfg.APIKey != "legacy-token" {
		t.Errorf("expected legacy fallback, got %+v", cfg)
	}
}

func TestLoadAuth_LegacyConfigWithTokenAndEmail(t *testing.T) {
	home := isolateAuthEnv(t)
	writeLegacyConfig(t, home, `{"api_url":"http://legacy.example.com","token":"legacy-token","email":"leg@example.com"}`)

	cfg := LoadAuth()
	if cfg == nil {
		t.Fatal("expected config from legacy file")
	}
	if cfg.APIKey != "legacy-token" {
		t.Errorf("APIKey = %q, want legacy-token", cfg.APIKey)
	}
	if cfg.Email != "leg@example.com" {
		t.Errorf("Email = %q, want leg@example.com", cfg.Email)
	}
	if cfg.CloudURL != "http://legacy.example.com" {
		t.Errorf("CloudURL = %q", cfg.CloudURL)
	}
}

func TestLoadAuth_LegacyPrefersTokenOverAPIKey(t *testing.T) {
	home := isolateAuthEnv(t)
	writeLegacyConfig(t, home, `{"token":"jwt-token","api_key":"agent-key","email":"e@example.com"}`)

	cfg := LoadAuth()
	if cfg.APIKey != "jwt-token" {
		t.Errorf("APIKey = %q, want JWT token preferred over agent API key", cfg.APIKey)
	}
}

func TestLoadAuth_LegacyAPIKeyFallback(t *testing.T) {
	home := isolateAuthEnv(t)
	writeLegacyConfig(t, home, `{"api_key":"agent-key","email":"e@example.com"}`)

	cfg := LoadAuth()
	if cfg == nil || cfg.APIKey != "agent-key" {
		t.Errorf("expected api_key fallback, got %+v", cfg)
	}
}

func TestLoadAuth_LegacyDefaultsCloudURL(t *testing.T) {
	home := isolateAuthEnv(t)
	writeLegacyConfig(t, home, `{"token":"tok","email":"e@example.com"}`)

	cfg := LoadAuth()
	if cfg.CloudURL != DefaultCloudURL {
		t.Errorf("CloudURL = %q, want %q when legacy file omits api_url", cfg.CloudURL, DefaultCloudURL)
	}
}

func TestLoadAuth_LegacyConfigMigratesEmailViaFetchMe(t *testing.T) {
	home := isolateAuthEnv(t)

	meCalls := 0
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meCalls++
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"email":"dev@example.com","id":"user-7"}`))
	}))
	defer srv.Close()

	writeLegacyConfig(t, home, fmt.Sprintf(`{"api_url":%q,"token":"legacy-token"}`, srv.URL))

	cfg := LoadAuth()
	if cfg == nil {
		t.Fatal("expected config from legacy file")
	}
	if cfg.APIKey != "legacy-token" {
		t.Errorf("APIKey = %q, want legacy-token", cfg.APIKey)
	}
	if gotPath != "/api/me" {
		t.Errorf("migration should call /api/me, got path %q", gotPath)
	}
	if gotAuth != "Bearer legacy-token" {
		t.Errorf("Authorization = %q, want Bearer legacy-token", gotAuth)
	}
	if cfg.Email != "dev@example.com" {
		t.Errorf("Email = %q, want dev@example.com from /api/me", cfg.Email)
	}
	if cfg.UserID != "user-7" {
		t.Errorf("UserID = %q, want user-7 from /api/me", cfg.UserID)
	}
	if meCalls != 1 {
		t.Errorf("expected exactly 1 /api/me call, got %d", meCalls)
	}

	// The migrated credentials must be persisted to the new auth.json so the
	// legacy file is not needed on the next run.
	data, err := os.ReadFile(filepath.Join(home, QmaxCodeConfigDir, authFileName))
	if err != nil {
		t.Fatalf("migrated auth.json missing: %v", err)
	}
	var saved AuthConfig
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("migrated auth.json not valid JSON: %v", err)
	}
	if saved.APIKey != "legacy-token" || saved.Email != "dev@example.com" || saved.UserID != "user-7" {
		t.Errorf("migrated auth.json = %+v", saved)
	}
}

func TestLoadAuth_LegacyFetchMeFailureStillReturnsConfig(t *testing.T) {
	home := isolateAuthEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	writeLegacyConfig(t, home, fmt.Sprintf(`{"api_url":%q,"token":"legacy-token"}`, srv.URL))

	cfg := LoadAuth()
	if cfg == nil || cfg.APIKey != "legacy-token" {
		t.Fatalf("expected config despite /api/me failure, got %+v", cfg)
	}
	if cfg.Email != "" {
		t.Errorf("Email = %q, want empty when /api/me fails", cfg.Email)
	}
	if _, err := os.Stat(filepath.Join(home, QmaxCodeConfigDir, authFileName)); !os.IsNotExist(err) {
		t.Error("auth.json should not be written when /api/me fails")
	}
}

func TestLoadAuth_ReturnsNilWhenNothingConfigured(t *testing.T) {
	isolateAuthEnv(t)
	if cfg := LoadAuth(); cfg != nil {
		t.Errorf("expected nil config, got %+v", cfg)
	}
}

// ---- loadAuthFile / loadLegacyQMaxAuth direct ----

func TestLoadAuthFile_MissingFileReturnsNil(t *testing.T) {
	isolateAuthEnv(t)
	if cfg := loadAuthFile(); cfg != nil {
		t.Errorf("expected nil for missing file, got %+v", cfg)
	}
}

func TestLoadAuthFile_InvalidJSONReturnsNil(t *testing.T) {
	home := isolateAuthEnv(t)
	writeAuthFile(t, home, `not json`)
	if cfg := loadAuthFile(); cfg != nil {
		t.Errorf("expected nil for invalid JSON, got %+v", cfg)
	}
}

func TestLoadLegacyQMaxAuth_MissingFileReturnsZero(t *testing.T) {
	isolateAuthEnv(t)
	if cfg := loadLegacyQMaxAuth(); cfg.Token != "" || cfg.APIKey != "" {
		t.Errorf("expected zero config for missing file, got %+v", cfg)
	}
}

func TestLoadLegacyQMaxAuth_ReadsFields(t *testing.T) {
	home := isolateAuthEnv(t)
	writeLegacyConfig(t, home, `{"api_url":"http://q.example.com","token":"tok","email":"e@example.com","api_key":"ak"}`)

	cfg := loadLegacyQMaxAuth()
	if cfg.CloudURL != "http://q.example.com" || cfg.Token != "tok" ||
		cfg.Email != "e@example.com" || cfg.APIKey != "ak" {
		t.Errorf("legacy config fields = %+v", cfg)
	}
}

// ---- SaveAuth ----

func TestSaveAuth_WritesJSONFile(t *testing.T) {
	home := isolateAuthEnv(t)

	cfg := &AuthConfig{APIKey: "qm-key", Email: "e@example.com", UserID: "u1", CloudURL: "http://c.example.com"}
	if err := SaveAuth(cfg); err != nil {
		t.Fatalf("SaveAuth: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, QmaxCodeConfigDir, authFileName))
	if err != nil {
		t.Fatalf("auth.json not written: %v", err)
	}
	var loaded AuthConfig
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("auth.json not valid JSON: %v", err)
	}
	if loaded.APIKey != "qm-key" || loaded.Email != "e@example.com" ||
		loaded.UserID != "u1" || loaded.CloudURL != "http://c.example.com" {
		t.Errorf("round-tripped auth.json = %+v", loaded)
	}
}

func TestSaveAuth_FailsWhenConfigDirIsAFile(t *testing.T) {
	home := isolateAuthEnv(t)
	// Occupy the ~/.qmax-code path with a regular file so MkdirAll fails.
	if err := os.WriteFile(filepath.Join(home, QmaxCodeConfigDir), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveAuth(&AuthConfig{APIKey: "k"}); err == nil {
		t.Error("expected error when config dir cannot be created")
	}
}

// ---- AuthConfig helpers ----

func TestIsAuthenticated(t *testing.T) {
	var nilCfg *AuthConfig
	if nilCfg.IsAuthenticated() {
		t.Error("nil config should not be authenticated")
	}
	if (&AuthConfig{}).IsAuthenticated() {
		t.Error("empty API key should not be authenticated")
	}
	if !(&AuthConfig{APIKey: "k"}).IsAuthenticated() {
		t.Error("non-empty API key should be authenticated")
	}
}

func TestGetCloudURL(t *testing.T) {
	var nilCfg *AuthConfig
	if got := nilCfg.GetCloudURL(); got != DefaultCloudURL {
		t.Errorf("nil GetCloudURL() = %q, want default", got)
	}
	if got := (&AuthConfig{}).GetCloudURL(); got != DefaultCloudURL {
		t.Errorf("empty GetCloudURL() = %q, want default", got)
	}
	if got := (&AuthConfig{CloudURL: "http://x.example.com//"}).GetCloudURL(); got != "http://x.example.com" {
		t.Errorf("GetCloudURL() = %q, want trailing slashes trimmed", got)
	}
}

// ---- LoginWithAPIKey ----

func TestLoginWithAPIKey_SuccessStripsPrefixAndSaves(t *testing.T) {
	home := isolateAuthEnv(t)
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"email":"dev@example.com","id":"user-9"}`))
	}))
	defer srv.Close()
	t.Setenv("QUALITYMAX_URL", srv.URL)

	cfg, err := LoginWithAPIKey("qm-secret")
	if err != nil {
		t.Fatalf("LoginWithAPIKey: %v", err)
	}
	if gotPath != "/api/me" {
		t.Errorf("validation should call /api/me, got %q", gotPath)
	}
	// The qm- prefix is stripped server-side; the full key is stored as-is.
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q, want Bearer secret (prefix stripped)", gotAuth)
	}
	if cfg.APIKey != "qm-secret" {
		t.Errorf("APIKey = %q, want full key retained", cfg.APIKey)
	}
	if cfg.Email != "dev@example.com" || cfg.UserID != "user-9" {
		t.Errorf("identity fields = %q/%q", cfg.Email, cfg.UserID)
	}

	data, err := os.ReadFile(filepath.Join(home, QmaxCodeConfigDir, authFileName))
	if err != nil {
		t.Fatalf("auth.json not saved: %v", err)
	}
	if !strings.Contains(string(data), "qm-secret") {
		t.Errorf("saved auth.json missing key: %s", data)
	}
}

func TestLoginWithAPIKey_RejectsInvalidKey(t *testing.T) {
	home := isolateAuthEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	t.Setenv("QUALITYMAX_URL", srv.URL)

	cfg, err := LoginWithAPIKey("bad-key")
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention HTTP 401, got %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil config on failure, got %+v", cfg)
	}
	if _, statErr := os.Stat(filepath.Join(home, QmaxCodeConfigDir, authFileName)); !os.IsNotExist(statErr) {
		t.Error("auth.json should not be written for an invalid key")
	}
}

func TestLoginWithAPIKey_UnreachableServer(t *testing.T) {
	isolateAuthEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	t.Setenv("QUALITYMAX_URL", srv.URL)

	cfg, err := LoginWithAPIKey("any-key")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "cannot reach QualityMax") {
		t.Errorf("error = %v, want cannot reach QualityMax", err)
	}
	if cfg != nil {
		t.Errorf("expected nil config, got %+v", cfg)
	}
}

// ---- fetchMe ----

func TestFetchMe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"email":"e@example.com","id":"u1"}`))
	}))
	defer srv.Close()

	me := fetchMe(&AuthConfig{APIKey: "qm-key", CloudURL: srv.URL})
	if me == nil || me["email"] != "e@example.com" {
		t.Errorf("fetchMe = %+v, want email e@example.com", me)
	}
}

func TestFetchMe_Non200ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if me := fetchMe(&AuthConfig{APIKey: "qm-key", CloudURL: srv.URL}); me != nil {
		t.Errorf("fetchMe should return nil on non-200, got %+v", me)
	}
}

func TestFetchMe_InvalidURLReturnsNil(t *testing.T) {
	if me := fetchMe(&AuthConfig{APIKey: "k", CloudURL: "http://exa mple.com"}); me != nil {
		t.Errorf("fetchMe should return nil on unparseable URL, got %+v", me)
	}
}

// ---- small helpers ----

func TestStripKeyPrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"qm-abc123", "abc123"},
		{"abc123", "abc123"},
		{"qm-", ""},
		{"qmax-abc", "qmax-abc"}, // only the exact qm- prefix is stripped
		{"", ""},
	}
	for _, tc := range cases {
		if got := stripKeyPrefix(tc.in); got != tc.want {
			t.Errorf("stripKeyPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("QM_ENV_OR_TEST", "value")
	if got := envOr("QM_ENV_OR_TEST", "fallback"); got != "value" {
		t.Errorf("envOr with value = %q, want value", got)
	}
	t.Setenv("QM_ENV_OR_TEST", "")
	if got := envOr("QM_ENV_OR_TEST", "fallback"); got != "fallback" {
		t.Errorf("envOr with empty value = %q, want fallback", got)
	}
}
