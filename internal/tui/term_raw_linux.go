//go:build linux

package tui

import (
	"os"

	"golang.org/x/sys/unix"
)

type TermState struct {
	old unix.Termios
}

func EnableRawMode() (*TermState, error) {
	fd := int(os.Stdin.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Lflag &^= unix.ECHO | unix.ICANON
	raw.Oflag |= unix.OPOST | unix.ONLCR
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, err
	}
	return &TermState{old: *old}, nil
}

func RestoreTermMode(state *TermState) {
	if state != nil {
		restored := state.old
		restored.Oflag |= unix.OPOST | unix.ONLCR
		_ = unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, &restored)
	}
}

// EnsureTTYNewlines turns output-postprocessing back on so '\n' returns the
// cursor to column 0. A previous process that died in full raw mode (Bubble
// Tea) can leave OPOST/ONLCR off, which makes every later line staircase.
func EnsureTTYNewlines() {
	seen := map[int]struct{}{}
	for _, fd := range []int{int(os.Stdin.Fd()), int(os.Stdout.Fd())} {
		if _, dup := seen[fd]; dup {
			continue
		}
		seen[fd] = struct{}{}
		t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
		if err != nil {
			continue
		}
		if t.Oflag&unix.OPOST != 0 && t.Oflag&unix.ONLCR != 0 {
			continue
		}
		t.Oflag |= unix.OPOST | unix.ONLCR
		_ = unix.IoctlSetTermios(fd, unix.TCSETS, t)
	}
}
