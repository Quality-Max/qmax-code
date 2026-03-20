package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SessionContext holds the runtime context for the agent session.
type SessionContext struct {
	ProjectID   int
	QMaxCfg     QMaxConfig
	QMaxBin     string   // resolved path to qmax binary
	QMaxInfo    string   // output of `qmax status` at startup
	GitInfo     *GitInfo // git context from cwd
	ProjectFile string   // name of .qmax.yml file if detected
}

// QMaxConfig mirrors the qmax CLI config (~/.qamax/config.json).
type QMaxConfig struct {
	CloudURL string `json:"api_url"`
	Token    string `json:"token"`
	Email    string `json:"email"`
	AgentID  string `json:"agent_id"`
	APIKey   string `json:"api_key"`
}

// GitInfo holds basic git context from the current directory.
type GitInfo struct {
	Branch        string
	RemoteURL     string
	RecentCommits string
	ChangedFiles  []string
}

// TokenUsage tracks cumulative token usage across the session.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	Requests     int
}

// TotalTokens returns the total token count.
func (u *TokenUsage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens
}

// EstimatedCost returns estimated cost in USD.
// Pricing: Sonnet input=$3/MTok output=$15/MTok, Opus input=$15/MTok output=$75/MTok, Haiku input=$0.25/MTok output=$1.25/MTok
func (u *TokenUsage) EstimatedCost(model string) float64 {
	var inputRate, outputRate float64
	switch {
	case strings.Contains(model, "opus"):
		inputRate, outputRate = 15.0, 75.0
	case strings.Contains(model, "haiku"):
		inputRate, outputRate = 0.25, 1.25
	default: // sonnet
		inputRate, outputRate = 3.0, 15.0
	}
	return (float64(u.InputTokens)/1_000_000)*inputRate + (float64(u.OutputTokens)/1_000_000)*outputRate
}

// loadQMaxConfig reads the qmax CLI config file.
// Returns an empty config if the file doesn't exist — the user can log in via the qmax CLI.
func loadQMaxConfig() QMaxConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return QMaxConfig{}
	}

	configPath := filepath.Join(home, ".qamax", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return QMaxConfig{}
	}

	var cfg QMaxConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return QMaxConfig{}
	}

	return cfg
}

// discoverQMaxBinary finds the qmax binary, checking multiple locations.
// Order: ./qmax, ~/.qmax/qmax, then PATH.
func discoverQMaxBinary() string {
	// 1. Current directory
	if _, err := os.Stat("./qmax"); err == nil {
		return "./qmax"
	}

	// 2. ~/.qamax/qmax
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".qamax", "qmax")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// 3. PATH
	if p, err := exec.LookPath("qmax"); err == nil {
		return p
	}

	return ""
}

// probeQMaxStatus runs `qmax status` to get auth/account info.
func probeQMaxStatus(binary string) string {
	if binary == "" {
		return ""
	}
	cmd := exec.Command(binary, "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// formatQMaxInstallHint returns install instructions when qmax is missing.
func formatQMaxInstallHint() string {
	return "qmax CLI not found. Install it:\n\n  curl -fsSL https://get.qualitymax.io/cli | sh\n\nOr download from: https://docs.qualitymax.io/cli"
}

// detectProjectFromCwd checks for .qmax.yml or .qualitymax.yml in cwd.
func detectProjectFromCwd() (int, string) {
	for _, name := range []string{".qmax.yml", ".qualitymax.yml", ".qmax.yaml", ".qualitymax.yaml"} {
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		// Simple YAML parsing — just look for project_id: N
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "project_id:") || strings.HasPrefix(line, "project-id:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					id := strings.TrimSpace(parts[1])
					n, _ := strconv.Atoi(id)
					if n > 0 {
						return n, name
					}
				}
			}
		}
	}
	return 0, ""
}

// detectGitInfo reads basic git info from the current directory.
func detectGitInfo() *GitInfo {
	// Check if we're in a git repo
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	if err := cmd.Run(); err != nil {
		return nil
	}

	info := &GitInfo{}

	// Branch
	if out, err := exec.Command("git", "branch", "--show-current").Output(); err == nil {
		info.Branch = strings.TrimSpace(string(out))
	}

	// Remote URL
	if out, err := exec.Command("git", "remote", "get-url", "origin").Output(); err == nil {
		info.RemoteURL = strings.TrimSpace(string(out))
	}

	// Recent commits (last 3)
	if out, err := exec.Command("git", "log", "--oneline", "-3").Output(); err == nil {
		info.RecentCommits = strings.TrimSpace(string(out))
	}

	// Changed files
	if out, err := exec.Command("git", "diff", "--name-only", "HEAD").Output(); err == nil {
		changed := strings.TrimSpace(string(out))
		if changed != "" {
			info.ChangedFiles = strings.Split(changed, "\n")
		}
	}

	return info
}
