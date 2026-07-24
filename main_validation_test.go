package main

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/agent"
)

// TestIsValidModelName is the regression test for QUA-579: pre-fix, the
// -model flag accepted any string and forwarded it to the API, where it
// produced a confusing 400/401 instead of a clear local error.
func TestIsValidModelName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Shorthands resolved by resolveModel.
		{"auto", true},
		{"sonnet", true},
		{"opus", true},
		{"haiku", true},
		{"AUTO", true}, // case-insensitive
		// Real model IDs from api.Model* constants.
		{"claude-opus-4-8", true},
		{"claude-opus-4-7", true},
		{"claude-sonnet-5", true},
		{"claude-fable-5", true},
		{"claude-sonnet-4-6", true},
		{"claude-haiku-4-5-20251001", true},
		// Rejected.
		{"nonsense", false},
		{"claude-future-model-9-0", false},
		{"gpt-4", false},
		{"", false},
		{"opus4", false},
	}
	for _, c := range cases {
		if got := isValidModelName(c.in); got != c.want {
			t.Errorf("isValidModelName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveLocalOnly(t *testing.T) {
	tests := []struct {
		name                        string
		flagEnabled, persisted, env bool
		want                        bool
	}{
		{name: "all disabled", want: false},
		{name: "flag", flagEnabled: true, want: true},
		{name: "persisted config", persisted: true, want: true},
		{name: "environment", env: true, want: true},
		{name: "multiple sources", flagEnabled: true, persisted: true, env: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveLocalOnly(tt.flagEnabled, tt.persisted, tt.env); got != tt.want {
				t.Fatalf("resolveLocalOnly(%v, %v, %v) = %v, want %v",
					tt.flagEnabled, tt.persisted, tt.env, got, tt.want)
			}
		})
	}
}

func TestShouldRunInteractiveSetup(t *testing.T) {
	tests := []struct {
		name         string
		localOnly    bool
		qmaxBin      string
		hasAPIClient bool
		want         bool
	}{
		{name: "standalone skips setup", localOnly: true, want: false},
		{name: "unconfigured connected mode runs setup", want: true},
		{name: "legacy CLI available", qmaxBin: "/usr/local/bin/qmax", want: false},
		{name: "direct API available", hasAPIClient: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRunInteractiveSetup(tt.localOnly, tt.qmaxBin, tt.hasAPIClient); got != tt.want {
				t.Fatalf("shouldRunInteractiveSetup(%v, %q, %v) = %v, want %v",
					tt.localOnly, tt.qmaxBin, tt.hasAPIClient, got, tt.want)
			}
		})
	}
}

func TestShouldUseStreamingBuiltIn(t *testing.T) {
	tests := []struct {
		name string
		ag   *agent.Agent
		want bool
	}{
		{name: "nil agent", want: false},
		{name: "direct API", ag: &agent.Agent{}, want: false},
		{name: "Cerebras", ag: &agent.Agent{Cerebras: &agent.CerebrasClient{}}, want: true},
		{name: "Ollama full", ag: &agent.Agent{Ollama: &agent.OllamaClient{}, Mode: agent.OllamaModeFull}, want: true},
		{name: "Ollama chat", ag: &agent.Agent{Ollama: &agent.OllamaClient{}, Mode: agent.OllamaModeChat}, want: true},
		{name: "Ollama off", ag: &agent.Agent{Ollama: &agent.OllamaClient{}, Mode: agent.OllamaModeOff}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUseStreamingBuiltIn(tt.ag); got != tt.want {
				t.Fatalf("shouldUseStreamingBuiltIn() = %v, want %v", got, tt.want)
			}
		})
	}
}
