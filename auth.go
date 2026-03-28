package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AuthConfig holds QualityMax authentication credentials.
type AuthConfig struct {
	APIKey   string `json:"api_key"`
	Email    string `json:"email,omitempty"`
	UserID   string `json:"user_id,omitempty"`
	CloudURL string `json:"cloud_url,omitempty"`
}

const authFileName = "auth.json"
const defaultCloudURL = "https://app.qualitymax.io"

// LoadAuth loads authentication from (in priority order):
// 1. QUALITYMAX_API_KEY env var
// 2. ~/.qmax-code/auth.json
// 3. Legacy ~/.qamax/config.json (backward compat with qmax CLI)
func LoadAuth() *AuthConfig {
	// 1. Environment variable
	if key := os.Getenv("QUALITYMAX_API_KEY"); key != "" {
		return &AuthConfig{
			APIKey:   key,
			CloudURL: envOr("QUALITYMAX_URL", defaultCloudURL),
		}
	}

	// 2. ~/.qmax-code/auth.json
	if cfg := loadAuthFile(); cfg != nil && cfg.APIKey != "" {
		return cfg
	}

	// 3. Legacy ~/.qamax/config.json
	legacy := loadQMaxConfig()
	if legacy.Token != "" || legacy.APIKey != "" {
		key := legacy.APIKey
		if key == "" {
			key = legacy.Token
		}
		url := legacy.CloudURL
		if url == "" {
			url = defaultCloudURL
		}
		return &AuthConfig{
			APIKey:   key,
			Email:    legacy.Email,
			CloudURL: url,
		}
	}

	return nil
}

// SaveAuth persists auth credentials to ~/.qmax-code/auth.json.
func SaveAuth(cfg *AuthConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, qmaxCodeConfigDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, authFileName), data, 0600)
}

// IsAuthenticated returns true if we have a valid API key.
func (a *AuthConfig) IsAuthenticated() bool {
	return a != nil && a.APIKey != ""
}

// GetCloudURL returns the QualityMax API URL.
func (a *AuthConfig) GetCloudURL() string {
	if a == nil || a.CloudURL == "" {
		return defaultCloudURL
	}
	return strings.TrimRight(a.CloudURL, "/")
}

// LoginWithAPIKey validates an API key against QualityMax and saves it.
func LoginWithAPIKey(apiKey string) (*AuthConfig, error) {
	cloudURL := envOr("QUALITYMAX_URL", defaultCloudURL)

	// Validate by calling /api/me
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", cloudURL+"/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+stripKeyPrefix(apiKey))

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach QualityMax: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("invalid API key (HTTP %d)", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var me map[string]interface{}
	_ = json.Unmarshal(body, &me)

	cfg := &AuthConfig{
		APIKey:   apiKey,
		CloudURL: cloudURL,
	}
	if email, ok := me["email"].(string); ok {
		cfg.Email = email
	}
	if uid, ok := me["id"].(string); ok {
		cfg.UserID = uid
	}

	if err := SaveAuth(cfg); err != nil {
		return cfg, fmt.Errorf("logged in but failed to save: %w", err)
	}

	return cfg, nil
}

// LoginInteractive prompts the user to paste their API key.
func LoginInteractive() (*AuthConfig, error) {
	fmt.Println()
	fmt.Println("  Get your API key from:")
	fmt.Println("  https://app.qualitymax.io/settings → API Keys")
	fmt.Println()
	fmt.Print("  Paste your API key (qm-...): ")

	reader := bufio.NewReader(os.Stdin)
	key, _ := reader.ReadString('\n')
	key = strings.TrimSpace(key)

	if key == "" {
		return nil, fmt.Errorf("no API key provided")
	}

	return LoginWithAPIKey(key)
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
func LoginViaBrowser() (*AuthConfig, error) {
	cloudURL := envOr("QUALITYMAX_URL", defaultCloudURL)
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

	// Step 3: Poll until authorized (every 2 seconds, up to 10 minutes)
	pollURL := cloudURL + "/api/auth/cli-poll?code=" + loginResp.Code
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
			cfg := &AuthConfig{
				APIKey:   poll.Token,
				Email:    poll.Email,
				UserID:   poll.UserID,
				CloudURL: cloudURL,
			}
			if err := SaveAuth(cfg); err != nil {
				return cfg, fmt.Errorf("logged in but failed to save: %w", err)
			}
			return cfg, nil

		case "expired":
			return nil, fmt.Errorf("auth code expired — please try again")

		default:
			// Still pending — show spinner
			fmt.Printf("\r  Waiting %s", SpinnerFrames[i%len(SpinnerFrames)])
			i++
		}
	}

	return nil, fmt.Errorf("timed out waiting for browser authorization")
}

// --- helpers ---

func loadAuthFile() *AuthConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	path := filepath.Join(home, qmaxCodeConfigDir, authFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var cfg AuthConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}

func stripKeyPrefix(key string) string {
	if strings.HasPrefix(key, "qm-") {
		return key[3:]
	}
	return key
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
