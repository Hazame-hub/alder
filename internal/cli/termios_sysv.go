//go:build aix || linux || solaris

package cli

import "golang.org/x/sys/unix"

// Linux, AIX and Solaris (illumos with it) name the terminal-attribute
// requests TCGETS and TCSETS.
const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)
