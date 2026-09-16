package diff

import (
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

var schemaTime = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

// base is a small schema with the references a comparison has to resolve.
var baseATs = []string{
	"( 2.5.4.0 NAME 'objectClass' EQUALITY objectIdentifierMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
	"( 2.5.4.41 NAME 'name' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
	"( 2.5.4.3 NAME ( 'cn' 'commonName' ) SUP name )",
	"( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )",
}

var baseOCs = []string{
	"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
	"( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP top AUXILIARY MUST alderTeam MAY ( cn ) )",
}

var baseRules = map[string][]string{
	schema.AttrMatchingRules: {
		"( 2.5.13.2 NAME 'caseIgnoreMatch' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.13.0 NAME 'objectIdentifierMatch' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
		"( 2.5.13.5 NAME 'caseExactMatch' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
	},
}

func schemaSnap(t *testing.T, vendor string, ats, ocs []string) *snapshot.SchemaSnapshot {
	t.Helper()
	attrs := map[string][]string{schema.AttrAttributeTypes: ats, schema.AttrObjectClasses: ocs}
	for k, v := range baseRules {
		attrs[k] = v
	}
	s, err := snapshot.BuildSchema(snapshot.SchemaCapture{Vendor: vendor, SubschemaEntry: "cn=schema", CreatedAt: schemaTime},
		schema.Load("cn=schema", attrs))
	if err != nil {
		t.Fatalf("BuildSchema: %v", err)
	}
	return s
}

func replaceDef(list []string, oidPrefix, with string) []string {
	out := append([]string{}, list...)
	for i, d := range out {
		if strings.HasPrefix(d, "( "+oidPrefix+" ") {
			out[i] = with
		}
	}
	return out
}

func compareSchemas(t *testing.T, src, tgt *snapshot.SchemaSnapshot, live bool) *SchemaResult {
	t.Helper()
	return CompareSchema(SchemaSide{Snapshot: src, Live: live}, SchemaSide{Snapshot: tgt}, SchemaOptions{})
}

func onlyItem(t *testing.T, r *SchemaResult) SchemaItem {
	t.Helper()
	if len(r.Items) != 1 {
		t.Fatalf("items %+v, want exactly one", r.Items)
	}
	return r.Items[0]
}

func fieldNames(it SchemaItem) string {
	var out []string
	for _, f := range it.Fields {
		out = append(out, f.Field+"/"+f.Category)
	}
	return strings.Join(out, ",")
}

func TestEqualSchemasHaveNoDifferences(t *testing.T) {
	a := schemaSnap(t, "A", baseATs, baseOCs)
	r := compareSchemas(t, a, schemaSnap(t, "A", baseATs, baseOCs), false)
	if len(r.Items) != 0 || !r.Complete || r.AttributeTypes.Unchanged != 4 || r.ObjectClasses.Unchanged != 2 {
		t.Errorf("result %+v", r)
	}
}

func TestRepresentationDoesNotMatter(t *testing.T) {
	a := schemaSnap(t, "A", baseATs, baseOCs)
	ats := replaceDef(baseATs, "2.5.4.3", "(   2.5.4.3   NAME ( 'commonName'   'cn' )   SUP   2.5.4.41 )")
	ats = replaceDef(ats, "1.3.6.1.4.1.99999.1.1",
		"( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' EQUALITY 2.5.13.2 SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )")
	ocs := replaceDef(baseOCs, "1.3.6.1.4.1.99999.2.1",
		"( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP 2.5.6.0 AUXILIARY MUST ( 1.3.6.1.4.1.99999.1.1 ) MAY ( commonName ) )")
	r := compareSchemas(t, a, schemaSnap(t, "A", ats, ocs), false)
	if len(r.Items) != 0 {
		t.Errorf("equivalent definitions differ: %+v", r.Items)
	}
}

func TestSemanticChangesAreModifications(t *testing.T) {
	team := "1.3.6.1.4.1.99999.1.1"
	emp := "1.3.6.1.4.1.99999.2.1"
	cases := []struct {
		name   string
		ats    []string
		ocs    []string
		fields string
	}{
		{"syntax", replaceDef(baseATs, team, "( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.27 SINGLE-VALUE )"), baseOCs, "syntax/core"},
		{"equality", replaceDef(baseATs, team, "( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' EQUALITY caseExactMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )"), baseOCs, "equality/core"},
		{"sup", replaceDef(baseATs, team, "( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' SUP name EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )"), baseOCs, "sup/core"},
		{"single-value", replaceDef(baseATs, team, "( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"), baseOCs, "singleValue/core"},
		{"obsolete", replaceDef(baseATs, team, "( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' OBSOLETE EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )"), baseOCs, "obsolete/core"},
		{"description", replaceDef(baseATs, team, "( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'The team' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )"), baseOCs, "desc/description"},
		{"renamed, same OID", replaceDef(baseATs, team, "( 1.3.6.1.4.1.99999.1.1 NAME 'alderSquad' DESC 'Team' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )"),
			replaceDef(baseOCs, emp, "( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP top AUXILIARY MUST alderSquad MAY ( cn ) )"), "names/core"},
		{"object class kind", baseATs, replaceDef(baseOCs, emp, "( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP top STRUCTURAL MUST alderTeam MAY ( cn ) )"), "kind/core"},
		{"MUST gains", baseATs, replaceDef(baseOCs, emp, "( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP top AUXILIARY MUST ( alderTeam $ cn ) )"), "must/core,may/core"},
		{"MAY loses", baseATs, replaceDef(baseOCs, emp, "( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP top AUXILIARY MUST alderTeam )"), "may/core"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := compareSchemas(t, schemaSnap(t, "A", baseATs, baseOCs), schemaSnap(t, "A", tc.ats, tc.ocs), false)
			it := onlyItem(t, r)
			if it.Kind != Modified || fieldNames(it) != tc.fields {
				t.Errorf("kind %s fields %s, want modified %s", it.Kind, fieldNames(it), tc.fields)
			}
		})
	}
}

func TestExtensionsAloneAreMetadata(t *testing.T) {
	ats := replaceDef(baseATs, "1.3.6.1.4.1.99999.1.1",
		"( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE X-ORIGIN ( 'Alder test harness' 'user defined' ) )")
	r := compareSchemas(t, schemaSnap(t, "OpenLDAP", baseATs, baseOCs), schemaSnap(t, "389 Project", ats, baseOCs), false)
	it := onlyItem(t, r)
	if it.Kind != MetadataOnly || fieldNames(it) != "X-ORIGIN/extension" || !r.CrossVendor {
		t.Errorf("kind %s fields %s crossVendor %v", it.Kind, fieldNames(it), r.CrossVendor)
	}
	if r.AttributeTypes.MetadataOnly != 1 || r.AttributeTypes.Modified != 0 {
		t.Errorf("counts %+v", r.AttributeTypes)
	}
}

func TestIdentityIsTheOIDAndNothingElse(t *testing.T) {
	// A different OID under the same name is a different element: removed and
	// added, never paired.
	ats := replaceDef(baseATs, "1.3.6.1.4.1.99999.1.1",
		"( 1.3.6.1.4.1.99999.1.2 NAME 'alderTeam' DESC 'Team' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )")
	r := compareSchemas(t, schemaSnap(t, "A", baseATs, nil), schemaSnap(t, "A", ats, nil), false)
	kinds := map[string]Kind{}
	for _, it := range r.Items {
		kinds[it.OID] = it.Kind
	}
	if kinds["1.3.6.1.4.1.99999.1.1"] != Removed || kinds["1.3.6.1.4.1.99999.1.2"] != Added || len(kinds) != 2 {
		t.Errorf("items %v", kinds)
	}
}

func TestAmbiguousNamesAreUnknown(t *testing.T) {
	ats := append(append([]string{}, baseATs...),
		"( 1.3.6.1.4.1.99999.1.9 NAME 'alderTeam' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
	r := compareSchemas(t, schemaSnap(t, "A", baseATs, baseOCs), schemaSnap(t, "A", ats, baseOCs), true)
	for _, it := range r.Items {
		if it.Element == ElementAttributeType && (it.OID == "1.3.6.1.4.1.99999.1.1" || it.OID == "1.3.6.1.4.1.99999.1.9") {
			if it.Kind != Unknown || !hasProblem(it.Problems, ProblemAmbiguousName) {
				t.Errorf("%s: kind %s problems %v", it.OID, it.Kind, it.Problems)
			}
			if c := DeriveSchema(r, it, subschemaTarget()); c.Blocked != BlockedUnknown {
				t.Errorf("%s: candidate %+v", it.OID, c)
			}
		}
	}
}

func TestUnparsedDefinitionsAreUnknownAndMakeTheComparisonIncomplete(t *testing.T) {
	broken := append(append([]string{}, baseATs...), "( 1.3.6.1.4.1.99999.1.7 NAME 'broken' SYNTAX")
	src := schemaSnap(t, "A", baseATs, baseOCs)
	tgt := schemaSnap(t, "A", broken, baseOCs)
	r := compareSchemas(t, src, tgt, true)
	if r.Complete || len(r.Reasons) != 1 || r.Reasons[0].Code != ReasonSchemaPartial {
		t.Fatalf("complete %v reasons %+v", r.Complete, r.Reasons)
	}
	it := onlyItem(t, r)
	if it.Kind != Unknown || it.OID != "1.3.6.1.4.1.99999.1.7" || !hasProblem(it.Problems, ProblemUnparsed) {
		t.Errorf("item %+v", it)
	}
	// And a removal from an incomplete comparison is never proposed.
	fewer := schemaSnap(t, "A", baseATs[:3], baseOCs[:1])
	r = compareSchemas(t, src, fewer, true)
	r.Complete = false
	for _, it := range r.Items {
		if it.Kind == Removed {
			if c := DeriveSchema(r, it, subschemaTarget()); c.Blocked != BlockedIncomplete || !c.Destructive || len(c.Records) != 0 {
				t.Errorf("%s: candidate %+v", it.OID, c)
			}
		}
	}
}

func TestDependencyOrder(t *testing.T) {
	src := schemaSnap(t, "A", baseATs[:3], baseOCs[:1])
	// The target adds a parent attribute type, a child of it, and a class using both.
	ats := append(append([]string{}, baseATs[:3]...),
		"( 1.3.6.1.4.1.99999.1.30 NAME 'alderChild' SUP alderParent )",
		"( 1.3.6.1.4.1.99999.1.4 NAME 'alderParent' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
	)
	ocs := append(append([]string{}, baseOCs[:1]...),
		"( 1.3.6.1.4.1.99999.2.0 NAME 'alderHolder' SUP top AUXILIARY MUST alderChild MAY alderParent )")
	r := compareSchemas(t, src, schemaSnap(t, "A", ats, ocs), true)
	pos := map[string]int{}
	for i, k := range r.Order {
		pos[k] = i
	}
	parent, child, holder := "attributeType:1.3.6.1.4.1.99999.1.4", "attributeType:1.3.6.1.4.1.99999.1.30", "objectClass:1.3.6.1.4.1.99999.2.0"
	if len(r.Order) != 3 || pos[parent] >= pos[child] || pos[child] >= pos[holder] {
		t.Errorf("order %v", r.Order)
	}
	holderItem, _ := r.Item(holder)
	cand := DeriveSchema(r, holderItem, subschemaTarget())
	if strings.Join(cand.Requires, ",") != strings.Join([]string{child, parent}, ",") && strings.Join(cand.Requires, ",") != strings.Join([]string{parent, child}, ",") {
		t.Errorf("requires %v", cand.Requires)
	}

	// Removing both: the class goes before the attribute types it names.
	r = compareSchemas(t, schemaSnap(t, "A", ats, ocs), src, true)
	pos = map[string]int{}
	for i, k := range r.Order {
		pos[k] = i
	}
	if pos[holder] >= pos[child] || pos[child] >= pos[parent] {
		t.Errorf("removal order %v", r.Order)
	}
}

func TestUnresolvedReferencesBlockTheChange(t *testing.T) {
	ocs := append(append([]string{}, baseOCs...), "( 1.3.6.1.4.1.99999.2.5 NAME 'alderOrphan' SUP top AUXILIARY MUST alderNowhere )")
	r := compareSchemas(t, schemaSnap(t, "A", baseATs, baseOCs), schemaSnap(t, "A", baseATs, ocs), true)
	it := onlyItem(t, r)
	if !hasProblem(it.Problems, ProblemUnresolvedReference) {
		t.Fatalf("problems %v", it.Problems)
	}
	if c := DeriveSchema(r, it, subschemaTarget()); c.Blocked != BlockedUnresolvedReference {
		t.Errorf("candidate %+v", c)
	}
}

// subschemaTarget is a directly writable subschema holding the base schema, with
// alderTeam marked as added by an administrator.
func subschemaTarget() SchemaTarget {
	return SchemaTarget{
		Write: directory.SchemaWrite{Style: directory.SchemaStyleSubschema, ObjectClassAttr: "objectClasses", AttributeTypeAttr: "attributeTypes",
			Targets: []directory.SchemaTarget{{DN: "cn=schema", Name: "the server schema"}}},
		Stored: func(_ string, kind directory.SchemaDefKind) ([]string, error) {
			if kind == directory.SchemaDefObjectClass {
				return []string{"( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP top AUXILIARY MUST alderTeam MAY cn X-ORIGIN 'user defined' )"}, nil
			}
			return []string{"( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE X-ORIGIN 'user defined' )"}, nil
		},
		Usage: func(string, string, []string) (bool, bool) { return false, false },
	}
}

func TestDerivingSchemaChanges(t *testing.T) {
	userTeam := "( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE X-ORIGIN 'user defined' )"
	live := schemaSnap(t, "389 Project", replaceDef(baseATs, "1.3.6.1.4.1.99999.1.1", userTeam), baseOCs)

	t.Run("an addition, without provenance", func(t *testing.T) {
		ats := append(replaceDef(baseATs, "1.3.6.1.4.1.99999.1.1", userTeam),
			"( 1.3.6.1.4.1.99999.1.5 NAME 'alderSite' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 X-ORIGIN 'somewhere' X-KEEP 'yes' )")
		r := compareSchemas(t, live, schemaSnap(t, "389 Project", ats, baseOCs), true)
		c := DeriveSchema(r, onlyItem(t, r), subschemaTarget())
		if c.Blocked != "" || c.Destructive || len(c.Records) != 1 {
			t.Fatalf("candidate %+v", c)
		}
		mod := c.Records[0].Mods[0]
		def := string(mod.Values[0])
		if mod.Op != directory.ModAdd || mod.Name != "attributeTypes" || strings.Contains(def, "X-ORIGIN") || !strings.Contains(def, "X-KEEP") {
			t.Errorf("record %+v (%s)", c.Records[0], def)
		}
	})

	t.Run("a modification of an administrator's definition", func(t *testing.T) {
		ats := replaceDef(baseATs, "1.3.6.1.4.1.99999.1.1",
			"( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
		r := compareSchemas(t, live, schemaSnap(t, "389 Project", ats, baseOCs), true)
		c := DeriveSchema(r, onlyItem(t, r), subschemaTarget())
		if c.Blocked != "" || len(c.Records) != 1 {
			t.Fatalf("candidate %+v", c)
		}
		if def := string(c.Records[0].Mods[len(c.Records[0].Mods)-1].Values[0]); strings.Contains(def, "SINGLE-VALUE") {
			t.Errorf("the replacement is not the target's definition: %s", def)
		}
	})

	t.Run("a server's own definition is not changed", func(t *testing.T) {
		ats := replaceDef(replaceDef(baseATs, "1.3.6.1.4.1.99999.1.1", userTeam), "2.5.4.41",
			"( 2.5.4.41 NAME 'name' EQUALITY caseExactMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
		r := compareSchemas(t, live, schemaSnap(t, "389 Project", ats, baseOCs), true)
		if c := DeriveSchema(r, onlyItem(t, r), subschemaTarget()); c.Blocked != BlockedServerDefined {
			t.Errorf("candidate %+v", c)
		}
	})

	t.Run("a removal is destructive and states its impact", func(t *testing.T) {
		withoutClass := baseOCs[:1]
		r := compareSchemas(t, schemaSnap(t, "389 Project", replaceDef(baseATs, "1.3.6.1.4.1.99999.1.1", userTeam),
			[]string{baseOCs[0], "( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP top AUXILIARY MUST alderTeam MAY cn X-ORIGIN 'user defined' )"}),
			schemaSnap(t, "389 Project", baseATs[:3], withoutClass), true)
		var classItem, teamItem SchemaItem
		for _, it := range r.Items {
			switch it.OID {
			case "1.3.6.1.4.1.99999.2.1":
				classItem = it
			case "1.3.6.1.4.1.99999.1.1":
				teamItem = it
			}
		}
		team := DeriveSchema(r, teamItem, subschemaTarget())
		if !team.Destructive || team.Blocked != "" || len(team.Records) != 1 || team.Records[0].Mods[0].Op != directory.ModDelete {
			t.Fatalf("attribute type candidate %+v", team)
		}
		if !hasProblem(team.Impact, ProblemReferencedBySchema) || !hasProblem(team.Impact, ProblemUsageUnknown) {
			t.Errorf("impact %v", team.Impact)
		}
		if strings.Join(team.Requires, ",") != "objectClass:1.3.6.1.4.1.99999.2.1" {
			t.Errorf("the attribute type's removal does not require the class's first: %v", team.Requires)
		}
		if cls := DeriveSchema(r, classItem, subschemaTarget()); !cls.Destructive || cls.Blocked != "" {
			t.Errorf("class candidate %+v", cls)
		}
	})

	t.Run("metadata, snapshots, and targets", func(t *testing.T) {
		r := compareSchemas(t, schemaSnap(t, "OpenLDAP", baseATs, baseOCs), live, true)
		if c := DeriveSchema(r, onlyItem(t, r), subschemaTarget()); c.Blocked != BlockedMetadataOnly {
			t.Errorf("metadata candidate %+v", c)
		}
		ats := append(append([]string{}, baseATs...), "( 1.3.6.1.4.1.99999.1.6 NAME 'alderDesk' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
		r = compareSchemas(t, schemaSnap(t, "A", baseATs, baseOCs), schemaSnap(t, "A", ats, baseOCs), false)
		if c := DeriveSchema(r, onlyItem(t, r), subschemaTarget()); c.Blocked != BlockedSourceNotLive {
			t.Errorf("snapshot source candidate %+v", c)
		}
		r = compareSchemas(t, schemaSnap(t, "A", baseATs, baseOCs), schemaSnap(t, "A", ats, baseOCs), true)
		config := SchemaTarget{Write: directory.SchemaWrite{Style: directory.SchemaStyleConfig, ObjectClassAttr: "olcObjectClasses",
			AttributeTypeAttr: "olcAttributeTypes", Targets: []directory.SchemaTarget{{DN: "cn={0}core,cn=schema,cn=config", Name: "core"},
				{DN: "cn={4}alder,cn=schema,cn=config", Name: "alder"}}}}
		if c := DeriveSchema(r, onlyItem(t, r), config); c.Blocked != BlockedSchemaTargetRequired {
			t.Errorf("config add without a target %+v", c)
		}
		config.AddTarget = "cn={4}alder,cn=schema,cn=config"
		if c := DeriveSchema(r, onlyItem(t, r), config); c.Blocked != "" || c.Records[0].DN.String() != "cn={4}alder,cn=schema,cn=config" {
			t.Errorf("config add with a target %+v", c)
		}
		if c := DeriveSchema(r, onlyItem(t, r), SchemaTarget{}); c.Blocked != BlockedSchemaNotEditable {
			t.Errorf("not editable %+v", c)
		}
	})
}

func TestNonNumericOIDsAreReportedButNotActedOn(t *testing.T) {
	ats := append(append([]string{}, baseATs...), "( nsHost-oid NAME 'nsHost' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
	r := compareSchemas(t, schemaSnap(t, "A", baseATs, baseOCs), schemaSnap(t, "A", ats, baseOCs), true)
	it := onlyItem(t, r)
	if it.Kind != Added || !hasProblem(it.Problems, ProblemNonNumericOID) {
		t.Fatalf("item %+v", it)
	}
	if c := DeriveSchema(r, it, subschemaTarget()); c.Blocked != BlockedNonNumericOID {
		t.Errorf("candidate %+v", c)
	}
}
