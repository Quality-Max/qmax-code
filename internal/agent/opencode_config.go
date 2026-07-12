package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/sysutil"
)

// OpenCodeConfigPath is the qmax-managed opencode config file. It is pointed at
// the opencode subprocess via the OPENCODE_CONFIG env var, which opencode MERGES
// on top of the user's own ~/.config/opencode/opencode.jsonc — so we never
// clobber the user's config. Kept under ~/.qmax-code so it lives beside the rest
// of qmax's state and is easy to sync.
func OpenCodeConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, api.QmaxCodeConfigDir, "opencode.json")
}

// openCodeStandardPermission is the permission policy written into the managed
// config for "standard" mode. opencode runs are launched with --auto (which
// auto-approves anything NOT explicitly denied), so denying file edits and
// destructive shell here reproduces the intent of CC's standard allowlist:
// reads / search / test runners run freely, but the model cannot mutate files
// or run destructive commands. This is a coarser model than CC's default-deny
// allowlist (unknown non-destructive bash still auto-approves), which is why
// Unattended remains the mode for full autonomy.
func openCodeStandardPermission() map[string]interface{} {
	return map[string]interface{}{
		"edit":     "deny", // covers edit/write/patch — no file mutation in standard
		"webfetch": "allow",
		"bash": map[string]interface{}{
			"*":          "allow",
			"rm":         "deny",
			"rm *":       "deny",
			"rmdir *":    "deny",
			"sudo *":     "deny",
			"chmod *":    "deny",
			"chown *":    "deny",
			"dd *":       "deny",
			"mkfs*":      "deny",
			"git push*":  "deny",
			"git reset*": "deny",
			"git clean*": "deny",
			"> *":        "deny",
		},
	}
}

// WriteOpenCodeConfig regenerates the managed opencode config from the user's
// enabled providers, the qmax MCP server entry, and the permission policy for
// permissionMode ("standard" | "unattended"), and returns its path.
//
// Secrets never land in this file: custom providers reference their key via
// opencode's "{env:VAR}" substitution, and the real key is injected into the
// subprocess environment at launch (see OpenCodeProviderEnv). The file is
// therefore safe to sync while no plaintext key touches disk.
func WriteOpenCodeConfig(cfg *api.Config, sctx *api.SessionContext, permissionMode string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		self = QmaxMCPCommand
	}

	// MCP server entry so opencode can call qmax's tools natively (mirrors the
	// env hand-off used for the cc/codex backends).
	mcpEnv := map[string]string{}
	if sctx != nil {
		if sctx.ProjectID > 0 {
			mcpEnv["QMAX_PROJECT_ID"] = fmt.Sprintf("%d", sctx.ProjectID)
		}
		if sctx.LiveFeed {
			mcpEnv["QMAX_LIVE_FEED"] = "1"
		}
	}
	if path := sysutil.LiveURLFilePath(); path != "" {
		mcpEnv["QMAX_LIVE_URL_FILE"] = path
	}
	if path := sysutil.ExecIDFilePath(); path != "" {
		mcpEnv["QMAX_EXEC_ID_FILE"] = path
	}

	root := map[string]interface{}{
		"$schema": "https://opencode.ai/config.json",
		"mcp": map[string]interface{}{
			"qmax": map[string]interface{}{
				"type":        "local",
				"command":     []string{self, "serve", "--mcp"},
				"enabled":     true,
				"environment": mcpEnv,
			},
		},
	}

	// Custom providers (not on models.dev) need a full openai-compatible block.
	// Known providers (groq/openrouter) are discovered by opencode via models.dev
	// once their standard env var is set, so they get no block here.
	providers := map[string]interface{}{}
	for _, p := range cfg.ActiveProviders() {
		if !p.Custom {
			continue
		}
		models := map[string]interface{}{}
		for _, m := range p.Models {
			models[m.ID] = map[string]interface{}{"name": m.Name}
		}
		apiKeyRef := ""
		if len(p.KeyEnvVars) > 0 {
			apiKeyRef = "{env:" + p.KeyEnvVars[0] + "}"
		}
		providers[p.ID] = map[string]interface{}{
			"npm": "@ai-sdk/openai-compatible",
			"options": map[string]interface{}{
				"baseURL": p.BaseURL,
				"apiKey":  apiKeyRef,
			},
			"models": models,
		}
	}
	if len(providers) > 0 {
		root["provider"] = providers
	}

	// Standard mode gets an explicit deny policy (see openCodeStandardPermission).
	// Unattended relies on --auto with no denies. Anything else (e.g. listing-only
	// regen with "") writes no policy.
	if permissionMode == "standard" {
		root["permission"] = openCodeStandardPermission()
	}

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}

	path := OpenCodeConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	return path, nil
}

// OpenCodeProviderEnv returns the environment variables that carry each enabled
// provider's API key into the opencode subprocess. For custom providers this is
// the "{env:VAR}" name referenced in the config block; for known providers it's
// opencode's standard var (GROQ_API_KEY, …). Keys are read from the OS keychain
// at launch — never written to the config file.
func OpenCodeProviderEnv(cfg *api.Config) map[string]string {
	env := map[string]string{}
	for _, p := range cfg.ActiveProviders() {
		key := api.LoadProviderKey(p.ID)
		if key == "" {
			continue
		}
		for _, name := range p.KeyEnvVars {
			env[name] = key
		}
	}
	return env
}

// OpenCodeModels lists the models opencode exposes for one provider, as
// "provider/model" strings. It shells out to `opencode models <providerID>`
// with OPENCODE_CONFIG pointed at the managed config so custom providers
// (whose models come from our seeded block) resolve too. providerEnv MUST carry
// the provider API keys (from OpenCodeProviderEnv): opencode 1.17 reports
// "Provider not found" for known providers like groq/openrouter when their key
// env var is absent, so without it those providers would never reach the
// picker. Returns nil on error.
func OpenCodeModels(bin, configPath string, providerEnv map[string]string, providerID string) []string {
	if bin == "" || providerID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "models", providerID)
	cmd.Env = append(os.Environ(), "OPENCODE_CONFIG="+configPath)
	for k, v := range providerEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}

	var models []string
	scanner := bufio.NewScanner(&out)
	scanner.Buffer(make([]byte, 1<<16), 1<<20)
	prefix := providerID + "/"
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, prefix) {
			continue
		}
		models = append(models, line)
	}
	return models
}
