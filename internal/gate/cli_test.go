package gate

import (
	"bytes"
	"testing"
)

func TestParseCLIArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantBase string
		wantErr  bool
	}{
		{name: "default", wantBase: DefaultBase},
		{name: "explicit", args: []string{"--base", "develop"}, wantBase: "develop"},
		{name: "revision expression", args: []string{"--base", "HEAD~3"}, wantBase: "HEAD~3"},
		{name: "extra argument", args: []string{"develop"}, wantErr: true},
		{name: "option shaped ref", args: []string{"--base", "--all"}, wantErr: true},
		{name: "empty ref", args: []string{"--base", ""}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			got, err := ParseCLIArgs(tc.args, &output)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseCLIArgs() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got.Base != tc.wantBase {
				t.Fatalf("base = %q, want %q", got.Base, tc.wantBase)
			}
		})
	}
}

func TestParseCLIHelp(t *testing.T) {
	var output bytes.Buffer
	_, err := ParseCLIArgs([]string{"--help"}, &output)
	if !IsHelp(err) {
		t.Fatalf("error = %v, want help", err)
	}
	if output.String() == "" {
		t.Fatal("help output is empty")
	}
}
