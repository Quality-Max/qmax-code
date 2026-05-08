package main

import (
	"github.com/qualitymax/qmax-code/internal/api"
	"os"
	"path/filepath"
	"testing"
)

// TestDetectProjectFramework exercises the first-run wizard's language
// sniffer. Priority matters for polyglot repos (a Python-with-Rust-extension
// project should still be detected as Rust since the Rust crate is the
// compile-heavy part that matters for CI).
func TestDetectProjectFramework(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string
	}{
		{name: "empty dir", files: nil, want: ""},
		{name: "rust crate", files: []string{"Cargo.toml"}, want: "rust_cargo"},
		{name: "go module", files: []string{"go.mod"}, want: "go_test"},
		{name: "playwright ts", files: []string{"playwright.config.ts"}, want: "playwright"},
		{name: "playwright js", files: []string{"playwright.config.js"}, want: "playwright"},
		{name: "playwright mjs", files: []string{"playwright.config.mjs"}, want: "playwright"},
		{name: "pytest via pyproject", files: []string{"pyproject.toml"}, want: "pytest"},
		{name: "pytest via pytest.ini", files: []string{"pytest.ini"}, want: "pytest"},
		{name: "pytest via tox.ini", files: []string{"tox.ini"}, want: "pytest"},
		{name: "node-only (no verdict)", files: []string{"package.json"}, want: ""},
		{
			// Rust trumps Python — compile-heavy crate is the CI driver.
			name:  "rust+python → rust wins",
			files: []string{"Cargo.toml", "pyproject.toml"},
			want:  "rust_cargo",
		},
		{
			// Rust trumps Go (arbitrary but stable — the priority is documented).
			name:  "rust+go → rust wins",
			files: []string{"Cargo.toml", "go.mod"},
			want:  "rust_cargo",
		},
		{
			// Go trumps playwright when both exist (hypothetical: a Go-backed
			// app with Playwright e2e tests — the Go toolchain is what CI
			// needs to exercise the compile step first).
			name:  "go+playwright → go wins",
			files: []string{"go.mod", "playwright.config.ts"},
			want:  "go_test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				path := filepath.Join(dir, f)
				if err := os.WriteFile(path, []byte("// fixture"), 0644); err != nil {
					t.Fatalf("failed to create fixture %s: %v", f, err)
				}
			}
			got := detectProjectFramework(dir)
			if got != tt.want {
				t.Errorf("detectProjectFramework(%v) = %q, want %q", tt.files, got, tt.want)
			}
		})
	}
}

// Edge cases added after the PR #29 review — verify the priority ladder
// behaves sensibly for file configurations that tripped reviewers.

func TestDetectProjectFramework_GoSumWithoutGoMod(t *testing.T) {
	// A repo with just go.sum but no go.mod is in a broken state. We
	// intentionally DON'T detect it as Go — go.mod is the required anchor.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if got := detectProjectFramework(dir); got != "" {
		t.Errorf("go.sum without go.mod should not be detected; got %q", got)
	}
}

func TestDetectProjectFramework_SymlinkedMarker(t *testing.T) {
	// Symlinked Cargo.toml (common in monorepos that share toolchain
	// configs). os.Stat follows symlinks, so detection should still succeed.
	realDir := t.TempDir()
	linkDir := t.TempDir()
	realCargo := filepath.Join(realDir, "Cargo.toml")
	if err := os.WriteFile(realCargo, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realCargo, filepath.Join(linkDir, "Cargo.toml")); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	if got := detectProjectFramework(linkDir); got != "rust_cargo" {
		t.Errorf("symlinked Cargo.toml not detected; got %q", got)
	}
}

func TestDetectProjectFramework_TriplePriority(t *testing.T) {
	// Worst case: repo has Cargo.toml + go.mod + playwright.config.ts.
	// Only possible in oddball monorepo setups but priority must stay
	// stable so CI generation is deterministic.
	dir := t.TempDir()
	for _, f := range []string{"Cargo.toml", "go.mod", "playwright.config.ts", "pyproject.toml"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if got := detectProjectFramework(dir); got != "rust_cargo" {
		t.Errorf("quadruple-framework repo: rust should still win, got %q", got)
	}
}

func TestDetectProjectFramework_NonExistentDir(t *testing.T) {
	// Defensive: caller passes a path that doesn't exist. Must return ""
	// (not crash, not error) — caller treats "" as "I don't know".
	got := detectProjectFramework("/definitely/does/not/exist/anywhere")
	if got != "" {
		t.Errorf("non-existent dir should return empty; got %q", got)
	}
}

func TestPrettyFrameworkName(t *testing.T) {
	cases := map[string]string{
		"rust_cargo": "Rust (cargo)",
		"go_test":    "Go (go test)",
		"playwright": "Playwright",
		"pytest":     "Python (pytest)",
		"unknown":    "unknown", // pass-through
		"":           "",
	}
	for in, want := range cases {
		if got := prettyFrameworkName(in); got != want {
			t.Errorf("prettyFrameworkName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestConfigPre180ForwardCompat loads a v1.7.x-shaped config.json
// (no `default_framework` field) and verifies upgrade doesn't crash or
// corrupt the existing fields. Users upgrading from v1.7.x should get
// DefaultFramework == "" (triggering re-detection or the "no default"
// fallback in the wizard).
func TestConfigPre180ForwardCompat(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Write a pre-1.8.0 config JSON with ONLY the fields that existed then.
	configDir := filepath.Join(tmpDir, ".qmax-code")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"default_model":"opus","default_project":42,"professional":true,"auto_save":false,"max_token_budget":50000}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}

	loaded := api.LoadQMaxCodeConfig()

	// All legacy fields preserved.
	if loaded.DefaultModel != "opus" {
		t.Errorf("DefaultModel: got %s, want opus", loaded.DefaultModel)
	}
	if loaded.DefaultProject != 42 {
		t.Errorf("DefaultProject: got %d, want 42", loaded.DefaultProject)
	}
	if !loaded.Professional {
		t.Error("Professional: lost through upgrade load")
	}
	if loaded.MaxTokenBudget != 50000 {
		t.Errorf("MaxTokenBudget: got %d, want 50000", loaded.MaxTokenBudget)
	}
	// New field defaults to empty string — the wizard will detect on next run.
	if loaded.DefaultFramework != "" {
		t.Errorf("DefaultFramework on legacy load should be empty; got %q", loaded.DefaultFramework)
	}
}

// TestConfigRoundTripIncludesDefaultFramework verifies the new field
// survives save/load (regression guard — forgetting to add it to either
// side would silently lose the wizard's detection result).
func TestConfigRoundTripIncludesDefaultFramework(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	cfg := api.DefaultConfig()
	cfg.DefaultFramework = "rust_cargo"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded := api.LoadQMaxCodeConfig()
	if loaded.DefaultFramework != "rust_cargo" {
		t.Errorf("DefaultFramework lost through round-trip: got %q, want rust_cargo", loaded.DefaultFramework)
	}
}
