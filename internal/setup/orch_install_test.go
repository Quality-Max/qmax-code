package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/skills"
	"github.com/Quality-Max/qmax-code/internal/tui"
)

// captureStdout uses a file so output cannot fill a pipe, and all cleanup runs
// even when fn fails the test.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = f
	defer func() {
		os.Stdout = orig
		f.Close()
	}()
	fn()
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// withFileHome points HOME at an existing regular file so every MkdirAll under
// it fails with ENOTDIR — a cheap way to exercise install error paths.
func withFileHome(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "home-is-a-file")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	orig := os.Getenv("HOME")
	os.Setenv("HOME", path)
	t.Cleanup(func() { os.Setenv("HOME", orig) })
}

func TestInstallCCWritesSettingsJSON(t *testing.T) {
	home := withTempHome(t)

	res, err := InstallCC()
	if err != nil {
		t.Fatalf("InstallCC: %v", err)
	}
	if want := filepath.Join(home, ".claude", "settings.json"); res.MCPPath != want {
		t.Fatalf("MCPPath = %q, want %q", res.MCPPath, want)
	}
	if res.AlreadyHadMCP {
		t.Fatal("fresh install must not report an existing MCP entry")
	}
	if !res.MCPWritten {
		t.Fatal("fresh install must report a write")
	}

	data, err := os.ReadFile(res.MCPPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode settings.json: %v (raw: %s)", err, data)
	}
	servers, ok := cfg["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing mcpServers object: %s", data)
	}
	qmax, ok := servers["qmax"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing qmax entry: %s", data)
	}
	if qmax["command"] != "qmax-code" {
		t.Fatalf("command = %v, want qmax-code", qmax["command"])
	}
	args, ok := qmax["args"].([]interface{})
	if !ok || len(args) != 2 || args[0] != "serve" || args[1] != "--mcp" {
		t.Fatalf("args = %v, want [serve --mcp]", qmax["args"])
	}
	if !IsOrchInstalled("cc") {
		t.Fatal("IsOrchInstalled(cc) must detect the written entry")
	}
}

func TestInstallCCPreservesExistingSettings(t *testing.T) {
	home := withTempHome(t)
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	existing := `{"model":"opus","mcpServers":{"other":{"command":"keep-me"},"qmax":{"command":"stale"}}}`
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}

	res, err := InstallCC()
	if err != nil {
		t.Fatalf("InstallCC: %v", err)
	}
	if !res.AlreadyHadMCP {
		t.Fatal("pre-existing qmax entry must set AlreadyHadMCP")
	}

	data, _ := os.ReadFile(path)
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode settings.json: %v", err)
	}
	if cfg["model"] != "opus" {
		t.Fatalf("unrelated top-level key lost: %s", data)
	}
	servers := cfg["mcpServers"].(map[string]interface{})
	if _, ok := servers["other"]; !ok {
		t.Fatalf("sibling MCP server lost: %s", data)
	}
	if servers["qmax"].(map[string]interface{})["command"] != "qmax-code" {
		t.Fatalf("stale qmax entry not refreshed: %s", data)
	}
}

func TestRunOrchInstallsEveryBackend(t *testing.T) {
	for _, backend := range []string{"cc", "codex", "agy"} {
		t.Run(backend, func(t *testing.T) {
			withTempHome(t)
			RunOrch(backend, &tui.Terminal{})
			if !IsOrchInstalled(backend) {
				t.Fatalf("RunOrch(%q) did not install the MCP entry", backend)
			}
		})
	}
}

func TestRunOrchReportsInstallFailureWithoutPanicking(t *testing.T) {
	withFileHome(t)

	for _, backend := range []string{"cc", "codex", "agy"} {
		RunOrch(backend, &tui.Terminal{}) // prints the failure, must not panic
	}
	// Unsupported backends are a silent no-op.
	RunOrch("unknown-backend", &tui.Terminal{})
}

func TestIsOrchInstalledEdgeCases(t *testing.T) {
	home := withTempHome(t)

	if !IsOrchInstalled("unknown") {
		t.Fatal("unknown backend must default to installed so setup is skipped")
	}
	if IsOrchInstalled("cc") {
		t.Fatal("fresh home must report nothing installed")
	}

	ccPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(ccPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ccPath, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if IsOrchInstalled("cc") {
		t.Fatal("invalid JSON must not count as installed")
	}
	if err := os.WriteFile(ccPath, []byte(`{"mcpServers":{"qmax":{}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if !IsOrchInstalled("cc") {
		t.Fatal("qmax entry in settings.json must count as installed")
	}

	cxPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cxPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cxPath, []byte("[mcp_servers.other]\ncommand = \"x\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if IsOrchInstalled("codex") {
		t.Fatal("codex config without the qmax table must not count as installed")
	}
}

func TestInstallSkillsMaterializesCatalog(t *testing.T) {
	home := withTempHome(t)

	res, err := InstallSkills("cc")
	if err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}
	catalog := skills.SortedCatalog()
	if len(catalog) == 0 {
		t.Fatal("catalog is empty; test cannot assert anything")
	}
	if len(res.Written) != len(catalog) {
		t.Fatalf("wrote %d skills, catalog has %d", len(res.Written), len(catalog))
	}
	if res.Dir != filepath.Join(home, ".claude", "skills") {
		t.Fatalf("Dir = %q, want cc skills dir under temp home", res.Dir)
	}
	skillMD := filepath.Join(res.Dir, catalog[0].Name, "SKILL.md")
	if _, err := os.Stat(skillMD); err != nil {
		t.Fatalf("catalog skill not materialized at %s: %v", skillMD, err)
	}
}

func TestInstallSkillsUnknownBackendFails(t *testing.T) {
	withTempHome(t)
	if _, err := InstallSkills("not-a-backend"); err == nil {
		t.Fatal("unknown backend must fail Materialize")
	}
}

func TestInstallSkillsReportSurfacesFailure(t *testing.T) {
	withFileHome(t)
	// Non-fatal by design: prints the error and returns without panicking.
	InstallSkillsReport("cc", &tui.Terminal{})
}

func TestInstallSkillsAllMaterializesEveryBackend(t *testing.T) {
	home := withTempHome(t)

	InstallSkillsAll(&tui.Terminal{})

	for _, dir := range []string{
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".config", "opencode", "skills"),
		filepath.Join(home, ".gemini", "antigravity-cli", "skills"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("skills dir missing for %s: %v", dir, err)
		}
		if len(entries) == 0 {
			t.Fatalf("no skills written under %s", dir)
		}
	}
}

func TestPrintSkillsStatusMarksInstalledBackends(t *testing.T) {
	withTempHome(t)
	if _, err := InstallSkills("cc"); err != nil {
		t.Fatalf("seed cc skills: %v", err)
	}

	out := captureStdout(t, func() { PrintSkillsStatus(&tui.Terminal{}) })

	catalog := skills.SortedCatalog()
	if len(catalog) == 0 {
		t.Fatal("catalog is empty; test cannot assert anything")
	}
	if !strings.Contains(out, catalog[0].Name) {
		t.Fatalf("status output missing catalog skill %q:\n%s", catalog[0].Name, out)
	}
	if !strings.Contains(out, "✓") {
		t.Fatalf("installed cc skills not check-marked:\n%s", out)
	}
	if !strings.Contains(out, "·") {
		t.Fatalf("uninstalled backends not dot-marked:\n%s", out)
	}
}
