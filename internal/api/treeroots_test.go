package api

import "testing"

// reachableFrom decides whether a root needs adding or is already covered.
// Getting it wrong shows the same subtree twice, or hides the schema entirely.
func TestReachableFrom(t *testing.T) {
	roots := []string{"dc=alder,dc=test", "cn=config"}

	for _, tc := range []struct {
		target string
		want   bool
		why    string
	}{
		{"cn=schema", false, "a subschema subentry sits outside every naming context"},
		{"cn={4}alder,cn=schema,cn=config", true, "schema in configuration is already under a root"},
		{"cn=config", true, "a root covers itself"},
		{"CN=CONFIG", true, "DNs compare case-insensitively"},
		{"ou=people,dc=alder,dc=test", true, "an ordinary entry is under its suffix"},
		{"cn=schema,cn=elsewhere", false, "a similar name under a different tree is not covered"},
		// The trap: a suffix match on the string alone would call this covered,
		// because it ends with "config".
		{"cn=notconfig", false, "a DN merely ending in the same text is not beneath it"},
	} {
		if got := reachableFrom(tc.target, roots); got != tc.want {
			t.Errorf("reachableFrom(%q) = %v, want %v — %s", tc.target, got, tc.want, tc.why)
		}
	}
}
