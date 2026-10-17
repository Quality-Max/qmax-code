package repl

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/agent"
	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/session"
	"github.com/Quality-Max/qmax-code/internal/setup"
	"github.com/Quality-Max/qmax-code/internal/tui"
	"github.com/Quality-Max/qmax-code/internal/update"
)

// pickModel makes /orch return the given selection.
func pickModel(backend, model, effort string) {
	showModelPicker = func(tui.ModelPickerOpts) tui.ModelPickerResult {
		return tui.ModelPickerResult{Backend: backend, ModelID: model, Effort: effort, Confirmed: true}
	}
}

func TestOrchCancelAndPickerOptions(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cfg := api.DefaultConfig()
	cfg.Backend = "cerebras"
	cfg.CerebrasModel = api.CerebrasGemma4Model
	cfg.CerebrasReasoningEffort = "none"
	ag := newTestAgent(cfg, nil)
	var seen tui.ModelPickerOpts
	showModelPicker = func(opts tui.ModelPickerOpts) tui.ModelPickerResult {
		seen = opts
		return tui.ModelPickerResult{}
	}

	runREPL(t, ag, nil, "/orch")

	if seen.CurrentBackend != "cerebras" || seen.Effort != "low" || seen.CurrentModelID != api.CerebrasGemma4Model {
		t.Fatalf("picker opts = %+v", seen)
	}
	if len(seen.OpenCodeModels) == 0 || !strings.HasPrefix(seen.OpenCodeModels[0].ModelID, "enable:") {
		t.Fatalf("with no providers enabled the picker should offer setup rows: %+v", seen.OpenCodeModels)
	}

	cfg.Backend = "codex"
	cfg.CodexModel = "gpt-5.6"
	runREPL(t, ag, nil, "/orch")
	if seen.CurrentModelID != "gpt-5.6" {
		t.Fatalf("codex picker model = %q", seen.CurrentModelID)
	}

	ag.Mode = agent.OllamaModeFull
	runREPL(t, ag, nil, "/orch")
	if seen.CurrentBackend != "ollama" {
		t.Fatalf("ollama mode picker backend = %q", seen.CurrentBackend)
	}
}

func TestOrchListsEnabledProviderModelsWhenOpenCodeIsInstalled(t *testing.T) {
	for _, installed := range []bool{false, true} {
		name := "missing CLI"
		if installed {
			name = "installed CLI"
		}
		t.Run(name, func(t *testing.T) {
			home := replEnv(t)
			stubSeams(t)
			cfg := api.DefaultConfig()
			cfg.EnabledProviders = []string{"groq"}
			ag := newTestAgent(cfg, nil)
			if installed {
				bin := writeStub(t, filepath.Join(home, "bin"), "opencode", `printf '%s\n' 'groq/test-model'`)
				findOpenCode = func() string { return bin }
			}
			var seen tui.ModelPickerOpts
			showModelPicker = func(opts tui.ModelPickerOpts) tui.ModelPickerResult {
				seen = opts
				return tui.ModelPickerResult{}
			}

			out := runREPL(t, ag, nil, "/orch")
			if got := strings.Contains(out, "Querying opencode models"); got != installed {
				t.Fatalf("model-query notice = %v, installed = %v", got, installed)
			}
			if seen.OpenCodeInstalled != installed {
				t.Fatalf("picker CLI availability = %v, want %v", seen.OpenCodeInstalled, installed)
			}
			if installed && (len(seen.OpenCodeModels) != 1 || seen.OpenCodeModels[0].ModelID != "groq/test-model") {
				t.Fatalf("enabled provider models did not reach the picker: %+v", seen.OpenCodeModels)
			}
		})
	}
}

func TestOrchSelectsOllama(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cfg := api.DefaultConfig()
	ag := newTestAgent(cfg, nil)
	pickModel("ollama", "", "")

	out := runREPL(t, ag, nil, "/orch")
	assertContains(t, out, "Ollama not configured.")

	cfg.OllamaURL, cfg.OllamaModel = "file:///etc/passwd", "llama3.2:3b"
	out = runREPL(t, ag, nil, "/orch")
	assertContains(t, out, "Ollama URL rejected")

	cfg.OllamaURL = "https://user:secret@llm.example.com"
	cli := &fakeCLI{}
	out = runREPL(t, ag, cli, "/orch")
	assertContains(t, out, "Backend: Ollama  model: llama3.2:3b")
	if strings.Contains(out, "secret") {
		t.Fatal("Ollama URL credentials were printed")
	}
	if ag.Mode != agent.OllamaModeFull || cli.cleaned == 0 {
		t.Fatalf("mode=%v cleaned=%d", ag.Mode, cli.cleaned)
	}
}

func TestOrchSelectsCerebras(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cfg := api.DefaultConfig()
	ag := newTestAgent(cfg, nil)
	pickModel("cerebras", "gemma", "high")

	out := runREPL(t, ag, nil, "/orch")
	assertContains(t, out, "No key entered — backend not changed.")

	readSecret = func(string) string { return "two words" }
	out = runREPL(t, ag, nil, "/orch")
	assertContains(t, out, "Backend not changed.")

	readSecret = func(string) string { return "csk-test-key" }
	out = runREPL(t, ag, &fakeCLI{}, "/orch")
	assertContains(t, out, "Saved to OS keychain.", "Backend: Cerebras", "reasoning: high")
	if cfg.Backend != "cerebras" || ag.Cerebras == nil {
		t.Fatalf("backend=%q cerebras=%v", cfg.Backend, ag.Cerebras)
	}

	pickModel("cerebras", api.CerebrasDefaultModel, "")
	out = runREPL(t, ag, nil, "/orch")
	assertContains(t, out, "Backend: Cerebras  model: "+api.CerebrasDefaultModel)
}

func TestOrchSelectsAnthropicAPI(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cfg := api.DefaultConfig()
	ag := newTestAgent(cfg, nil)
	pickModel("", "auto", "medium")

	out := runREPL(t, ag, nil, "/orch")
	assertContains(t, out, "Anthropic API is not configured.")

	ag.Cfg.AnthropicKey = "sk-ant-test"
	out = runREPL(t, ag, &fakeCLI{}, "/orch")
	assertContains(t, out, "Backend: Anthropic API  model: auto")
	if !ag.Cfg.AutoRoute || cfg.DefaultModel != "auto" || cfg.Effort != "medium" {
		t.Fatalf("auto route=%v default=%q effort=%q", ag.Cfg.AutoRoute, cfg.DefaultModel, cfg.Effort)
	}
}

func TestOrchCLIBackends(t *testing.T) {
	home := replEnv(t)
	stubSeams(t)
	bin := filepath.Join(home, "bin")
	cfg := api.DefaultConfig()
	ag := newTestAgent(cfg, nil)

	for _, backend := range []string{"cc", "codex", "agy", "opencode"} {
		pickModel(backend, "", "low")
		out := runREPL(t, ag, nil, "/orch")
		assertContains(t, out, "not found")
	}

	claude := writeStub(t, bin, "claude", "exit 0")
	codex := writeStub(t, bin, "codex", "exit 0")
	agy := writeStub(t, bin, "agy", "exit 0")
	opencode := writeStub(t, bin, "opencode", "exit 0")
	findClaudeCode = func() string { return claude }
	findCodex = func() string { return codex }
	findAgy = func() string { return agy }
	findOpenCode = func() string { return opencode }

	pickModel("cc", "opus", "high")
	out := runREPL(t, ag, nil, "/orch")
	assertContains(t, out, "Backend not changed.")

	promptOrchConsent = func(*api.Config, string) setup.ConsentResult {
		return setup.ConsentResult{Proceed: true, PermissionMode: "unattended", GlobalInstall: true}
	}
	logins := 0
	promptAgyLogin = func(string) { logins++ }

	cases := []struct {
		backend, model, want string
	}{
		{"cc", "opus", "Backend: Claude Code  model: opus  effort: high"},
		{"codex", "gpt-5.6", "Backend: Codex  model: gpt-5.6"},
		{"codex", "", "Backend: Codex  model: Codex config"},
		{"agy", "", "Backend: Antigravity  model: Antigravity config"},
		{"opencode", "groq/llama", "Backend: opencode  model: groq/llama"},
	}
	for _, tc := range cases {
		pickModel(tc.backend, tc.model, "high")
		out := runREPL(t, ag, &fakeCLI{}, "/orch")
		assertContains(t, out, tc.want)
		if cfg.Backend != tc.backend || ag.Cfg.Context.Backend != tc.backend {
			t.Fatalf("%s: backend = %q/%q", tc.backend, cfg.Backend, ag.Cfg.Context.Backend)
		}
		if cfg.OrchPermissionMode != "unattended" {
			t.Fatalf("%s: permission mode = %q", tc.backend, cfg.OrchPermissionMode)
		}
	}
	if cfg.CodexModel != "" {
		t.Fatalf("codex model = %q, want empty after default selection", cfg.CodexModel)
	}
	if logins != 1 {
		t.Fatalf("agy login prompts = %d, want 1", logins)
	}

	out = runREPL(t, ag, nil, "/reconnect")
	assertContains(t, out, "No CC/Codex/Antigravity MCP transport is active")
}

func TestOrchEnablesProviderFromSetupRow(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cfg := api.DefaultConfig()
	ag := newTestAgent(cfg, nil)
	pickModel("opencode-enable", "groq", "")
	readSecret = func(string) string { return "gsk_testkey" }

	out := runREPL(t, ag, nil, "/orch")

	assertContains(t, out, "Enabled Groq", "opencode CLI isn't installed")
	if !cfg.IsProviderEnabled("groq") {
		t.Fatal("groq provider was not enabled")
	}
}

func TestProvidersCommand(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cfg := api.DefaultConfig()
	ag := newTestAgent(cfg, nil)

	out := runREPL(t, ag, nil,
		"/providers enable",
		"/providers disable",
		"/providers frob x",
		"/providers enable nope",
		"/providers disable nope",
		"/providers enable groq",
	)
	assertContains(t, out,
		"Usage: /providers enable <id>",
		"Usage: /providers disable <id>",
		`Unknown subcommand "frob"`,
		`Unknown provider "nope". See /providers`,
		`Unknown provider "nope".`,
		"No key entered — provider not enabled.",
	)

	readSecret = func(string) string { return "bad key" }
	out = runREPL(t, ag, nil, "/providers enable openrouter")
	assertContains(t, out, "Not enabled.")

	readSecret = func(string) string { return "unprefixed-key" }
	out = runREPL(t, ag, nil, "/providers enable openrouter")
	assertContains(t, out, "saving anyway", "Enabled OpenRouter")

	var items []tui.ProviderPickerItem
	showProviderPicker = func(in []tui.ProviderPickerItem) tui.ProviderPickerResult {
		items = in
		return tui.ProviderPickerResult{ID: "zai-coding-plan", Confirmed: true}
	}
	t.Setenv("OPENROUTER_API_KEY", "sk-or-env")
	readSecret = func(string) string { return "" }
	out = runREPL(t, ag, nil, "/providers")
	statuses := map[string]string{}
	for _, it := range items {
		statuses[it.ID] = it.Status
	}
	if statuses["openrouter"] != "enabled" || statuses["groq"] != "available" {
		t.Fatalf("provider statuses = %v", statuses)
	}
	assertContains(t, out, "provider not enabled")

	out = runREPL(t, ag, nil, "/providers disable openrouter")
	assertContains(t, out, "Disabled openrouter.")
	if cfg.IsProviderEnabled("openrouter") {
		t.Fatal("openrouter still enabled after disable")
	}
}

func TestRunBannerAndUpdateProposal(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	id := session.GenerateSessionID()
	if err := session.SaveSession(id, []api.Message{{Role: "user", Content: "hi"}}, 0, api.TokenUsage{}, api.ModelSonnet, api.ConversationState{}); err != nil {
		t.Fatal(err)
	}
	maybeCheckUpdate = func(string) *update.Release { return &update.Release{Version: "99.0.0", Asset: "http://127.0.0.1:1/x"} }
	answers := []string{"n", "yes"}
	readConsent = func(*tui.Terminal) (string, error) {
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	ag := newTestAgent(nil, &api.SessionContext{Backend: "cc"})

	for i := 0; i < 2; i++ {
		scriptInputs(t)
		out := captureOutput(t, func() { Run(ag, nil, false, "1.0.0", nil) })
		assertContains(t, out, "Session: ", "Update available: 99.0.0")
		if i == 0 {
			assertContains(t, out, "Recent session: "+id, "Skipped — /update checks again anytime.", "Agent SDK")
		} else {
			assertContains(t, out, "Update failed")
		}
	}
}

func TestRunSkillsCommands(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	ag := newTestAgent(nil, nil)

	out := runREPL(t, ag, nil, "/skills", "/skills install")

	if !strings.Contains(strings.ToLower(out), "skill") {
		t.Fatalf("skills output missing:\n%s", out)
	}
}

func TestRunImagesWithoutCLIUseEmbeddedBackend(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	ag := newTestAgent(nil, nil)
	ag.Cfg.AnthropicKey = "sk-ant-test"
	img := &tui.ImageAttachment{MediaType: "image/png", Data: "aGk=", FileName: "shot.png"}
	captureScreenshot = func() (*tui.ImageAttachment, error) { return img, nil }
	// A cancelled context makes the embedded request fail fast without network.
	runTurnViewport = func(_ *tui.Terminal, _ string, _ *tui.StatusInfo, cancel func(), run func()) tui.TurnInputResult {
		done := make(chan struct{})
		go func() {
			defer close(done)
			run()
		}()
		select {
		case <-done:
		case <-time.After(50 * time.Millisecond):
			cancel()
			<-done
		}
		return tui.TurnInputResult{}
	}

	out := runREPL(t, ag, nil, "/screenshot")

	assertContains(t, out, "Attached 1 image(s): shot.png", "offline test transport")
}
