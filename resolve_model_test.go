package main

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestResolveModelUsesCentralModelConstants(t *testing.T) {
	cases := map[string]string{
		"haiku":  api.ModelHaiku,
		"sonnet": api.ModelSonnet5,
		"opus":   api.ModelOpus55,
		"custom": "custom",
	}
	for input, want := range cases {
		if got := resolveModel(input); got != want {
			t.Errorf("resolveModel(%q) = %q, want %q", input, got, want)
		}
	}
}
