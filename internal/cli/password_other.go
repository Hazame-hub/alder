//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || windows)

package cli

import (
	"errors"
	"os"
)

// echoOff has no way to turn echo off here, so the client never asks for a
// password on this platform: a password typed in view is not one to accept.
func echoOff(*os.File) (func(), error) {
	return nil, errors.ErrUnsupported
}
