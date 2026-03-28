//go:build linux

package main

import (
	"golang.org/x/sys/unix"
	"os"
)

type termState struct {
	old unix.Termios
}

func enableRawMode() (*termState, error) {
	fd := int(os.Stdin.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Lflag &^= unix.ECHO | unix.ICANON
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, err
	}
	return &termState{old: *old}, nil
}

func restoreTermMode(state *termState) {
	if state != nil {
		_ = unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, &state.old)
	}
}
