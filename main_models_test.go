package main

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestResolveSessionModel(t *testing.T) {
	for _, tc := range []struct {
		name, backend, requested, savedCodex, wantModel, wantCodex, wantClaude string
		invalid                                                                bool
	}{
		{name: "Astra flag", backend: "codex", requested: "gpt-6-astra", wantModel: "auto", wantCodex: "gpt-6-astra", wantClaude: "saved-claude"},
		{name: "saved Astra", backend: "codex", savedCodex: "gpt-6-astra", wantModel: "auto", wantCodex: "gpt-6-astra", wantClaude: "saved-claude"},
		{name: "Codex config", backend: "codex", wantModel: "auto", wantClaude: "saved-claude"},
		{name: "reset Codex", backend: "codex", requested: "auto", savedCodex: "gpt-6-astra", wantModel: "auto", wantClaude: "saved-claude"},
		{name: "Codex typo", backend: "codex", requested: "gpt-6-astro", invalid: true},
		{name: "invalid saved Codex", backend: "codex", savedCodex: "gpt-unknown", invalid: true},
		{name: "wrong provider", backend: "api", requested: "gpt-6-astra", invalid: true},
		{name: "Fable harness", backend: "cc", requested: "fable", wantModel: api.ModelFable51, wantClaude: api.ModelFable51},
		{name: "Fable API", requested: api.ModelFable51, wantModel: api.ModelFable51, wantClaude: "saved-claude"},
		{name: "Claude default", backend: "cc", requested: "auto", wantModel: "auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &api.Config{Backend: tc.backend, CodexModel: tc.savedCodex, ModelOverride: "saved-claude"}
			got, err := resolveSessionModel(cfg, tc.requested)
			if (err != nil) != tc.invalid {
				t.Fatalf("error = %v, invalid = %v", err, tc.invalid)
			}
			if !tc.invalid && (got != tc.wantModel || cfg.CodexModel != tc.wantCodex || cfg.ModelOverride != tc.wantClaude) {
				t.Fatalf("models = (%q, %q, %q), want (%q, %q, %q)", got, cfg.CodexModel, cfg.ModelOverride, tc.wantModel, tc.wantCodex, tc.wantClaude)
			}
		})
	}
}
