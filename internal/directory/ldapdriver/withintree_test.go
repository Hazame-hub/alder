package ldapdriver

import "testing"

// withinTree decides which identity an operation runs as, so it compares
// DNs as DNs: an escaped comma is part of a value, not a separator.
func TestWithinTreeComparesDNsNotText(t *testing.T) {
	cases := []struct {
		target, base string
		want         bool
	}{
		{"cn=config", "cn=config", true},
		{"olcDatabase={1}mdb,cn=config", "cn=config", true},
		{"OLCDATABASE={1}MDB, CN=Config", "cn=config", true},
		{`cn=a\,cn=config`, "cn=config", false},
		{`ou=x,cn=y\,cn=config`, "cn=config", false},
		{"cn=configuration", "cn=config", false},
		{"uid=alice,ou=people,dc=example,dc=com", "cn=config", false},
		{"not a dn", "cn=config", false},
		{"cn=config", "", false},
	}
	for _, c := range cases {
		if got := withinTree(c.target, c.base); got != c.want {
			t.Errorf("withinTree(%q, %q) = %v, want %v", c.target, c.base, got, c.want)
		}
	}
}
