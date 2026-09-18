package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
)

func TestShouldUseVisionSidecar(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *api.Config
		backend string
		want    bool
	}{
		{"nil config", nil, "opencode", false},
		{"mode off", &api.Config{VisionSidecar: api.VisionSidecarOff, CerebrasKey: "k"}, "opencode", false},
		{"no key", &api.Config{VisionSidecar: api.VisionSidecarAlways}, "opencode", false},
		{"mode always", &api.Config{VisionSidecar: api.VisionSidecarAlways, CerebrasKey: "k"}, "anything", true},
		{"mode auto, other backend", &api.Config{VisionSidecar: api.VisionSidecarAuto, CerebrasKey: "k"}, "anthropic", false},
		{"mode auto, opencode with text-only override", &api.Config{VisionSidecar: api.VisionSidecarAuto, CerebrasKey: "k", ModelOverride: "zai-coding-plan/glm-5.2"}, "opencode", true},
		{"mode auto, opencode unknown override", &api.Config{VisionSidecar: api.VisionSidecarAuto, CerebrasKey: "k", ModelOverride: "openai/gpt-4"}, "opencode", false},
		{"mode auto, opencode with only zai-coding-plan enabled", &api.Config{VisionSidecar: api.VisionSidecarAuto, CerebrasKey: "k", EnabledProviders: []string{"zai-coding-plan"}}, "opencode", true},
		{"mode auto, opencode with multiple providers", &api.Config{VisionSidecar: api.VisionSidecarAuto, CerebrasKey: "k", EnabledProviders: []string{"zai-coding-plan", "openai"}}, "opencode", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldUseVisionSidecar(tt.cfg, tt.backend); got != tt.want {
				t.Errorf("ShouldUseVisionSidecar() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildSidecarAugmentedPrompt(t *testing.T) {
	imgs := []tui.ImageAttachment{
		{FileName: "test1.png"},
		{FileName: "test2.jpg"},
	}

	got, err := BuildSidecarAugmentedPrompt("Hello world", imgs, "This is a description.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(got, "Hello world") {
		t.Errorf("missing original prompt: %q", got)
	}
	if !strings.Contains(got, "[gemma-4 vision sidecar read 2 image(s): test1.png, test2.jpg]") {
		t.Errorf("missing header: %q", got)
	}
	if !strings.Contains(got, "<image-descriptions>\nThis is a description.\n</image-descriptions>") {
		t.Errorf("missing description block: %q", got)
	}

	// test no images
	_, err = BuildSidecarAugmentedPrompt("Hello", nil, "desc")
	if err == nil {
		t.Errorf("expected error for no images")
	}

	// test empty desc
	_, err = BuildSidecarAugmentedPrompt("Hello", imgs, "  \n  ")
	if err == nil {
		t.Errorf("expected error for empty description")
	}
}

func TestDescribeImagesWithGemma(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req oaiChatRequest
		json.NewDecoder(r.Body).Decode(&req)

		if req.Model != api.CerebrasGemma4Model {
			t.Errorf("expected model %q, got %q", api.CerebrasGemma4Model, req.Model)
		}

		resp := oaiChatResponse{
			Choices: []struct {
				Message struct {
					Content   string        `json:"content"`
					ToolCalls []oaiToolCall `json:"tool_calls"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{Message: struct {
					Content   string        `json:"content"`
					ToolCalls []oaiToolCall `json:"tool_calls"`
				}{Content: "simulated description"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := &api.Config{
		CerebrasKey:     "test-key",
		CerebrasBaseURL: ts.URL,
	}

	imgs := []tui.ImageAttachment{
		{FileName: "test.png", MediaType: "image/png", Data: "abcd"},
	}

	desc, err := DescribeImagesWithGemma(context.Background(), cfg, imgs, "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "simulated description" {
		t.Errorf("expected 'simulated description', got %q", desc)
	}

	// test no key
	_, err = DescribeImagesWithGemma(context.Background(), &api.Config{}, imgs, "")
	if err == nil {
		t.Errorf("expected error for no key")
	}
}
