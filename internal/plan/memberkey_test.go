package plan

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

// The fast path in memberKey is only allowed to exist if it gives the answer
// parsing gives. This holds it to that over the shapes it accepts and the ones
// it must refuse.
func TestPlainDNKeysAsParsingDoes(t *testing.T) {
	parsed := func(s string) (string, bool) {
		d, err := dn.Parse(s)
		if err != nil {
			return "", false
		}
		return strings.ToLower(d.String()), true
	}

	inputs := []string{
		"uid=alice,ou=people,dc=alder,dc=test",
		"UID=Alice,OU=People,DC=Alder,DC=Test",
		"cn=group-1,ou=groups,dc=example,dc=com",
		"mail=a.b@example.com,dc=example",
		"uid=first_last,dc=x",
		"x-custom-attr=value,dc=x",
		"cn=a",
		// Everything below must not take the fast path.
		"uid=alice, ou=people,dc=alder,dc=test",
		"uid = alice,dc=test",
		"cn=Smith\\, John,dc=test",
		"cn=a+sn=b,dc=test",
		"cn=#04,dc=test",
		"cn=Ünïcödé,dc=test",
		"2.5.4.3=alice,dc=test",
		"cn=,dc=test",
		"=alice,dc=test",
		"cn=a,,dc=test",
		"cn=a,",
		",cn=a",
		"cn=a=b,dc=test",
		"1cn=a,dc=test",
		"alice",
		"",
	}
	for _, in := range inputs {
		if !plainDN(in) {
			continue
		}
		want, ok := parsed(in)
		if !ok {
			t.Errorf("%q takes the fast path but does not parse as a DN", in)
			continue
		}
		if got := strings.ToLower(in); got != want {
			t.Errorf("%q: fast key %q, parsed key %q", in, got, want)
		}
	}

	for _, in := range []string{"uid=alice,ou=people,dc=alder,dc=test", "cn=a"} {
		if !plainDN(in) {
			t.Errorf("%q should take the fast path", in)
		}
	}
	for _, in := range []string{"cn=Smith\\, John,dc=test", "uid=alice, ou=people", "cn=a+sn=b", "alice", "cn=a,"} {
		if plainDN(in) {
			t.Errorf("%q must be parsed, not scanned", in)
		}
	}
}

// memberKey still treats two spellings of one member as one, whichever path
// each spelling takes.
func TestMemberKeyAcrossSpellings(t *testing.T) {
	same := [][2]string{
		{"uid=alice,ou=people,dc=alder,dc=test", "UID=Alice, OU=People, DC=alder, DC=test"},
		{"uid=alice,ou=people,dc=alder,dc=test", "uid=alice,ou=people,dc=alder,dc=test#'0101'B"},
	}
	for _, pair := range same {
		if memberKey([]byte(pair[0])) != memberKey([]byte(pair[1])) {
			t.Errorf("%q and %q should be the same member", pair[0], pair[1])
		}
	}
}
