package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/hazame-hub/alder/internal/apiclient"
)

// A saved plan carries a password change as its intent only: the entry, bound
// by its identity, and no password (docs/proposals/saved-password-changes.md).
// alder apply --plan takes the passwords, one per password change in the order
// the plan lists them, from exactly one of --new-password-file,
// --new-password-stdin or the terminal. Never from the environment: a
// variable is inherited by every child process, and a user's new password has
// no reason to be there.

// maxNewPasswordsBytes bounds a file of new passwords: a plan holds at most
// MaxChangesetChanges changes, each password at most maxSecretBytes.
const maxNewPasswordsBytes = 1 << 20

// withNewPasswords returns the saved changes with each password change given
// its password, or the changes as they are when there is none.
func withNewPasswords(ctx context.Context, env *Env, o planOptions, changes []apiclient.ChangeRequest) ([]apiclient.ChangeRequest, error) {
	var at []int
	for i, c := range changes {
		if c.Type == apiclient.ChangeRequestTypeSetpassword {
			at = append(at, i)
		}
	}
	given := o.newPasswordFile != "" || o.newPasswordStdin
	if len(at) == 0 {
		if given {
			return nil, usagef("%s holds no password change, so --new-password-file and --new-password-stdin have nothing to supply", safe(o.plan))
		}
		return changes, nil
	}

	var passwords []string
	var err error
	switch {
	case o.newPasswordFile != "":
		passwords, err = readNewPasswords(func() ([]byte, error) {
			return readSmallFile(o.newPasswordFile, maxNewPasswordsBytes)
		}, safe(o.newPasswordFile))
	case o.newPasswordStdin:
		if err := env.claimStdin("the new passwords"); err != nil {
			return nil, err
		}
		passwords, err = readNewPasswords(func() ([]byte, error) {
			data, err := io.ReadAll(io.LimitReader(env.Stdin, maxNewPasswordsBytes+1))
			if err == nil && len(data) > maxNewPasswordsBytes {
				return nil, usagef("standard input is larger than %d KB", maxNewPasswordsBytes>>10)
			}
			return data, err
		}, "standard input")
	case o.yes || !env.canAskNewPassword():
		return nil, usagef("%s sets %s, and no password was given for it: pass --new-password-file or --new-password-stdin%s",
			safe(o.plan), plural(len(at), "password", "passwords"), whyNotAskedNewPassword(env, o.yes))
	default:
		passwords, err = askNewPasswords(ctx, env, changes, at)
	}
	if err != nil {
		return nil, err
	}
	if len(passwords) != len(at) {
		return nil, usagef("%s sets %s, and %s given: one per password change, in the order the plan lists them",
			safe(o.plan), plural(len(at), "password", "passwords"), plural(len(passwords), "was", "were"))
	}

	out := append([]apiclient.ChangeRequest{}, changes...)
	for n, i := range at {
		password := passwords[n]
		out[i].NewPassword = &password
	}
	writef(env.Stderr, "alder: %s supplied for the password changes in the plan\n", plural(len(at), "password", "passwords"))
	return out, nil
}

// readNewPasswords reads one password per line. A final line ending is
// allowed, a carriage return before a line ending is dropped, and an empty
// line is refused, as an empty bind password is.
func readNewPasswords(read func() ([]byte, error), from string) ([]string, error) {
	data, err := read()
	if err != nil {
		var exit *ExitError
		if errors.As(err, &exit) {
			return nil, err
		}
		return nil, failf("input", "cannot read the new passwords from %s: %v", from, err)
	}
	defer clear(data)
	data = bytes.TrimSuffix(data, []byte("\n"))
	data = bytes.TrimSuffix(data, []byte("\r"))
	if len(data) == 0 {
		return nil, usagef("%s holds no password", from)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case line == "":
			return nil, usagef("line %d of %s is empty, and a password cannot be", i+1, from)
		case len(line) > maxSecretBytes:
			return nil, usagef("line %d of %s is too long to be a password", i+1, from)
		}
		lines[i] = line
	}
	return lines, nil
}

// askNewPasswords asks at the terminal, for each entry by its DN, twice.
func askNewPasswords(ctx context.Context, env *Env, changes []apiclient.ChangeRequest, at []int) ([]string, error) {
	out := make([]string, 0, len(at))
	for _, i := range at {
		dn := safe(changes[i].Dn)
		first, err := env.ReadPassword(ctx, "New password for "+dn+": ")
		if err != nil {
			return nil, askFailed(err)
		}
		if first == "" {
			return nil, usagef("no password was typed for %s", dn)
		}
		again, err := env.ReadPassword(ctx, "Again, for "+dn+": ")
		if err != nil {
			return nil, askFailed(err)
		}
		if again != first {
			return nil, usagef("the two passwords typed for %s differ; nothing was applied", dn)
		}
		out = append(out, first)
	}
	return out, nil
}

func askFailed(err error) error {
	if errors.Is(err, errCannotAsk) {
		return usagef("this terminal cannot read a password without showing it: pass --new-password-file or --new-password-stdin")
	}
	return err
}

// canAskNewPassword is canAsk without an environment variable: a new password
// is never read from one.
func (e *Env) canAskNewPassword() bool {
	return e.ReadPassword != nil && e.stdinOwner == "" && e.Interactive != nil && e.Interactive()
}

func whyNotAskedNewPassword(env *Env, yes bool) string {
	if yes {
		return "; with --yes nothing is asked at the terminal"
	}
	return env.whyNotAsked()
}
