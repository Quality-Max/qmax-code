//go:build darwin

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
	old, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Lflag &^= unix.ECHO | unix.ICANON
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &raw); err != nil {
		return nil, err
	}
	return &termState{old: *old}, nil
}

func restoreTermMode(state *termState) {
	if state != nil {
		_ = unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TIOCSETA, &state.old)
	}
}
