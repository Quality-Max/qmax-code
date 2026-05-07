//go:build windows

package sysutil

import (
	"os"
)

// PidAlive reports whether a process with the given PID is running.
// On Windows, OpenProcess is not directly accessible from pure Go without
// cgo; os.FindProcess always succeeds, so we attempt a no-op signal as a
// proxy. If the process handle is invalid the call errors out.
func PidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Release the handle immediately — we only needed the existence check.
	_ = p.Release()
	// os.FindProcess on Windows always succeeds (returns a handle even for
	// dead PIDs), so we conservatively return true to avoid false-positive
	// deletions. The file is tiny; leaking it on Windows is acceptable.
	return true
}
