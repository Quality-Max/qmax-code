package main

import (
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

// TestConfigRoundTripIncludesDefaultFramework verifies the new field
// survives save/load (regression guard — forgetting to add it to either
// side would silently lose the wizard's detection result).
func TestConfigRoundTripIncludesDefaultFramework(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	cfg := defaultConfig()
	cfg.DefaultFramework = "rust_cargo"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded := LoadQMaxCodeConfig()
	if loaded.DefaultFramework != "rust_cargo" {
		t.Errorf("DefaultFramework lost through round-trip: got %q, want rust_cargo", loaded.DefaultFramework)
	}
}
