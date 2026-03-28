//go:build !windows

package main

import (
	"golang.org/x/sys/unix"
	"os"
)

// enableRawMode puts the terminal into raw mode (no echo, no line buffering).
// Returns the old state for restoration.
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

// restoreTermMode restores the terminal to its previous state.
func restoreTermMode(old *unix.Termios) {
	if old != nil {
		fd := int(os.Stdin.Fd())
		_ = unix.IoctlSetTermios(fd, unix.TIOCSETA, old)
	}
}
