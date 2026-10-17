package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
)

// unsetOverridingEnv clears the env vars LoadQMaxCodeConfig layers on top of
// config.json, so assertions about printed values stay deterministic no matter
// what the developer's shell exports.
func unsetOverridingEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OLLAMA_BASE_URL", "OLLAMA_MODEL",
		"CEREBRAS_API_KEY", "CEREBRAS_API_BASE", "CEREBRAS_MODEL", "CEREBRAS_REASONING_EFFORT",
	} {
		orig, had := os.LookupEnv(key)
		if !had {
			continue
		}
		os.Unsetenv(key)
		t.Cleanup(func() { os.Setenv(key, orig) })
	}
}

func TestHandleConfigCommandShowIsTheDefault(t *testing.T) {
	withTempHome(t)
	isolatePathWithFailingSecurity(t)
	unsetOverridingEnv(t)
	stdout, _ := captureOutput(t)

	for _, args := range [][]string{nil, {"show"}} {
		handleConfigCommand(args)
	}

	// Two invocations → the header shows up twice; both must render defaults.
	out := stdout()
	if n := strings.Count(out, "Current config"); n != 2 {
		t.Fatalf("header appeared %d times, want 2:\n%s", n, out)
	}
	if !strings.Contains(out, "default_model") || !strings.Contains(out, `"auto"`) {
		t.Fatalf("default_model missing from show output:\n%s", out)
	}
	if !strings.Contains(out, "plan_window_hours = 5 (default)") {
		t.Fatalf("plan_window_hours default missing from show output:\n%s", out)
	}
	if !strings.Contains(out, `"historic"`) {
		t.Fatalf("default theme missing from show output:\n%s", out)
	}
}

func TestHandleConfigCommandSet(t *testing.T) {
	withTempHome(t)
	stdout, _ := captureOutput(t)

	handleConfigCommand([]string{"set", "default_framework", "playwright"})

	if out := stdout(); !strings.Contains(out, "Set default_framework = playwright") {
		t.Fatalf("stdout = %q, want set confirmation", out)
	}
	loaded := api.LoadQMaxCodeConfig()
	if loaded.DefaultFramework != "playwright" {
		t.Fatalf("persisted DefaultFramework = %q, want playwright", loaded.DefaultFramework)
	}
}

func TestHandleConfigCommandSetMissingValueExits(t *testing.T) {
	withTempHome(t)
	_, stderr := captureOutput(t)

	codes := runWithStubbedExit(t, func() { handleConfigCommand([]string{"set", "default_framework"}) })

	if len(codes) != 1 || codes[0] != 2 {
		t.Fatalf("exit codes = %v, want [2]", codes)
	}
	if out := stderr(); !strings.Contains(out, "Usage: qmax-code config set KEY VALUE") {
		t.Fatalf("stderr = %q, want usage message", out)
	}
}

func TestHandleConfigCommandSetInvalidValueExits(t *testing.T) {
	withTempHome(t)
	_, stderr := captureOutput(t)

	codes := runWithStubbedExit(t, func() { handleConfigCommand([]string{"set", "default_framework", "bash"}) })

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("exit codes = %v, want [1]", codes)
	}
	if out := stderr(); !strings.Contains(out, "Error:") {
		t.Fatalf("stderr = %q, want error message", out)
	}
}

func TestHandleConfigCommandUnset(t *testing.T) {
	withTempHome(t)
	if err := setConfigField("default_framework", "go_test"); err != nil {
		t.Fatal(err)
	}
	stdout, _ := captureOutput(t)

	handleConfigCommand([]string{"unset", "default_framework"})

	if out := stdout(); !strings.Contains(out, "Cleared default_framework") {
		t.Fatalf("stdout = %q, want cleared confirmation", out)
	}
	if loaded := api.LoadQMaxCodeConfig(); loaded.DefaultFramework != "" {
		t.Fatalf("DefaultFramework after unset = %q, want empty", loaded.DefaultFramework)
	}
}

func TestHandleConfigCommandUnsetMissingKeyExits(t *testing.T) {
	withTempHome(t)
	_, stderr := captureOutput(t)

	codes := runWithStubbedExit(t, func() { handleConfigCommand([]string{"unset"}) })

	if len(codes) != 1 || codes[0] != 2 {
		t.Fatalf("exit codes = %v, want [2]", codes)
	}
	if out := stderr(); !strings.Contains(out, "Usage: qmax-code config unset KEY") {
		t.Fatalf("stderr = %q, want usage message", out)
	}
}

func TestHandleConfigCommandReset(t *testing.T) {
	home := withTempHome(t)
	if err := setConfigField("auto_save", "false"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigField("theme", "ocean"); err != nil {
		t.Fatal(err)
	}
	stdout, _ := captureOutput(t)

	handleConfigCommand([]string{"reset"})

	if out := stdout(); !strings.Contains(out, "api.Config reset to defaults") {
		t.Fatalf("stdout = %q, want reset confirmation", out)
	}
	data, err := os.ReadFile(filepath.Join(home, ".qmax-code", "config.json"))
	if err != nil {
		t.Fatalf("config file not rewritten by reset: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("reset config malformed: %v", err)
	}
	loaded := api.LoadQMaxCodeConfig()
	if !loaded.AutoSave || loaded.Theme != "" || loaded.DefaultModel != "auto" {
		t.Fatalf("post-reset config = %+v, want defaults", loaded)
	}
}

func TestHandleConfigCommandUnknownSubcommandExits(t *testing.T) {
	withTempHome(t)
	_, stderr := captureOutput(t)

	codes := runWithStubbedExit(t, func() { handleConfigCommand([]string{"bogus"}) })

	if len(codes) != 1 || codes[0] != 2 {
		t.Fatalf("exit codes = %v, want [2]", codes)
	}
	out := stderr()
	if !strings.Contains(out, "Unknown config command: bogus") {
		t.Fatalf("stderr = %q, want unknown-command message", out)
	}
	if !strings.Contains(out, "Try: qmax-code config [show|set|unset|reset]") {
		t.Fatalf("stderr = %q, want hint", out)
	}
}

func TestPrintConfigDefaults(t *testing.T) {
	withTempHome(t)
	isolatePathWithFailingSecurity(t)
	unsetOverridingEnv(t)
	stdout, _ := captureOutput(t)

	printConfig()

	out := stdout()
	for _, want := range []string{
		"plan_window_hours = 5 (default)",
		"narrate_tool_calls",
		`"brief"`,
		"gpt-oss-120b (default)",
		"https://api.cerebras.ai/v1 (default)",
		"none (default — Gemma 4 thinking off)",
		"anthropic_key     = (not set)",
		"cerebras_key      = (not set)",
		"ollama_url        = (not set)",
		"cloud_sync        = not set (will prompt on next eligible session)",
		"live_feed         = false",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printConfig output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "providers") {
		t.Errorf("printConfig should omit the providers line when none are enabled:\n%s", out)
	}
}

func TestPrintConfigOllamaMasked(t *testing.T) {
	withTempHome(t)
	unsetOverridingEnv(t)
	if err := setConfigField("ollama_url", "http://alice:s3cr3t@127.0.0.1:11434"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigField("ollama_model", "llama3"); err != nil {
		t.Fatal(err)
	}
	stdout, _ := captureOutput(t)

	printConfig()

	out := stdout()
	if !strings.Contains(out, "127.0.0.1:11434") {
		t.Errorf("ollama_url missing host:\n%s", out)
	}
	if !strings.Contains(out, "alice:%2A%2A%2A%2A@") {
		t.Errorf("ollama_url password not masked:\n%s", out)
	}
	if strings.Contains(out, "s3cr3t") {
		t.Errorf("ollama_url password leaked:\n%s", out)
	}
	if !strings.Contains(out, `"llama3"`) {
		t.Errorf("ollama_model missing:\n%s", out)
	}
}

func TestPrintConfigCustomPlanWindow(t *testing.T) {
	withTempHome(t)
	if err := setConfigField("plan_window_hours", "8"); err != nil {
		t.Fatal(err)
	}
	stdout, _ := captureOutput(t)

	printConfig()

	out := stdout()
	if !strings.Contains(out, "plan_window_hours = 8") {
		t.Errorf("custom plan_window_hours missing:\n%s", out)
	}
	if strings.Contains(out, "plan_window_hours = 8 (default)") {
		t.Errorf("non-default window should not be marked default:\n%s", out)
	}
}

func TestPrintConfigCloudSyncAndLiveFeed(t *testing.T) {
	withTempHome(t)
	stdout, _ := captureOutput(t)

	printConfig() // fresh: not set
	if err := setConfigField("cloud_sync", "true"); err != nil {
		t.Fatal(err)
	}
	if err := setConfigField("live_feed", "true"); err != nil {
		t.Fatal(err)
	}
	printConfig() // enabled
	if err := setConfigField("cloud_sync", "false"); err != nil {
		t.Fatal(err)
	}
	printConfig() // disabled

	out := stdout()
	for _, want := range []string{
		"cloud_sync        = not set (will prompt on next eligible session)",
		"cloud_sync        = enabled",
		"cloud_sync        = disabled",
		"live_feed         = true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintConfigCerebrasBackendWithKey(t *testing.T) {
	withTempHome(t)
	unsetOverridingEnv(t)
	setenv(t, "CEREBRAS_API_KEY", "csk-test-key")
	if err := setConfigField("backend", "cerebras"); err != nil {
		t.Fatal(err)
	}
	stdout, _ := captureOutput(t)

	printConfig()

	out := stdout()
	if !strings.Contains(out, `= "cerebras"`) {
		t.Errorf("backend selection missing:\n%s", out)
	}
	if !strings.Contains(out, "(cerebras key set; model: gpt-oss-120b") {
		t.Errorf("cerebras key branch missing:\n%s", out)
	}
	if !strings.Contains(out, "cerebras_key      = (set; stored in OS keychain)") {
		t.Errorf("cerebras_key set line missing:\n%s", out)
	}
}

func TestPrintConfigCerebrasBackendWithoutKeyWarns(t *testing.T) {
	withTempHome(t)
	isolatePathWithFailingSecurity(t)
	unsetOverridingEnv(t)
	if err := setConfigField("backend", "cerebras"); err != nil {
		t.Fatal(err)
	}
	stdout, _ := captureOutput(t)

	printConfig()

	if out := stdout(); !strings.Contains(out, "(WARNING: CEREBRAS_API_KEY not set)") {
		t.Fatalf("cerebras no-key warning missing:\n%s", out)
	}
}

func TestPrintConfigBackendBinariesFound(t *testing.T) {
	withTempHome(t)

	cases := []struct {
		backend string
		binary  string
		want    string
	}{
		{"cc", "claude", "(claude found: "},
		{"codex", "codex", "(codex found: "},
		{"agy", "agy", "(agy found: "},
		{"opencode", "opencode", "(opencode found: "},
	}
	for _, tc := range cases {
		t.Run(tc.backend, func(t *testing.T) {
			prependFakeBinaries(t, tc.binary)
			if err := setConfigField("backend", tc.backend); err != nil {
				t.Fatal(err)
			}
			stdout, _ := captureOutput(t)

			printConfig()

			if out := stdout(); !strings.Contains(out, tc.want) {
				t.Fatalf("output missing %q:\n%s", tc.want, out)
			}
		})
	}
}

func TestPrintConfigProviders(t *testing.T) {
	home := withTempHome(t)
	// EnabledProviders has no setConfigField key; write config.json directly.
	dir := filepath.Join(home, ".qmax-code")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := api.DefaultConfig()
	cfg.EnabledProviders = []string{"zai-coding-plan", "groq"}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _ := captureOutput(t)

	printConfig()

	if out := stdout(); !strings.Contains(out, "providers         = zai-coding-plan, groq") {
		t.Fatalf("providers line missing:\n%s", out)
	}
}
