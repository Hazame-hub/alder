package plan

import (
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

// A schema whose attributes carry the equality rules the planner models, one
// it deliberately does not, and none at all.
func equalitySchema(t *testing.T) *schema.Schema {
	t.Helper()
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrAttributeTypes: {
			"( 2.5.4.41 NAME 'name' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			// Inherits caseIgnoreMatch through SUP, as cn does on both servers.
			"( 2.5.4.3 NAME 'cn' SUP name )",
			"( 0.9.2342.19200300.100.1.3 NAME 'mail' EQUALITY caseIgnoreIA5Match SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
			"( 2.5.4.16 NAME 'postalAddress' EQUALITY caseIgnoreListMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.41 )",
			"( 2.5.4.34 NAME 'seeAlso' EQUALITY distinguishedNameMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
			"( 2.5.4.50 NAME 'uniqueMember' EQUALITY uniqueMemberMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.34 )",
			// Named by OID rather than by name: caseIgnoreMatch.
			"( 2.5.4.13 NAME 'description' EQUALITY 2.5.13.2 SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			// A rule Alder knows but that is not one add and delete fold under.
			"( 1.3.6.1.4.1.99999.1 NAME 'exactCode' EQUALITY caseExactMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			// A rule Alder does not model at all.
			"( 1.3.6.1.4.1.99999.2 NAME 'sitePolicy' EQUALITY 1.3.6.1.4.1.99999.9.9 SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			// No rule.
			"( 1.3.6.1.4.1.99999.3 NAME 'noRule' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		},
	})
	if len(sch.Errors) != 0 {
		t.Fatalf("the schema does not parse: %v", sch.Errors)
	}
	return sch
}

func TestAddAndDeleteCompareUnderKnownEqualityRules(t *testing.T) {
	sch := equalitySchema(t)
	live := directory.NewEntry(mustParse(t, "uid=ana,ou=people,dc=alder,dc=test"))
	set := func(name string, values ...string) {
		raw := make([][]byte, 0, len(values))
		for _, v := range values {
			raw = append(raw, []byte(v))
		}
		live.Set(name, raw)
	}
	set("cn", "Ana  Lima")
	set("mail", "Ana@Example.com")
	set("postalAddress", "1 Main St$Springfield")
	set("seeAlso", "cn=Platform,ou=Groups,dc=alder,dc=test")
	set("uniqueMember", "uid=ana,ou=people,dc=alder,dc=test", "uid=bea,ou=people,dc=alder,dc=test#'0101'B")
	set("description", "platform")
	set("exactCode", "abc")
	set("sitePolicy", "strict")
	set("noRule", "value")

	cases := []struct {
		name string
		mod  directory.Mod
		want bool
	}{
		// Held under the rule, so adding it is nothing to do...
		{"caseIgnoreMatch through SUP, case and spaces", add("cn", " ana lima "), true},
		{"caseIgnoreIA5Match", add("mail", "ana@example.COM"), true},
		{"caseIgnoreListMatch", add("postalAddress", "1 MAIN ST$springfield"), true},
		{"distinguishedNameMatch", add("seeAlso", "CN=platform, OU=groups,DC=alder,DC=test"), true},
		{"uniqueMemberMatch, a bare name", add("uniqueMember", "UID=ana, OU=People,DC=alder,DC=test"), true},
		{"uniqueMemberMatch, a name with the same UID", add("uniqueMember", "UID=Bea,ou=people,dc=alder,dc=test#'0101'B"), true},
		{"a rule named by its OID", add("description", "PLATFORM"), true},
		// ...and deleting it is a change, because the directory would remove it.
		{"delete under caseIgnoreMatch", del("cn", "ANA LIMA"), false},
		{"delete under distinguishedNameMatch", del("seeAlso", "cn=platform,ou=groups,dc=alder,dc=test"), false},
		{"delete under uniqueMemberMatch", del("uniqueMember", "uid=ANA,ou=people,dc=alder,dc=test"), false},
		// The UID is never folded away: a bare name is not the name with a UID,
		// and two UIDs are two values, whichever way the server reads them.
		{"uniqueMemberMatch, the name without its UID", add("uniqueMember", "uid=bea,ou=people,dc=alder,dc=test"), false},
		{"uniqueMemberMatch, the name with a UID it does not hold", add("uniqueMember", "uid=ana,ou=people,dc=alder,dc=test#'1'B"), false},
		{"uniqueMemberMatch, another UID", add("uniqueMember", "uid=bea,ou=people,dc=alder,dc=test#'0110'B"), false},
		// Bytes wherever the rule is not one of the five, or is not known.
		{"caseExactMatch stays bytes", add("exactCode", "ABC"), false},
		{"an unknown rule stays bytes", add("sitePolicy", "STRICT"), false},
		{"no rule stays bytes", add("noRule", "VALUE"), false},
		{"an attribute the schema does not define stays bytes", add("undefinedThing", "x"), false},
		{"a delete under an unknown rule, byte-different, is unchanged as before", del("sitePolicy", "STRICT"), true},
		// A value that really is absent is still a change.
		{"absent under the rule", add("cn", "Bea Lima"), false},
		// A replacement is satisfied only by the bytes: a case correction is meant.
		{"replace differing in case only", directory.Mod{Op: directory.ModReplace, Name: "description",
			Values: [][]byte{[]byte("Platform")}}, false},
		{"replace with the same bytes", directory.Mod{Op: directory.ModReplace, Name: "description",
			Values: [][]byte{[]byte("platform")}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := satisfied([]directory.Mod{c.mod}, live, sch); got != c.want {
				t.Errorf("satisfied = %v, want %v", got, c.want)
			}
		})
	}

	// Without a schema, every comparison is bytes.
	if satisfied([]directory.Mod{add("cn", "ana lima")}, live, nil) {
		t.Error("without a schema, a case-different add was taken as held")
	}
}

func add(name, value string) directory.Mod {
	return directory.Mod{Op: directory.ModAdd, Name: name, Values: [][]byte{[]byte(value)}}
}

func del(name, value string) directory.Mod {
	return directory.Mod{Op: directory.ModDelete, Name: name, Values: [][]byte{[]byte(value)}}
}
