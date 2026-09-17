package preflight

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// --- schema snapshots --------------------------------------------------------

func schemaSnapshotOf(t testing.TB, vendor string, sch *schema.Schema) *snapshot.SchemaSnapshot {
	t.Helper()
	s, err := snapshot.BuildSchema(snapshot.SchemaCapture{Vendor: vendor, SubschemaEntry: "cn=schema",
		CreatedAt: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)}, sch)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func preflightSchema(t testing.TB, s *snapshot.SchemaSnapshot, target *fakeTarget) *Report {
	t.Helper()
	r, err := SchemaSnapshot(context.Background(), s, "verified", target, Options{NotFound: notFound, Now: fixedClock})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func byOID(oid string) func(Finding) bool {
	return func(f Finding) bool { return f.Source.OID == oid }
}

func userDefined(def string) string {
	return strings.TrimSuffix(def, ")") + "X-ORIGIN 'user defined' )"
}

func TestSchemaSnapshotPreflight(t *testing.T) {
	const (
		dupOID      = "1.3.6.1.4.1.99999.50.1"
		conflictOID = "1.3.6.1.4.1.99999.50.2"
		newOID      = "1.3.6.1.4.1.99999.50.3"
		classOID    = "1.3.6.1.4.1.99999.50.4"
		noSupOID    = "1.3.6.1.4.1.99999.50.5"
		syntaxOIDx  = "1.3.6.1.4.1.99999.50.6"
		ruleOID     = "1.3.6.1.4.1.99999.50.7"
		extOID      = "1.3.6.1.4.1.99999.50.8"
		builtinOID  = "1.3.6.1.4.1.99999.50.9"
	)
	source := targetSchema(t, []string{
		userDefined("( " + dupOID + " NAME 'alderDup' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " )"),
		userDefined("( " + conflictOID + " NAME 'alderConflict' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " SINGLE-VALUE )"),
		userDefined("( " + newOID + " NAME 'alderNew' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " )"),
		userDefined("( " + noSupOID + " NAME 'alderNoSup' SUP alderNowhere )"),
		userDefined("( " + syntaxOIDx + " NAME 'alderJpeg' SYNTAX 1.3.6.1.4.1.1466.115.121.1.28 )"),
		userDefined("( " + ruleOID + " NAME 'alderIA5' EQUALITY caseExactIA5Match SYNTAX " + syntaxString + " )"),
		"( " + extOID + " NAME 'alderOrdered' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " X-ORIGIN 'user defined' X-ORDERED 'VALUES' )",
		"( " + builtinOID + " NAME 'vendorOnly' SYNTAX " + syntaxString + " X-ORIGIN 'Vendor Server' )",
	}, []string{
		userDefined("( " + classOID + " NAME 'alderNewClass' SUP top AUXILIARY MUST alderNew )"),
	})
	snap := schemaSnapshotOf(t, "Source Directory", source)

	target := newTarget(t, targetSchema(t, []string{
		"( " + dupOID + " NAME 'alderDup' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " X-ORIGIN 'some file' )",
		"( " + conflictOID + " NAME 'alderConflict' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " )",
		"( " + extOID + " NAME 'alderOrdered' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " )",
	}, nil), baseEntries(t)...)
	r := preflightSchema(t, snap, target)

	if f := one(t, r, CodeDefinitionPresent, byOID(dupOID)); f.Classification != AlreadySatisfied {
		t.Errorf("present: %+v", f)
	}
	if f := one(t, r, CodeDefinitionConflict, byOID(conflictOID)); f.Classification != Incompatible || !strings.Contains(f.Target.Detail, "singleValue") {
		t.Errorf("conflict: %+v", f)
	}
	one(t, r, CodeDefinitionPortable, byOID(newOID))
	class := one(t, r, CodeDefinitionPortable, byOID(classOID))
	if len(class.Prerequisites) != 1 || class.Prerequisites[0].OID != newOID {
		t.Errorf("class prerequisites: %+v", class.Prerequisites)
	}
	if f := one(t, r, CodeUndefinedReference, byOID(noSupOID)); f.Classification != PrerequisiteRequired || !f.ManualAction {
		t.Errorf("missing SUP: %+v", f)
	}
	one(t, r, CodeSyntaxUnavailable, byOID(syntaxOIDx))
	one(t, r, CodeMatchingRuleUnavailable, byOID(ruleOID))
	if f := one(t, r, CodeDefinitionExtensionsDiffer, byOID(extOID)); f.Classification != AlreadySatisfied || !strings.Contains(f.Target.Detail, "X-ORDERED") {
		t.Errorf("a behavioural extension difference must be reported separately: %+v", f)
	}
	if f := one(t, r, CodeSourceServerDefined, byOID(builtinOID)); f.Classification != Excluded || f.BlocksPortability {
		t.Errorf("server defined: %+v", f)
	}
	// Definitions both sides hold identically -- the standard ones -- are
	// already satisfied, and the target's own extra definitions are not
	// findings at all.
	one(t, r, CodeDefinitionPresent, byOID("2.5.4.3"))
	if r.Overall != NotCompatible {
		t.Errorf("overall %s", r.Overall)
	}
	if target.writes != 0 {
		t.Fatal("a schema preflight wrote")
	}
}

func TestSchemaSnapshotAgainstAReadOnlySchema(t *testing.T) {
	source := targetSchema(t, []string{userDefined("( 1.3.6.1.4.1.99999.51.1 NAME 'alderRO' SYNTAX " + syntaxString + " )")}, nil)
	target := newTarget(t, targetSchema(t, nil, nil), baseEntries(t)...)
	target.caps.SchemaWrite = directory.SchemaWrite{Style: directory.SchemaStyleNone, Unavailable: "the schema is not writable"}
	r := preflightSchema(t, schemaSnapshotOf(t, "Source", source), target)
	one(t, r, CodeSchemaNotWritable, byOID("1.3.6.1.4.1.99999.51.1"))

	// Nothing to add: a read-only schema is no finding.
	r = preflightSchema(t, schemaSnapshotOf(t, "Source", targetSchema(t, nil, nil)), target)
	if len(find(r, CodeSchemaNotWritable, nil)) != 0 || len(r.Capabilities) != 0 || r.Overall != Compatible {
		t.Fatalf("a read-only schema was held against a source that adds nothing\n%s", describe(r))
	}
}

func TestSchemaSnapshotWithoutPublishedSyntaxesIsIncomplete(t *testing.T) {
	source := targetSchema(t, []string{userDefined("( 1.3.6.1.4.1.99999.52.1 NAME 'alderX' SYNTAX " + syntaxString + " )")}, nil)
	sch := targetSchema(t, nil, nil)
	sch.Syntaxes = nil
	target := newTarget(t, sch, baseEntries(t)...)
	r := preflightSchema(t, schemaSnapshotOf(t, "Source", source), target)
	one(t, r, CodeSyntaxUnknown, byOID("1.3.6.1.4.1.99999.52.1"))
	if r.Overall != Incomplete {
		t.Errorf("overall %s", r.Overall)
	}
}

// --- data snapshots ----------------------------------------------------------

func dataSnapshotOf(t testing.TB, sch *schema.Schema, base string, operational bool, entries ...*directory.Entry) *snapshot.Snapshot {
	t.Helper()
	s, err := snapshot.Build(snapshot.Capture{Base: mustDN(t, base), Scope: "sub", Filter: "(objectClass=*)", Vendor: "Source Directory",
		Operational: operational, CreatedAt: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)}, sch, entries)
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip, so the snapshot is exactly what a file would hold.
	var buf strings.Builder
	if err := snapshot.Encode(&buf, s); err != nil {
		t.Fatal(err)
	}
	decoded, _, err := snapshot.Decode([]byte(buf.String()))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func captureFrom(target *fakeTarget) func(ctx context.Context, base dn.DN, scope, filter string) (*snapshot.Snapshot, bool, error) {
	return func(ctx context.Context, base dn.DN, scope, filter string) (*snapshot.Snapshot, bool, error) {
		var entries []*directory.Entry
		for key, e := range target.entries {
			if target.hidden[key] {
				continue
			}
			if e.DN.HasSuffix(base) {
				entries = append(entries, e)
			}
		}
		target.reads++
		s, err := snapshot.Build(snapshot.Capture{Base: base, Scope: scope, Filter: filter, Vendor: target.caps.VendorName,
			CreatedAt: time.Unix(0, 0)}, target.sch, entries)
		return s, false, err
	}
}

func preflightData(t testing.TB, s *snapshot.Snapshot, target *fakeTarget) *Report {
	t.Helper()
	r, err := DataSnapshot(context.Background(), s, "verified", target, Options{NotFound: notFound, Now: fixedClock, Capture: captureFrom(target)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func byDN(d string) func(Finding) bool {
	return func(f Finding) bool { return strings.EqualFold(f.Source.DN, d) }
}

func TestDataSnapshotPreflight(t *testing.T) {
	// The source knows a class the target does not, and is laxer about one
	// attribute.
	sourceSchema := targetSchema(t, []string{
		"( 1.3.6.1.4.1.99999.60.1 NAME 'alderBadge' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " )",
	}, []string{
		"( 1.3.6.1.4.1.99999.60.2 NAME 'alderBadged' SUP top AUXILIARY MAY alderBadge )",
	})

	hiddenDN := "uid=hidden,ou=people,dc=alder,dc=test"
	target := newTarget(t, targetSchema(t, nil, nil), append(baseEntries(t),
		entry(t, "uid=same,ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "cn", "Same", "sn", "S"),
		entry(t, "uid=diff,ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "cn", "Old name", "sn", "D"),
		entry(t, "uid=targetonly,ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "cn", "T", "sn", "T"),
		entry(t, managerDN, "objectClass", "top", "objectClass", "person", "cn", "Boss", "sn", "B"),
		entry(t, hiddenDN, "objectClass", "top", "objectClass", "person", "cn", "H", "sn", "H"),
	)...)
	target.hidden[strings.ToLower(hiddenDN)] = true

	source := dataSnapshotOf(t, sourceSchema, "dc=alder,dc=test", true,
		entry(t, "dc=alder,dc=test", "objectClass", "top", "objectClass", "dcObject", "dc", "alder"),
		entry(t, "ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "organizationalUnit", "ou", "people"),
		entry(t, "uid=same,ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "cn", "Same", "sn", "S"),
		entry(t, "uid=diff,ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "cn", "New name", "sn", "D"),
		entry(t, "uid=new,ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "cn", "New", "sn", "N",
			"userPassword", "{SSHA}notARealHash", "createTimestamp", "20260101000000Z", "entryUUID", "0b0e0c0d-0000-4000-8000-000000000001",
			"manager", managerDN),
		entry(t, "ou=fresh,dc=alder,dc=test", "objectClass", "top", "objectClass", "organizationalUnit", "ou", "fresh"),
		entry(t, "uid=child,ou=fresh,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "cn", "Child", "sn", "C"),
		entry(t, "uid=orphan,ou=gone,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "cn", "Orphan", "sn", "O"),
		entry(t, "uid=badged,ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "objectClass", "alderBadged",
			"cn", "Badged", "sn", "B", "alderBadge", "gold"),
		entry(t, "cn=group,ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "groupOfNames", "cn", "group",
			"member", managerDN, "member", "uid=ghost,ou=people,dc=alder,dc=test", "member", hiddenDN, "member", "uid=new,ou=people,dc=alder,dc=test"),
	)
	r := preflightData(t, source, target)

	one(t, r, CodeEntryPresent, byDN("uid=same,ou=people,dc=alder,dc=test"))
	if f := one(t, r, CodeEntryDiffers, byDN("uid=diff,ou=people,dc=alder,dc=test")); f.Target == nil || !strings.Contains(f.Target.Detail, "cn") {
		t.Errorf("differs should name the attribute: %+v", f)
	}
	one(t, r, CodeEntryPortable, byDN("uid=new,ou=people,dc=alder,dc=test"))
	one(t, r, CodeEntryPortable, byDN("ou=fresh,dc=alder,dc=test"))
	one(t, r, CodeEntryPortable, byDN("uid=child,ou=fresh,dc=alder,dc=test"))
	if f := one(t, r, CodeParentMissing, byDN("uid=orphan,ou=gone,dc=alder,dc=test")); f.Classification != PrerequisiteRequired {
		t.Errorf("%+v", f)
	}
	if f := one(t, r, CodeObjectClassMissing, byDN("uid=badged,ou=people,dc=alder,dc=test")); f.Classification != PrerequisiteRequired {
		t.Errorf("%+v", f)
	}
	sensitive := one(t, r, CodeSensitiveNotMigratable, nil)
	if sensitive.Source.Attribute != "userPassword" || !sensitive.ManualAction {
		t.Errorf("sensitive: %+v", sensitive)
	}
	if f := one(t, r, CodeServerOwned, func(f Finding) bool { return f.Source.Attribute == "createTimestamp" }); f.Classification != Excluded {
		t.Errorf("operational: %+v", f)
	}
	if f := one(t, r, CodeTargetGenerated, nil); f.Count != 1 {
		t.Errorf("identity: %+v", f)
	}
	one(t, r, CodeReferenceReady, byDN("uid=new,ou=people,dc=alder,dc=test"))
	group := byDN("cn=group,ou=people,dc=alder,dc=test")
	if f := one(t, r, CodeReferenceMissing, group); f.Source.Value != "uid=ghost,ou=people,dc=alder,dc=test" {
		t.Errorf("missing: %+v", f)
	}
	if f := one(t, r, CodeReferenceUnknown, group); f.Source.Value != hiddenDN {
		t.Errorf("a hidden member must be unknown: %+v", f)
	}
	one(t, r, CodeReferenceProvided, group)
	one(t, r, CodeReferenceReady, group)

	// A target-only entry is not a finding, and nothing proposes a deletion.
	if len(find(r, "", byDN("uid=targetonly,ou=people,dc=alder,dc=test"))) != 0 {
		t.Fatal("a target-only entry produced a finding")
	}
	for _, f := range r.Findings {
		if strings.EqualFold(f.Source.DN, "uid=targetonly,ou=people,dc=alder,dc=test") {
			t.Fatalf("a target-only entry produced a finding: %+v", f)
		}
	}
	if r.Overall != Incomplete {
		t.Errorf("overall %s (a hidden member keeps it from being complete)", r.Overall)
	}
	if target.writes != 0 {
		t.Fatal("a data preflight wrote")
	}
}

func TestDataSnapshotContentTheTargetRefuses(t *testing.T) {
	lax := targetSchema(t, []string{
		"( 1.3.6.1.4.1.99999.1.9 NAME 'alderEmployeeNumber' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " )",
	}, []string{
		"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST cn MAY ( sn $ userPassword $ description $ uid $ manager $ alderEmployeeNumber ) )",
	})
	lax = replaceDefinitions(t, lax)
	target := newTarget(t, targetSchema(t, nil, nil), baseEntries(t)...)
	source := dataSnapshotOf(t, lax, "ou=people,dc=alder,dc=test", false,
		entry(t, "uid=twonumbers,ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "cn", "Two", "sn", "T",
			"alderEmployeeNumber", "1", "alderEmployeeNumber", "2"),
		entry(t, "uid=nosurname,ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "person", "cn", "Nobody"),
	)
	r := preflightData(t, source, target)
	one(t, r, CodeSingleValueViolation, byDN("uid=twonumbers,ou=people,dc=alder,dc=test"))
	one(t, r, CodeMissingRequired, byDN("uid=nosurname,ou=people,dc=alder,dc=test"))
	if r.Overall != NotCompatible {
		t.Errorf("overall %s", r.Overall)
	}
}

func TestDataSnapshotOutsideTheTargetNamingContexts(t *testing.T) {
	sch := targetSchema(t, nil, nil)
	target := newTarget(t, sch, baseEntries(t)...)
	source := dataSnapshotOf(t, sch, "dc=dev,dc=example", false,
		entry(t, "dc=dev,dc=example", "objectClass", "top", "objectClass", "dcObject", "dc", "dev"),
		entry(t, "uid=alice,dc=dev,dc=example", "objectClass", "top", "objectClass", "person", "cn", "Alice", "sn", "A"),
	)
	r := preflightData(t, source, target)
	if got := find(r, CodeNamingContextMismatch, nil); len(got) != 2 {
		t.Fatalf("want both entries outside the target's naming contexts\n%s", describe(r))
	}
	for _, f := range r.Findings {
		if strings.Contains(strings.ToLower(f.Source.DN), "dc=alder,dc=test") {
			t.Fatalf("a DN was rewritten toward the target: %+v", f)
		}
	}
}

// --- helpers ----------------------------------------------------------------

// replaceDefinitions keeps the last definition of each OID, so a fixture can
// override one of the base schema's.
func replaceDefinitions(t testing.TB, s *schema.Schema) *schema.Schema {
	t.Helper()
	lastAT := map[string]string{}
	var atOrder []string
	for _, at := range s.AttributeTypes {
		key := strings.ToLower(at.OID)
		if _, seen := lastAT[key]; !seen {
			atOrder = append(atOrder, key)
		}
		lastAT[key] = at.Raw
	}
	lastOC := map[string]string{}
	var ocOrder []string
	for _, oc := range s.ObjectClasses {
		key := strings.ToLower(oc.OID)
		if _, seen := lastOC[key]; !seen {
			ocOrder = append(ocOrder, key)
		}
		lastOC[key] = oc.Raw
	}
	attrs := map[string][]string{}
	for _, k := range atOrder {
		attrs[schema.AttrAttributeTypes] = append(attrs[schema.AttrAttributeTypes], lastAT[k])
	}
	for _, k := range ocOrder {
		attrs[schema.AttrObjectClasses] = append(attrs[schema.AttrObjectClasses], lastOC[k])
	}
	for _, sy := range s.Syntaxes {
		attrs[schema.AttrLDAPSyntaxes] = append(attrs[schema.AttrLDAPSyntaxes], sy.Raw)
	}
	for _, mr := range s.MatchingRules {
		attrs[schema.AttrMatchingRules] = append(attrs[schema.AttrMatchingRules], mr.Raw)
	}
	return schema.Load(s.DN, attrs)
}
