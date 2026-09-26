package access

import (
	"strings"
	"testing"
)

// The rules in these tests are the harness's own, copied from the two servers
// rather than invented, because an access rule parser is only worth what it
// does to real syntax.

const (
	userPasswordRule = `{0}to attrs=userPassword  by self write  by anonymous auth  by * none`
	servicesRule     = `{1}to dn.subtree="ou=services,dc=alder,dc=test"  by dn.base="cn=svc-alder,ou=services,dc=alder,dc=test" none  by users read  by * read`
	oneAttrRule      = `{2}to dn.base="uid=user0002,ou=people,dc=alder,dc=test"  attrs=alderTeam  by dn.base="cn=svc-alder,ou=services,dc=alder,dc=test" none  by users read  by * read`
	catchAllRule     = `{3}to *  by self write  by users read  by * read`

	peopleACI = `(targetattr!="userPassword")(version 3.0; acl "svc-alder administers people and groups"; allow (read,search,compare) userdn = "ldap:///cn=svc-alder,ou=services,dc=alder,dc=test";)`
	denyACI   = `(targetattr="*")(version 3.0; acl "the service accounts are invisible to svc-alder"; deny (read,search,compare) userdn = "ldap:///cn=svc-alder,ou=services,dc=alder,dc=test";)`
	teamACI   = `(targetattr="alderTeam")(version 3.0; acl "alderTeam on this entry is not for svc-alder"; deny (read,search,compare) userdn = "ldap:///cn=svc-alder,ou=services,dc=alder,dc=test";)`
)

const (
	alice    = "uid=alice,ou=people,dc=alder,dc=test"
	user0002 = "uid=user0002,ou=people,dc=alder,dc=test"
	svc      = "cn=svc-alder,ou=services,dc=alder,dc=test"
	database = "olcDatabase={1}mdb,cn=config"
	suffix   = "dc=alder,dc=test"
)

func TestOpenLDAPRulesAreReadInTheirOwnTerms(t *testing.T) {
	rule := ParseOpenLDAP(userPasswordRule, database, alice)
	if !rule.Parsed {
		t.Fatalf("this is the ordinary form and must parse: %+v", rule)
	}
	if rule.Index == nil || *rule.Index != 0 {
		t.Errorf("index: %v — the order is the mechanism, and it must be reported", rule.Index)
	}
	if rule.Target == nil || len(rule.Target.Attributes) != 1 || rule.Target.Attributes[0] != "userPassword" {
		t.Errorf("target: %+v", rule.Target)
	}
	if len(rule.Grants) != 3 {
		t.Fatalf("three by clauses, got %d: %+v", len(rule.Grants), rule.Grants)
	}
	if rule.Grants[0].Subject != "self" || rule.Grants[0].Access != "write" {
		t.Errorf("first clause: %+v", rule.Grants[0])
	}
	if rule.Grants[1].Subject != "anonymous" || rule.Grants[1].Access != "auth" {
		t.Errorf("second clause: %+v", rule.Grants[1])
	}
	// An OpenLDAP level is neither an allow nor a deny; it is a level, and the
	// first matching rule wins.
	if rule.Grants[0].Kind != KindLevel {
		t.Errorf("kind %q: olcAccess grants levels", rule.Grants[0].Kind)
	}
	// It is about an attribute of every entry, so it bears on this one.
	if rule.Applies != AppliesYes {
		t.Errorf("applies %q (%s)", rule.Applies, rule.Why)
	}
	if rule.Raw != userPasswordRule {
		t.Error("the raw rule must survive parsing untouched")
	}
}

func TestOpenLDAPTargetsDecideWhatBearsOnAnEntry(t *testing.T) {
	cases := []struct {
		name  string
		rule  string
		entry string
		want  string
	}{
		{"a subtree rule bears on entries inside it", servicesRule, svc, AppliesYes},
		{"and not on entries outside it", servicesRule, alice, AppliesNo},
		{"a base rule names one entry", oneAttrRule, user0002, AppliesYes},
		{"and no other", oneAttrRule, alice, AppliesNo},
		{"a rule about everything bears on everything", catchAllRule, alice, AppliesYes},
		{"a filter is not evaluated", `{0}to filter=(objectClass=person)  by * read`, alice, AppliesMaybe},
		{"nor is a regular expression", `{0}to dn.regex="^uid=[^,]+,ou=people,dc=alder,dc=test$"  by * read`, alice, AppliesMaybe},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rule := ParseOpenLDAP(c.rule, database, c.entry)
			if rule.Applies != c.want {
				t.Errorf("applies = %q, want %q (%s)", rule.Applies, c.want, rule.Why)
			}
			if rule.Why == "" {
				t.Error("every answer must say why in words")
			}
		})
	}
}

func TestOpenLDAPKeepsWhatItCannotRead(t *testing.T) {
	// A set specification: legal slapd, and not something Alder takes apart.
	raw := `{0}to dn.subtree="dc=alder,dc=test"  by set="this/manager & user" write  by * read`
	rule := ParseOpenLDAP(raw, database, alice)
	if rule.Raw != raw {
		t.Fatal("the raw rule must be reported whole")
	}
	// The target is plain, so whether it bears on the entry is still known.
	if rule.Applies != AppliesYes {
		t.Errorf("applies %q: the target is an ordinary subtree", rule.Applies)
	}
	nonsense := ParseOpenLDAP("{9}something else entirely", database, alice)
	if nonsense.Parsed {
		t.Error("a rule that is not a to/by rule must not be reported as parsed")
	}
	if nonsense.Applies != AppliesMaybe || nonsense.Why == "" {
		t.Errorf("an unread rule may still bear on the entry, and must say so: %+v", nonsense)
	}
}

func TestACIsAreReadAndPlaced(t *testing.T) {
	rule := ParseACI(peopleACI, suffix, alice)
	if !rule.Parsed {
		t.Fatalf("the ordinary aci form must parse: %+v", rule)
	}
	if rule.Name != "svc-alder administers people and groups" {
		t.Errorf("name %q", rule.Name)
	}
	if !rule.Inherited {
		t.Error("an aci on the suffix is inherited by an entry below it")
	}
	if rule.Applies != AppliesYes {
		t.Errorf("applies %q (%s)", rule.Applies, rule.Why)
	}
	if len(rule.Grants) != 1 || rule.Grants[0].Kind != KindAllow {
		t.Fatalf("grants: %+v", rule.Grants)
	}
	if rule.Grants[0].Subject != svc {
		t.Errorf("subject %q", rule.Grants[0].Subject)
	}
	if rule.Grants[0].Access != "read,search,compare" {
		t.Errorf("access %q", rule.Grants[0].Access)
	}
	// targetattr!= is an exclusion, and the "!" is kept rather than dropped:
	// a rule about every attribute except the password is not a rule about the
	// password.
	if rule.Target == nil || len(rule.Target.Attributes) != 1 || rule.Target.Attributes[0] != "!userPassword" {
		t.Errorf("target: %+v", rule.Target)
	}

	deny := ParseACI(denyACI, "ou=services,dc=alder,dc=test", svc)
	if len(deny.Grants) != 1 || deny.Grants[0].Kind != KindDeny {
		t.Fatalf("a deny must be reported as a deny: %+v", deny.Grants)
	}

	own := ParseACI(teamACI, user0002, user0002)
	if own.Inherited {
		t.Error("an aci on the entry itself is not inherited")
	}
	if own.Applies != AppliesYes {
		t.Errorf("applies %q", own.Applies)
	}
}

func TestACIsThatDoNotBearOnTheEntry(t *testing.T) {
	// An explicit target elsewhere.
	elsewhere := ParseACI(
		`(target="ldap:///ou=groups,dc=alder,dc=test")(targetattr="*")(version 3.0; acl "groups"; allow (read) userdn="ldap:///anyone";)`,
		suffix, alice)
	if elsewhere.Applies != AppliesNo {
		t.Errorf("applies %q (%s)", elsewhere.Applies, elsewhere.Why)
	}

	// A scope that stops at the entry the rule is written on.
	base := ParseACI(
		`(targetscope="base")(targetattr="*")(version 3.0; acl "this entry only"; allow (read) userdn="ldap:///anyone";)`,
		suffix, alice)
	if base.Applies != AppliesNo {
		t.Errorf("a base-scoped aci on an ancestor does not reach down: %q (%s)", base.Applies, base.Why)
	}

	// A filter nobody here evaluates.
	filtered := ParseACI(
		`(targetfilter="(objectClass=person)")(targetattr="*")(version 3.0; acl "people"; allow (read) userdn="ldap:///anyone";)`,
		suffix, alice)
	if filtered.Applies != AppliesMaybe {
		t.Errorf("applies %q (%s)", filtered.Applies, filtered.Why)
	}
}

func TestACIKeepsWhatItCannotRead(t *testing.T) {
	raw := `(targetattr="*")(version 4.0; something nobody has written yet)`
	rule := ParseACI(raw, alice, alice)
	if rule.Parsed {
		t.Error("an aci Alder did not read must not be reported as parsed")
	}
	if rule.Raw != raw {
		t.Error("the raw rule must be reported whole")
	}
}

func TestDisclaimerSaysWhatThisIsNot(t *testing.T) {
	// The one sentence every surface repeats. If it stops saying that Alder
	// does not evaluate the rules, the feature has started lying.
	for _, phrase := range []string{"does not evaluate", "the directory"} {
		if !strings.Contains(Disclaimer, phrase) {
			t.Errorf("the disclaimer no longer says %q: %s", phrase, Disclaimer)
		}
	}
}
