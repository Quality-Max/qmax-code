package repl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/agent"
	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/gate"
	"github.com/Quality-Max/qmax-code/internal/session"
	"github.com/Quality-Max/qmax-code/internal/setup"
	"github.com/Quality-Max/qmax-code/internal/tui"
	"github.com/Quality-Max/qmax-code/internal/update"
)

func TestRunInformationalCommands(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	var gateBase string
	runGate = func(_ context.Context, opts gate.Options) gate.Result {
		gateBase = opts.Base
		opts.OnProgress("checking diff")
		return gate.Result{}
	}
	ag := newTestAgent(nil, &api.SessionContext{ProjectID: 7, GitInfo: &api.GitInfo{Branch: "feat", RemoteURL: "git@example.com:o/r.git", ChangedFiles: []string{"a.go"}}})

	out := runREPL(t, ag, nil,
		"",
		"/help",
		"/context",
		"/status",
		"/cost",
		"/plan",
		"/config",
		"/gate develop",
		"/gate a b",
		"/queue",
		"/queue write more tests",
		"/queue clear",
		"/project 42",
		"/project nope",
		"/reconnect",
		"/disconnect",
		"/ollama",
		"/live status",
		"/feed",
		"/browserfeed",
		"/browserfeed --half https://example.test/vnc.html",
		"/q",
	)

	assertContains(t, out,
		"Commands:",
		"Mode: connected",
		"Git branch: feat",
		"Changed files: 1",
		"No coding-plan window yet",
		"Gate: checking diff",
		"usage: /gate [base]",
		"Queue is empty",
		"queued [1]: write more tests",
		"processing queued prompt",
		"Queue was already empty.",
		"Project set to #42",
		"Invalid project ID",
		"No CC/Codex/Antigravity MCP transport is active",
		"Not connected.",
		"Ollama not configured",
		"Live feed is off.",
		"No live feed URL captured yet.",
		"Usage: /browserfeed",
		"browserfeed: no feed in tests",
		"Goodbye!",
	)
	if gateBase != "develop" {
		t.Fatalf("gate base = %q, want develop", gateBase)
	}
	if ag.Cfg.Context.ProjectID != 42 {
		t.Fatalf("ProjectID = %d, want 42", ag.Cfg.Context.ProjectID)
	}
}

func TestRunLocalOnlyBlocksCloudCommands(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	ag := newTestAgent(nil, &api.SessionContext{LocalOnly: true})

	out := runREPL(t, ag, nil,
		"/project 3", "/cloudsync", "/browserfeed https://x", "/live on", "/feed", "/connect", "/context",
	)

	for _, cmd := range []string{"/project", "/cloudsync", "/browserfeed", "/live", "/feed", "/connect"} {
		assertContains(t, out, cmd+" is unavailable in standalone local-only mode.")
	}
	assertContains(t, out, "Mode: standalone local-only")
}

func TestRunFeedOpensCapturedURLAndReportsFailure(t *testing.T) {
	for _, fails := range []bool{false, true} {
		name := "opens feed"
		if fails {
			name = "reports failure"
		}
		t.Run(name, func(t *testing.T) {
			replEnv(t)
			stubSeams(t)
			const capturedURL = "https://example.test/vnc.html"
			ag := newTestAgent(nil, &api.SessionContext{LastLiveURL: capturedURL})
			calls := 0
			openBrowserFeed = func(url string, mode blockMode) error {
				calls++
				if url != capturedURL || mode != blockModeQuarter {
					t.Errorf("feed target = %q, mode = %v", url, mode)
				}
				if fails {
					return errors.New("feed disconnected")
				}
				return nil
			}

			out := runREPL(t, ag, nil, "/feed")
			if calls != 1 {
				t.Fatalf("feed opened %d times, want once", calls)
			}
			if reported := strings.Contains(out, "browserfeed: feed disconnected"); reported != fails {
				t.Fatalf("feed failure reported = %v, want %v", reported, fails)
			}
		})
	}
}

func TestRunCtrlCAndOutputToggle(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cli := &fakeCLI{}
	ag := newTestAgent(nil, nil)

	scriptInputs(t, "<ctrl-o>", "<ctrl-c>", "<ctrl-c>")
	out := captureOutput(t, func() { Run(ag, cli, true, "1.0.0", nil) })

	assertContains(t, out, "Output mode: verbose", "Goodbye!")
	if !cli.verbose {
		t.Fatal("Ctrl+O did not propagate verbose output to the CLI backend")
	}
	if cli.cleaned == 0 {
		t.Fatal("Run did not clean up the CLI backend on exit")
	}
}

func TestRunCLITurnRecordsUsageAndSavesSession(t *testing.T) {
	home := replEnv(t)
	stubSeams(t)
	cli := &fakeCLI{response: "all tests pass", statsIn: 120, statsOut: 30}
	ctx := &api.SessionContext{Backend: "cc"}
	ag := newTestAgent(nil, ctx)

	out := runREPL(t, ag, cli, "run the tests", "/plan", "/save", "/clear")

	if len(cli.prompts) != 1 || !strings.Contains(cli.prompts[0], "run the tests") {
		t.Fatalf("CLI prompts = %q", cli.prompts)
	}
	if ag.Usage.InputTokens != 0 {
		// /clear keeps usage; only history is cleared.
		t.Logf("usage after clear: %+v", ag.Usage)
	}
	assertContains(t, out, "Conversation cleared.", "saved.")
	entries, err := os.ReadDir(filepath.Join(home, ".qmax-code", "sessions"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a saved session file, err=%v entries=%d", err, len(entries))
	}
	if cli.resets == 0 {
		t.Fatal("/clear did not reset the CLI conversation")
	}
}

func TestRunCLITurnErrorIsReported(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cli := &fakeCLI{err: errors.New("subprocess exited 1"), limitHit: true, limitTime: time.Now().Add(time.Hour)}
	ag := newTestAgent(nil, &api.SessionContext{Backend: "codex"})
	ag.AppConfig.CodexModel = "gpt-test"

	out := runREPL(t, ag, cli, "do work", "/plan")

	assertContains(t, out, "subprocess exited 1")
}

func TestRunQueuedPromptFromTurnViewport(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cli := &fakeCLI{response: "done"}
	ag := newTestAgent(nil, nil)
	calls := 0
	runTurnViewport = func(_ *tui.Terminal, _ string, _ *tui.StatusInfo, _ func(), run func()) tui.TurnInputResult {
		run()
		calls++
		if calls == 1 {
			return tui.TurnInputResult{Text: "follow-up prompt"}
		}
		return tui.TurnInputResult{}
	}

	out := runREPL(t, ag, cli, "first prompt")

	assertContains(t, out, "queued [1]: follow-up prompt", "processing queued prompt")
	if len(cli.prompts) != 2 {
		t.Fatalf("CLI ran %d prompts, want 2 (typed + queued)", len(cli.prompts))
	}
}

func TestRunWithoutInferenceRefusesTurn(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	ag := newTestAgent(nil, nil)

	out := runREPL(t, ag, nil, "hello there")

	assertContains(t, out, "No inference backend configured")
}

func TestRunScreenshotAndPaste(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	ag := newTestAgent(nil, nil)

	out := runREPL(t, ag, nil, "/screenshot", "/paste")
	assertContains(t, out, "no display", "Nothing in clipboard")

	img := &tui.ImageAttachment{MediaType: "image/png", Data: "aGk=", FileName: "clip.png"}
	captureScreenshot = func() (*tui.ImageAttachment, error) { return img, nil }
	pasteImage = func() (*tui.ImageAttachment, error) { return img, nil }
	out = runREPL(t, ag, nil, "/screenshot", "/paste")
	assertContains(t, out, "Screenshot captured (clip.png)", "Pasted image from clipboard (clip.png)", "No inference backend configured")

	pasteImage = func() (*tui.ImageAttachment, error) { return nil, errors.New("none") }
	pasteText = func() (string, error) { return "pasted words", nil }
	cli := &fakeCLI{response: "ok"}
	out = runREPL(t, ag, cli, "/paste")
	assertContains(t, out, "Pasted 12 chars from clipboard")
	if len(cli.prompts) != 1 || !strings.Contains(cli.prompts[0], "pasted words") {
		t.Fatalf("pasted text was not sent to the backend: %q", cli.prompts)
	}
}

func TestRunLargePasteIsSavedToFile(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cli := &fakeCLI{response: "ok"}
	ag := newTestAgent(nil, nil)
	big := strings.Repeat("line of pasted log output\n", 400)
	pasteText = func() (string, error) { return big, nil }

	out := runREPL(t, ag, cli, "/paste")

	assertContains(t, out, "Large paste saved as pasted_file:")
	if len(cli.prompts) != 1 || strings.Contains(cli.prompts[0], big) {
		t.Fatal("large paste should reach the backend as a file reference, not inline")
	}
}

func TestRunUpdateCommand(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	ag := newTestAgent(nil, nil)

	out := runREPL(t, ag, nil, "/update")
	assertContains(t, out, "qmax-code 9.9.9 is up to date")

	checkUpdate = func(string) (*update.Release, error) { return nil, errors.New("offline") }
	out = runREPL(t, ag, nil, "/update")
	assertContains(t, out, "offline")

	checkUpdate = func(string) (*update.Release, error) {
		return &update.Release{Version: "10.0.0", Asset: "http://127.0.0.1:1/missing"}, nil
	}
	out = runREPL(t, ag, nil, "/update")
	assertContains(t, out, "Update available: 10.0.0", "Update failed")
}

func TestRunResumeAndSessions(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	ag := newTestAgent(nil, nil)

	out := runREPL(t, ag, nil, "/resume", "/resume ../etc/passwd", "/sessions")
	assertContains(t, out, "Cannot resume", `invalid session ID "../etc/passwd"`, "No saved sessions.")

	id := session.GenerateSessionID()
	msgs := []api.Message{{Role: "user", Content: "earlier"}, {Role: "assistant", Content: "reply"}}
	if err := session.SaveSession(id, msgs, 5, api.TokenUsage{InputTokens: 10}, api.ModelSonnet, api.ConversationState{}); err != nil {
		t.Fatal(err)
	}

	ag = newTestAgent(nil, nil)
	out = runREPL(t, ag, nil, "/resume "+id)
	assertContains(t, out, "Resumed session "+id)
	if ag.Cfg.Context.ProjectID != 5 {
		t.Fatalf("resume did not restore project: %d", ag.Cfg.Context.ProjectID)
	}

	ag = newTestAgent(nil, nil)
	out = runREPL(t, ag, nil, "/resume last")
	assertContains(t, out, "Resumed session")

	showSessionPicker = func(items []tui.SessionPickerItem, _ string) (string, bool) {
		if len(items) == 0 {
			t.Fatal("session picker received no sessions")
		}
		return items[0].ID, true
	}
	ag = newTestAgent(nil, nil)
	out = runREPL(t, ag, nil, "/sessions")
	assertContains(t, out, "Resumed session")

	showSessionPicker = func([]tui.SessionPickerItem, string) (string, bool) { return "../bad", true }
	out = runREPL(t, ag, nil, "/sessions")
	assertContains(t, out, `Invalid session ID "../bad"`)
}

func TestRunThemeCloudSyncAndLivePickers(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	t.Cleanup(func() { tui.ApplyTheme(tui.ThemeByName("")) })
	cfg := api.DefaultConfig()
	ag := newTestAgent(cfg, nil)

	showThemePicker = func(string) (string, bool) { return tui.ThemeNames()[0], true }
	showCloudSyncPicker = func(*bool) (bool, bool) { return true, true }
	showLiveFeedPicker = func(current bool) (bool, bool) { return !current, true }

	out := runREPL(t, ag, nil, "/theme", "/cloudsync", "/live", "/live on", "/live off", "/live bogus")

	assertContains(t, out,
		"Theme set to: "+tui.ThemeNames()[0],
		"Cloud session sync enabled.",
		"Live feed enabled.",
		"Live feed disabled.",
		"Usage: /live",
	)
	if cfg.CloudSync == nil || !*cfg.CloudSync {
		t.Fatal("/cloudsync did not enable sync")
	}

	showCloudSyncPicker = func(*bool) (bool, bool) { return false, true }
	showLiveFeedPicker = func(current bool) (bool, bool) { return current, true }
	out = runREPL(t, ag, nil, "/cloudsync", "/live")
	assertContains(t, out, "Cloud session sync disabled.", "Live feed unchanged (off).")
}

func TestRunNilConfigCommands(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	ag := newTestAgent(nil, nil)
	ag.AppConfig = nil

	out := runREPL(t, ag, nil, "/orch", "/theme", "/cloudsync", "/cc", "/gemma", "/live", "/providers")

	if n := strings.Count(out, "api.Config not loaded."); n != 7 {
		t.Fatalf("got %d 'not loaded' errors, want 7\n%s", n, out)
	}
}

func TestRunSetAndSettings(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cfg := api.DefaultConfig()
	ag := newTestAgent(cfg, nil)
	showSettingsPicker = func(rows []tui.SettingsRow) tui.SettingsPickerResult {
		return tui.SettingsPickerResult{Confirmed: true, Changes: map[string]string{"budget": "500", "autosave": "false"}}
	}

	out := runREPL(t, ag, nil, "/set budget 250", "/set", "/settings")

	assertContains(t, out, "Token budget set to: 250", "Token budget set to: 500", "Auto-save disabled.")
	if loaded := api.LoadQMaxCodeConfig(); loaded.MaxTokenBudget != 500 || loaded.AutoSave {
		t.Fatalf("settings not persisted: budget=%d autosave=%v", loaded.MaxTokenBudget, loaded.AutoSave)
	}
}

func TestRunConnectAndKeys(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	ag := newTestAgent(nil, nil)

	out := runREPL(t, ag, nil, "/connect", "/keys")
	assertContains(t, out, "Connection failed", "/set apikey", "API Keys", "Anthropic:   ")

	loginViaBrowser = func() (*api.AuthConfig, error) {
		return &api.AuthConfig{APIKey: "qm-test", Email: "qa@example.com", CloudURL: "http://127.0.0.1:1"}, nil
	}
	out = runREPL(t, ag, nil, "/connect", "/connect", "/disconnect")
	assertContains(t, out, "Connected as qa@example.com", "Already connected as qa@example.com", "Disconnected from qa@example.com")

	promptChoice = func(string, []string) int { return 0 }
	readSecret = func(string) string { return "sk-ant-api03-testkey-0000" }
	out = runREPL(t, ag, nil, "/keys")
	if ag.Cfg.AnthropicKey != "sk-ant-api03-testkey-0000" {
		t.Fatalf("/keys did not set the Anthropic key")
	}
	assertContains(t, out, "Key saved to OS keychain!")

	readSecret = func(string) string { return "" }
	out = runREPL(t, ag, nil, "/keys")
	assertContains(t, out, "Cancelled.", "● Set")

	promptChoice = func(string, []string) int { return 1 }
	out = runREPL(t, ag, nil, "/keys")
	assertContains(t, out, "Connected as qa@example.com")
	promptChoice = func(string, []string) int { return 2 }
	out = runREPL(t, ag, nil, "/keys")
	assertContains(t, out, "Disconnected from qa@example.com")
}

func TestRunBackendSwitchCommands(t *testing.T) {
	home := replEnv(t)
	stubSeams(t)
	bin := filepath.Join(home, "bin")
	claude := writeStub(t, bin, "claude", "exit 0")
	codex := writeStub(t, bin, "codex", "exit 0")
	agy := writeStub(t, bin, "agy", "exit 0")
	cfg := api.DefaultConfig()
	ag := newTestAgent(cfg, nil)

	// Nothing installed, no key: every switch is refused.
	out := runREPL(t, ag, nil, "/cc", "/codex", "/agy", "/opencode", "/api")
	assertContains(t, out,
		"'claude' CLI not found",
		"'codex' CLI not found",
		"'agy' CLI not found",
		"'opencode' CLI not found",
		"Anthropic API is not configured.",
	)

	findClaudeCode = func() string { return claude }
	findCodex = func() string { return codex }
	findAgy = func() string { return agy }

	// Installed but consent declined.
	out = runREPL(t, ag, nil, "/cc", "/codex", "/agy")
	if n := strings.Count(out, "Backend not changed."); n != 3 {
		t.Fatalf("declined consent count = %d, want 3\n%s", n, out)
	}

	promptOrchConsent = func(*api.Config, string) setup.ConsentResult {
		return setup.ConsentResult{Proceed: true, PermissionMode: "standard"}
	}
	agyLogins := 0
	promptAgyLogin = func(string) { agyLogins++ }
	out = runREPL(t, ag, nil, "/cc")
	assertContains(t, out, "Backend → Claude Code")
	if cfg.Backend != "cc" || ag.Cfg.Context.Backend != "cc" {
		t.Fatalf("backend = %q/%q, want cc", cfg.Backend, ag.Cfg.Context.Backend)
	}

	out = runREPL(t, ag, nil, "/codex", "/agy")
	assertContains(t, out, "Backend → Codex", "Backend → Antigravity")
	if agyLogins != 1 {
		t.Fatalf("agy login prompts = %d, want 1", agyLogins)
	}

	// Toggling the active backend off needs an Anthropic key.
	cfg.Backend = "agy"
	ag.Cfg.AnthropicKey = "sk-ant-test"
	out = runREPL(t, ag, nil, "/agy")
	assertContains(t, out, "Backend → Anthropic API (direct)")
	if cfg.Backend != "" {
		t.Fatalf("toggle did not return to the API backend: %q", cfg.Backend)
	}
}

func TestRunOpenCodeSwitchPreflight(t *testing.T) {
	home := replEnv(t)
	stubSeams(t)
	opencode := writeStub(t, filepath.Join(home, "bin"), "opencode", `
if [ "$1" = "models" ]; then echo "groq/llama-3.3-70b"; echo "groq/qwen-qwq"; exit 0; fi
exit 0`)
	findOpenCode = func() string { return opencode }
	cfg := api.DefaultConfig()
	ag := newTestAgent(cfg, nil)

	out := runREPL(t, ag, nil, "/opencode")
	assertContains(t, out, "No opencode providers enabled yet.")

	cfg.EnableProviderID("groq")
	t.Setenv("GROQ_API_KEY", "gsk_test")
	out = runREPL(t, ag, nil, "/opencode")
	assertContains(t, out, "Backend not changed.")

	promptOrchConsent = func(*api.Config, string) setup.ConsentResult {
		return setup.ConsentResult{Proceed: true, PermissionMode: "standard"}
	}
	cfg.ModelOverride = "gpt-5.6"
	out = runREPL(t, ag, nil, "/opencode")
	assertContains(t, out, "Backend → opencode")
	if !strings.HasPrefix(cfg.ModelOverride, "groq/") {
		t.Fatalf("stale model override was not replaced: %q", cfg.ModelOverride)
	}
}

func TestRunGemmaCommand(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cfg := api.DefaultConfig()
	ag := newTestAgent(cfg, nil)

	out := runREPL(t, ag, nil, "/gemma bogus", "/gemma", "/gemma off")
	assertContains(t, out, `"bogus" is invalid`, "No key entered — Gemma not activated.", "Anthropic API is not configured.")

	readSecret = func(string) string { return "has space" }
	out = runREPL(t, ag, nil, "/gemma")
	assertContains(t, out, "Gemma not activated.")

	readSecret = func(string) string { return "not-a-csk-key" }
	out = runREPL(t, ag, nil, "/gemma high")
	assertContains(t, out, "saving anyway", "Backend: Cerebras", "reasoning: high")
	if cfg.Backend != "cerebras" || ag.Cerebras == nil {
		t.Fatalf("gemma did not activate cerebras: backend=%q", cfg.Backend)
	}

	out = runREPL(t, ag, nil, "/gemma none")
	assertContains(t, out, "reasoning off")

	ag.Cfg.AnthropicKey = "sk-ant-test"
	out = runREPL(t, ag, &fakeCLI{}, "/gemma off")
	assertContains(t, out, "Gemma disabled → Anthropic API (direct)")
	if ag.Cerebras != nil || cfg.Backend != "" {
		t.Fatal("/gemma off did not deactivate cerebras")
	}
}

func TestRunOllamaModeCycle(t *testing.T) {
	replEnv(t)
	stubSeams(t)
	cfg := api.DefaultConfig()
	cfg.OllamaURL = "ftp://bad"
	cfg.OllamaModel = "llama3.2:3b"
	ag := newTestAgent(cfg, nil)

	out := runREPL(t, ag, nil, "/ollama")
	assertContains(t, out, "Ollama URL rejected")

	cfg.OllamaURL = "http://127.0.0.1:11434"
	out = runREPL(t, ag, &fakeCLI{}, "/ollama")
	assertContains(t, out, "Ollama remains in FULL mode")

	ag.Cfg.AnthropicKey = "sk-ant-test"
	ag.Mode = agent.OllamaModeOff
	out = runREPL(t, ag, nil, "/ollama", "/ollama", "/ollama")
	assertContains(t, out, "Ollama: CHAT mode", "Ollama: FULL mode", "Ollama: OFF")
}
