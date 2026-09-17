package preflight

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/changepkg"
	"github.com/hazame-hub/alder/internal/directory"
)

const (
	teamOID   = "1.3.6.1.4.1.99999.40.1"
	teamDef   = "( " + teamOID + " NAME 'alderTeam' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " )"
	staffOID  = "1.3.6.1.4.1.99999.40.2"
	staffDef  = "( " + staffOID + " NAME 'alderStaff' SUP top AUXILIARY MUST alderTeam )"
	patDN     = "uid=pat,ou=people,dc=alder,dc=test"
	peopleDN  = "ou=people,dc=alder,dc=test"
	managerDN = "uid=boss,ou=people,dc=alder,dc=test"
)

// mixed is schema, then an entry that needs it, then a group naming the entry.
func mixed(t testing.TB) *changepkg.Package {
	t.Helper()
	return buildPackage(t,
		schemaChange("c1", changepkg.ElementAttributeType, changepkg.SchemaAdd, teamOID, teamDef),
		schemaChange("c2", changepkg.ElementObjectClass, changepkg.SchemaAdd, staffOID, staffDef),
		addEntry("c3", patDN,
			attr("objectClass", "top", "person", "alderStaff"), attr("uid", "pat"), attr("cn", "Pat"), attr("sn", "Doe"),
			attr("alderTeam", "platform"), attr("manager", managerDN)),
		addEntry("c4", "cn=platform,ou=groups,dc=alder,dc=test",
			attr("objectClass", "top", "groupOfNames"), attr("cn", "platform"), attr("member", patDN)),
	)
}

func withManager(t testing.TB) *fakeTarget {
	entries := append(baseEntries(t), entry(t, managerDN, "objectClass", "top", "objectClass", "person", "cn", "Boss", "sn", "B", "uid", "boss"))
	return newTarget(t, targetSchema(t, nil, nil), entries...)
}

func TestAPortablePackageIsCompatible(t *testing.T) {
	target := withManager(t)
	r := preflightPackage(t, mixed(t), target)
	if r.Overall != Compatible || !r.Complete {
		t.Fatalf("want compatible\n%s", describe(r))
	}
	one(t, r, CodeDefinitionPortable, byItem("c1"))
	class := one(t, r, CodeDefinitionPortable, byItem("c2"))
	if len(class.Prerequisites) != 1 || class.Prerequisites[0].OID != teamOID || class.Prerequisites[0].ProvidedBy == "" {
		t.Errorf("the class should name the attribute type it needs, supplied by c1: %+v", class.Prerequisites)
	}
	one(t, r, CodeChangePortable, byItem("c3"))
	one(t, r, CodeChangePortable, byItem("c4"))
	one(t, r, CodeReferenceReady, byItem("c3"))
	one(t, r, CodeReferenceProvided, byItem("c4"))
	if target.writes != 0 {
		t.Fatalf("a preflight wrote %d time(s)", target.writes)
	}
	for _, f := range r.Findings {
		if f.ValidationStatus == "" && f.Source.Item != "" && f.Category != CategoryReferences {
			t.Errorf("finding %s about item %s does not carry the item's validation status", f.Code, f.Source.Item)
		}
	}
}

// A schema chain whose root cannot be carried: the attribute type conflicts
// with the target, so the class that needs it cannot be added, so the entry
// that uses the class cannot be placed. Three findings, linked, not three
// unrelated errors.
func TestACausalChainLinksTheEntryToTheRootCause(t *testing.T) {
	sch := targetSchema(t, []string{"( " + teamOID + " NAME 'alderTeam' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " SINGLE-VALUE )"}, nil)
	target := newTarget(t, sch, append(baseEntries(t), entry(t, managerDN, "objectClass", "person", "cn", "B", "sn", "B"))...)
	r := preflightPackage(t, mixed(t), target)

	root := one(t, r, CodeDefinitionConflict, byItem("c1"))
	if root.Classification != Incompatible || !root.BlocksPortability || root.Target == nil || !strings.Contains(root.Target.Detail, "singleValue") {
		t.Fatalf("the attribute type should conflict on singleValue: %+v", root)
	}
	class := one(t, r, CodeDependencyBlocked, byItem("c2"))
	if len(class.Causes) != 1 || class.Causes[0] != root.ID {
		t.Fatalf("the class should be blocked by the attribute type %s, causes %v\n%s", root.ID, class.Causes, describe(r))
	}
	main := one(t, r, CodeEntryBlocked, byItem("c3"))
	chain := map[string]bool{}
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		if depth > 10 || chain[id] {
			return
		}
		chain[id] = true
		if f := byID(r, id); f != nil {
			for _, c := range f.Causes {
				walk(c, depth+1)
			}
		}
	}
	walk(main.ID, 0)
	if !chain[class.ID] || !chain[root.ID] {
		t.Fatalf("the entry's causes do not lead to the class and the attribute type\n%s", describe(r))
	}
	if r.Overall != NotCompatible {
		t.Errorf("overall %s, want incompatible", r.Overall)
	}
}

func TestAlreadySatisfiedPackageStaysUnchangedAndSaysSo(t *testing.T) {
	sch := targetSchema(t, []string{teamDef + " X-ORIGIN 'user defined'"}, []string{staffDef})
	entries := append(baseEntries(t), entry(t, managerDN, "objectClass", "person", "cn", "B", "sn", "B"),
		entry(t, patDN, "objectClass", "top", "objectClass", "person", "objectClass", "alderStaff", "uid", "pat", "cn", "Pat", "sn", "Doe",
			"alderTeam", "platform", "manager", managerDN))
	target := newTarget(t, sch, entries...)
	p := mixed(t)
	before := p.Checksum
	r := preflightPackage(t, p, target)
	present := one(t, r, CodeDefinitionPresent, byItem("c1"))
	if present.Classification != AlreadySatisfied {
		t.Errorf("an X-ORIGIN-only difference should be already satisfied: %+v", present)
	}
	one(t, r, CodeDefinitionPresent, byItem("c2"))
	one(t, r, CodeChangeSatisfied, byItem("c3"))
	if p.Checksum != before {
		t.Fatal("the package changed during a preflight")
	}
}

func TestMissingSyntaxAndMatchingRuleAreUnsupported(t *testing.T) {
	target := withManager(t)
	p := buildPackage(t,
		schemaChange("c1", changepkg.ElementAttributeType, changepkg.SchemaAdd, "1.3.6.1.4.1.99999.40.7",
			"( 1.3.6.1.4.1.99999.40.7 NAME 'alderPhoto' SYNTAX 1.3.6.1.4.1.1466.115.121.1.28 )"),
		schemaChange("c2", changepkg.ElementAttributeType, changepkg.SchemaAdd, "1.3.6.1.4.1.99999.40.8",
			"( 1.3.6.1.4.1.99999.40.8 NAME 'alderCode' EQUALITY caseExactIA5Match SYNTAX "+syntaxString+" )"),
		schemaChange("c3", changepkg.ElementAttributeType, changepkg.SchemaAdd, "1.3.6.1.4.1.99999.40.9",
			"( 1.3.6.1.4.1.99999.40.9 NAME 'alderLabel' ORDERING caseIgnoreOrderingMatch SYNTAX "+syntaxString+" )"),
	)
	r := preflightPackage(t, p, target)
	if f := one(t, r, CodeSyntaxUnavailable, byItem("c1")); f.Classification != Unsupported || !f.BlocksPortability {
		t.Errorf("missing syntax: %+v", f)
	}
	one(t, r, CodeMatchingRuleUnavailable, byItem("c2"))
	one(t, r, CodeMatchingRuleUnavailable, byItem("c3"))
	if r.Overall != NotCompatible {
		t.Errorf("overall %s", r.Overall)
	}
}

func TestATargetThatPublishesNoMatchingRulesIsUnknownNotIncompatible(t *testing.T) {
	sch := targetSchema(t, nil, nil)
	sch.MatchingRules = nil
	target := newTarget(t, sch, baseEntries(t)...)
	p := buildPackage(t, schemaChange("c1", changepkg.ElementAttributeType, changepkg.SchemaAdd, teamOID, teamDef))
	r := preflightPackage(t, p, target)
	one(t, r, CodeMatchingRuleUnknown, byItem("c1"))
	if r.Overall != Incomplete || r.Complete {
		t.Errorf("overall %s complete %v, want incomplete", r.Overall, r.Complete)
	}
}

func TestAReadOnlySchemaMakesDefinitionsUnsupportedOnlyWhenTheSourceNeedsOne(t *testing.T) {
	// Needs a schema write, and cannot have one.
	target := withManager(t)
	target.caps.SchemaWrite = directory.SchemaWrite{Style: directory.SchemaStyleNone, Unavailable: "this bind may not write the schema"}
	r := preflightPackage(t, buildPackage(t, schemaChange("c1", changepkg.ElementAttributeType, changepkg.SchemaAdd, teamOID, teamDef)), target)
	f := one(t, r, CodeSchemaNotWritable, byItem("c1"))
	if f.Classification != Unsupported {
		t.Errorf("classification %s", f.Classification)
	}
	if len(r.Capabilities) != 1 || r.Capabilities[0].Capability != "schema_write" || r.Capabilities[0].Available {
		t.Errorf("capabilities %+v", r.Capabilities)
	}

	// The same target, a package with no schema: no schema finding, and no
	// capability listed at all.
	r = preflightPackage(t, buildPackage(t, addEntry("c1", "uid=kim,ou=people,dc=alder,dc=test",
		attr("objectClass", "top", "person"), attr("cn", "Kim"), attr("sn", "K"))), target)
	if len(find(r, CodeSchemaNotWritable, nil)) != 0 || len(r.Capabilities) != 0 {
		t.Fatalf("a target was penalised for a capability the source does not need\n%s", describe(r))
	}
	if r.Overall != Compatible {
		t.Errorf("overall %s", r.Overall)
	}
}

func TestSameNameDifferentOIDIsANameConflictNotAMatch(t *testing.T) {
	target := withManager(t)
	// description exists on the target as 2.5.4.13; this is something else.
	p := buildPackage(t, schemaChange("c1", changepkg.ElementAttributeType, changepkg.SchemaAdd, "1.3.6.1.4.1.99999.40.20",
		"( 1.3.6.1.4.1.99999.40.20 NAME 'description' EQUALITY caseIgnoreMatch SYNTAX "+syntaxString+" )"))
	r := preflightPackage(t, p, target)
	f := one(t, r, CodeNameConflict, byItem("c1"))
	if f.Classification != Incompatible || f.Source.OID != "1.3.6.1.4.1.99999.40.20" {
		t.Errorf("%+v", f)
	}
}

func TestAnEntryIsJudgedAgainstTheSchemaThePackageLeaves(t *testing.T) {
	target := withManager(t)
	p := mixed(t)
	r := preflightPackage(t, p, target)
	if len(find(r, CodeObjectClassMissing, nil)) != 0 || len(find(r, CodeAttributeMissing, nil)) != 0 {
		t.Fatalf("the entry was judged against the schema before the package\n%s", describe(r))
	}

	// Without the package's schema, the entry's class and attribute are
	// missing prerequisites.
	alone := p.Changes[2]
	alone.DependsOn = nil
	r = preflightPackage(t, buildPackage(t, alone), target)
	one(t, r, CodeObjectClassMissing, byItem("c3"))
	one(t, r, CodeAttributeMissing, byItem("c3"))
	if r.Overall != CompatibleWithPrerequisite {
		t.Errorf("overall %s\n%s", r.Overall, describe(r))
	}
}

func TestContentTheTargetSchemaRefusesIsIncompatible(t *testing.T) {
	target := withManager(t)
	cases := map[string]struct {
		attrs []changepkg.Attribute
		code  string
	}{
		"missing MUST": {[]changepkg.Attribute{attr("objectClass", "top", "person"), attr("cn", "Kim")}, CodeMissingRequired},
		"SINGLE-VALUE": {[]changepkg.Attribute{attr("objectClass", "top", "person"), attr("cn", "Kim"), attr("sn", "K"),
			attr("alderEmployeeNumber", "1", "2")}, CodeSingleValueViolation},
		"not allowed": {[]changepkg.Attribute{attr("objectClass", "top", "organizationalUnit"), attr("ou", "x"), attr("sn", "K")}, CodeAttributeNotAllowed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := preflightPackage(t, buildPackage(t, addEntry("c1", "uid=kim,ou=people,dc=alder,dc=test", tc.attrs...)), target)
			f := one(t, r, tc.code, byItem("c1"))
			if f.Classification != Incompatible || !f.BlocksPortability {
				t.Errorf("%+v", f)
			}
		})
	}
}

func TestAServerOwnedAttributeInAPackageIsUnsupported(t *testing.T) {
	target := withManager(t)
	r := preflightPackage(t, buildPackage(t, addEntry("c1", "uid=kim,ou=people,dc=alder,dc=test",
		attr("objectClass", "top", "person"), attr("cn", "Kim"), attr("sn", "K"), attr("createTimestamp", "20260101000000Z"))), target)
	f := one(t, r, CodeServerOwned, nil)
	if f.Classification != Unsupported {
		t.Errorf("%+v", f)
	}
}

func TestNamingContextMismatchIsReportedAndNeverRewritten(t *testing.T) {
	target := withManager(t)
	source := "uid=alice,ou=people,dc=dev,dc=alder,dc=example"
	p := buildPackage(t, addEntry("c1", source, attr("objectClass", "top", "person"), attr("cn", "Alice"), attr("sn", "A")))
	r := preflightPackage(t, p, target)
	f := one(t, r, CodeNamingContextMismatch, byItem("c1"))
	if f.Source.DN != source || f.Classification != Incompatible {
		t.Errorf("%+v", f)
	}
	for _, f := range r.Findings {
		for _, pre := range f.Prerequisites {
			if strings.Contains(strings.ToLower(pre.DN), "dc=alder,dc=test") && strings.Contains(strings.ToLower(pre.DN), "alice") {
				t.Fatalf("a finding proposes a rewritten DN: %+v", f)
			}
		}
		if f.Source.DN != "" && f.Source.DN != source {
			t.Fatalf("a finding names a DN the source does not hold: %+v", f)
		}
	}
	if p.Changes[0].Data.DN != source {
		t.Fatal("the package was changed")
	}
}

func TestReferencesAreReadyProvidedMissingOrUnknown(t *testing.T) {
	target := withManager(t)
	hiddenDN := "uid=secret,ou=people,dc=alder,dc=test"
	target.hidden[strings.ToLower(hiddenDN)] = true
	p := buildPackage(t,
		addEntry("c1", "uid=kim,ou=people,dc=alder,dc=test", attr("objectClass", "top", "person"), attr("cn", "Kim"), attr("sn", "K")),
		addEntry("c2", "cn=team,ou=groups,dc=alder,dc=test", attr("objectClass", "top", "groupOfNames"), attr("cn", "team"),
			attr("member", managerDN, "uid=kim,ou=people,dc=alder,dc=test", "uid=ghost,ou=people,dc=alder,dc=test", hiddenDN,
				"uid=far,dc=elsewhere,dc=example")),
	)
	r := preflightPackage(t, p, target)
	if f := one(t, r, CodeReferenceReady, byItem("c2")); f.Count != 1 {
		t.Errorf("ready %+v", f)
	}
	if f := one(t, r, CodeReferenceProvided, byItem("c2")); f.Count != 1 {
		t.Errorf("provided %+v", f)
	}
	missing := one(t, r, CodeReferenceMissing, byItem("c2"))
	if missing.Classification != PrerequisiteRequired || missing.Source.Value != "uid=ghost,ou=people,dc=alder,dc=test" {
		t.Errorf("missing %+v", missing)
	}
	unknown := one(t, r, CodeReferenceUnknown, byItem("c2"))
	if unknown.Classification != Unknown || unknown.Source.Value != hiddenDN || unknown.Target.Fact != "hidden" {
		t.Errorf("a hidden reference must be unknown, not missing: %+v", unknown)
	}
	one(t, r, CodeReferenceOutside, byItem("c2"))
	if r.Overall != NotCompatible {
		t.Errorf("overall %s", r.Overall)
	}
}

func TestAHiddenParentIsUnknownAndTheReportIsNotComplete(t *testing.T) {
	target := newTarget(t, targetSchema(t, nil, nil), baseEntries(t)[0])
	target.hidden[strings.ToLower(peopleDN)] = true
	r := preflightPackage(t, buildPackage(t, addEntry("c1", "uid=kim,ou=people,dc=alder,dc=test",
		attr("objectClass", "top", "person"), attr("cn", "Kim"), attr("sn", "K"))), target)
	f := one(t, r, CodeParentUnknown, byItem("c1"))
	if f.Classification != Unknown {
		t.Errorf("%+v", f)
	}
	if len(find(r, CodeParentMissing, nil)) != 0 {
		t.Fatal("a hidden parent was reported missing")
	}
	if r.Overall != Incomplete || r.Complete {
		t.Errorf("overall %s complete %v", r.Overall, r.Complete)
	}

	// Absent, not hidden: a prerequisite.
	target = newTarget(t, targetSchema(t, nil, nil), baseEntries(t)[0])
	r = preflightPackage(t, buildPackage(t, addEntry("c1", "uid=kim,ou=people,dc=alder,dc=test",
		attr("objectClass", "top", "person"), attr("cn", "Kim"), attr("sn", "K"))), target)
	if f := one(t, r, CodeParentMissing, byItem("c1")); f.Prerequisites[0].DN != peopleDN {
		t.Errorf("%+v", f)
	}
	if r.Overall != CompatibleWithPrerequisite {
		t.Errorf("overall %s", r.Overall)
	}
}

func TestAnExistingDifferentEntryIsAFactNotAVerdict(t *testing.T) {
	target := newTarget(t, targetSchema(t, nil, nil), append(baseEntries(t),
		entry(t, "uid=kim,ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "cn", "Kimberly", "sn", "K"))...)
	r := preflightPackage(t, buildPackage(t, addEntry("c1", "uid=kim,ou=people,dc=alder,dc=test",
		attr("objectClass", "top", "person"), attr("cn", "Kim"), attr("sn", "K"))), target)
	f := one(t, r, CodeEntryDiffers, byItem("c1"))
	if f.Classification != PrerequisiteRequired || !f.ManualAction || !f.BlocksPlan || f.BlocksPortability {
		t.Errorf("%+v", f)
	}
}

func TestOmittedSecretsAreReportedAsNotMigratable(t *testing.T) {
	target := withManager(t)
	p := buildPackage(t, addEntry("c1", "uid=kim,ou=people,dc=alder,dc=test", attr("objectClass", "top", "person"), attr("cn", "Kim"), attr("sn", "K")))
	p.Omitted = []changepkg.Omitted{{Subject: "uid=kim,ou=people,dc=alder,dc=test", Kind: "setpassword", Reason: changepkg.OmittedSecret}}
	r := preflightPackage(t, p, target)
	f := one(t, r, CodeSensitiveNotMigratable, nil)
	if f.Classification != Excluded || !f.ManualAction {
		t.Errorf("%+v", f)
	}
	if r.Overall != CompatibleWithPrerequisite {
		t.Errorf("a secret to set separately is work to do: overall %s", r.Overall)
	}
}

func TestVendorMismatchAloneDecidesNothing(t *testing.T) {
	target := withManager(t)
	p := mixed(t)
	p.Source.Vendor = "Some Other Directory"
	r := preflightPackage(t, p, target)
	if !r.Target.CrossVendor || r.Overall != Compatible {
		t.Fatalf("cross vendor %v overall %s\n%s", r.Target.CrossVendor, r.Overall, describe(r))
	}
}
