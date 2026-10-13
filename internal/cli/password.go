package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"unicode/utf8"
)

// errCannotAsk is a terminal that cannot be asked for a password after all:
// its echo could not be turned off, or this platform has no way to. The
// command then fails as it would with no terminal, naming the other sources,
// rather than asking with the password shown as it is typed.
var errCannotAsk = errors.New("the terminal cannot read a password without echoing it")

// askTerminal asks for a password on a terminal: the question written to out,
// the answer read from in with echo off. Env's ReadPassword is this, with
// standard input and standard error.
//
// The read runs on a goroutine of its own so that the command does not wait
// on it after Ctrl+C. The client catches Ctrl+C to cancel the command's
// context (see command), so the signal no longer ends the process, and a
// read from a terminal does not return when a signal arrives: left in the
// foreground, an interrupted prompt would sit there until Enter, with echo
// still off. Instead the echo is restored and the command returns as soon as
// the context ends; the goroutine is left blocked on a read that the exiting
// process abandons.
func askTerminal(ctx context.Context, in *os.File, out io.Writer, prompt string) (string, error) {
	restore, err := echoOff(in)
	if err != nil {
		return "", errCannotAsk
	}
	var once sync.Once
	restoreOnce := func() { once.Do(restore) }
	defer restoreOnce()

	writef(out, "%s", prompt)
	type answer struct {
		line []byte
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		line, readErr := readPasswordLine(in)
		done <- answer{line, readErr}
	}()
	select {
	case a := <-done:
		restoreOnce()
		// Enter was not echoed either, so the cursor is still on the prompt.
		writeln(out)
		if a.err != nil {
			return "", failf("input", "cannot read the password from the terminal: %v", a.err)
		}
		return string(a.line), nil
	case <-ctx.Done():
		restoreOnce()
		writeln(out)
		return "", transportFailure(ctx, "asking for the password", ctx.Err())
	}
}

// readPasswordLine reads one line, a byte at a time, up to the line ending or
// the end of input.
//
// One byte at a time, because standard input is shared with what comes after:
// apply asks for its confirmation on the same terminal, and a buffered reader
// here would take the start of that answer with it. The terminal hands over
// a whole line at once anyway, so this is not slow in any way a person sees.
//
// Backspace is handled here as well as by the terminal: the Windows console
// with line input off delivers it as a character. Carriage return ends the
// line for the same reason, which is also what Enter sends to a Unix terminal
// with ICRNL off.
func readPasswordLine(r io.Reader) ([]byte, error) {
	var line []byte
	var b [1]byte
	for {
		n, err := r.Read(b[:])
		if n == 1 {
			switch b[0] {
			case '\n', '\r':
				return line, nil
			case '\b', 0x7f:
				if len(line) > 0 {
					_, size := utf8.DecodeLastRune(line)
					line = line[:len(line)-size]
				}
			default:
				if len(line) >= maxSecretBytes {
					return nil, errors.New("the line typed is too long to be a password")
				}
				line = append(line, b[0])
			}
		}
		if errors.Is(err, io.EOF) {
			return line, nil
		}
		if err != nil {
			return nil, err
		}
	}
}
