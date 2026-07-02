package api

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SessionContext holds the runtime context for the agent session.
type SessionContext struct {
	ProjectID   int
	QMaxCfg     QMaxConfig
	QMaxBin     string      // resolved path to qmax binary (empty = standalone mode)
	QMaxInfo    string      // output of `qmax status` at startup
	GitInfo     *GitInfo    // git context from cwd
	ProjectFile string      // name of .qmax.yml file if detected
	API         *APIClient  // direct API client (standalone mode, no qmax CLI needed)
	Auth        *AuthConfig // authentication credentials
	Backend     string      // "" | "cc" | "codex" — active CLI inference backend

	// LiveFeed enables QM Cloud Sandbox execution for run_test / start_crawl
	// and turns on auto-launch of /browserfeed when a poll response surfaces
	// a live URL. Mirrors Config.LiveFeed; refreshed on every /set live_feed
	// or /live toggle so the dispatcher always sees the current value.
	LiveFeed bool

	// LastLiveURL holds the most recent `live_browser_url` extracted from a
	// run/crawl status poll. The REPL drains this between agent turns to
	// auto-launch the feed; tool handlers write to it via captureLiveURL.
	LastLiveURL string

	// turn-scoped diagnostic flags reset by the REPL before each agent
	// invocation. Make captureLiveURL chatty *once* per turn rather than
	// per poll, so users see "live URL captured" or a fallback warning
	// without having every poll spam the screen.
	LiveURLLogged       bool
	SandboxModeLogged   bool
	SandboxFallbackSeen bool
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

// EstimatedCost returns estimated cost in USD, based on standard per-MTok API
// rates (https://platform.claude.com/docs/en/about-claude/pricing). The 1M
// context window bills at these same standard rates, so a flat rate per model
// is correct. Pricing: Fable 5 input=$10/MTok output=$50/MTok,
// Opus 4.6/4.7/4.8 input=$5/MTok output=$25/MTok, Sonnet 4.6/5 input=$3/MTok
// output=$15/MTok, Haiku 4.5 input=$1/MTok output=$5/MTok.
func (u *TokenUsage) EstimatedCost(model string) float64 {
	var inputRate, outputRate float64
	switch {
	case strings.Contains(model, "fable"):
		inputRate, outputRate = 10.0, 50.0
	case strings.Contains(model, "opus"):
		inputRate, outputRate = 5.0, 25.0
	case strings.Contains(model, "haiku"):
		inputRate, outputRate = 1.0, 5.0
	default: // sonnet
		inputRate, outputRate = 3.0, 15.0
	}
	return (float64(u.InputTokens)/1_000_000)*inputRate + (float64(u.OutputTokens)/1_000_000)*outputRate
}

// LoadQMaxConfig reads the qmax CLI config file.
// Returns an empty config if the file doesn't exist — the user can log in via the qmax CLI.
func LoadQMaxConfig() QMaxConfig {
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

// DiscoverQMaxBinary finds the qmax binary, checking multiple locations.
// Order: ./qmax, ~/.qmax/qmax, then PATH.
func DiscoverQMaxBinary() string {
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

// ProbeQMaxStatus runs `qmax status` to get auth/account info with a timeout.
func ProbeQMaxStatus(binary string) string {
	if binary == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "status")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// FormatQMaxInstallHint returns install instructions when qmax is missing.
func FormatQMaxInstallHint() string {
	return "qmax CLI not found. Install it:\n\n  curl -fsSL https://get.qualitymax.io/cli | sh\n\nOr download from: https://docs.qualitymax.io/cli"
}

// DetectProjectFromCwd checks for .qmax.yml or .qualitymax.yml in cwd.
func DetectProjectFromCwd() (int, string) {
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

// DetectGitInfo reads basic git info from the current directory.
func DetectGitInfo() *GitInfo {
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
