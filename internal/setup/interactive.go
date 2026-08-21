package setup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/qualitymax/qmax-code/internal/httpx"
	"github.com/qualitymax/qmax-code/internal/tui"
)

var (
	// ErrStandaloneSkip signals the user chose standalone local mode during
	// onboarding instead of connecting a QualityMax account. Callers should
	// switch to standalone mode rather than treating this as a failure.
	ErrStandaloneSkip = errors.New("standalone local mode selected")

	// ErrEmptyAPIKey is returned when the API key prompt ends with no input.
	ErrEmptyAPIKey = errors.New("no API key provided")
)

// LoginInteractive prompts the user to paste their API key.
func LoginInteractive() (*api.AuthConfig, error) {
	fmt.Println()
	fmt.Println("  Get your API key from:")
	fmt.Println("  https://app.qualitymax.io/settings → API Keys")
	fmt.Println()
	key := ReadSecret("  Paste your API key (qm-...): ")

	if key == "" {
		return nil, ErrEmptyAPIKey
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

type apiTokenResponse struct {
	Token string `json:"token"`
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

const cliAPITokenLifetimeDays = 90

func newBrowserLoginHTTPClient() *http.Client {
	client := httpx.NewClient(10 * time.Second)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func mintCLIAPIToken(ctx context.Context, client httpDoer, cloudURL, accessToken string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"name":         "qmax-code CLI",
		"expires_days": cliAPITokenLifetimeDays,
	})
	if err != nil {
		return "", fmt.Errorf("encode API token request: %w", err)
	}

	req, err := httpx.NewRequest(
		ctx,
		http.MethodPost,
		strings.TrimRight(cloudURL, "/")+"/api/auth/api-token",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("build API token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("create API token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("create API token failed (HTTP %d)", resp.StatusCode)
	}

	var result apiTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode API token response: %w", err)
	}
	if !strings.HasPrefix(result.Token, "qm-") || len(result.Token) == len("qm-") {
		return "", fmt.Errorf("create API token returned an invalid token")
	}
	return result.Token, nil
}

func completeBrowserLogin(
	ctx context.Context,
	client httpDoer,
	cloudURL string,
	poll cliPollResponse,
	saveAuth func(*api.AuthConfig) error,
) (*api.AuthConfig, error) {
	apiToken, err := mintCLIAPIToken(ctx, client, cloudURL, poll.Token)
	if err != nil {
		return nil, fmt.Errorf("browser authorized but API token setup failed: %w", err)
	}
	cfg := &api.AuthConfig{
		APIKey:   apiToken,
		Email:    poll.Email,
		UserID:   poll.UserID,
		CloudURL: cloudURL,
	}
	if err := saveAuth(cfg); err != nil {
		return cfg, fmt.Errorf("logged in but failed to save: %w", err)
	}
	return cfg, nil
}

// LoginViaBrowser performs Railway-style browser login:
//  1. POST /api/auth/cli-login → get code + auth URL
//  2. Open browser to auth URL
//  3. Poll /api/auth/cli-poll until authorized or expired
//  4. Exchange the user access token for a registered API token that works
//     with both the REST API and the cloud MCP endpoint
func LoginViaBrowser() (*api.AuthConfig, error) {
	cloudURL := os.Getenv("QUALITYMAX_URL")
	if cloudURL == "" {
		cloudURL = api.DefaultCloudURL
	}
	client := newBrowserLoginHTTPClient()

	// Step 1: Get a CLI auth code
	req, err := httpx.NewRequest(context.Background(), "POST", cloudURL+"/api/auth/cli-login", nil)
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

		pollReq, _ := httpx.NewRequest(context.Background(), "GET", pollURL, nil)
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
			return completeBrowserLogin(context.Background(), client, cloudURL, poll, api.SaveAuth)

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

// RunInteractive guides a first-time user through login and project selection.
// It returns failures to main so the session receipt can be finalized before
// the process exits.
func RunInteractive() (*api.AuthConfig, int, error) {
	fmt.Println()
	tui.AnimateMax(tui.MoodWaving, tui.GetMaxGreeting())
	fmt.Println()
	fmt.Println("  Looks like this is your first time here.")
	fmt.Println("  Let's get you set up — it takes 30 seconds.")
	fmt.Println()

	// Step 1: Account check. The standalone option is the escape hatch for
	// users without a QualityMax account — without it, an empty key at the
	// paste prompt used to kill the whole setup (QUA: onboarding dead-end).
	choice := PromptChoice("  Do you have a QualityMax account?", []string{
		"Yes, log me in (opens browser)",
		"No, create one (free)",
		"I have an API key already",
		"Skip — use standalone local mode (no account needed)",
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
		if errors.Is(err, ErrEmptyAPIKey) {
			// Empty paste is usually "I don't actually have a key" — offer
			// the standalone exit instead of failing the whole setup.
			retry := PromptChoice("  No key entered. What now?", []string{
				"Try again — paste a key",
				"Skip — use standalone local mode",
			})
			if retry == 0 {
				auth, err = loginWithKeyPrompt()
			} else {
				err = useStandaloneMode()
			}
		}
	case 3: // Skip — standalone local mode
		err = useStandaloneMode()
	}

	if errors.Is(err, ErrStandaloneSkip) {
		tui.AnimateMax(tui.MoodHappy, "Standalone mode it is!")
		fmt.Println()
		fmt.Println("  No account needed. Only local workspace tools are enabled;")
		fmt.Println("  cloud features (crawls, test runs, reviews) stay off.")
		fmt.Println()
		fmt.Println("  Connect later anytime with: qmax-code login")
		fmt.Println("  (after: qmax-code config set local_only false)")
		return nil, 0, ErrStandaloneSkip
	}

	if err != nil {
		tui.AnimateMax(tui.MoodSad, "Login failed: "+err.Error())
		fmt.Println()
		fmt.Println("  Try again with: qmax-code login")
		fmt.Println("  Or skip the account: qmax-code --local")
		return nil, 0, fmt.Errorf("interactive login: %w", err)
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
		confirm := PromptChoice(
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
		key := ReadSecret("  Paste your Anthropic key (sk-ant-...): ")
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

	return auth, projectID, nil
}

// useStandaloneMode persists local_only=true so the choice survives restarts,
// and returns ErrStandaloneSkip so the caller can switch modes cleanly.
func useStandaloneMode() error {
	cfg := api.LoadQMaxCodeConfig()
	if !cfg.LocalOnly {
		cfg.LocalOnly = true
		_ = cfg.Save()
	}
	return ErrStandaloneSkip
}

// loginWithKeyPrompt asks the user to paste their API key.
func loginWithKeyPrompt() (*api.AuthConfig, error) {
	key := ReadSecret("  Paste your API key (qm-...): ")

	if key == "" {
		return nil, ErrEmptyAPIKey
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

	choice := PromptChoice("  Which project do you want to work with?", options)

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

// ReadSecret reads a line of input with characters hidden (replaced with dots).
// Shows a masked preview after completion.
func ReadSecret(prompt string) string {
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

// PromptChoice shows an interactive menu and returns the selected index.
// On a TTY it renders an arrow-key chooser (↑/↓ or j/k to move, Enter to
// confirm, digits 1-9 to select directly). When raw mode is unavailable
// (piped stdin, CI), it falls back to the numeric prompt.
func PromptChoice(prompt string, options []string) int {
	fmt.Println(prompt)

	oldState, rawErr := tui.EnableRawMode()
	if rawErr != nil {
		// Not a TTY (or raw mode unsupported): numeric fallback.
		return promptChoiceNumeric(options)
	}
	defer tui.RestoreTermMode(oldState)

	printMenu := func(sel int, typed string) {
		for i, opt := range options {
			if i == sel {
				fmt.Printf("    \033[36m› %s\033[0m\n", opt)
			} else {
				fmt.Printf("      %s\n", opt)
			}
		}
		if typed != "" {
			fmt.Printf("  \033[2mGo to %s + Enter, or ↑/↓ then Enter\033[0m\n", typed)
		} else {
			fmt.Println("  \033[2m↑/↓ to move · Enter to select · 1-9 jumps\033[0m")
		}
	}

	redraw := func(sel int, typed string) {
		for i := 0; i < len(options)+1; i++ {
			fmt.Print("\033[A\033[2K") // up one line, clear it
		}
		printMenu(sel, typed)
	}

	sel := 0
	typed := ""
	printMenu(sel, typed)

	buf := make([]byte, 1)
	for {
		n, _ := os.Stdin.Read(buf)
		if n == 0 {
			tui.RestoreTermMode(oldState)
			return promptChoiceNumeric(options)
		}
		switch buf[0] {
		case '\r', '\n':
			tui.RestoreTermMode(oldState)
			fmt.Println()
			if typed != "" {
				if idx := parseChoiceLine(typed, len(options)); idx >= 0 {
					sel = idx
				}
			}
			fmt.Printf("  Selected: %s\n", options[sel])
			return sel
		case 3: // Ctrl+C — cancel with the default (first) option
			tui.RestoreTermMode(oldState)
			fmt.Println()
			return 0
		case 27: // ESC — expect '[' then A/B
			b2 := make([]byte, 1)
			if n2, _ := os.Stdin.Read(b2); n2 == 1 && b2[0] == '[' {
				b3 := make([]byte, 1)
				if n3, _ := os.Stdin.Read(b3); n3 == 1 {
					switch b3[0] {
					case 'A': // up
						if sel > 0 {
							sel--
							typed = ""
							redraw(sel, typed)
						}
					case 'B': // down
						if sel < len(options)-1 {
							sel++
							typed = ""
							redraw(sel, typed)
						}
					}
				}
			}
		case 'k':
			if sel > 0 {
				sel--
				typed = ""
				redraw(sel, typed)
			}
		case 'j':
			if sel < len(options)-1 {
				sel++
				typed = ""
				redraw(sel, typed)
			}
		default:
			if buf[0] >= '1' && buf[0] <= '9' {
				candidate := typed + string(buf[0])
				if idx := parseChoiceLine(candidate, len(options)); idx >= 0 {
					sel = idx
					typed = candidate
					redraw(sel, typed)
				}
			}
		}
	}
}

// promptChoiceNumeric prints the classic "Choice (1-N, default 1)" prompt.
func promptChoiceNumeric(options []string) int {
	fmt.Print("  Choice (1-" + strconv.Itoa(len(options)) + ", default 1): ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	if idx := parseChoiceLine(strings.TrimSpace(line), len(options)); idx >= 0 {
		return idx
	}
	return 0
}

// parseChoiceLine converts a typed choice like "3" into a zero-based index.
// Returns -1 for empty or out-of-range input.
func parseChoiceLine(line string, n int) int {
	line = strings.TrimSpace(line)
	if line == "" {
		return -1
	}
	v, err := strconv.Atoi(line)
	if err != nil || v < 1 || v > n {
		return -1
	}
	return v - 1
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
