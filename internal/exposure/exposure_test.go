package exposure

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		host string
		path string
		want string
	}{
		{"anthropic messages", "api.anthropic.com", "/v1/messages", CatLLMPrompt},
		{"cerebras completions", "api.cerebras.ai", "/v1/chat/completions", CatLLMPrompt},
		{"ollama openai chat", "localhost:11434", "/v1/chat/completions", CatLLMPrompt},
		{"ollama native chat", "localhost:11434", "/api/chat", CatLLMPrompt},
		{"ollama generate", "localhost:11434", "/api/generate", CatLLMPrompt},

		{"auth me", "app.qualitymax.io", "/api/me", CatControl},
		{"cli login", "app.qualitymax.io", "/api/auth/cli-login", CatControl},
		{"cli poll", "app.qualitymax.io", "/api/auth/cli-poll", CatControl},
		{"ollama tags probe", "localhost:11434", "/api/tags", CatControl},
		{"job health", "app.qualitymax.io", "/api/job-health/background/abc", CatControl},

		{"projects list", "app.qualitymax.io", "/api/projects", CatCloudAPI},
		{"script fetch", "app.qualitymax.io", "/api/automation/scripts/1234", CatCloudAPI},
		{"codex connect", "app.qualitymax.io", "/api/integrations/codex/connect", CatCloudAPI},

		{"unknown", "example.com", "/some/other/path", CatUncategorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.host, tc.path); got != tc.want {
				t.Errorf("Classify(%q, %q) = %q, want %q", tc.host, tc.path, got, tc.want)
			}
		})
	}
}
