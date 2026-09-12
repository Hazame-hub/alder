package main

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func TestEnvName(t *testing.T) {
	cases := map[string]string{
		"addr":                    "ALDER_ADDR",
		"allowed-targets":         "ALDER_ALLOWED_TARGETS",
		"session-idle-timeout":    "ALDER_SESSION_IDLE_TIMEOUT",
		"i-know-this-is-insecure": "ALDER_I_KNOW_THIS_IS_INSECURE",
	}
	for flag, want := range cases {
		if got := envName(flag); got != want {
			t.Errorf("envName(%q) = %q, want %q", flag, got, want)
		}
	}
}

// A flag typed on the command line was typed on purpose. An inherited
// environment silently overriding it is the failure this ordering avoids.
func TestTheCommandLineWinsOverTheEnvironment(t *testing.T) {
	flags := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	addr := flags.String("addr", ":8443", "")
	if err := flags.Parse([]string{"--addr", ":9999"}); err != nil {
		t.Fatalf("parsing: %v", err)
	}
	env := map[string]string{"ALDER_ADDR": ":1234"}
	if err := applyEnv(flags, lookupIn(env)); err != nil {
		t.Fatalf("applyEnv: %v", err)
	}
	if *addr != ":9999" {
		t.Errorf("addr = %q, want the flag's :9999", *addr)
	}
}

func TestTheEnvironmentFillsInWhatTheCommandLineDidNot(t *testing.T) {
	flags := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	addr := flags.String("addr", ":8443", "")
	targets := flags.String("allowed-targets", "", "")
	readOnly := flags.Bool("read-only", false, "")
	idle := flags.Duration("session-idle-timeout", 30*time.Minute, "")
	if err := flags.Parse(nil); err != nil {
		t.Fatalf("parsing: %v", err)
	}

	env := map[string]string{
		"ALDER_ALLOWED_TARGETS":      "ldap1.example.com:636",
		"ALDER_READ_ONLY":            "true",
		"ALDER_SESSION_IDLE_TIMEOUT": "5m",
	}
	if err := applyEnv(flags, lookupIn(env)); err != nil {
		t.Fatalf("applyEnv: %v", err)
	}

	if *addr != ":8443" {
		t.Errorf("addr = %q, want the default", *addr)
	}
	if *targets != "ldap1.example.com:636" {
		t.Errorf("allowed-targets = %q", *targets)
	}
	if !*readOnly {
		t.Error("read-only did not come from the environment")
	}
	if *idle != 5*time.Minute {
		t.Errorf("session-idle-timeout = %v, want 5m", *idle)
	}
}

// An empty variable means "not set". Clearing one is how a base image's
// environment is neutralised, and reading that as a deliberate empty value
// would make ALDER_TLS_CERT= different from an absent one for no benefit.
func TestAnEmptyVariableIsNotAValue(t *testing.T) {
	flags := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	cert := flags.String("tls-cert", "default.pem", "")
	if err := flags.Parse(nil); err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if err := applyEnv(flags, lookupIn(map[string]string{"ALDER_TLS_CERT": "   "})); err != nil {
		t.Fatalf("applyEnv: %v", err)
	}
	if *cert != "default.pem" {
		t.Errorf("tls-cert = %q, want the default", *cert)
	}
}

// A misspelled duration in a security setting that quietly left the default in
// place is a control not applied. Failing to start is the kinder outcome.
func TestAnUnparseableVariableIsAnError(t *testing.T) {
	flags := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	flags.Duration("session-max-lifetime", time.Hour, "")
	if err := flags.Parse(nil); err != nil {
		t.Fatalf("parsing: %v", err)
	}
	err := applyEnv(flags, lookupIn(map[string]string{"ALDER_SESSION_MAX_LIFETIME": "twelve hours"}))
	if err == nil {
		t.Fatal("applyEnv accepted an unparseable duration")
	}
	if !strings.Contains(err.Error(), "ALDER_SESSION_MAX_LIFETIME") {
		t.Errorf("the error does not name the variable: %v", err)
	}
}

// The environment message is safe because no flag carries a secret -- the
// wrapped parse error can quote the value, and Go's duration error does.
//
// So the invariant worth pinning is the premise, not the message: if a flag
// that holds a credential is ever added, this fails and whoever added it has to
// deal with the value type rather than discovering the leak in a log.
func TestNoServeFlagLooksLikeASecret(t *testing.T) {
	suspicious := []string{"password", "passwd", "secret", "token", "credential"}
	serveCmd().Flags().VisitAll(func(f *pflag.Flag) {
		for _, word := range suspicious {
			if strings.Contains(strings.ToLower(f.Name), word) {
				t.Errorf("--%s looks like it carries a secret. applyEnv wraps the "+
					"parse error, which can quote the value; give it a flag value "+
					"type that refuses to print itself before adding it", f.Name)
			}
		}
	})
}

// Every flag serve defines is reachable from the environment, so the promise in
// docs/COMPATIBILITY.md covers all of them rather than the ones somebody
// remembered to wire up.
func TestEveryServeFlagReadsAVariable(t *testing.T) {
	cmd := serveCmd()
	flags := cmd.Flags()

	env := map[string]string{}
	flags.VisitAll(func(f *pflag.Flag) {
		switch f.Value.Type() {
		case "bool":
			env[envName(f.Name)] = "true"
		case "int":
			env[envName(f.Name)] = "8"
		case "duration":
			env[envName(f.Name)] = "7m"
		default:
			env[envName(f.Name)] = "from-the-environment"
		}
	})
	if err := applyEnv(flags, lookupIn(env)); err != nil {
		t.Fatalf("applyEnv: %v", err)
	}
	flags.VisitAll(func(f *pflag.Flag) {
		if !f.Changed {
			t.Errorf("--%s did not read %s", f.Name, envName(f.Name))
		}
	})
}

func lookupIn(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}
