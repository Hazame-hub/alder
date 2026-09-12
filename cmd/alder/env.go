package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/pflag"
)

// Every flag also reads an environment variable.
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
// twenty lines. If a file is ever wanted, this is the layer it slots under.
//
// The flag wins. Something on the command line was typed on purpose, and an
// inherited environment silently overriding it is the failure mode this
// ordering exists to avoid.

// envPrefix is prepended to the flag name to make the variable.
const envPrefix = "ALDER_"

// envName is the variable a flag reads: the flag name upper-cased, with dashes
// as underscores, behind the prefix. --session-idle-timeout is
// ALDER_SESSION_IDLE_TIMEOUT.
func envName(flag string) string {
	return envPrefix + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

// applyEnv fills in every flag the command line did not set from the
// environment.
//
// An unparseable value is an error rather than a warning. A misspelled duration
// in ALDER_SESSION_MAX_LIFETIME that silently left the default in place would be
// a security setting quietly not applied, which is the case where failing to
// start is the kinder outcome.
func applyEnv(flags *pflag.FlagSet, lookup func(string) (string, bool)) error {
	var err error
	flags.VisitAll(func(f *pflag.Flag) {
		if err != nil || f.Changed {
			return
		}
		name := envName(f.Name)
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
			// It is safe because no flag serve defines carries a secret: they
			// are addresses, paths, levels, durations and the allowlist.
			// TestNoServeFlagLooksLikeASecret is what keeps that true. A flag
			// that did carry one would need a value type that refuses to print
			// itself, not a careful message here.
			err = fmt.Errorf("alder: the value of %s is not usable for --%s: %w",
				name, f.Name, setErr)
			return
		}
		f.Changed = true
	})
	return err
}

// applyEnvFromOS is applyEnv against the real environment.
func applyEnvFromOS(flags *pflag.FlagSet) error {
	return applyEnv(flags, os.LookupEnv)
}
