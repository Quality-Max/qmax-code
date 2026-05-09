//go:build darwin

package tui

import (
	"golang.org/x/sys/unix"
	"os"
)

type TermState struct {
	old unix.Termios
}

func EnableRawMode() (*TermState, error) {
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
	return &TermState{old: *old}, nil
}

func RestoreTermMode(state *TermState) {
	if state != nil {
		_ = unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TIOCSETA, &state.old)
	}
}
