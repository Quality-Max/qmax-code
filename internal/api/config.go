package api

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds persistent user preferences for qmax-code.
type Config struct {
	DefaultModel   string `json:"default_model,omitempty"` // "auto", "sonnet", "opus", "haiku"
	DefaultProject int    `json:"default_project,omitempty"`
	// DefaultFramework is set by the first-run wizard based on filesystem
	// detection (Cargo.toml → rust_cargo, go.mod → go_test, etc.). The agent
	// reads this to default the `framework` param on generate_test_code so
	// Rust/Go users don't get Playwright scripts by accident.
	DefaultFramework string `json:"default_framework,omitempty"` // "", "playwright", "pytest", "rust_cargo", "go_test"
	Professional     bool   `json:"professional,omitempty"`      // disable cat personality
	AutoSave         bool   `json:"auto_save"`                   // auto-save session on exit (default true)
	MaxTokenBudget   int    `json:"max_token_budget,omitempty"`
	AnthropicKey     string `json:"-"` // NOT stored in JSON — use keychain instead

	// Ollama integration — use a self-hosted local model for chat and,
	// optionally, tool dispatch.
	OllamaURL        string `json:"ollama_url,omitempty"`         // e.g. "https://user:pass@llm.example.com"
	OllamaModel      string `json:"ollama_model,omitempty"`       // e.g. "gemma3:4b-it-q4_K_M" (chat)
	OllamaAgentModel string `json:"ollama_agent_model,omitempty"` // e.g. "gemma3:12b-it-q4_K_M" (tools, heavier tasks)

	// Backend selects the LLM inference backend.
	//   ""  / "api" → Anthropic API directly (default, requires ANTHROPIC_API_KEY)
	//   "cc"        → Claude Code CLI subprocess (uses CC subscription, no API key needed)
	//   "codex"     → OpenAI Codex CLI subprocess (uses OpenAI subscription, no API key needed)
	// In both CLI modes qmax tools are served to the CLI via an embedded MCP server.
	Backend string `json:"backend,omitempty"`

	// ModelOverride is the specific model ID selected via the /orch TUI picker.
	// Empty means "let the CLI pick its default". Used with CC and Codex backends.
	ModelOverride string `json:"model_override,omitempty"`

	// Effort controls how thorough the CLI agent should be: "low", "medium", "high".
	// Injected into the system prompt on every turn.
	Effort string `json:"effort,omitempty"`

	// OrchPermissionMode records the autonomy level the user consented to for
	// CC/Codex backends:
	//   ""           = no consent yet; activation will prompt
	//   "standard"   = curated allowlist auto-approved (reads, test runners,
	//                  qmax MCP tools); edits and destructive shell still gated
	//   "unattended" = --dangerously-skip-permissions / --full-auto, full autonomy
	// Persistence avoids re-prompting once chosen; user can revoke via /orch.
	OrchPermissionMode string `json:"orch_permission_mode,omitempty"`

	// OrchGlobalInstall records that the user opted into writing the qmax MCP
	// entry into the CLI's global config (~/.claude/settings.json or
	// ~/.codex/config.toml). False = use a per-session temp config only.
	OrchGlobalInstall bool `json:"orch_global_install,omitempty"`

	// Theme selects the terminal color scheme.
	// Available: historic, ocean, neon, ember, aurora (dark) or paper, sky, sparkling, radiance, goldenhour (light). Empty defaults to "historic".
	Theme string `json:"theme,omitempty"`

	// OutputVerbose controls the user-facing answer style for CLI backends.
	// false = compact terminal reports; true = previous detailed report style.
	OutputVerbose bool `json:"output_verbose,omitempty"`

	// CloudSync controls whether sessions are synced to the QualityMax cloud.
	// nil = not asked yet (prompt fires on the next eligible session).
	// true = opted in, false = opted out.
	CloudSync *bool `json:"cloud_sync,omitempty"`

	// LiveFeed opts every test run / AI crawl into running inside a QM Cloud
	// Sandbox so the browser is streamed live into the terminal. When true,
	// MCP tool wrappers (run_test, run_tests_batch, start_crawl) flip the
	// server's use_e2b flag and the REPL auto-launches /browserfeed at the
	// end of each agent turn that produced a live URL.
	//
	// Default false — sandbox runs cost more than the pooled runners, and
	// users should opt in deliberately. Toggle via /live on|off or
	// /set live_feed true|false.
	LiveFeed bool `json:"live_feed,omitempty"`
}

const QmaxCodeConfigDir = ".qmax-code"
const qmaxCodeConfigFile = "config.json"

// LoadQMaxCodeConfig reads persistent user preferences from ~/.qmax-code/config.json
// and loads the Anthropic key from the OS keychain.
func LoadQMaxCodeConfig() *Config {
	cfg := DefaultConfig()

	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, QmaxCodeConfigDir, qmaxCodeConfigFile)
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, cfg)
		}
	}

	// Load Anthropic key from OS keychain (never stored in JSON)
	if key, err := LoadFromKeychain("anthropic_key"); err == nil && key != "" {
		cfg.AnthropicKey = key
	}

	// Env vars override config file for Ollama (useful for Railway/CI)
	if url := os.Getenv("OLLAMA_BASE_URL"); url != "" {
		cfg.OllamaURL = url
	}
	if model := os.Getenv("OLLAMA_MODEL"); model != "" {
		cfg.OllamaModel = model
	}

	return cfg
}

// SaveAnthropicKey securely stores the Anthropic API key in the OS keychain.
func SaveAnthropicKey(key string) error {
	return SaveToKeychain("anthropic_key", key)
}

// Save persists the config to ~/.qmax-code/config.json.
func (c *Config) Save() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, QmaxCodeConfigDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, qmaxCodeConfigFile), data, 0600)
}

func DefaultConfig() *Config {
	return &Config{
		DefaultModel:   "auto",
		AutoSave:       true,
		MaxTokenBudget: 200000,
	}
}
