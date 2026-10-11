package schema

import "testing"

// A secret written by OID is a secret. "2.5.4.35: {SSHA}..." in an LDIF file
// is userPassword, and matching names alone let it through.
func TestSensitiveAttributesAreRecognisedByOID(t *testing.T) {
	for _, d := range []string{
		"2.5.4.35",              // userPassword
		"2.5.4.35;binary",       // with an option
		" 2.5.4.35 ",            // as fold trims
		"1.2.840.113556.1.4.90", // unicodePwd
		"2.16.840.1.113719.1.301.4.39.1",
		"1.3.6.1.4.1.42.2.27.8.1.20",      // pwdHistory, OpenLDAP ppolicy
		"2.16.840.1.113730.3.1.96",        // passwordHistory, 389 DS
		"1.3.6.1.4.1.4203.1.12.2.3.2.0.9", // olcRootPW
	} {
		if !IsSensitive(d) {
			t.Errorf("IsSensitive(%q) = false; it is a secret written by OID", d)
		}
	}
	for _, d := range []string{"2.5.4.3", "2.5.4.4", "0.9.2342.19200300.100.1.1", "2.5.4.35.1", "12.5.4.35"} {
		if IsSensitive(d) {
			t.Errorf("IsSensitive(%q) = true; it is not a sensitive attribute", d)
		}
	}
}

// Every OID in the table names an attribute the name table already treats as
// sensitive, so the two cannot disagree about what a secret is.
func TestEverySensitiveOIDNamesASensitiveAttribute(t *testing.T) {
	for oid, name := range sensitiveOIDs {
		if !sensitiveAttrs[name] {
			t.Errorf("%s maps to %q, which sensitiveAttrs does not list", oid, name)
		}
		if fold(oid) != oid {
			t.Errorf("%s is not stored in the folded form lookups use", oid)
		}
	}
}

// The export's per-value rule follows the OID too: a digest of a password may
// be released when asked for, the password itself and the never-released
// secrets may not.
func TestReleasableFollowsTheOID(t *testing.T) {
	cases := []struct {
		desc  string
		value string
		want  bool
	}{
		{"2.5.4.35", "{SSHA}abc", true},
		{"2.5.4.35", "plain-password", false},
		{"2.16.840.1.113719.1.301.4.39.1", "{SSHA}abc", false}, // krbPrincipalKey is never released
		{"1.3.6.1.4.1.4203.1.12.2.3.2.0.11", "{SSHA}abc", false},
		{"2.5.4.3", "anything", true},
	}
	for _, c := range cases {
		if got := Releasable(c.desc, []byte(c.value)); got != c.want {
			t.Errorf("Releasable(%q, %q) = %v, want %v", c.desc, c.value, got, c.want)
		}
	}
}
