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
	LocalOnly   bool // true = no QualityMax auth/cloud tools for this process
	QMaxCfg     QMaxConfig
	QMaxBin     string      // resolved path to legacy qmax binary; empty for direct API/local-only paths
	QMaxInfo    string      // output of `qmax status` at startup
	GitInfo     *GitInfo    // git context from cwd
	ProjectFile string      // name of .qmax.yml file if detected
	API         *APIClient  // direct QualityMax API client (no legacy qmax CLI needed)
	Auth        *AuthConfig // authentication credentials
	Backend     string      // "" | "cc" | "codex" | "agy" | "opencode" — active CLI inference backend

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
	InputTokens              int
	OutputTokens             int
	Requests                 int
	CacheReadInputTokens     int      `json:",omitempty"`
	CacheCreationInputTokens int      `json:",omitempty"`
	CostUSD                  *float64 `json:",omitempty"`
}

// TotalInputTokens includes both uncached and cached prompt tokens.
func (u *TokenUsage) TotalInputTokens() int {
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

// TotalTokens returns the total token count.
func (u *TokenUsage) TotalTokens() int {
	return u.TotalInputTokens() + u.OutputTokens
}

// RecordRequest retains each request's estimated API cost before totals lose
// its model, pricing date, and prompt-size tier. Legacy sessions without costs
// use their aggregate estimate for the existing portion of the session.
func (u *TokenUsage) RecordRequest(model string, usage APIUsage) {
	request := TokenUsage{
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
	}
	cost := u.EstimatedCost(model) + request.EstimatedCost(model)
	u.CostUSD = &cost
	u.InputTokens += usage.InputTokens
	u.OutputTokens += usage.OutputTokens
	u.CacheReadInputTokens += usage.CacheReadInputTokens
	u.CacheCreationInputTokens += usage.CacheCreationInputTokens
	u.Requests++
}

// EstimatedCost returns recorded per-request estimates when available. Legacy
// aggregate-only usage is estimated using the supplied model and current rates.
// Cache writes use the five-minute rate; CLI plan billing is provider-owned.
func (u *TokenUsage) EstimatedCost(model string) float64 {
	return u.estimatedCostAt(model, time.Now())
}

func (u *TokenUsage) estimatedCostAt(model string, now time.Time) float64 {
	if u.CostUSD != nil {
		return *u.CostUSD
	}
	var inputRate, outputRate float64
	switch {
	case strings.Contains(model, "fable"):
		inputRate, outputRate = 10.0, 50.0
	case strings.Contains(model, "opus"):
		inputRate, outputRate = 5.0, 25.0
	case model == ModelHaiku55:
		inputRate, outputRate = 0.10, 0.50
		if u.TotalInputTokens() > 100_000 {
			inputRate, outputRate = 0.50, 2.50
		}
	case strings.Contains(model, "haiku"):
		inputRate, outputRate = 1.0, 5.0
	default: // sonnet
		inputRate, outputRate = sonnetCostRates(model, now)
	}
	input := float64(u.InputTokens) + float64(u.CacheReadInputTokens)*0.1 + float64(u.CacheCreationInputTokens)*1.25
	return (input/1_000_000)*inputRate + (float64(u.OutputTokens)/1_000_000)*outputRate
}

func sonnetCostRates(model string, now time.Time) (inputRate, outputRate float64) {
	inputRate, outputRate = 3.0, 15.0
	if strings.Contains(model, "sonnet-5") && now.Before(sonnet5StandardPricingStart) {
		inputRate, outputRate = 2.0, 10.0
	}
	return inputRate, outputRate
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
