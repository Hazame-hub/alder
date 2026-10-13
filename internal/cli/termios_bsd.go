//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package cli

import "golang.org/x/sys/unix"

// The BSDs, macOS among them, name the terminal-attribute requests TIOCGETA
// and TIOCSETA.
const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)
