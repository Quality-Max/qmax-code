//go:build !windows

package sysutil

import (
	"os"
	"syscall"
)

// PidAlive reports whether a process with the given PID is running.
// Uses signal 0: no signal is sent but the kernel validates the PID.
func PidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
