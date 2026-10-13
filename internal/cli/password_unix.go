//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// echoOff stops a terminal echoing what is typed, and returns what puts it
// back. Only ECHO is cleared: the line is still edited by the terminal
// (ICANON), and Ctrl+C still interrupts (ISIG), so a person can correct a typo
// and can give up. ECHONL is left as it was; the newline Enter would have
// shown is written by askTerminal.
//
// This is what golang.org/x/term does, written against golang.org/x/sys,
// which the client already links, rather than adding x/term for one call.
func echoOff(f *os.File) (func(), error) {
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, err
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	quiet.Iflag |= unix.ICRNL
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &quiet); err != nil {
		return nil, err
	}
	return func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, old) }, nil
}
