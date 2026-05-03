//go:build !windows

package main

import (
	"os"
	"syscall"
)

// pidAlive reports whether a process with the given PID is running.
// Uses signal 0: no signal is sent but the kernel validates the PID.
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
