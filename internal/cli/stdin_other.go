//go:build !windows

package cli

import "os"

// stdinIsTerminal is true when standard input is a terminal a person can type
// at: a character device that is not the null device. A job whose input was
// redirected from /dev/null is not someone to ask.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(fi, null)
}
