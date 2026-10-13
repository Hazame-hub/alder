//go:build windows

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// echoOff stops the console echoing what is typed, and returns what puts it
// back. Line input goes with echo -- the console does not allow one without
// the other -- so readPasswordLine does the line editing that remains
// (Backspace) and stops at the carriage return Enter sends. Processed input
// stays on, so Ctrl+C still interrupts.
func echoOff(f *os.File) (func(), error) {
	h := windows.Handle(f.Fd())
	var old uint32
	if err := windows.GetConsoleMode(h, &old); err != nil {
		return nil, err
	}
	quiet := old &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT)
	quiet |= windows.ENABLE_PROCESSED_INPUT
	if err := windows.SetConsoleMode(h, quiet); err != nil {
		return nil, err
	}
	return func() { _ = windows.SetConsoleMode(h, old) }, nil
}
