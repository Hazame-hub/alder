package directory

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

// The password preview shows an ldappasswd command to copy. Whoever named the
// entry chose its DN, and whoever resets its password pastes the command, so
// the DN is an argument a shell must receive as text and never run.
func TestThePasswordCommandQuotesTheDN(t *testing.T) {
	names := []string{
		"uid=alice,ou=people,dc=example,dc=com",
		"cn=$(touch /tmp/alder-pwned) `id`,dc=example,dc=com",
		`cn=a|b & c\; d,dc=example,dc=com`,
		"cn=O'Brien,dc=example,dc=com",
		"cn=Ünïcödé,dc=example,dc=com",
	}
	for _, name := range names {
		d, err := dn.Parse(name)
		if err != nil {
			t.Fatalf("parsing %q: %v", name, err)
		}
		c := ChangeRecord{DN: d, Type: ChangeSetPassword}
		for _, text := range []string{c.LDIF(), c.LDIFFolded()} {
			line := commandLine(t, text)
			arg := strings.TrimPrefix(line, `#   ldappasswd -H "$LDAP_URI" -D "$BIND_DN" -W -S `)
			if arg == line {
				t.Fatalf("no ldappasswd line in:\n%s", text)
			}
			if got := throughShell(t, arg); got != d.String() {
				t.Errorf("%q: a shell reads the argument as %q, want %q", name, got, d.String())
			}
		}
	}
}

func commandLine(t *testing.T, text string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "ldappasswd") {
			return line
		}
	}
	return ""
}

// throughShell has a POSIX shell print the argument as it would pass it on.
func throughShell(t *testing.T, arg string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell here to check the quoting with")
	}
	out, err := exec.CommandContext(t.Context(), sh, "-c", "printf %s "+arg).Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	return string(out)
}
