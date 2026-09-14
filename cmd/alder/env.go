package main

import (
	"os"

	"github.com/spf13/pflag"

	"github.com/hazame-hub/alder/internal/envflags"
)

// Every flag of alder serve also reads an environment variable: the flag name
// upper-cased, with dashes as underscores, behind ALDER_. The flag wins. See
// internal/envflags for why, and for the client commands' exceptions.

// envPrefix is prepended to the flag name to make the variable.
const envPrefix = envflags.Prefix

// envName is the variable a flag reads.
func envName(flag string) string { return envflags.Name(flag) }

// applyEnv fills in every flag the command line did not set from the
// environment.
func applyEnv(flags *pflag.FlagSet, lookup func(string) (string, bool)) error {
	return envflags.Apply(flags, lookup)
}

// applyEnvFromOS is applyEnv against the real environment.
func applyEnvFromOS(flags *pflag.FlagSet) error {
	return applyEnv(flags, os.LookupEnv)
}
