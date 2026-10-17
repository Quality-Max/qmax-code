package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the discovery/context helpers in context.go that were previously
// uncovered. These tests mutate HOME, PATH, and the process working
// directory — they must not run in parallel.

// chdirTemp switches the test process into a fresh temp dir and restores the
// original working directory on cleanup.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir
}

// ---- TokenUsage ----

func TestTokenUsageTotalTokens(t *testing.T) {
	u := TokenUsage{InputTokens: 100, OutputTokens: 50, Requests: 3}
	if got := u.TotalTokens(); got != 150 {
		t.Errorf("TotalTokens() = %d, want 150", got)
	}
}

func TestEstimatedCostUsesOpusRates(t *testing.T) {
	// The configured Opus rates have no date-based promotion, so this result
	// does not depend on time.Now() inside EstimatedCost.
	u := TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	if got := u.EstimatedCost("claude-opus-4-8"); got != 30.0 {
		t.Errorf("EstimatedCost(opus) = %v, want 30.0", got)
	}
}

// ---- LoadQMaxConfig ----

func TestLoadQMaxConfig_ReadsLegacyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".qamax"), 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"api_url":"https://q.example.com","token":"tok","email":"e@example.com","agent_id":"a1","api_key":"ak"}`
	if err := os.WriteFile(filepath.Join(home, ".qamax", "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := LoadQMaxConfig()
	if cfg.CloudURL != "https://q.example.com" || cfg.Token != "tok" ||
		cfg.Email != "e@example.com" || cfg.AgentID != "a1" || cfg.APIKey != "ak" {
		t.Errorf("LoadQMaxConfig() = %+v", cfg)
	}
}

func TestLoadQMaxConfig_MissingFileReturnsZero(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if cfg := LoadQMaxConfig(); cfg != (QMaxConfig{}) {
		t.Errorf("expected zero config for missing file, got %+v", cfg)
	}
}

func TestLoadQMaxConfig_InvalidJSONReturnsZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".qamax"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".qamax", "config.json"), []byte(`{invalid`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg := LoadQMaxConfig(); cfg != (QMaxConfig{}) {
		t.Errorf("expected zero config for invalid JSON, got %+v", cfg)
	}
}

// ---- DiscoverQMaxBinary ----

func TestDiscoverQMaxBinary_NoneFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	chdirTemp(t)

	if got := DiscoverQMaxBinary(); got != "" {
		t.Errorf("DiscoverQMaxBinary() = %q, want empty when nothing exists", got)
	}
}

func TestDiscoverQMaxBinary_FromCwd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	dir := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(dir, "qmax"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := DiscoverQMaxBinary(); got != "./qmax" {
		t.Errorf("DiscoverQMaxBinary() = %q, want ./qmax", got)
	}
}

func TestDiscoverQMaxBinary_FromHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	chdirTemp(t)

	want := filepath.Join(home, ".qamax", "qmax")
	if err := os.MkdirAll(filepath.Join(home, ".qamax"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := DiscoverQMaxBinary(); got != want {
		t.Errorf("DiscoverQMaxBinary() = %q, want %q", got, want)
	}
}

func TestDiscoverQMaxBinary_FromPATH(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	chdirTemp(t)

	want := filepath.Join(bin, "qmax")
	if err := os.WriteFile(want, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := DiscoverQMaxBinary(); got != want {
		t.Errorf("DiscoverQMaxBinary() = %q, want %q", got, want)
	}
}

func TestDiscoverQMaxBinary_CwdWinsOverHomeAndPATH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	dir := chdirTemp(t)

	for _, p := range []string{
		filepath.Join(home, ".qamax", "qmax"),
		filepath.Join(bin, "qmax"),
		filepath.Join(dir, "qmax"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if got := DiscoverQMaxBinary(); got != "./qmax" {
		t.Errorf("DiscoverQMaxBinary() = %q, want ./qmax to take precedence", got)
	}
}

// ---- ProbeQMaxStatus ----

func TestProbeQMaxStatus_EmptyBinary(t *testing.T) {
	if got := ProbeQMaxStatus(""); got != "" {
		t.Errorf("ProbeQMaxStatus(\"\") = %q, want empty", got)
	}
}

func TestProbeQMaxStatus_MissingBinaryReturnsEmpty(t *testing.T) {
	if got := ProbeQMaxStatus("/nonexistent/qmax-binary"); got != "" {
		t.Errorf("ProbeQMaxStatus(missing) = %q, want empty", got)
	}
}

func TestProbeQMaxStatus_RunsBinaryAndTrimsOutput(t *testing.T) {
	if _, err := os.Stat("/bin/echo"); err != nil {
		t.Skip("/bin/echo not available")
	}
	// `echo status` prints "status\n"; the probe must trim the newline.
	if got := ProbeQMaxStatus("/bin/echo"); got != "status" {
		t.Errorf("ProbeQMaxStatus(echo) = %q, want status", got)
	}
}

func TestProbeQMaxStatus_FailingBinaryReturnsEmpty(t *testing.T) {
	if _, err := os.Stat("/bin/false"); err != nil {
		t.Skip("/bin/false not available")
	}
	if got := ProbeQMaxStatus("/bin/false"); got != "" {
		t.Errorf("ProbeQMaxStatus(false) = %q, want empty on non-zero exit", got)
	}
}

// ---- FormatQMaxInstallHint ----

func TestFormatQMaxInstallHint(t *testing.T) {
	hint := FormatQMaxInstallHint()
	if !strings.Contains(hint, "qmax CLI not found") || !strings.Contains(hint, "https://get.qualitymax.io/cli") {
		t.Errorf("install hint missing expected text: %q", hint)
	}
}

// ---- DetectProjectFromCwd ----

func TestDetectProjectFromCwd_NoFile(t *testing.T) {
	chdirTemp(t)
	if id, file := DetectProjectFromCwd(); id != 0 || file != "" {
		t.Errorf("DetectProjectFromCwd() = %d, %q; want 0, empty", id, file)
	}
}

func TestDetectProjectFromCwd_QmaxYml(t *testing.T) {
	dir := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(dir, ".qmax.yml"), []byte("project_id: 42\nname: demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, file := DetectProjectFromCwd(); id != 42 || file != ".qmax.yml" {
		t.Errorf("DetectProjectFromCwd() = %d, %q; want 42, .qmax.yml", id, file)
	}
}

func TestDetectProjectFromCwd_QualitymaxYamlHyphenated(t *testing.T) {
	dir := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(dir, ".qualitymax.yaml"), []byte("project-id: 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, file := DetectProjectFromCwd(); id != 7 || file != ".qualitymax.yaml" {
		t.Errorf("DetectProjectFromCwd() = %d, %q; want 7, .qualitymax.yaml", id, file)
	}
}

func TestDetectProjectFromCwd_InvalidIDReturnsZero(t *testing.T) {
	dir := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(dir, ".qmax.yml"), []byte("project_id: not-a-number\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, file := DetectProjectFromCwd(); id != 0 || file != "" {
		t.Errorf("DetectProjectFromCwd() = %d, %q; want 0, empty for unparseable id", id, file)
	}
}

// ---- DetectGitInfo ----

func TestDetectGitInfo_InTempRepo(t *testing.T) {
	dir := chdirTemp(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_COUNT", "0")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "file.txt")
	run("-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	run("remote", "add", "origin", "https://github.com/acme/app.git")
	// Leave one uncommitted change so ChangedFiles is populated.
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	info := DetectGitInfo()
	if info == nil {
		t.Fatal("expected GitInfo inside a git repo")
	}
	if info.Branch == "" {
		t.Error("Branch should be detected")
	}
	if info.RemoteURL != "https://github.com/acme/app.git" {
		t.Errorf("RemoteURL = %q", info.RemoteURL)
	}
	if info.RecentCommits == "" {
		t.Error("RecentCommits should be detected after a commit")
	}
	if len(info.ChangedFiles) != 1 || info.ChangedFiles[0] != "file.txt" {
		t.Errorf("ChangedFiles = %v, want [file.txt]", info.ChangedFiles)
	}
}

func TestDetectGitInfo_OutsideRepoReturnsNil(t *testing.T) {
	chdirTemp(t) // fresh temp dir is not inside a work tree
	if info := DetectGitInfo(); info != nil {
		t.Errorf("expected nil outside a repo, got %+v", info)
	}
}
