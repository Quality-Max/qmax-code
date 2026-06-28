package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/sysutil"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// handleConfigCommand implements the `qmax-code config ...` subcommand
// family. The flag package is avoided here on purpose — subcommand args
// don't fit its one-parse-at-start model, and the surface is small enough
// that manual parsing is clearer.
//
// Usage:
//
//	qmax-code config                   → print current values
//	qmax-code config show              → same as above (explicit)
//	qmax-code config set KEY VALUE     → set a single field
//	qmax-code config unset KEY         → clear a single field
//	qmax-code config reset             → wipe to defaults
//
// Supported keys mirror the public api.Config fields:
//
//	default_framework → "", "playwright", "pytest", "rust_cargo", "go_test"
//	default_project   → integer project ID
//	default_model     → "auto", "sonnet", "opus", "haiku", or known full model ID
//	professional      → bool ("true" / "false")
//	auto_save         → bool
//	output_verbose    → bool (compact vs previous detailed answer style)
//	max_token_budget  → integer
func handleConfigCommand(args []string) {
	if len(args) == 0 || args[0] == "show" {
		printConfig()
		return
	}

	switch args[0] {
	case "set":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: qmax-code config set KEY VALUE")
			os.Exit(2)
		}
		if err := setConfigField(args[1], args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  Set %s = %s\n", args[1], configSetDisplayValue(args[1], args[2]))

	case "unset":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: qmax-code config unset KEY")
			os.Exit(2)
		}
		if err := setConfigField(args[1], ""); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  Cleared %s\n", args[1])

	case "reset":
		cfg := api.DefaultConfig()
		if err := cfg.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("  api.Config reset to defaults")

	default:
		fmt.Fprintf(os.Stderr, "Unknown config command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Try: qmax-code config [show|set|unset|reset]")
		os.Exit(2)
	}
}

func printConfig() {
	cfg := api.LoadQMaxCodeConfig()
	fmt.Println("  Current config (~/.qmax-code/config.json):")
	fmt.Printf("    default_model     = %q\n", cfg.DefaultModel)
	fmt.Printf("    default_project   = %d\n", cfg.DefaultProject)
	fmt.Printf("    default_framework = %q\n", cfg.DefaultFramework)
	fmt.Printf("    professional      = %t\n", cfg.Professional)
	fmt.Printf("    auto_save         = %t\n", cfg.AutoSave)
	fmt.Printf("    output_verbose    = %t\n", cfg.OutputVerbose)
	fmt.Printf("    max_token_budget  = %d\n", cfg.MaxTokenBudget)
	if cfg.AnthropicKey != "" {
		fmt.Println("    anthropic_key     = (set; stored in OS keychain)")
	} else {
		fmt.Println("    anthropic_key     = (not set)")
	}
	if cfg.OllamaURL != "" {
		// Mask credentials in URL for display
		fmt.Printf("    ollama_url        = %q\n", sysutil.MaskURL(cfg.OllamaURL))
		fmt.Printf("    ollama_model      = %q\n", cfg.OllamaModel)
	} else {
		fmt.Println("    ollama_url        = (not set)")
	}
	if cfg.CerebrasKey != "" {
		fmt.Println("    cerebras_key      = (set; stored in OS keychain)")
	} else {
		fmt.Println("    cerebras_key      = (not set)")
	}
	cerebrasModel := cfg.CerebrasModel
	if cerebrasModel == "" {
		cerebrasModel = api.CerebrasDefaultModel + " (default)"
	}
	fmt.Printf("    cerebras_model    = %q\n", cerebrasModel)
	cerebrasBase := cfg.CerebrasBaseURL
	if cerebrasBase == "" {
		cerebrasBase = api.CerebrasAPIBase + " (default)"
	}
	fmt.Printf("    cerebras_base_url = %q\n", cerebrasBase)
	backend := cfg.Backend
	if backend == "" {
		backend = "api"
	}
	theme := cfg.Theme
	if theme == "" {
		theme = "historic"
	}
	fmt.Printf("    theme             = %q  (available: historic, ocean, neon, ember, aurora, paper, sky, sparkling, radiance, goldenhour)\n", theme)
	cloudSync := "not set (will prompt on next eligible session)"
	if cfg.CloudSync != nil {
		if *cfg.CloudSync {
			cloudSync = "enabled"
		} else {
			cloudSync = "disabled"
		}
	}
	fmt.Printf("    cloud_sync        = %s\n", cloudSync)
	fmt.Printf("    live_feed         = %t  (when on: test runs / AI crawls execute in QM Cloud Sandbox with live ASCII feed)\n", cfg.LiveFeed)
	fmt.Printf("    backend           = %q", backend)
	switch backend {
	case "cc":
		if bin := agent.FindClaudeCode(); bin != "" {
			fmt.Printf("  (claude found: %s)", bin)
		} else {
			fmt.Print("  (WARNING: claude binary not found in PATH)")
		}
	case "codex":
		if bin := agent.FindCodex(); bin != "" {
			fmt.Printf("  (codex found: %s)", bin)
		} else {
			fmt.Print("  (WARNING: codex binary not found in PATH)")
		}
	case "cerebras":
		if cfg.CerebrasKey != "" {
			fmt.Printf("  (cerebras key set; model: %s)", cerebrasModel)
		} else {
			fmt.Print("  (WARNING: CEREBRAS_API_KEY not set)")
		}
	}
	fmt.Println()
}

// setConfigField writes the given value into the Config. Empty value
// clears the field (same semantics as `unset`). Validation is strict —
// wrong value types return an error so users find out immediately rather
// than discovering a silently-ignored setting later.
func setConfigField(key, value string) error {
	cfg := api.LoadQMaxCodeConfig()

	switch key {
	case "default_framework":
		if value != "" && !api.AllowedFrameworks[value] {
			return fmt.Errorf("invalid framework %q; allowed: playwright, pytest, go, rust, go_test, rust_cargo", value)
		}
		cfg.DefaultFramework = value

	case "default_project":
		if value == "" {
			cfg.DefaultProject = 0
		} else {
			n, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("default_project must be an integer, got %q", value)
			}
			cfg.DefaultProject = n
		}

	case "default_model":
		if value != "" && !api.IsValidClaudeModelName(value) {
			return fmt.Errorf("invalid default_model %q; allowed: %s", value, api.ValidClaudeModelsHelp())
		}
		cfg.DefaultModel = api.ResolveClaudeModel(value)

	case "professional":
		b, err := parseConfigBool(value)
		if err != nil {
			return err
		}
		cfg.Professional = b

	case "auto_save":
		b, err := parseConfigBool(value)
		if err != nil {
			return err
		}
		cfg.AutoSave = b

	case "output_verbose":
		b, err := parseConfigBool(value)
		if err != nil {
			return err
		}
		cfg.OutputVerbose = b

	case "max_token_budget":
		if value == "" {
			cfg.MaxTokenBudget = 200000
		} else {
			n, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("max_token_budget must be an integer, got %q", value)
			}
			cfg.MaxTokenBudget = n
		}

	case "ollama_url":
		cfg.OllamaURL = value

	case "ollama_model":
		cfg.OllamaModel = value

	case "ollama_agent_model":
		cfg.OllamaAgentModel = value

	case "backend":
		switch value {
		case "", "api", "cc", "codex", "cerebras":
			cfg.Backend = value
		default:
			return fmt.Errorf("invalid backend %q; allowed: api, cc, codex, cerebras", value)
		}

	case "cerebras_key":
		// Stored in the OS keychain, never in config.json. Empty clears it.
		if value != "" {
			looks, verr := api.ValidateCerebrasKey(value)
			if verr != nil {
				return fmt.Errorf("invalid cerebras_key: %w", verr)
			}
			if !looks {
				fmt.Fprintln(os.Stderr, "  Note: key doesn't start with \"csk-\" — double-check it's a Cerebras key.")
			}
		}
		return api.SaveCerebrasKey(value)

	case "cerebras_model":
		cfg.CerebrasModel = value

	case "cerebras_base_url":
		cfg.CerebrasBaseURL = value

	case "theme":
		return tui.SaveTheme(cfg, value)

	case "cloud_sync":
		if value == "" {
			cfg.CloudSync = nil
		} else {
			b, err := parseConfigBool(value)
			if err != nil {
				return err
			}
			cfg.CloudSync = &b
		}

	case "live_feed", "live-feed", "livefeed":
		b, err := parseConfigBool(value)
		if err != nil {
			return err
		}
		cfg.LiveFeed = b

	default:
		return fmt.Errorf("unknown config key %q", key)
	}

	return cfg.Save()
}

func parseConfigBool(s string) (bool, error) {
	switch s {
	case "", "false", "no", "0", "off":
		return false, nil
	case "true", "yes", "1", "on":
		return true, nil
	}
	return false, fmt.Errorf("expected true/false, got %q", s)
}

func configSetDisplayValue(key, value string) string {
	switch key {
	case "cerebras_key":
		if value == "" {
			return "(cleared)"
		}
		return "(set; stored in OS keychain)"
	default:
		return value
	}
}
