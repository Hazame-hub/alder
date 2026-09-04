package api

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

func rec(t *testing.T, target string, kind directory.ChangeType, attrs ...string) directory.ChangeRecord {
	t.Helper()
	d, err := dn.Parse(target)
	if err != nil {
		t.Fatalf("dn.Parse(%q): %v", target, err)
	}
	r := directory.ChangeRecord{DN: d, Type: kind}
	for _, a := range attrs {
		r.Mods = append(r.Mods, directory.Mod{
			Op: directory.ModReplace, Name: a, Values: [][]byte{[]byte("x")},
		})
	}
	return r
}

func TestLDAPHint(t *testing.T) {
	configCaps := directory.Capabilities{
		Config: directory.ConfigAccess{DN: "cn=config", Readable: true},
		SchemaWrite: directory.SchemaWrite{
			Style:   directory.SchemaStyleConfig,
			Targets: []directory.SchemaTarget{{DN: "cn={4}alder,cn=schema,cn=config"}},
		},
	}

	tests := []struct {
		name   string
		code   uint16
		record directory.ChangeRecord
		caps   directory.Capabilities
		want   string
	}{
		{
			// The exact refusal that prompted this: deleting a schema
			// collection. "Unwilling To Perform (code 53)" alone says nothing.
			name:   "deleting a schema collection explains that servers refuse it",
			code:   53,
			record: rec(t, "cn={4}alder,cn=schema,cn=config", directory.ChangeDelete),
			caps:   configCaps,
			want:   "while it is loaded",
		},
		{
			name:   "deleting another configuration entry gets the general reason",
			code:   53,
			record: rec(t, "olcDatabase={1}mdb,cn=config", directory.ChangeDelete),
			caps:   configCaps,
			want:   "currently using",
		},
		{
			name:   "renaming a configuration entry says the server owns the name",
			code:   53,
			record: rec(t, "olcDatabase={1}mdb,cn=config", directory.ChangeModRDN),
			caps:   configCaps,
			want:   "the server owns that name",
		},
		{
			name:   "a refusal on ordinary data still gets an explanation",
			code:   53,
			record: rec(t, "uid=bob,ou=people,dc=alder,dc=test", directory.ChangeModify, "cn"),
			caps:   configCaps,
			want:   "understood this and declined it",
		},
		{
			// The other half of today's findings: a config write refused for
			// access should point at the second identity, not just say no.
			name:   "no rights in the configuration tree points at the config credentials",
			code:   50,
			record: rec(t, "cn=config", directory.ChangeModify, "olcLogLevel"),
			caps:   configCaps,
			want:   "supply configuration credentials",
		},
		{
			name:   "an add onto a missing parent says to create the parent first",
			code:   32,
			record: rec(t, "cn=x,ou=nope,dc=alder,dc=test", directory.ChangeAdd),
			caps:   configCaps,
			want:   "parent entry does not exist",
		},
		{
			name:   "deleting an entry with children explains why",
			code:   66,
			record: rec(t, "ou=people,dc=alder,dc=test", directory.ChangeDelete),
			caps:   configCaps,
			want:   "has children",
		},
		{
			name:   "a constraint violation on a password names the policy",
			code:   19,
			record: rec(t, "uid=bob,ou=people,dc=alder,dc=test", directory.ChangeModify, "userPassword"),
			caps:   configCaps,
			want:   "password policy",
		},
		{
			name:   "a constraint violation elsewhere does not claim it was a password",
			code:   19,
			record: rec(t, "uid=bob,ou=people,dc=alder,dc=test", directory.ChangeModify, "cn"),
			caps:   configCaps,
			want:   "beyond the schema",
		},
		{
			name:   "an object class violation says which way it failed",
			code:   65,
			record: rec(t, "uid=bob,ou=people,dc=alder,dc=test", directory.ChangeModify, "cn"),
			caps:   configCaps,
			want:   "require is missing",
		},
		{
			name:   "an undefined attribute type suggests adding it to the schema",
			code:   17,
			record: rec(t, "uid=bob,ou=people,dc=alder,dc=test", directory.ChangeModify, "nope"),
			caps:   configCaps,
			want:   "add the attribute type to the schema",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ldapHint(tc.code, tc.record, tc.caps)
			if !strings.Contains(got, tc.want) {
				t.Errorf("hint does not mention %q\n  got: %q", tc.want, got)
			}
		})
	}
}

// A hint is a help, not a claim. Codes Alder has nothing useful to say about
// must produce nothing rather than a platitude.
func TestLDAPHintStaysQuietWhenItHasNothingToAdd(t *testing.T) {
	for _, code := range []uint16{0, 1, 18, 80, 4096} {
		if got := ldapHint(code, directory.ChangeRecord{}, directory.Capabilities{}); got != "" {
			t.Errorf("code %d produced a hint it should not have: %q", code, got)
		}
	}
}

// The hint must never repeat what the server said. The driver withholds the
// diagnostic on purpose, and a hint that leaked it back would undo that.
func TestLDAPHintIsWrittenFromTheCodeAlone(t *testing.T) {
	r := rec(t, "cn=secret,ou=hidden,dc=alder,dc=test", directory.ChangeDelete)
	got := ldapHint(53, r, directory.Capabilities{})
	if strings.Contains(got, "secret") || strings.Contains(got, "hidden") {
		t.Errorf("the hint repeats part of the request: %q", got)
	}
}

func TestStripPositionPrefix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"{5}alder", "alder"},
		{"{0}core", "core"},
		{"alder", "alder"},
		{"{}alder", "{}alder"},       // not a number
		{"{abc}alder", "{abc}alder"}, // not a number
		{"{5alder", "{5alder"},       // unterminated
		{"", ""},
	} {
		if got := stripPositionPrefix(tc.in); got != tc.want {
			t.Errorf("stripPositionPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitRDN(t *testing.T) {
	for _, tc := range []struct {
		in        string
		attr, val string
		ok        bool
	}{
		{"cn=alder,cn=schema,cn=config", "cn", "alder", true},
		{"cn={5}alder,cn=schema,cn=config", "cn", "{5}alder", true},
		{"uid=bob", "uid", "bob", true},
		// An escaped comma belongs to the value, not to the DN's structure.
		{`cn=Liddell\, Alice,ou=people,dc=x`, "cn", `Liddell\, Alice`, true},
		{"nonsense", "", "", false},
		{"=novalue", "", "", false},
		{"noequals=", "", "", false},
	} {
		attr, val, ok := splitRDN(tc.in)
		if ok != tc.ok || attr != tc.attr || val != tc.val {
			t.Errorf("splitRDN(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, attr, val, ok, tc.attr, tc.val, tc.ok)
		}
	}
}
