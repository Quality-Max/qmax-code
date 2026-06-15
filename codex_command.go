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

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/setup"
)

const maxCodexAuthBytes = 64 * 1024

var (
	findCodexForConnect = agent.FindCodex
	runCodexLogin       = func(bin string) error {
		cmd := exec.Command(bin, "login")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	loadQualityMaxAuth = api.LoadAuth
	loginQualityMax    = setup.LoginViaBrowser
	uploadCodexAuth    = func(ctx context.Context, auth *api.AuthConfig, authJSON string) (*api.CodexConnection, error) {
		return api.NewAPIClient(auth).ConnectCodex(ctx, authJSON)
	}
)

func handleCodexCommand(args []string) error {
	if len(args) != 1 || args[0] != "connect" {
		return errors.New("usage: qmax-code codex connect")
	}
	return connectCodex(context.Background())
}

func connectCodex(ctx context.Context) error {
	bin := findCodexForConnect()
	if bin == "" {
		return errors.New("Codex CLI not found; install it with: npm install -g @openai/codex")
	}

	fmt.Println("Refreshing your Codex login...")
	if err := runCodexLogin(bin); err != nil {
		return fmt.Errorf("codex login failed: %w", err)
	}

	authJSON, err := readCodexAuthFile()
	if err != nil {
		return err
	}

	auth := loadQualityMaxAuth()
	if auth == nil || !auth.IsAuthenticated() {
		fmt.Println("Connect this computer to QualityMax in your browser...")
		auth, err = loginQualityMax()
		if err != nil {
			return fmt.Errorf("QualityMax login failed: %w", err)
		}
	}

	connection, err := uploadCodexAuth(ctx, auth, authJSON)
	var statusErr *api.HTTPStatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
		fmt.Println("Your QualityMax login expired. Reauthorizing...")
		auth, err = loginQualityMax()
		if err != nil {
			return fmt.Errorf("QualityMax login failed: %w", err)
		}
		connection, err = uploadCodexAuth(ctx, auth, authJSON)
	}
	if err != nil {
		return fmt.Errorf("attach Codex to QualityMax: %w", err)
	}

	if connection.AccountLabel != "" {
		fmt.Printf("Codex connected to QualityMax as %s.\n", connection.AccountLabel)
	} else {
		fmt.Println("Codex connected to QualityMax.")
	}
	return nil
}

func readCodexAuthFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	path := filepath.Join(home, ".codex", "auth.json")

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxCodexAuthBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxCodexAuthBytes {
		return "", fmt.Errorf("%s is larger than %d bytes", path, maxCodexAuthBytes)
	}

	var blob struct {
		Tokens map[string]any `json:"tokens"`
	}
	if err := json.Unmarshal(data, &blob); err != nil {
		return "", fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	for _, key := range []string{"access_token", "refresh_token"} {
		value, ok := blob.Tokens[key].(string)
		if !ok || value == "" {
			return "", fmt.Errorf("%s does not contain a valid %s; run codex login again", path, key)
		}
	}
	return string(data), nil
}
