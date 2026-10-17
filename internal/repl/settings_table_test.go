package repl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/agent"
	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/tui"
)

func TestApplySettingValueTable(t *testing.T) {
	home := replEnv(t)
	stubSeams(t)
	t.Cleanup(func() { tui.ApplyTheme(tui.ThemeByName("")) })
	claude := writeStub(t, filepath.Join(home, "bin"), "claude", "exit 0")

	type check func(*testing.T, *agent.Agent)
	cases := []struct {
		key, value string
		local      bool
		want       settingResult
		check      check
	}{
		{key: "local_only", value: "on", want: settingApplied, check: func(t *testing.T, ag *agent.Agent) {
			if !ag.AppConfig.LocalOnly {
				t.Error("local_only not set")
			}
		}},
		{key: "local", value: "off", want: settingApplied},
		{key: "local-only", value: "maybe", want: settingInvalid},
		{key: "model", value: "opus", want: settingApplied, check: func(t *testing.T, ag *agent.Agent) {
			if ag.AppConfig.DefaultModel == "" || ag.AppConfig.DefaultModel == "auto" {
				t.Errorf("model = %q", ag.AppConfig.DefaultModel)
			}
		}},
		{key: "model", value: "gpt-4", want: settingInvalid},
		{key: "project", value: "5", local: true, want: settingInvalid},
		{key: "professional", value: "yes", want: settingApplied, check: func(t *testing.T, ag *agent.Agent) {
			if !ag.Cfg.Professional {
				t.Error("professional not applied to the live agent")
			}
		}},
		{key: "professional", value: "no", want: settingApplied},
		{key: "professional", value: "?", want: settingInvalid},
		{key: "autosave", value: "1", want: settingApplied},
		{key: "autosave", value: "0", want: settingApplied},
		{key: "autosave", value: "x", want: settingInvalid},
		{key: "output", value: "verbose", want: settingApplied, check: func(t *testing.T, ag *agent.Agent) {
			if !ag.Cfg.OutputVerbose {
				t.Error("verbose output not applied")
			}
		}},
		{key: "output-verbose", value: "compact", want: settingApplied},
		{key: "output_verbose", value: "loud", want: settingInvalid},
		{key: "cloud_sync", value: "on", local: true, want: settingInvalid},
		{key: "cloudsync", value: "nah", want: settingInvalid},
		{key: "live_feed", value: "on", want: settingApplied, check: func(t *testing.T, ag *agent.Agent) {
			if !ag.Cfg.Context.LiveFeed {
				t.Error("live feed not applied to the session")
			}
		}},
		{key: "live-feed", value: "off", want: settingApplied},
		{key: "livefeed", value: "sometimes", want: settingInvalid},
		{key: "live_feed", value: "on", local: true, want: settingInvalid},
		{key: "budget", value: "-1", want: settingInvalid},
		{key: "apikey", value: "qm-x", local: true, want: settingInvalid},
		{key: "apikey", value: "", want: settingInvalid},
		{key: "ollama", value: "on", want: settingInvalid},
		{key: "ollama", value: "maybe", want: settingInvalid},
		{key: "ollama", value: "off", want: settingAppliedNoSave},
		{key: "backend", value: "cc", want: settingInvalid},
		{key: "backend", value: "codex", want: settingInvalid},
		{key: "backend", value: "agy", want: settingInvalid},
		{key: "backend", value: "api", want: settingApplied},
		{key: "backend", value: "cerebras", want: settingInvalid},
		{key: "cerebras_model", value: "gemma", want: settingApplied, check: func(t *testing.T, ag *agent.Agent) {
			if ag.AppConfig.CerebrasModel != api.CerebrasGemma4Model {
				t.Errorf("cerebras model = %q", ag.AppConfig.CerebrasModel)
			}
		}},
		{key: "cerebras_reasoning_effort", value: "high", want: settingApplied},
		{key: "cerebras-reasoning-effort", value: "", want: settingApplied},
		{key: "cerebras_reasoning_effort", value: "ultra", want: settingInvalid},
		{key: "vision_sidecar", value: " ALWAYS ", want: settingApplied, check: func(t *testing.T, ag *agent.Agent) {
			if ag.AppConfig.VisionSidecar != api.VisionSidecarAlways {
				t.Error("vision sidecar setting was not normalized")
			}
		}},
		{key: "vision-sidecar", value: "off", want: settingApplied},
		{key: "vision_sidecar", value: "sometimes", want: settingInvalid},
		{key: "theme", value: "no-such-theme", want: settingInvalid},
		{key: "theme", value: tui.ThemeNames()[0], want: settingApplied},
		{key: "anthropic_key", value: "sk-ant-api03-table", want: settingAppliedNoSave, check: func(t *testing.T, ag *agent.Agent) {
			if ag.Cfg.AnthropicKey != "sk-ant-api03-table" {
				t.Error("anthropic key not applied")
			}
		}},
		{key: "frobnicate", value: "1", want: settingInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			ag := newTestAgent(nil, &api.SessionContext{LocalOnly: tc.local})
			var got settingResult
			captureOutput(t, func() { got = applySettingValue(tc.key, tc.value, ag, &tui.Terminal{}) })
			if got != tc.want {
				t.Fatalf("applySettingValue(%q, %q) = %v, want %v", tc.key, tc.value, got, tc.want)
			}
			if tc.check != nil {
				tc.check(t, ag)
			}
		})
	}

	t.Run("backend cc when installed", func(t *testing.T) {
		findClaudeCode = func() string { return claude }
		ag := newTestAgent(nil, nil)
		captureOutput(t, func() {
			if got := applySettingValue("backend", "cc", ag, &tui.Terminal{}); got != settingApplied || ag.AppConfig.Backend != "cc" {
				t.Fatalf("backend cc = %v (%q)", got, ag.AppConfig.Backend)
			}
		})
		findCodex = func() string { return claude }
		findAgy = func() string { return claude }
		captureOutput(t, func() {
			applySettingValue("backend", "codex", ag, &tui.Terminal{})
			applySettingValue("backend", "agy", ag, &tui.Terminal{})
		})
		if ag.AppConfig.Backend != "agy" {
			t.Fatalf("backend = %q, want agy", ag.AppConfig.Backend)
		}
	})

	t.Run("ollama on and live cerebras updates", func(t *testing.T) {
		cfg := api.DefaultConfig()
		cfg.OllamaURL, cfg.OllamaModel = "http://127.0.0.1:11434", "llama3.2:3b"
		ag := newTestAgent(cfg, nil)
		ag.Cerebras = &agent.CerebrasClient{}
		captureOutput(t, func() {
			if got := applySettingValue("ollama", "enabled", ag, &tui.Terminal{}); got != settingAppliedNoSave || ag.Ollama == nil {
				t.Fatalf("ollama on = %v", got)
			}
			applySettingValue("cerebras_model", "gemma", ag, &tui.Terminal{})
			applySettingValue("cerebras_reasoning_effort", "low", ag, &tui.Terminal{})
		})
		if ag.Cerebras.Model != api.CerebrasGemma4Model || ag.Cerebras.ReasoningEffort != "low" {
			t.Fatalf("live cerebras = %+v", ag.Cerebras)
		}
		cfg.OllamaURL = "javascript:alert(1)"
		captureOutput(t, func() {
			if got := applySettingValue("ollama", "on", ag, &tui.Terminal{}); got != settingInvalid {
				t.Fatalf("bad ollama URL = %v", got)
			}
		})
	})

	t.Run("ollama off refused without fallback", func(t *testing.T) {
		ag := newTestAgent(nil, &api.SessionContext{LocalOnly: true})
		captureOutput(t, func() {
			if got := applySettingValue("ollama", "off", ag, &tui.Terminal{}); got != settingInvalid {
				t.Fatalf("ollama off with no fallback = %v", got)
			}
		})
	})

	t.Run("nil config is created", func(t *testing.T) {
		ag := newTestAgent(nil, nil)
		ag.AppConfig = nil
		captureOutput(t, func() { applySettingValue("budget", "10", ag, &tui.Terminal{}) })
		if ag.AppConfig == nil || ag.AppConfig.MaxTokenBudget != 10 {
			t.Fatal("applySettingValue did not create a config")
		}
	})
}

func TestHandleSetCommandPromptsForSecrets(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	ag := newTestAgent(nil, nil)
	term := &tui.Terminal{}

	out := captureOutput(t, func() {
		handleSetCommand("/set anthropic_key", ag, term)
		handleSetCommand("/set budget", ag, term)
	})
	assertContains(t, out, "No value entered — nothing changed.", "Usage: /set <key> <value>")

	readSecret = func(string) string { return "sk-ant-api03-hidden" }
	captureOutput(t, func() { handleSetCommand("/set anthropic-key", ag, term) })
	if ag.Cfg.AnthropicKey != "sk-ant-api03-hidden" {
		t.Fatal("hidden prompt value was not applied")
	}

	ag.AppConfig = nil
	out = captureOutput(t, func() { applyAndSaveSetting("budget", "77", ag, term) })
	assertContains(t, out, "api.Config saved")
}

func TestRunSettingsPickerBranches(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	ag := newTestAgent(nil, nil)
	ag.AppConfig = nil
	term := &tui.Terminal{}

	out := captureOutput(t, func() { runSettingsPicker(ag, term) })
	assertContains(t, out, "Settings unchanged.")

	showSettingsPicker = func(rows []tui.SettingsRow) tui.SettingsPickerResult {
		for _, r := range rows {
			if r.Display != nil {
				_ = r.Display("")
				_ = r.Display("true")
				_ = r.Display("verbose")
			}
		}
		return tui.SettingsPickerResult{Confirmed: true, Changes: map[string]string{"budget": "bogus"}}
	}
	out = captureOutput(t, func() { runSettingsPicker(ag, term) })
	if strings.Contains(out, "api.Config saved") {
		t.Fatalf("invalid change should not save:\n%s", out)
	}
}

func TestPrintConfigInfoVariants(t *testing.T) {
	replEnv(t)
	term := &tui.Terminal{}
	out := captureOutput(t, func() { printConfigInfo(nil, nil, term) })
	assertContains(t, out, "No config loaded")

	cfg := api.DefaultConfig()
	on, off := true, false
	cfg.CloudSync = &on
	cfg.LiveFeed = true
	cfg.OutputVerbose = true
	cfg.OllamaURL, cfg.OllamaModel = "https://u:p@llm.example.com", "m"
	out = captureOutput(t, func() { printConfigInfo(cfg, &api.SessionContext{LocalOnly: true}, term) })
	assertContains(t, out, "standalone local-only", "verbose", "enabled", "on (test/crawl", "Ollama model:")

	cfg.CloudSync = &off
	out = captureOutput(t, func() { printConfigInfo(cfg, nil, term) })
	assertContains(t, out, "disabled")
}

func TestToggleOutputVerboseCreatesConfig(t *testing.T) {
	replEnv(t)
	toggleOutputVerbose(nil, nil, &tui.Terminal{})
	ag := newTestAgent(nil, nil)
	ag.AppConfig = nil
	out := captureOutput(t, func() { toggleOutputVerbose(ag, nil, &tui.Terminal{}) })
	if ag.AppConfig == nil || !ag.AppConfig.OutputVerbose {
		t.Fatal("toggle did not create and flip the config")
	}
	assertContains(t, out, "Output mode: verbose")

	t.Setenv("HOME", "/dev/null/nope")
	out = captureOutput(t, func() { toggleOutputVerbose(ag, nil, &tui.Terminal{}) })
	assertContains(t, out, "could not save config")
}

func TestHandleDisconnectClearsLegacyToken(t *testing.T) {
	home := replEnv(t)
	legacy := filepath.Join(home, ".qamax", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"token":"t","api_key":"k","cloud_url":"u"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ag := newTestAgent(nil, &api.SessionContext{Auth: &api.AuthConfig{APIKey: "qm-k", Email: "me@example.com"}})

	out := captureOutput(t, func() { handleDisconnect(ag, &tui.Terminal{}) })

	assertContains(t, out, "Disconnected from me@example.com")
	data, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["token"] != "" || m["api_key"] != "" || m["cloud_url"] != "u" {
		t.Fatalf("legacy config = %v", m)
	}
}

func TestReconnectMCPTransportPerBackend(t *testing.T) {
	replEnv(t)
	ctx := &api.SessionContext{}
	term := &tui.Terminal{}
	for _, cli := range []agent.CLIAgent{
		agent.NewCCAgent("claude", "", "", "standard", false, "", ctx),
		agent.NewCodexAgent("codex", "", "", false, "", ctx),
		agent.NewAgyAgent("agy", "", "", "standard", false, "", ctx),
	} {
		out := captureOutput(t, func() { reconnectMCPTransport(cli, term) })
		if !strings.Contains(out, "transport restored") && !strings.Contains(out, "Could not restore") {
			t.Fatalf("%T: unexpected output %q", cli, out)
		}
	}
}

func TestFormatDurationAndLabels(t *testing.T) {
	cases := []struct{ got, want string }{
		{formatDuration(42 * time.Second), "42s"},
		{formatDuration(5 * time.Minute), "5m"},
		{formatDuration(3 * time.Hour), "3h"},
		{codexModelLabel(""), "Codex config"},
		{codexModelLabel("m"), "m"},
		{agyModelLabel(""), "Antigravity config"},
		{agyModelLabel("g"), "g"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if isPlanBackend("api") || !isPlanBackend("agy") {
		t.Error("isPlanBackend")
	}
	if currentBackend(nil) != "" {
		t.Error("currentBackend(nil)")
	}
}
