package plan

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

func liveSchema(t *testing.T) *schema.Schema {
	t.Helper()
	return schema.Load("cn=schema", map[string][]string{
		schema.AttrAttributeTypes: {
			"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.41 NAME 'name' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.3 NAME 'cn' SUP name )",
			"( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		},
		schema.AttrObjectClasses: {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP top AUXILIARY MUST alderTeam )",
		},
	})
}

func schemaLocator(target dn.DN, attribute string) (directory.SchemaDefKind, bool) {
	if !strings.EqualFold(target.String(), "cn=schema") {
		return "", false
	}
	switch strings.ToLower(attribute) {
	case "attributetypes":
		return directory.SchemaDefAttributeType, true
	case "objectclasses":
		return directory.SchemaDefObjectClass, true
	}
	return "", false
}

func schemaItem(t *testing.T, mods ...directory.Mod) Item {
	t.Helper()
	return Item{DN: mustParse(t, "cn=schema"), Action: ActionModify, Baseline: "token",
		Record: directory.ChangeRecord{DN: mustParse(t, "cn=schema"), Type: directory.ChangeModify, Mods: mods}}
}

func defMod(op directory.ModOp, attr, def string) directory.Mod {
	return directory.Mod{Op: op, Name: attr, Values: [][]byte{[]byte(def)}}
}

func check(t *testing.T, items ...Item) Plan {
	t.Helper()
	p := Plan{Items: items, Counts: Counts{Examined: len(items), Modify: len(items)}}
	CheckSchemaDependencies(&p, liveSchema(t), schemaLocator)
	return p
}

func TestAClassNeedsTheAttributeTypesItNames(t *testing.T) {
	class := defMod(directory.ModAdd, "objectClasses", "( 1.3.6.1.4.1.99999.2.2 NAME 'alderSite' SUP top AUXILIARY MUST alderSiteCode )")
	p := check(t, schemaItem(t, class))
	it := p.Items[0]
	if it.Action != ActionConflict || it.Problem == nil || it.Problem.Code != ProblemDependencyRequired ||
		it.Problem.Attribute != "alderSiteCode" || it.Baseline != "" || p.Counts.Conflict != 1 || p.Counts.Modify != 0 {
		t.Fatalf("item %+v counts %+v", it, p.Counts)
	}

	// Added first, in the same set, it holds.
	at := defMod(directory.ModAdd, "attributeTypes", "( 1.3.6.1.4.1.99999.1.3 NAME 'alderSiteCode' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
	p = check(t, schemaItem(t, at), schemaItem(t, class))
	for i, it := range p.Items {
		if it.Action != ActionModify {
			t.Errorf("item %d: %s %+v", i, it.Action, it.Problem)
		}
	}
	// In the same change, too.
	if p = check(t, schemaItem(t, at, class)); p.Items[0].Action != ActionModify {
		t.Errorf("together: %+v", p.Items[0].Problem)
	}
	// In the wrong order it does not.
	if p = check(t, schemaItem(t, class), schemaItem(t, at)); p.Items[0].Action != ActionConflict || p.Items[1].Action != ActionModify {
		t.Errorf("reversed: %s, %s", p.Items[0].Action, p.Items[1].Action)
	}
}

func TestAChildNeedsItsParent(t *testing.T) {
	child := defMod(directory.ModAdd, "attributeTypes", "( 1.3.6.1.4.1.99999.1.5 NAME 'alderChild' SUP alderParent )")
	if p := check(t, schemaItem(t, child)); p.Items[0].Problem == nil || p.Items[0].Problem.Code != ProblemDependencyRequired {
		t.Errorf("child without parent: %+v", p.Items[0])
	}
	parent := defMod(directory.ModAdd, "attributeTypes", "( 1.3.6.1.4.1.99999.1.4 NAME 'alderParent' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
	if p := check(t, schemaItem(t, parent), schemaItem(t, child)); p.Items[1].Action != ActionModify {
		t.Errorf("child after parent: %+v", p.Items[1].Problem)
	}
}

func TestRemovingWhatIsStillNamed(t *testing.T) {
	removeTeam := defMod(directory.ModDelete, "attributeTypes", "( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
	p := check(t, schemaItem(t, removeTeam))
	if it := p.Items[0]; it.Problem == nil || it.Problem.Code != ProblemReferencedBySchema || it.Problem.Attribute != "alderEmployee" {
		t.Fatalf("item %+v", it)
	}
	removeClass := defMod(directory.ModDelete, "objectClasses", "( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP top AUXILIARY MUST alderTeam )")
	if p = check(t, schemaItem(t, removeClass), schemaItem(t, removeTeam)); p.Items[0].Action != ActionModify || p.Items[1].Action != ActionModify {
		t.Errorf("class then attribute: %+v / %+v", p.Items[0].Problem, p.Items[1].Problem)
	}
	// A supertype still has a subtype.
	removeName := defMod(directory.ModDelete, "attributeTypes", "( 2.5.4.41 NAME 'name' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
	if p = check(t, schemaItem(t, removeName)); p.Items[0].Problem == nil || p.Items[0].Problem.Attribute != "cn" {
		t.Errorf("supertype: %+v", p.Items[0].Problem)
	}
	// A superclass still has a subclass.
	removeTop := defMod(directory.ModDelete, "objectClasses", "( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )")
	if p = check(t, schemaItem(t, removeTop)); p.Items[0].Problem == nil || p.Items[0].Problem.Code != ProblemReferencedBySchema {
		t.Errorf("superclass: %+v", p.Items[0].Problem)
	}
	// A definition removed earlier cannot be named later.
	addUsingTeam := defMod(directory.ModAdd, "objectClasses", "( 1.3.6.1.4.1.99999.2.3 NAME 'alderLater' SUP top AUXILIARY MAY alderTeam )")
	p = check(t, schemaItem(t, removeClass), schemaItem(t, removeTeam), schemaItem(t, addUsingTeam))
	if p.Items[2].Problem == nil || p.Items[2].Problem.Code != ProblemDependencyRequired {
		t.Errorf("named after removal: %+v", p.Items[2])
	}
}

func TestAReplaceIsNotARemoval(t *testing.T) {
	// A configuration collection replaces by deleting and adding the same OID
	// in one change, with the load-order prefix on the stored value.
	replace := schemaItem(t,
		defMod(directory.ModDelete, "attributeTypes", "{3}( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"),
		defMod(directory.ModAdd, "attributeTypes", "( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'edited' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"),
	)
	if p := check(t, replace); p.Items[0].Action != ActionModify {
		t.Errorf("replace: %+v", p.Items[0].Problem)
	}
	// And ordinary data changes are not looked at.
	data := Item{DN: mustParse(t, "uid=a,dc=alder,dc=test"), Action: ActionModify,
		Record: directory.ChangeRecord{DN: mustParse(t, "uid=a,dc=alder,dc=test"), Type: directory.ChangeModify,
			Mods: []directory.Mod{defMod(directory.ModAdd, "attributeTypes", "( 1.2.3 NAME 'x' SUP nowhere )")}}}
	if p := check(t, data); p.Items[0].Action != ActionModify {
		t.Errorf("data change: %+v", p.Items[0].Problem)
	}
}
