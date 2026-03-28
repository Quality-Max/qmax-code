package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const keychainService = "qmax-code"

// SaveToKeychain securely stores a key in the OS keychain.
// macOS: Keychain Access, Linux: secret-tool, Fallback: config.json (0600 perms)
func SaveToKeychain(account, secret string) error {
	switch runtime.GOOS {
	case "darwin":
		// Delete existing entry first (ignore errors)
		_ = exec.Command("security", "delete-generic-password",
			"-s", keychainService, "-a", account).Run()
		// Add new entry
		cmd := exec.Command("security", "add-generic-password",
			"-s", keychainService, "-a", account,
			"-w", secret, "-U")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("keychain save failed: %s", strings.TrimSpace(string(out)))
		}
		return nil

	case "linux":
		cmd := exec.Command("secret-tool", "store",
			"--label", fmt.Sprintf("qmax-code %s", account),
			"service", keychainService, "account", account)
		cmd.Stdin = strings.NewReader(secret)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("secret-tool not available: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("keychain not supported on %s", runtime.GOOS)
	}
}

// LoadFromKeychain retrieves a key from the OS keychain.
func LoadFromKeychain(account string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("security", "find-generic-password",
			"-s", keychainService, "-a", account, "-w").Output()
		if err != nil {
			return "", fmt.Errorf("not found in keychain")
		}
		return strings.TrimSpace(string(out)), nil

	case "linux":
		out, err := exec.Command("secret-tool", "lookup",
			"service", keychainService, "account", account).Output()
		if err != nil {
			return "", fmt.Errorf("not found in secret-tool")
		}
		return strings.TrimSpace(string(out)), nil

	default:
		return "", fmt.Errorf("keychain not supported on %s", runtime.GOOS)
	}
}

// DeleteFromKeychain removes a key from the OS keychain.
func DeleteFromKeychain(account string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("security", "delete-generic-password",
			"-s", keychainService, "-a", account).Run()
	case "linux":
		return exec.Command("secret-tool", "clear",
			"service", keychainService, "account", account).Run()
	default:
		return nil
	}
}
