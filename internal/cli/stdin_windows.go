//go:build windows

package cli

import (
	"os"
	"syscall"
)

// stdinIsTerminal is true when standard input is a console a person can type
// at. Asking the console for its mode succeeds only for a console: the null
// device, a pipe and a file all refuse, although the null device also reports
// itself as a character device.
func stdinIsTerminal() bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(os.Stdin.Fd()), &mode) == nil
}
