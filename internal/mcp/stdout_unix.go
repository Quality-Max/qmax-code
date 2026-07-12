//go:build darwin || linux

package mcp

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func redirectStdoutForMCP() (*os.File, func(), error) {
	origStdout := os.Stdout

	jsonFD, err := unix.Dup(int(origStdout.Fd()))
	if err != nil {
		return nil, func() {}, fmt.Errorf("duplicate stdout: %w", err)
	}
	jsonOut := os.NewFile(uintptr(jsonFD), "qmax-mcp-json-stdout")
	if jsonOut == nil {
		_ = unix.Close(jsonFD)
		return nil, func() {}, fmt.Errorf("wrap duplicated stdout")
	}

	restoreFD, err := unix.Dup(int(origStdout.Fd()))
	if err != nil {
		_ = jsonOut.Close()
		return nil, func() {}, fmt.Errorf("duplicate stdout for restore: %w", err)
	}

	if err := unix.Dup2(int(os.Stderr.Fd()), int(origStdout.Fd())); err != nil {
		_ = jsonOut.Close()
		_ = unix.Close(restoreFD)
		return nil, func() {}, fmt.Errorf("redirect stdout to stderr: %w", err)
	}

	os.Stdout = os.Stderr

	return jsonOut, func() {
		_ = jsonOut.Close()
		_ = unix.Dup2(restoreFD, int(origStdout.Fd()))
		_ = unix.Close(restoreFD)
		os.Stdout = origStdout
	}, nil
}
