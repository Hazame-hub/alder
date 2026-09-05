package api

import (
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// An editor cannot offer the right control for an attribute it knows nothing
// about, and it knew nothing about any attribute the entry did not already
// carry — which is every attribute anybody adds.
//
// The visible symptom was a bare text box badged "not in the schema" for an
// attribute the schema describes perfectly well, with no description, no
// syntax, and no Boolean control on something the schema calls a Boolean.

func candidateSchema(t *testing.T) *schema.Schema {
	t.Helper()
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrObjectClasses: {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 1.2.3.4 NAME 'testThing' SUP top STRUCTURAL MUST cn " +
				"MAY ( description $ testFlag $ seeAlso ) )",
		},
		schema.AttrAttributeTypes: {
			"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.3 NAME 'cn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.13 NAME 'description' DESC 'A human readable note' " +
				"SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 1.2.3.5 NAME 'testFlag' DESC 'Whether the thing is on' " +
				"SYNTAX 1.3.6.1.4.1.1466.115.121.1.7 SINGLE-VALUE )",
			"( 2.5.4.34 NAME 'seeAlso' SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
		},
	})
	if len(sch.Errors) != 0 {
		t.Fatalf("the test schema does not parse: %v", sch.Errors)
	}
	return sch
}

func candidateEntry(t *testing.T) *directory.Entry {
	t.Helper()
	d, err := dn.Parse("cn=thing,dc=example,dc=test")
	if err != nil {
		t.Fatal(err)
	}
	e := directory.NewEntry(d)
	e.Set("objectClass", [][]byte{[]byte("top"), []byte("testThing")})
	e.Set("cn", [][]byte{[]byte("thing")})
	return e
}

func kindNamed(kinds []AttributeKind, name string) *AttributeKind {
	for i := range kinds {
		if kinds[i].Name == name {
			return &kinds[i]
		}
	}
	return nil
}

func TestCandidateKindsDescribeWhatTheEntryCouldStillHave(t *testing.T) {
	sch := candidateSchema(t)
	entry := candidateEntry(t)
	req := sch.Requirements(entry.ObjectClasses())

	kinds := candidateKinds(entry, sch, req)

	// The Boolean is the case that mattered: without this it arrived as a
	// string, and a string gets a text box rather than TRUE/FALSE/not set.
	flag := kindNamed(kinds, "testFlag")
	if flag == nil {
		t.Fatalf("testFlag is not offered; got %v", kindNames(kinds))
	}
	if flag.Kind != "boolean" {
		t.Errorf("testFlag is %q, want boolean — the editor picks its control from this",
			flag.Kind)
	}
	if !flag.Known {
		t.Error("testFlag is reported as not in the schema, and the schema defines it")
	}
	if flag.Desc == nil || *flag.Desc != "Whether the thing is on" {
		t.Errorf("testFlag carries desc %v, want the schema's own", flag.Desc)
	}
	if flag.SingleValue == nil || !*flag.SingleValue {
		t.Error("testFlag is not reported single-valued, and the schema says it is")
	}

	// A DN-valued one, because that is what decides whether the picker appears.
	if also := kindNamed(kinds, "seeAlso"); also == nil || also.Kind != "dn" {
		t.Errorf("seeAlso is %v, want a dn kind so the entry picker is offered", also)
	}
}

// What the entry already has is not a candidate: it is already on screen, with
// its own control, and offering it again would let it be added twice.
func TestCandidateKindsExcludeWhatIsAlreadyThere(t *testing.T) {
	sch := candidateSchema(t)
	entry := candidateEntry(t)
	req := sch.Requirements(entry.ObjectClasses())

	kinds := candidateKinds(entry, sch, req)
	for _, name := range []string{"cn", "objectClass"} {
		if kindNamed(kinds, name) != nil {
			t.Errorf("%s is offered as a candidate, and the entry already carries it", name)
		}
	}
	if kindNamed(kinds, "description") == nil {
		t.Error("description is not offered, and the entry may have it but does not")
	}
}

// Attribute names are case-insensitive, so an entry holding CN must not be
// offered cn as though it were missing.
func TestCandidateKindsFoldCase(t *testing.T) {
	sch := candidateSchema(t)
	d, err := dn.Parse("cn=thing,dc=example,dc=test")
	if err != nil {
		t.Fatal(err)
	}
	entry := directory.NewEntry(d)
	entry.Set("OBJECTCLASS", [][]byte{[]byte("top"), []byte("testThing")})
	entry.Set("CN", [][]byte{[]byte("thing")})

	kinds := candidateKinds(entry, sch, sch.Requirements(entry.ObjectClasses()))
	if kindNamed(kinds, "cn") != nil {
		t.Errorf("cn is offered though the entry holds CN; got %v", kindNames(kinds))
	}
}

func kindNames(kinds []AttributeKind) []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k.Name)
	}
	return out
}
