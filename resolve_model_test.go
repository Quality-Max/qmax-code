package main

import "testing"

func TestResolveModelUsesCentralModelConstants(t *testing.T) {
	cases := map[string]string{
		"haiku":  ModelHaiku,
		"sonnet": ModelSonnet,
		"opus":   ModelOpus,
		"custom": "custom",
	}
	for input, want := range cases {
		if got := resolveModel(input); got != want {
			t.Errorf("resolveModel(%q) = %q, want %q", input, got, want)
		}
	}
}
