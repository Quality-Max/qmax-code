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
	json.Unmarshal(body, &me)

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
