//go:build !darwin && !linux

package mcp

import "os"

func redirectStdoutForMCP() (*os.File, func(), error) {
	origStdout := os.Stdout
	os.Stdout = os.Stderr
	return origStdout, func() {
		os.Stdout = origStdout
	}, nil
}
