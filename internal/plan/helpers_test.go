package plan

import (
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

func mustParse(t *testing.T, s string) dn.DN {
	t.Helper()
	d, err := dn.Parse(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return d
}

// A miniature schema, enough to tell an attribute an operator owns from one the
// directory does.
//
// Deliberately its own rather than the API package's: what this package needs
// from a schema is the usage and no-user-modification flags, and a fixture that
// grew for the object-view derivation would keep changing under it for reasons
// that have nothing to do with planning.
func testSchema(t testing.TB) *schema.Schema {
	t.Helper()
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrObjectClasses: {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) " +
				"MAY ( userPassword $ telephoneNumber $ description ) )",
			"( 2.16.840.1.113730.3.2.2 NAME 'inetOrgPerson' SUP person STRUCTURAL " +
				"MAY ( uid $ mail $ givenName ) )",
			"( 2.5.6.5 NAME 'organizationalUnit' SUP top STRUCTURAL MUST ou )",
		},
		schema.AttrAttributeTypes: {
			"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.3 NAME 'cn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.4 NAME 'sn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.11 NAME 'ou' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.13 NAME 'description' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.20 NAME 'telephoneNumber' SYNTAX 1.3.6.1.4.1.1466.115.121.1.50 )",
			"( 2.5.4.35 NAME 'userPassword' SYNTAX 1.3.6.1.4.1.1466.115.121.1.40 )",
			"( 2.5.4.42 NAME 'givenName' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 0.9.2342.19200300.100.1.1 NAME 'uid' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 0.9.2342.19200300.100.1.3 NAME 'mail' SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
			// The directory's own, which a plan must never try to enforce.
			"( 1.3.6.1.1.16.4 NAME 'entryUUID' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 " +
				"NO-USER-MODIFICATION SINGLE-VALUE USAGE directoryOperation )",
			"( 2.5.18.2 NAME 'modifyTimestamp' SYNTAX 1.3.6.1.4.1.1466.115.121.1.24 " +
				"NO-USER-MODIFICATION SINGLE-VALUE USAGE directoryOperation )",
		},
	})
	if len(sch.Errors) != 0 {
		t.Fatalf("the test schema does not parse: %v", sch.Errors)
	}
	return sch
}
