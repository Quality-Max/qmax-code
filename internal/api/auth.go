package api

import (
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
const DefaultCloudURL = "https://app.qualitymax.io"

// LoadAuth loads authentication from (in priority order):
// 1. QUALITYMAX_API_KEY env var
// 2. ~/.qmax-code/auth.json
// 3. Legacy ~/.qamax/config.json (backward compat with qmax CLI)
func LoadAuth() *AuthConfig {
	// 1. Environment variable
	if key := os.Getenv("QUALITYMAX_API_KEY"); key != "" {
		return &AuthConfig{
			APIKey:   key,
			CloudURL: envOr("QUALITYMAX_URL", DefaultCloudURL),
		}
	}

	// 2. ~/.qmax-code/auth.json
	if cfg := loadAuthFile(); cfg != nil && cfg.APIKey != "" {
		return cfg
	}

	// 3. Legacy ~/.qamax/config.json. We read it as a small local shape rather
	// than importing the qmax-CLI QMaxConfig type, so this file has no
	// dependencies on other root-package types.
	legacy := loadLegacyQMaxAuth()
	if legacy.Token != "" || legacy.APIKey != "" {
		// Prefer JWT token over agent API key (hex key is for agent registration, not user auth)
		key := legacy.Token
		if key == "" {
			key = legacy.APIKey
		}
		url := legacy.CloudURL
		if url == "" {
			url = DefaultCloudURL
		}
		cfg := &AuthConfig{
			APIKey:   key,
			Email:    legacy.Email,
			CloudURL: url,
		}
		// Legacy config often has no email — fetch from /api/me and migrate to auth.json
		if cfg.Email == "" {
			if me := fetchMe(cfg); me != nil {
				if email, ok := me["email"].(string); ok {
					cfg.Email = email
				}
				if uid, ok := me["id"].(string); ok {
					cfg.UserID = uid
				}
				// Save to new auth.json so we don't need legacy next time
				_ = SaveAuth(cfg)
			}
		}
		return cfg
	}

	return nil
}

// SaveAuth persists auth credentials to ~/.qmax-code/auth.json.
func SaveAuth(cfg *AuthConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, QmaxCodeConfigDir)
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
		return DefaultCloudURL
	}
	return strings.TrimRight(a.CloudURL, "/")
}

// LoginWithAPIKey validates an API key against QualityMax and saves it.
func LoginWithAPIKey(apiKey string) (*AuthConfig, error) {
	cloudURL := envOr("QUALITYMAX_URL", DefaultCloudURL)

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

// --- helpers ---

// fetchMe calls /api/me to get user info (email, id) from the API.
func fetchMe(cfg *AuthConfig) map[string]interface{} {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", cfg.GetCloudURL()+"/api/me", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+stripKeyPrefix(cfg.APIKey))
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var me map[string]interface{}
	_ = json.Unmarshal(body, &me)
	return me
}

func loadAuthFile() *AuthConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	path := filepath.Join(home, QmaxCodeConfigDir, authFileName)
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

// legacyQMaxAuth is the subset of fields from the qmax CLI config we care
// about for the legacy auth fallback. Reading these inline avoids depending
// on the (richer) QMaxConfig type defined in context.go.
type legacyQMaxAuth struct {
	CloudURL string `json:"api_url"`
	Token    string `json:"token"`
	Email    string `json:"email"`
	APIKey   string `json:"api_key"`
}

func loadLegacyQMaxAuth() legacyQMaxAuth {
	home, err := os.UserHomeDir()
	if err != nil {
		return legacyQMaxAuth{}
	}
	data, err := os.ReadFile(filepath.Join(home, ".qamax", "config.json"))
	if err != nil {
		return legacyQMaxAuth{}
	}
	var cfg legacyQMaxAuth
	_ = json.Unmarshal(data, &cfg)
	return cfg
}
