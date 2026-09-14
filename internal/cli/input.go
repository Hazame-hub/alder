package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
)

// Limits on what is read, each the server's own limit for that input: reading
// more only to have Alder refuse it would be memory spent on an answer already
// known.
const (
	// maxRequestBytes is alder serve's request body limit.
	maxRequestBytes = 16 << 20
	// maxLDIFBytes is the server's LDIF import limit.
	maxLDIFBytes = 8 << 20
	// maxSecretBytes bounds a password file or a password on standard input.
	maxSecretBytes = 64 << 10
)

// readInput reads a file, or standard input when arg is "-".
func (e *Env) readInput(arg, purpose string, limit int64) ([]byte, string, error) {
	if arg == "-" {
		if err := e.claimStdin(purpose); err != nil {
			return nil, "", err
		}
		data, err := io.ReadAll(io.LimitReader(e.Stdin, limit+1))
		if err != nil {
			return nil, "", failf("input", "cannot read %s from standard input: %v", purpose, err)
		}
		if int64(len(data)) > limit {
			return nil, "", tooLarge("standard input", purpose, limit)
		}
		return data, "standard input", nil
	}
	// #nosec G304 -- a file the user named on the command line.
	f, err := os.Open(arg)
	if err != nil {
		return nil, "", failf("input", "cannot read %s: %v", purpose, err)
	}
	defer func() { _ = f.Close() }()
	if fi, statErr := f.Stat(); statErr == nil && fi.Size() > limit {
		return nil, "", tooLarge(arg, purpose, limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, "", failf("input", "cannot read %s: %v", arg, err)
	}
	if int64(len(data)) > limit {
		return nil, "", tooLarge(arg, purpose, limit)
	}
	return data, arg, nil
}

func tooLarge(name, purpose string, limit int64) *ExitError {
	return failf("input_too_large", "%s is larger than the %d MB Alder accepts for %s",
		name, limit>>20, purpose)
}

// readSecret reads a password: the first line, without its line ending. A
// password file written by an editor ends in a newline nobody meant to be part
// of the password, and one written on Windows ends in two characters.
func readSecret(r io.Reader, name string) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxSecretBytes+1))
	if err != nil {
		return "", failf("input", "cannot read the password from %s: %v", name, err)
	}
	if len(data) > maxSecretBytes {
		return "", failf("input_too_large", "%s is too large to be a password", name)
	}
	line, _, _ := bufio.NewReader(bytes.NewReader(data)).ReadLine()
	secret := string(bytes.TrimSuffix(line, []byte("\r")))
	if secret == "" {
		// An empty password makes a simple bind unauthenticated on many
		// servers: it binds, as nobody. Refusing it here means a missing
		// secret cannot quietly become an anonymous session.
		return "", usagef("the password read from %s is empty", name)
	}
	return secret, nil
}

func readSecretFile(path string) (string, error) {
	// #nosec G304 -- a password file the user named on the command line.
	f, err := os.Open(path)
	if err != nil {
		return "", failf("input", "cannot read the password file: %v", err)
	}
	defer func() { _ = f.Close() }()
	return readSecret(f, fmt.Sprintf("the password file %s", path))
}
