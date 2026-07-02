package main

import "testing"

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
