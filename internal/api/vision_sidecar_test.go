package api

import "testing"

func TestVisionSidecarConfigCompatibility(t *testing.T) {
	// Older configs omit this setting; future or manually edited values must
	// not silently enable the explicit always mode.
	if got := (*Config)(nil).VisionSidecarMode(); got != VisionSidecarAuto {
		t.Fatalf("nil config mode = %q", got)
	}
	for _, tc := range []struct {
		value string
		mode  string
		valid bool
	}{
		{"", VisionSidecarAuto, true},
		{" AUTO ", VisionSidecarAuto, true},
		{" ALWAYS ", VisionSidecarAlways, true},
		{" Off ", VisionSidecarOff, true},
		{"future-mode", VisionSidecarAuto, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			cfg := &Config{VisionSidecar: tc.value}
			if got := cfg.VisionSidecarMode(); got != tc.mode {
				t.Errorf("mode = %q, want %q", got, tc.mode)
			}
			if got := ValidVisionSidecarMode(tc.value); got != tc.valid {
				t.Errorf("valid = %v, want %v", got, tc.valid)
			}
		})
	}
}

func TestVisionSidecarUnknownCatalogDoesNotClaimTextOnly(t *testing.T) {
	for _, provider := range []string{"groq", "openrouter", "custom-provider", ""} {
		if lacks, known := ProviderModelLacksVision(provider, "unknown-model"); lacks || known {
			t.Errorf("provider %q: lacksVision=%v, known=%v", provider, lacks, known)
		}
	}
	for _, model := range []string{"glm-4.5", "unseeded-model"} {
		if lacks, known := ProviderModelLacksVision("zai-coding-plan", model); !lacks || !known {
			t.Errorf("coding-plan model %q: lacksVision=%v, known=%v", model, lacks, known)
		}
	}
}
