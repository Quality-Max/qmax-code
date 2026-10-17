package repl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/agent"
	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/gate"
	"github.com/Quality-Max/qmax-code/internal/setup"
	"github.com/Quality-Max/qmax-code/internal/tui"
	"github.com/Quality-Max/qmax-code/internal/update"
)

// fakeCLI is a scripted CLI backend: it records prompts and returns canned
// responses without spawning a subprocess.
type fakeCLI struct {
	mu        sync.Mutex
	prompts   []string
	response  string
	err       error
	verbose   bool
	cleaned   int
	resets    int
	canceled  int
	statsIn   int
	statsOut  int
	limitHit  bool
	limitTime time.Time
}

func (f *fakeCLI) Run(msg string, term *tui.Terminal) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, msg)
	return f.response, f.err
}
func (f *fakeCLI) Cancel()                 { f.canceled++ }
func (f *fakeCLI) SetOutputVerbose(v bool) { f.verbose = v }
func (f *fakeCLI) Cleanup()                { f.cleaned++ }
func (f *fakeCLI) ResetConversation()      { f.resets++ }
func (f *fakeCLI) LastTurnStats() (int, int, bool) {
	return f.statsIn, f.statsOut, f.statsIn > 0 || f.statsOut > 0
}
func (f *fakeCLI) LastPlanLimit() (time.Time, bool) { return f.limitTime, f.limitHit }

// replEnv isolates a REPL test from the developer machine: a temporary HOME,
// a PATH holding only fake keychain tools, and no update checks.
func replEnv(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("REPL harness uses POSIX shell stubs")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("QMAX_NO_UPDATE_CHECK", "1")
	t.Setenv("ANTHROPIC_API_KEY", "")
	for _, key := range []string{
		"CEREBRAS_API_KEY", "CEREBRAS_API_BASE", "CEREBRAS_MODEL", "CEREBRAS_REASONING_EFFORT",
		"OLLAMA_BASE_URL", "OLLAMA_MODEL", "QUALITYMAX_API_KEY",
		"GROQ_API_KEY", "OPENROUTER_API_KEY", "QMAX_PC_ZAI_CODING_PLAN",
	} {
		t.Setenv(key, "")
	}
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Keychain stand-ins: writes succeed, lookups miss. The real OS keychain
	// is never touched.
	writeStub(t, bin, "security", `case "$1" in find-generic-password) exit 44;; esac; exit 0`)
	writeStub(t, bin, "secret-tool", `case "$1" in lookup) exit 1;; esac; cat >/dev/null; exit 0`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	// Clients built during the test wrap http.DefaultTransport; fail every
	// request locally so no test reaches a real API.
	origTransport := http.DefaultTransport
	http.DefaultTransport = offlineTransport{}
	t.Cleanup(func() { http.DefaultTransport = origTransport })
	return home
}

type offlineTransport struct{}

func (offlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("offline test transport: %s %s", req.Method, req.URL.Host)
}

func writeStub(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// stubSeams replaces every interactive entry point with an inert default for
// the duration of the test. Individual tests override what they exercise.
func stubSeams(t *testing.T) {
	t.Helper()
	saved := []func(){}
	save := func(restore func()) { saved = append(saved, restore) }
	{
		v := readInput
		save(func() { readInput = v })
	}
	{
		v := runTurnViewport
		save(func() { runTurnViewport = v })
	}
	{
		v := showModelPicker
		save(func() { showModelPicker = v })
	}
	{
		v := showThemePicker
		save(func() { showThemePicker = v })
	}
	{
		v := showSessionPicker
		save(func() { showSessionPicker = v })
	}
	{
		v := showCloudSyncPicker
		save(func() { showCloudSyncPicker = v })
	}
	{
		v := showLiveFeedPicker
		save(func() { showLiveFeedPicker = v })
	}
	{
		v := showSettingsPicker
		save(func() { showSettingsPicker = v })
	}
	{
		v := showProviderPicker
		save(func() { showProviderPicker = v })
	}
	{
		v := captureScreenshot
		save(func() { captureScreenshot = v })
	}
	{
		v := pasteImage
		save(func() { pasteImage = v })
	}
	{
		v := pasteText
		save(func() { pasteText = v })
	}
	{
		v := promptOrchConsent
		save(func() { promptOrchConsent = v })
	}
	{
		v := readSecret
		save(func() { readSecret = v })
	}
	{
		v := promptAgyLogin
		save(func() { promptAgyLogin = v })
	}
	{
		v := promptChoice
		save(func() { promptChoice = v })
	}
	{
		v := loginViaBrowser
		save(func() { loginViaBrowser = v })
	}
	{
		v := checkUpdate
		save(func() { checkUpdate = v })
	}
	{
		v := runGate
		save(func() { runGate = v })
	}
	{
		v1, v2 := maybeCheckUpdate, readConsent
		save(func() { maybeCheckUpdate, readConsent = v1, v2 })
	}
	{
		v := openBrowserFeed
		save(func() { openBrowserFeed = v })
	}
	{
		v1, v2, v3, v4 := findClaudeCode, findCodex, findAgy, findOpenCode
		save(func() { findClaudeCode, findCodex, findAgy, findOpenCode = v1, v2, v3, v4 })
	}
	t.Cleanup(func() {
		for _, restore := range saved {
			restore()
		}
	})

	runTurnViewport = func(_ *tui.Terminal, _ string, _ *tui.StatusInfo, _ func(), run func()) tui.TurnInputResult {
		run()
		return tui.TurnInputResult{}
	}
	showModelPicker = func(tui.ModelPickerOpts) tui.ModelPickerResult { return tui.ModelPickerResult{} }
	showThemePicker = func(string) (string, bool) { return "", false }
	showSessionPicker = func([]tui.SessionPickerItem, string) (string, bool) { return "", false }
	showCloudSyncPicker = func(*bool) (bool, bool) { return false, false }
	showLiveFeedPicker = func(bool) (bool, bool) { return false, false }
	showSettingsPicker = func([]tui.SettingsRow) tui.SettingsPickerResult { return tui.SettingsPickerResult{} }
	showProviderPicker = func([]tui.ProviderPickerItem) tui.ProviderPickerResult { return tui.ProviderPickerResult{} }
	captureScreenshot = func() (*tui.ImageAttachment, error) { return nil, errors.New("no display") }
	pasteImage = func() (*tui.ImageAttachment, error) { return nil, errors.New("no image") }
	pasteText = func() (string, error) { return "", errors.New("empty") }
	promptOrchConsent = func(*api.Config, string) setup.ConsentResult { return setup.ConsentResult{} }
	readSecret = func(string) string { return "" }
	promptAgyLogin = func(string) {}
	promptChoice = func(string, []string) int { return 3 }
	loginViaBrowser = func() (*api.AuthConfig, error) { return nil, errors.New("browser login disabled in tests") }
	checkUpdate = func(string) (*update.Release, error) { return nil, nil }
	maybeCheckUpdate = func(string) *update.Release { return nil }
	readConsent = func(*tui.Terminal) (string, error) { return "", errors.New("no tty") }
	runGate = func(context.Context, gate.Options) gate.Result { return gate.Result{} }
	openBrowserFeed = func(string, blockMode) error { return errors.New("no feed in tests") }
	findClaudeCode = func() string { return "" }
	findCodex = func() string { return "" }
	findAgy = func() string { return "" }
	findOpenCode = func() string { return "" }
}

// scriptInputs feeds lines to the REPL prompt in order, then exits.
func scriptInputs(t *testing.T, lines ...string) {
	t.Helper()
	var mu sync.Mutex
	i := 0
	readInput = func(string, []string, bool, *tui.StatusInfo) tui.InputResult {
		mu.Lock()
		defer mu.Unlock()
		if i >= len(lines) {
			return tui.InputResult{Text: "/exit"}
		}
		line := lines[i]
		i++
		switch line {
		case "<ctrl-c>":
			return tui.InputResult{CtrlC: true}
		case "<ctrl-o>":
			return tui.InputResult{OutputToggle: true}
		}
		return tui.InputResult{Text: line}
	}
}

// captureOutput runs fn with stdout and stderr redirected and returns both.
func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = w, w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	defer func() {
		os.Stdout, os.Stderr = origOut, origErr
	}()
	fn()
	_ = w.Close()
	os.Stdout, os.Stderr = origOut, origErr
	return <-done
}

func newTestAgent(cfg *api.Config, ctx *api.SessionContext) *agent.Agent {
	if cfg == nil {
		cfg = api.DefaultConfig()
	}
	if ctx == nil {
		ctx = &api.SessionContext{}
	}
	ag := agent.NewAgent(agent.AgentConfig{Model: api.ModelSonnet, Context: ctx})
	ag.AppConfig = cfg
	return ag
}

// runREPL drives Run with the scripted inputs and returns everything printed.
func runREPL(t *testing.T, ag *agent.Agent, cli agent.CLIAgent, lines ...string) string {
	t.Helper()
	scriptInputs(t, lines...)
	finalized := false
	out := captureOutput(t, func() {
		Run(ag, cli, true, "9.9.9", func() { finalized = true })
	})
	if !finalized {
		t.Fatal("Run returned without finalizing the receipt")
	}
	return out
}

func assertContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}
