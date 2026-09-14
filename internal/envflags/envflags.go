// Package envflags lets a command-line flag also read an environment variable.
//
// docs/COMPATIBILITY.md has promised since 1.0 that "configuration file keys and
// their environment-variable equivalents" keep their meanings within 1.x, and
// there were none: the binary was flags-only. The promise is the useful half, so
// this makes it true rather than deleting it.
//
// Environment rather than a configuration file, and stdlib rather than a
// configuration library. A container is how Alder is run and an environment
// variable is how a container is configured; a file format would be a third
// place for a setting to hide, and koanf would be a dependency bought for
// twenty lines.
//
// The flag wins. Something on the command line was typed on purpose, and an
// inherited environment silently overriding it is the failure mode this
// ordering exists to avoid.
//
// It lives in its own package because two kinds of command read it: alder serve,
// where every flag has a variable, and the command-line client, where the flags
// that say "yes, write this" or "overwrite that file" deliberately do not. An
// inherited ALDER_YES confirming an apply nobody typed is exactly the kind of
// setting that must not hide in an environment.
package envflags

import (
	"fmt"
	"strings"

	"github.com/spf13/pflag"
)

// Prefix is prepended to the flag name to make the variable.
const Prefix = "ALDER_"

// NoEnv is the annotation that keeps a flag from reading the environment.
const NoEnv = "alder-no-env"

// Name is the variable a flag reads: the flag name upper-cased, with dashes as
// underscores, behind the prefix. --session-idle-timeout is
// ALDER_SESSION_IDLE_TIMEOUT.
func Name(flag string) string {
	return Prefix + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

// Exclude marks flags that must only ever be set on the command line.
func Exclude(flags *pflag.FlagSet, names ...string) {
	for _, name := range names {
		if err := flags.SetAnnotation(name, NoEnv, []string{"true"}); err != nil {
			panic("envflags: no flag named " + name)
		}
	}
}

// Reads reports whether a flag reads the environment.
func Reads(f *pflag.Flag) bool {
	_, excluded := f.Annotations[NoEnv]
	return !excluded
}

// Apply fills in every flag the command line did not set from the environment.
//
// An unparseable value is an error rather than a warning. A misspelled duration
// in ALDER_SESSION_MAX_LIFETIME that silently left the default in place would be
// a security setting quietly not applied, which is the case where failing to
// start is the kinder outcome.
func Apply(flags *pflag.FlagSet, lookup func(string) (string, bool)) error {
	var err error
	flags.VisitAll(func(f *pflag.Flag) {
		if err != nil || f.Changed || !Reads(f) {
			return
		}
		name := Name(f.Name)
		raw, ok := lookup(name)
		if !ok {
			return
		}
		// An empty variable means "not set". Setting one to nothing is how a
		// container image's base environment is cleared, and reading that as a
		// deliberate empty value would make ALDER_TLS_CERT= a different thing
		// from an absent one for no benefit.
		if strings.TrimSpace(raw) == "" {
			return
		}
		if setErr := f.Value.Set(raw); setErr != nil {
			// This message names the variable rather than quoting the value,
			// but the wrapped parse error can still contain it -- Go's own
			// duration error quotes what it was given -- so this is not a
			// redaction and must not be relied on as one.
			//
			// It is safe because no flag that reads the environment carries a
			// secret: they are addresses, paths, levels, durations and names.
			// The tests beside each command keep that true. A flag that did
			// carry one would need a value type that refuses to print itself,
			// not a careful message here.
			err = fmt.Errorf("alder: the value of %s is not usable for --%s: %w",
				name, f.Name, setErr)
			return
		}
		f.Changed = true
	})
	return err
}
