package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// RunInteractiveSetup guides a first-time user through login and project selection.
// Returns the AuthConfig and selected project ID.
func RunInteractiveSetup() (*AuthConfig, int) {
	fmt.Println()
	AnimateMax(MoodWaving, GetMaxGreeting())
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

	var auth *AuthConfig
	var err error

	switch choice {
	case 0: // Yes, log me in → browser auth (Railway-style)
		AnimateMax(MoodThinking, "Opening browser...")
		auth, err = LoginViaBrowser()
	case 1: // No, create one
		openBrowser("https://qualitymax.io/auth/email/signup?ref=qmax-code")
		fmt.Println()
		fmt.Println("  Browser opened! Create your free account, then come back.")
		fmt.Println("  Press Enter when you're ready to log in...")
		waitForEnter()
		AnimateMax(MoodThinking, "Opening browser...")
		auth, err = LoginViaBrowser()
	case 2: // I have an API key
		auth, err = loginWithKeyPrompt()
	}

	if err != nil {
		AnimateMax(MoodSad, "Login failed: "+err.Error())
		fmt.Println()
		fmt.Println("  Try again with: qmax-code login")
		os.Exit(1)
	}

	// Show success
	AnimateMaxTransition(MoodThinking, MoodExcited, "")
	fmt.Printf("  Logged in as %s\n", auth.Email)
	fmt.Println()

	// Step 2: Project selection
	projectID := selectProject(auth)

	// Step 3: Anthropic key check
	cfg := LoadQMaxCodeConfig()
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicKey == "" {
		anthropicKey = cfg.AnthropicKey
	}
	if anthropicKey == "" {
		fmt.Println()
		AnimateMax(MoodThinking, "One more thing...")
		fmt.Println()
		fmt.Println("  I need an Anthropic API key to think (that's my brain!).")
		fmt.Println("  Get one at: https://console.anthropic.com/settings/keys")
		fmt.Println()
		key := readSecret("  Paste your Anthropic key (sk-ant-...): ")
		if key != "" {
			os.Setenv("ANTHROPIC_API_KEY", key)
			// Save to OS keychain
			if err := SaveAnthropicKey(key); err != nil {
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
	AnimateMaxTransition(MoodNeutral, MoodHappy, "All set! Let's hunt some bugs.")
	fmt.Println()
	fmt.Println("  Examples:")
	fmt.Println("    \"crawl staging.myapp.com and generate tests\"")
	fmt.Println("    \"show me all failing tests\"")
	fmt.Println("    \"review the latest PR for security issues\"")
	fmt.Println()

	return auth, projectID
}

// loginWithKeyPrompt asks the user to paste their API key.
func loginWithKeyPrompt() (*AuthConfig, error) {
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
				fmt.Printf("\r  Validating %s", SpinnerFrames[i%len(SpinnerFrames)])
				i++
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	auth, err := LoginWithAPIKey(key)
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
func selectProject(auth *AuthConfig) int {
	api := NewAPIClient(auth)
	if api == nil {
		return 0
	}

	fmt.Println("  Loading projects...")
	result := api.ListProjects(context.Background())

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
	cfg := LoadQMaxCodeConfig()
	cfg.DefaultProject = id
	_ = cfg.Save()

	return id
}

// readSecret reads a line of input with characters hidden (replaced with dots).
// Shows a masked preview after completion.
func readSecret(prompt string) string {
	fmt.Print(prompt)

	// Switch terminal to raw mode to hide input
	oldState, err := enableRawMode()
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
			restoreTermMode(oldState)
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
			restoreTermMode(oldState)
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
