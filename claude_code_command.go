package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/setup"
)

const maxClaudeCodeAuthBytes = 64 * 1024

var (
	loadQualityMaxAuthForCC = api.LoadAuth
	loginQualityMaxForCC    = setup.LoginViaBrowser
	uploadClaudeCodeAuth    = func(ctx context.Context, auth *api.AuthConfig, authJSON string) (*api.ClaudeCodeConnection, error) {
		return api.NewAPIClient(auth).ConnectClaudeCode(ctx, authJSON)
	}
)

func handleCCCommand(args []string) error {
	if len(args) != 1 || args[0] != "connect" {
		return errors.New("usage: qmax-code cc connect")
	}
	return connectClaudeCode(context.Background())
}

func loadClaudeCodeFromKeychain() (string, error) {
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("security", "find-generic-password", "-s", "claude-code", "-w")
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("not found in macOS keychain")
		}
		return strings.TrimSpace(string(out)), nil
	}
	return "", fmt.Errorf("keychain not supported on %s", runtime.GOOS)
}

func readClaudeCodeAuth() (string, error) {
	// 1. Try macOS keychain first
	if runtime.GOOS == "darwin" {
		if creds, err := loadClaudeCodeFromKeychain(); err == nil && creds != "" {
			return creds, nil
		}
	}

	// 2. Try file fallbacks
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}

	paths := []string{
		filepath.Join(home, ".config", "claude", "credentials.json"),
		filepath.Join(home, ".claude", "credentials.json"),
		filepath.Join(home, ".config", "claude-code", "credentials.json"),
		filepath.Join(home, "Library", "Application Support", "claude", "claude-code", "credentials.json"),
	}

	var lastErr error
	for _, path := range paths {
		if file, err := os.Open(path); err == nil {
			defer file.Close()
			data, err := io.ReadAll(io.LimitReader(file, maxClaudeCodeAuthBytes+1))
			if err != nil {
				lastErr = err
				continue
			}
			if len(data) > maxClaudeCodeAuthBytes {
				lastErr = fmt.Errorf("%s is larger than %d bytes", path, maxClaudeCodeAuthBytes)
				continue
			}
			return string(data), nil
		} else {
			lastErr = err
		}
	}

	return "", fmt.Errorf("could not find Claude Code credentials in keychain or files. Make sure you run 'claude' once first to authenticate (last error: %v)", lastErr)
}

func connectClaudeCode(ctx context.Context) error {
	fmt.Println("Reading your Claude Code active login session...")
	authJSON, err := readClaudeCodeAuth()
	if err != nil {
		return err
	}

	// Validate it's a valid JSON object
	var parsed map[string]any
	if err := json.Unmarshal([]byte(authJSON), &parsed); err != nil {
		return fmt.Errorf("credentials are not a valid JSON object: %w", err)
	}

	auth := loadQualityMaxAuthForCC()
	if auth == nil || !auth.IsAuthenticated() {
		fmt.Println("Connect this computer to QualityMax in your browser...")
		auth, err = loginQualityMaxForCC()
		if err != nil {
			return fmt.Errorf("QualityMax login failed: %w", err)
		}
	}

	connection, err := uploadClaudeCodeAuth(ctx, auth, authJSON)
	var statusErr *api.HTTPStatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
		fmt.Println("Your QualityMax login expired. Reauthorizing...")
		auth, err = loginQualityMaxForCC()
		if err != nil {
			return fmt.Errorf("QualityMax login failed: %w", err)
		}
		connection, err = uploadClaudeCodeAuth(ctx, auth, authJSON)
	}
	if err != nil {
		return fmt.Errorf("attach Claude Code to QualityMax: %w", err)
	}

	if connection.AccountLabel != "" {
		fmt.Printf("Claude Code connected to QualityMax as %s.\n", connection.AccountLabel)
	} else {
		fmt.Println("Claude Code connected to QualityMax.")
	}
	return nil
}
