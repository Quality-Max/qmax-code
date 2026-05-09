package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// LoginInteractive prompts the user to paste their API key.
func LoginInteractive() (*api.AuthConfig, error) {
	fmt.Println()
	fmt.Println("  Get your API key from:")
	fmt.Println("  https://app.qualitymax.io/settings → API Keys")
	fmt.Println()
	key := readSecret("  Paste your API key (qm-...): ")

	if key == "" {
		return nil, fmt.Errorf("no API key provided")
	}

	return api.LoginWithAPIKey(key)
}

// --- Browser-based login (Railway-style) ---

type cliLoginResponse struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expires_at"`
	AuthURL   string `json:"auth_url"`
}

type cliPollResponse struct {
	Status string `json:"status"`
	Token  string `json:"token,omitempty"`
	Email  string `json:"email,omitempty"`
	UserID string `json:"user_id,omitempty"`
}

// LoginViaBrowser performs Railway-style browser login:
// 1. POST /api/auth/cli-login → get code + auth URL
// 2. Open browser to auth URL
// 3. Poll /api/auth/cli-poll until authorized or expired
func LoginViaBrowser() (*api.AuthConfig, error) {
	cloudURL := os.Getenv("QUALITYMAX_URL")
	if cloudURL == "" {
		cloudURL = api.DefaultCloudURL
	}
	client := &http.Client{Timeout: 10 * time.Second}

	// Step 1: Get a CLI auth code
	req, err := http.NewRequest("POST", cloudURL+"/api/auth/cli-login", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach QualityMax: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("CLI login failed (HTTP %d)", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var loginResp cliLoginResponse
	if err := json.Unmarshal(body, &loginResp); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}

	// Step 2: Open browser
	fmt.Println()
	fmt.Printf("  Your auth code: \033[1;35m%s\033[0m\n", loginResp.Code)
	fmt.Println()
	fmt.Println("  Opening browser to authorize...")
	openBrowser(loginResp.AuthURL)
	fmt.Println()
	fmt.Printf("  If the browser didn't open, visit:\n  %s\n", loginResp.AuthURL)
	fmt.Println()
	fmt.Println("  Waiting for authorization...")

	// Step 3: Poll until authorized (every 2 seconds, up to 10 minutes).
	// QueryEscape the code defensively — it's server-supplied and goes into
	// a URL component, so a code containing &, #, or other URL-reserved
	// characters would otherwise produce a malformed request.
	pollURL := cloudURL + "/api/auth/cli-poll?code=" + url.QueryEscape(loginResp.Code)
	deadline := time.Now().Add(10 * time.Minute)

	i := 0
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)

		pollReq, _ := http.NewRequest("GET", pollURL, nil)
		pollResp, err := client.Do(pollReq)
		if err != nil {
			// Network hiccup — keep trying
			continue
		}

		pollBody, _ := io.ReadAll(pollResp.Body)
		pollResp.Body.Close()

		var poll cliPollResponse
		if err := json.Unmarshal(pollBody, &poll); err != nil {
			continue
		}

		switch poll.Status {
		case "authorized":
			cfg := &api.AuthConfig{
				APIKey:   poll.Token,
				Email:    poll.Email,
				UserID:   poll.UserID,
				CloudURL: cloudURL,
			}
			if err := api.SaveAuth(cfg); err != nil {
				return cfg, fmt.Errorf("logged in but failed to save: %w", err)
			}
			return cfg, nil

		case "expired":
			return nil, fmt.Errorf("auth code expired — please try again")

		default:
			// Still pending — show spinner
			fmt.Printf("\r  Waiting %s", tui.SpinnerFrames[i%len(tui.SpinnerFrames)])
			i++
		}
	}

	return nil, fmt.Errorf("timed out waiting for browser authorization")
}

// RunInteractiveSetup guides a first-time user through login and project selection.
// Returns the api.AuthConfig and selected project ID.
func RunInteractiveSetup() (*api.AuthConfig, int) {
	fmt.Println()
	tui.AnimateMax(tui.MoodWaving, tui.GetMaxGreeting())
	fmt.Println()
	fmt.Println("  Looks like this is your first time here.")
	fmt.Println("  Let's get you set up — it takes 30 seconds.")
	fmt.Println()

	// Step 1: Account check
	choice := promptChoice("  Do you have a QualityMax account?", []string{
		"Yes, log me in (opens browser)",
		"No, create one (free)",
		"I have an API key already",
	})

	var auth *api.AuthConfig
	var err error

	switch choice {
	case 0: // Yes, log me in → browser auth (Railway-style)
		tui.AnimateMax(tui.MoodThinking, "Opening browser...")
		auth, err = LoginViaBrowser()
	case 1: // No, create one
		openBrowser("https://qualitymax.io/auth/email/signup?ref=qmax-code")
		fmt.Println()
		fmt.Println("  Browser opened! Create your free account, then come back.")
		fmt.Println("  Press Enter when you're ready to log in...")
		waitForEnter()
		tui.AnimateMax(tui.MoodThinking, "Opening browser...")
		auth, err = LoginViaBrowser()
	case 2: // I have an API key
		auth, err = loginWithKeyPrompt()
	}

	if err != nil {
		tui.AnimateMax(tui.MoodSad, "Login failed: "+err.Error())
		fmt.Println()
		fmt.Println("  Try again with: qmax-code login")
		os.Exit(1)
	}

	// Show success
	tui.AnimateMaxTransition(tui.MoodThinking, tui.MoodExcited, "")
	fmt.Printf("  Logged in as %s\n", auth.Email)
	fmt.Println()

	// Step 2: Project selection
	projectID := selectProject(auth)

	// Step 2.5: Detect the project's framework from the local working
	// directory so the agent can default `generate_test_code` to the right
	// value without the user having to specify it on every call. We ask for
	// confirmation before saving — users often run `qmax-code login` from
	// the wrong cwd on first setup (e.g. ~/), and a silent save would stick
	// them with a stale default.
	detected := detectProjectFramework(".")
	if detected != "" {
		fmt.Println()
		fmt.Printf("  Detected a %s project in this directory.\n", prettyFrameworkName(detected))
		confirm := promptChoice(
			fmt.Sprintf("  Save %s as the default framework for future test generation?", detected),
			[]string{"Yes, save it", "No, I'll pick per-call"},
		)
		if confirm == 0 {
			cfg := api.LoadQMaxCodeConfig()
			cfg.DefaultFramework = detected
			_ = cfg.Save()
			fmt.Printf("  Saved. You can change it later by editing ~/.qmax-code/config.json.\n")
		} else {
			fmt.Println("  OK, I'll ask for the framework each time.")
		}
	} else {
		// Silent success is confusing — users should know detection ran
		// and came back empty so they know to pass --framework explicitly.
		fmt.Println()
		fmt.Println("  Couldn't auto-detect a framework in this directory.")
		fmt.Println("  Pass --framework rust_cargo | go_test | playwright | pytest when")
		fmt.Println("  generating tests, or set it in ~/.qmax-code/config.json.")
	}

	// Step 3: Anthropic key check
	cfg := api.LoadQMaxCodeConfig()
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicKey == "" {
		anthropicKey = cfg.AnthropicKey
	}
	if anthropicKey == "" {
		fmt.Println()
		tui.AnimateMax(tui.MoodThinking, "One more thing...")
		fmt.Println()
		fmt.Println("  I need an Anthropic API key to think (that's my brain!).")
		fmt.Println("  Get one at: https://console.anthropic.com/settings/keys")
		fmt.Println()
		key := readSecret("  Paste your Anthropic key (sk-ant-...): ")
		if key != "" {
			os.Setenv("ANTHROPIC_API_KEY", key)
			// Save to OS keychain
			if err := api.SaveAnthropicKey(key); err != nil {
				// Fallback: warn but continue
				fmt.Printf("\n  Note: Could not save to keychain (%s)\n", err)
				fmt.Println("  Key is set for this session. Set ANTHROPIC_API_KEY in your shell profile to persist.")
			} else {
				fmt.Println()
				fmt.Println("  Key saved securely to OS keychain")
			}
		}
	} else {
		os.Setenv("ANTHROPIC_API_KEY", anthropicKey)
	}

	// All set!
	fmt.Println()
	tui.AnimateMaxTransition(tui.MoodNeutral, tui.MoodHappy, "All set! Let's hunt some bugs.")
	fmt.Println()
	fmt.Println("  Examples:")
	fmt.Println("    \"crawl staging.myapp.com and generate tests\"")
	fmt.Println("    \"show me all failing tests\"")
	fmt.Println("    \"review the latest PR for security issues\"")
	fmt.Println()

	return auth, projectID
}

// loginWithKeyPrompt asks the user to paste their API key.
func loginWithKeyPrompt() (*api.AuthConfig, error) {
	key := readSecret("  Paste your API key (qm-...): ")

	if key == "" {
		return nil, fmt.Errorf("no API key provided")
	}

	fmt.Println()
	// Show thinking animation
	fmt.Print("  Validating ")
	done := make(chan bool)
	go func() {
		i := 0
		for {
			select {
			case <-done:
				return
			default:
				fmt.Printf("\r  Validating %s", tui.SpinnerFrames[i%len(tui.SpinnerFrames)])
				i++
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	auth, err := api.LoginWithAPIKey(key)
	done <- true
	fmt.Print("\r  Validating ")

	if err != nil {
		fmt.Println("✗")
		return nil, err
	}

	fmt.Println("✓")
	return auth, nil
}

// selectProject lists projects and lets the user pick one.
func selectProject(auth *api.AuthConfig) int {
	client := api.NewAPIClient(auth)
	if client == nil {
		return 0
	}

	fmt.Println("  Loading projects...")
	result := client.ListProjects(context.Background())

	// Try to parse as JSON array
	var projects []map[string]interface{}
	if err := parseJSON(result, &projects); err != nil || len(projects) == 0 {
		fmt.Println("  No projects found. You can create one later.")
		return 0
	}

	fmt.Printf("  Found %d project(s)\n\n", len(projects))

	// Show up to 10 projects
	options := make([]string, 0, len(projects)+1)
	for i, p := range projects {
		if i >= 10 {
			break
		}
		name := strVal2(p, "name")
		id := intVal2(p, "id")
		options = append(options, fmt.Sprintf("%s (ID: %d)", name, id))
	}
	options = append(options, "Skip — I'll choose later")

	choice := promptChoice("  Which project do you want to work with?", options)

	if choice >= len(projects) {
		return 0
	}

	id := intVal2(projects[choice], "id")
	fmt.Printf("\n  Selected: %s\n", strVal2(projects[choice], "name"))

	// Save to config
	cfg := api.LoadQMaxCodeConfig()
	cfg.DefaultProject = id
	_ = cfg.Save()

	return id
}

// readSecret reads a line of input with characters hidden (replaced with dots).
// Shows a masked preview after completion.
func readSecret(prompt string) string {
	fmt.Print(prompt)

	// Switch terminal to raw mode to hide input
	oldState, err := tui.EnableRawMode()
	if err != nil {
		// Fallback: plain read + mask after
		reader := bufio.NewReader(os.Stdin)
		key, _ := reader.ReadString('\n')
		key = strings.TrimSpace(key)
		if key != "" {
			masked := maskKey(key)
			fmt.Printf("\033[1A\033[2K%s%s\n", prompt, masked)
		}
		return key
	}

	var input []byte
	buf := make([]byte, 1)
	for {
		n, _ := os.Stdin.Read(buf)
		if n == 0 {
			continue
		}
		ch := buf[0]
		switch ch {
		case '\n', '\r':
			tui.RestoreTermMode(oldState)
			fmt.Println()
			key := strings.TrimSpace(string(input))
			if key != "" {
				masked := maskKey(key)
				fmt.Printf("\033[1A\033[2K%s%s\n", prompt, masked)
			}
			return key
		case 127, '\b': // backspace
			if len(input) > 0 {
				input = input[:len(input)-1]
				fmt.Print("\b \b")
			}
		case 3: // Ctrl+C
			tui.RestoreTermMode(oldState)
			fmt.Println()
			return ""
		default:
			if ch >= 32 { // printable
				input = append(input, ch)
				fmt.Print("•")
			}
		}
	}
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "••••"
	}
	return key[:4] + "•••" + key[len(key)-4:]
}

// --- UI helpers ---

// promptChoice shows an interactive menu and returns the selected index.
func promptChoice(prompt string, options []string) int {
	fmt.Println(prompt)
	for i, opt := range options {
		if i == 0 {
			fmt.Printf("    \033[36m› %s\033[0m\n", opt) // highlight first
		} else {
			fmt.Printf("      %s\n", opt)
		}
	}
	fmt.Println()

	// Simple input: type number or press enter for first option
	fmt.Print("  Choice (1-" + strconv.Itoa(len(options)) + ", default 1): ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)

	if line == "" {
		return 0
	}

	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(options) {
		return 0
	}
	return n - 1
}

// waitForEnter waits for the user to press Enter.
func waitForEnter() {
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')
}

// openBrowser opens a URL in the default browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}

// --- JSON helpers ---

func parseJSON(data string, v interface{}) error {
	return json.Unmarshal([]byte(data), v)
}

func strVal2(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func intVal2(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok && v != nil {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

// detectProjectFramework inspects the given directory and returns the
// QualityMax framework name for the toolchain it detects.
// Returns "" when nothing recognizable is present. Priority:
//   - Cargo.toml       → "rust_cargo"
//   - go.mod           → "go_test"
//   - playwright.config.* or .spec.ts in tests/ → "playwright"
//   - pytest.ini / pyproject.toml with pytest / requirements*.txt → "pytest"
//
// Priority matters for polyglot repos (a Python-with-Rust-extension project
// should still be a Rust project for CI purposes since the Rust crate is
// the compile-heavy part).
func detectProjectFramework(dir string) string {
	exists := func(name string) bool {
		_, err := os.Stat(dir + "/" + name)
		return err == nil
	}
	if exists("Cargo.toml") {
		return "rust_cargo"
	}
	if exists("go.mod") {
		return "go_test"
	}
	if exists("playwright.config.ts") || exists("playwright.config.js") || exists("playwright.config.mjs") {
		return "playwright"
	}
	if exists("pyproject.toml") || exists("pytest.ini") || exists("tox.ini") {
		return "pytest"
	}
	if exists("package.json") {
		// Default-ish — node project without an explicit test framework.
		// Don't force a choice; let the user pick later.
		return ""
	}
	return ""
}

func prettyFrameworkName(fw string) string {
	switch fw {
	case "rust_cargo":
		return "Rust (cargo)"
	case "go_test":
		return "Go (go test)"
	case "playwright":
		return "Playwright"
	case "pytest":
		return "Python (pytest)"
	default:
		return fw
	}
}
