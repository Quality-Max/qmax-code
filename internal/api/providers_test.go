package api

import "testing"

func TestBuiltinProvidersHaveRequiredFields(t *testing.T) {
	for _, p := range BuiltinProviders() {
		if p.ID == "" || p.DisplayName == "" {
			t.Errorf("provider missing id/name: %+v", p)
		}
		if len(p.KeyEnvVars) == 0 {
			t.Errorf("provider %q has no KeyEnvVars", p.ID)
		}
		if p.Custom && p.BaseURL == "" {
			t.Errorf("custom provider %q missing BaseURL", p.ID)
		}
		if p.Custom && len(p.Models) == 0 {
			t.Errorf("custom provider %q has no seed Models", p.ID)
		}
	}
}

func TestProviderByIDAndAllowed(t *testing.T) {
	if _, ok := ProviderByID("groq"); !ok {
		t.Fatal("groq should be a builtin provider")
	}
	if _, ok := ProviderByID("nope"); ok {
		t.Fatal("unknown provider should not resolve")
	}
	if !ProviderAllowed("groq") {
		t.Error("groq should be allowed by the entitlement seam today")
	}
	if ProviderAllowed("nope") {
		t.Error("unknown provider should not be allowed")
	}
}

func TestEnableDisableProviderID(t *testing.T) {
	cfg := &Config{}
	cfg.EnableProviderID("groq")
	cfg.EnableProviderID("groq") // idempotent
	cfg.EnableProviderID("openrouter")
	if !cfg.IsProviderEnabled("groq") || !cfg.IsProviderEnabled("openrouter") {
		t.Fatal("providers should be enabled")
	}
	if len(cfg.EnabledProviders) != 2 {
		t.Fatalf("expected 2 enabled, got %v", cfg.EnabledProviders)
	}
	cfg.DisableProviderID("groq")
	if cfg.IsProviderEnabled("groq") {
		t.Fatal("groq should be disabled")
	}
	if len(cfg.EnabledProviders) != 1 {
		t.Fatalf("expected 1 enabled, got %v", cfg.EnabledProviders)
	}
}

func TestActiveProvidersRespectsEntitlementAndEnable(t *testing.T) {
	cfg := &Config{EnabledProviders: []string{"groq", "bogus-unentitled"}}
	active := cfg.ActiveProviders()
	if len(active) != 1 || active[0].ID != "groq" {
		t.Fatalf("ActiveProviders should filter to allowed+enabled, got %+v", active)
	}
}

func TestValidateProviderKey(t *testing.T) {
	if _, err := ValidateProviderKey("groq", ""); err == nil {
		t.Error("empty key should error")
	}
	if _, err := ValidateProviderKey("groq", "has space"); err == nil {
		t.Error("whitespace key should error")
	}
	if looks, err := ValidateProviderKey("groq", "gsk_abc123"); err != nil || !looks {
		t.Errorf("valid groq key: looks=%v err=%v", looks, err)
	}
	if looks, err := ValidateProviderKey("groq", "abc123"); err != nil || looks {
		t.Errorf("wrong-prefix key should be soft-flagged: looks=%v err=%v", looks, err)
	}
}

func TestLoadProviderKeyEnvFallback(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "gsk_fromenv")
	if got := LoadProviderKey("groq"); got != "gsk_fromenv" {
		t.Errorf("LoadProviderKey env fallback = %q", got)
	}
}
