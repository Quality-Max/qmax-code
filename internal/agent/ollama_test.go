package agent

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)


func TestValidateOllamaURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"https ok", "https://llm.example.com", false},
		{"http ok", "http://localhost:11434", false},
		{"with userinfo + path", "https://user:pass@llm.example.com/v1", false},
		{"empty", "", true},
		{"bad scheme file", "file:///etc/passwd", true},
		{"bad scheme javascript", "javascript:alert(1)", true},
		{"bad scheme ftp", "ftp://example.com", true},
		{"missing host", "https://", true},
		{"unparseable", "://nope", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOllamaURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateOllamaURL(%q) err=%v, wantErr=%v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestNewOllamaClientRejectsInvalidURL(t *testing.T) {
	cfg := &api.Config{OllamaURL: "file:///etc/passwd", OllamaModel: "x"}
	if got := NewOllamaClient(cfg); got != nil {
		t.Errorf("NewOllamaClient with invalid URL returned non-nil client: %+v", got)
	}
}
