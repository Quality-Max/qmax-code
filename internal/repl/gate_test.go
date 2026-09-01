package repl

import (
	"testing"

	"github.com/qualitymax/qmax-code/internal/gate"
)

func TestParseGateCommand(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "/gate", want: gate.DefaultBase},
		{input: "/gate develop", want: "develop"},
		{input: "/gate HEAD~3", want: "HEAD~3"},
		{input: "/gate develop extra", wantErr: true},
		{input: "/other", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseGateCommand(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseGateCommand() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("base = %q, want %q", got, tc.want)
			}
		})
	}
}
