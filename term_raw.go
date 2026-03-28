//go:build darwin

package main

import (
	"golang.org/x/sys/unix"
	"os"
)

func enableRawMode() (*unix.Termios, error) {
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
	return old, nil
}

func restoreTermMode(old *unix.Termios) {
	if old != nil {
		_ = unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TIOCSETA, old)
	}
}
