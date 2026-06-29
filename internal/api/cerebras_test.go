package api

import "testing"

func TestResolveCerebrasModel(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", CerebrasDefaultModel},
		{"default", CerebrasDefaultModel},
		{"DEFAULT", CerebrasDefaultModel}, // case-insensitive
		{"gemma", CerebrasGemma4Model},
		{"Gemma-4", CerebrasGemma4Model},
		{"gemma4", CerebrasGemma4Model},
		{"gemma-4-31b", CerebrasGemma4Model},
		{"gemma4-31b", CerebrasGemma4Model},
		{"gemma-4-31", CerebrasGemma4Model},
		{"zai-glm-4.7", "zai-glm-4.7"},     // unknown passes through
		{"  gemma  ", CerebrasGemma4Model}, // whitespace trimmed
	}
	for _, c := range cases {
		if got := ResolveCerebrasModel(c.in); got != c.want {
			t.Errorf("ResolveCerebrasModel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsCerebrasGemma4Model(t *testing.T) {
	if !IsCerebrasGemma4Model("gemma") {
		t.Error(`"gemma" should be recognized as Gemma 4`)
	}
	if !IsCerebrasGemma4Model(CerebrasGemma4Model) {
		t.Error("gemma-4-31b should be recognized as Gemma 4")
	}
	if IsCerebrasGemma4Model(CerebrasDefaultModel) {
		t.Error("default model should not be Gemma 4")
	}
}

func TestValidCerebrasReasoningEffort(t *testing.T) {
	valid := []string{"", "none", "low", "medium", "high", " None ", "HIGH"}
	for _, v := range valid {
		if !ValidCerebrasReasoningEffort(v) {
			t.Errorf("expected %q to be valid", v)
		}
	}
	invalid := []string{"max", "1", "ultra"}
	for _, v := range invalid {
		if ValidCerebrasReasoningEffort(v) {
			t.Errorf("expected %q to be invalid", v)
		}
	}
}

func TestNormalizeCerebrasReasoningEffort(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"none", "none"},
		{"Medium", "medium"},
		{"  high ", "high"},
		{"bogus", ""}, // invalid normalizes to "" (off / omitted)
	}
	for _, c := range cases {
		if got := NormalizeCerebrasReasoningEffort(c.in); got != c.want {
			t.Errorf("NormalizeCerebrasReasoningEffort(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
