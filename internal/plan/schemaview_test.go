package plan

import (
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

// viewSchema is the live schema plus what a subschema entry itself needs: the
// class it holds and the two attributes definitions are written to. Without
// them a modification of cn=schema is invalid before the view is ever
// consulted.
func viewSchema(t *testing.T) *schema.Schema {
	t.Helper()
	live := liveSchema(t)
	attrs := make([]string, 0, len(live.AttributeTypes)+4)
	for _, at := range live.AttributeTypes {
		attrs = append(attrs, at.Raw)
	}
	attrs = append(attrs,
		"( 2.5.21.5 NAME 'attributeTypes' SYNTAX 1.3.6.1.4.1.1466.115.121.1.3 USAGE directoryOperation )",
		"( 2.5.21.6 NAME 'objectClasses' SYNTAX 1.3.6.1.4.1.1466.115.121.1.37 USAGE directoryOperation )",
		"( 2.5.4.4 NAME 'sn' SUP name )",
		"( 0.9.2342.19200300.100.1.1 NAME 'uid' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
	classes := make([]string, 0, len(live.ObjectClasses)+3)
	for _, oc := range live.ObjectClasses {
		classes = append(classes, oc.Raw)
	}
	classes = append(classes,
		"( 2.5.20.1 NAME 'subschema' AUXILIARY MAY ( attributeTypes $ objectClasses ) )",
		"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) MAY uid )",
		"( 2.5.6.5 NAME 'organizationalUnit' SUP top STRUCTURAL MUST ou )")
	return schema.Load("cn=schema", map[string][]string{
		schema.AttrAttributeTypes: attrs,
		schema.AttrObjectClasses:  classes,
	})
}

// 1.11: a set that changes the schema and then uses it.
//
// The planner reads every change against the directory as it is. For the schema
// that is not enough: a change may add a definition the next change uses, and
// judging the second against the schema as it is now would refuse a change the
// directory accepts.

const (
	viewAttrDef  = "( 1.3.6.1.4.1.99999.30.1 NAME 'alderViewTeam' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"
	viewClassDef = "( 1.3.6.1.4.1.99999.30.2 NAME 'alderViewClass' SUP top AUXILIARY MUST alderViewTeam )"
	teamDef      = "( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"
)

// schemaAdd is a change that installs a definition, as the schema editor writes
// one.
func schemaAdd(t *testing.T, attribute, definition string) directory.ChangeRecord {
	t.Helper()
	return directory.ChangeRecord{DN: mustParse(t, "cn=schema"), Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModAdd, Name: attribute, Values: [][]byte{[]byte(definition)}}}}
}

// entryUsing is an entry that needs the new class and the new attribute type.
func entryUsing(t *testing.T) directory.ChangeRecord {
	t.Helper()
	return directory.ChangeRecord{DN: mustParse(t, "uid=pat,ou=people,dc=alder,dc=test"), Type: directory.ChangeAdd,
		Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("person"), []byte("alderViewClass")}},
			{Name: "cn", Values: [][]byte{[]byte("Pat")}},
			{Name: "sn", Values: [][]byte{[]byte("Doe")}},
			{Name: "alderViewTeam", Values: [][]byte{[]byte("platform")}},
		}}
}

func TestAChangeMayUseSchemaAnEarlierChangeAdds(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, "cn=schema", []string{"objectClass", "top", "subschema"})
	d.put(t, "ou=people,dc=alder,dc=test", []string{"objectClass", "top", "organizationalUnit"})
	opts := Options{SchemaLocator: schemaLocator}

	p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, viewSchema(t), Exact(
		schemaAdd(t, "attributeTypes", viewAttrDef),
		schemaAdd(t, "objectClasses", viewClassDef),
		entryUsing(t),
	), opts)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range p.Items {
		if item.Action != ActionModify && item.Action != ActionAdd {
			t.Fatalf("change %d planned as %s (%+v): %s", i, item.Action, item.Problem, item.Reason)
		}
	}

	// The other way round, the entry really is invalid: nothing in the set has
	// defined the class by the time it runs.
	p, err = newTestPlanner(t).ComputeProposals(t.Context(), d, viewSchema(t), Exact(
		entryUsing(t),
		schemaAdd(t, "attributeTypes", viewAttrDef),
		schemaAdd(t, "objectClasses", viewClassDef),
	), opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Items[0].Action != ActionInvalid || p.Items[0].Problem == nil ||
		p.Items[0].Problem.Code != ProblemObjectClassUndefined {
		t.Errorf("the entry before its schema: %s %+v", p.Items[0].Action, p.Items[0].Problem)
	}
}

func TestASetThatRemovesSchemaJudgesLaterChangesWithoutIt(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, "cn=schema", []string{"objectClass", "top", "subschema"},
		[]string{"attributeTypes", teamDef})
	d.put(t, "uid=alice,ou=people,dc=alder,dc=test",
		[]string{"objectClass", "top", "alderEmployee"}, []string{"alderTeam", "platform"})

	// The set removes the attribute type, then a later change sets it. The
	// second change is invalid, and saying so is the point: the directory
	// would refuse it too.
	remove := directory.ChangeRecord{DN: mustParse(t, "cn=schema"), Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModDelete, Name: "attributeTypes",
			Values: [][]byte{[]byte(teamDef)}}}}
	use := directory.ChangeRecord{DN: mustParse(t, "uid=alice,ou=people,dc=alder,dc=test"), Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModReplace, Name: "alderTeam", Values: [][]byte{[]byte("infra")}}}}

	p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, viewSchema(t), Exact(remove, use),
		Options{SchemaLocator: schemaLocator})
	if err != nil {
		t.Fatal(err)
	}
	// The removal itself is refused first, because the schema still names the
	// definition -- and because it is refused, the attribute is still there for
	// the change that follows.
	if p.Items[0].Action != ActionConflict || p.Items[1].Action != ActionModify {
		t.Fatalf("removal %s, use %s (%+v)", p.Items[0].Action, p.Items[1].Action, p.Items[1].Problem)
	}
}
