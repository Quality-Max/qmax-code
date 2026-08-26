package codexrunner

import "errors"

const (
	// DefaultModel is the deterministic Codex model selected by QualityMax when
	// a product client chooses Auto or has no saved preference.
	DefaultModel = "gpt-5.6-terra"
)

var (
	// ErrInvalidModel means a caller supplied a model outside the exact product
	// allowlist. The value is never forwarded to the process boundary.
	ErrInvalidModel = errors.New("codex runner: invalid model")

	supportedModels = [...]string{
		"gpt-5.6-sol",
		DefaultModel,
		"gpt-5.6-luna",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex-spark",
	}
)

// SupportedModels returns a defensive copy of the exact Codex model
// allowlist shared by the QualityMax coding-agent contract.
func SupportedModels() []string {
	models := make([]string, len(supportedModels))
	copy(models, supportedModels[:])
	return models
}

// ValidateModel accepts only exact allowlisted model IDs. Empty and "auto"
// are product-level choices that must be resolved before a durable handoff.
func ValidateModel(model string) error {
	for _, supported := range supportedModels {
		if model == supported {
			return nil
		}
	}
	return ErrInvalidModel
}
