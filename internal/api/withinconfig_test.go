package api

import "testing"

func TestWithinConfigTreeComparesDNsNotText(t *testing.T) {
	for _, c := range []struct {
		target string
		want   bool
	}{
		{"cn=config", true},
		{"cn=schema,cn=config", true},
		{`cn=a\,cn=config`, false},
		{"cn=configuration", false},
	} {
		if got := withinConfigTree(c.target, "cn=config"); got != c.want {
			t.Errorf("withinConfigTree(%q) = %v, want %v", c.target, got, c.want)
		}
	}
}
