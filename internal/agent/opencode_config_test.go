package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestWriteOpenCodeConfigProducesProviderBlockAndMCP(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// Custom provider key via env so no keychain is touched.
	t.Setenv("QMAX_PC_ZAI_CODING_PLAN", "zai-secret")

	cfg := &api.Config{EnabledProviders: []string{"zai-coding-plan", "groq"}}
	path, err := WriteOpenCodeConfig(cfg, &api.SessionContext{ProjectID: 7, LocalOnly: true}, "standard")
	if err != nil {
		t.Fatalf("WriteOpenCodeConfig: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}

	// MCP server entry present.
	mcp, ok := root["mcp"].(map[string]any)
	if !ok || mcp["qmax"] == nil {
		t.Fatalf("expected mcp.qmax entry, got %v", root["mcp"])
	}
	qmaxMCP, ok := mcp["qmax"].(map[string]any)
	if !ok {
		t.Fatalf("expected mcp.qmax map, got %T", mcp["qmax"])
	}
	mcpEnv, ok := qmaxMCP["environment"].(map[string]any)
	if !ok || mcpEnv[api.LocalOnlyEnv] != "1" {
		t.Fatalf("standalone MCP environment missing %s=1: %v", api.LocalOnlyEnv, qmaxMCP["environment"])
	}

	// Custom provider (zai) gets a block; known provider (groq) does not.
	prov, ok := root["provider"].(map[string]any)
	if !ok {
		t.Fatalf("expected provider map, got %v", root["provider"])
	}
	if prov["groq"] != nil {
		t.Error("known provider groq should not get a provider block")
	}
	zai, ok := prov["zai-coding-plan"].(map[string]any)
	if !ok {
		t.Fatalf("expected zai-coding-plan block, got %v", prov["zai-coding-plan"])
	}
	opts, _ := zai["options"].(map[string]any)
	if opts["apiKey"] != "{env:QMAX_PC_ZAI_CODING_PLAN}" {
		t.Errorf("apiKey should reference env, got %v", opts["apiKey"])
	}
	if opts["apiKey"] == "zai-secret" {
		t.Error("plaintext key must never be written to the config file")
	}

	// No plaintext secret anywhere in the file.
	if containsSecret := string(data); len(containsSecret) > 0 {
		if contains(containsSecret, "zai-secret") {
			t.Error("config file leaked the plaintext key")
		}
	}
}

func TestWriteOpenCodeConfigStandardWritesPermissionPolicy(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Standard mode → deny policy present.
	path, err := WriteOpenCodeConfig(&api.Config{}, nil, "standard")
	if err != nil {
		t.Fatalf("WriteOpenCodeConfig: %v", err)
	}
	var root map[string]any
	data, _ := os.ReadFile(path)
	_ = json.Unmarshal(data, &root)
	perm, ok := root["permission"].(map[string]any)
	if !ok {
		t.Fatalf("standard mode should write a permission policy, got %v", root["permission"])
	}
	if perm["edit"] != "deny" {
		t.Errorf("standard should deny edits, got %v", perm["edit"])
	}

	// Unattended mode → no deny policy (full autonomy via --auto).
	path2, _ := WriteOpenCodeConfig(&api.Config{}, nil, "unattended")
	var root2 map[string]any
	data2, _ := os.ReadFile(path2)
	_ = json.Unmarshal(data2, &root2)
	if root2["permission"] != nil {
		t.Errorf("unattended should not write a permission policy, got %v", root2["permission"])
	}
}

// TestOpenCodeModelsPassesProviderEnv is a regression test for the bug where
// `opencode models <provider>` was invoked without the provider's key env var,
// so known providers (groq/openrouter) reported "Provider not found" and never
// reached the picker. A stub binary echoes back the key it received; the test
// asserts it was passed through.
func TestOpenCodeModelsPassesProviderEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a shell script")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "opencode-stub")
	// Prints "groq/model-<GROQ_API_KEY>" so a missing env var is detectable.
	script := "#!/bin/sh\necho \"$2/model-$GROQ_API_KEY\"\n"
	if err := os.WriteFile(stub, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	got := OpenCodeModels(stub, filepath.Join(dir, "cfg.json"), map[string]string{"GROQ_API_KEY": "gsk_regress"}, "groq")
	want := "groq/model-gsk_regress"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("OpenCodeModels did not pass provider env: got %v, want [%s]", got, want)
	}
}

func TestOpenCodeProviderEnvInjectsKeys(t *testing.T) {
	oldLoadProviderKey := loadProviderKey
	t.Cleanup(func() { loadProviderKey = oldLoadProviderKey })
	loadProviderKey = func(providerID string) string {
		switch providerID {
		case "zai-coding-plan":
			return "test-zai-key"
		case "groq":
			return "test-groq-key"
		default:
			return ""
		}
	}
	cfg := &api.Config{EnabledProviders: []string{"zai-coding-plan", "groq"}}
	env := OpenCodeProviderEnv(cfg)
	if env["QMAX_PC_ZAI_CODING_PLAN"] != "test-zai-key" {
		t.Error("zai env not injected")
	}
	if env["GROQ_API_KEY"] != "test-groq-key" {
		t.Error("groq env not injected")
	}
}

func TestPlaintextKeyInUserOpenCodeConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, "opencode")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", tmp)

	// No config → nothing found.
	if _, found := PlaintextKeyInUserOpenCodeConfig(); found {
		t.Fatal("no config should mean no plaintext key")
	}

	// {env:...} reference is safe → not flagged.
	safe := `{ "provider": { "zai": { "options": { "apiKey": "{env:QMAX_PC_ZAI}" } } } }`
	if err := os.WriteFile(filepath.Join(cfgDir, "opencode.jsonc"), []byte(safe), 0600); err != nil {
		t.Fatal(err)
	}
	if _, found := PlaintextKeyInUserOpenCodeConfig(); found {
		t.Fatal("{env:...} reference must not be flagged as plaintext")
	}

	// Literal key → flagged, with the path.
	leak := `{ "provider": { "zai": { "options": { "apiKey": "sk-literal-secret" } } } }`
	if err := os.WriteFile(filepath.Join(cfgDir, "opencode.jsonc"), []byte(leak), 0600); err != nil {
		t.Fatal(err)
	}
	path, found := PlaintextKeyInUserOpenCodeConfig()
	if !found {
		t.Fatal("literal apiKey should be flagged")
	}
	if path == "" {
		t.Error("should return the offending file path")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
