package main

import (
	"fmt"

	"github.com/qualitymax/qmax-code/codexrunner"
	"github.com/qualitymax/qmax-code/internal/api"
)

// resolveSessionModel applies CLI selection to the active harness and returns
// the Claude model for the built-in loop. OpenAI IDs must never reach that loop.
func resolveSessionModel(cfg *api.Config, requested string) (string, error) {
	if cfg.Backend == "codex" {
		model := cfg.CodexModel
		if requested != "" {
			model = requested
			if model == "auto" {
				model = ""
			}
		}
		if model != "" && codexrunner.ValidateModel(model) != nil {
			return "", fmt.Errorf("unrecognized Codex model %q; use auto or one of %v", model, codexrunner.SupportedModels())
		}
		cfg.CodexModel = model
		return "auto", nil
	}

	model := requested
	if model == "" {
		model = cfg.DefaultModel
	}
	if model == "" {
		model = "auto"
	}
	model = api.ResolveClaudeModel(model)
	if !api.IsValidClaudeModelName(model) {
		return "", fmt.Errorf("unrecognized Claude model %q; valid: %s", model, api.ValidClaudeModelsHelp())
	}
	if cfg.Backend == "cc" && requested != "" {
		cfg.ModelOverride = model
		if model == "auto" {
			cfg.ModelOverride = ""
		}
	}
	return model, nil
}
