package api

import (
	"reflect"
	"testing"

	"github.com/hazame-hub/alder/internal/schema"
)

// An edit must change only what was edited.
//
// The form the editor is given has to survive being sent back: parse a
// definition, hand it over as an editable form, convert it back, render it, and
// the result must mean exactly what the original did. Anything the form cannot
// carry is a field an edit silently deletes — which is how SUBSTR disappeared
// from an attribute type whose only intended change was its description.
func TestEditFormRoundTripsAnAttributeType(t *testing.T) {
	definitions := []string{
		// The one that broke: SUBSTR is not in the display summary.
		"( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team the person belongs to' " +
			"EQUALITY caseIgnoreMatch SUBSTR caseIgnoreSubstringsMatch " +
			"SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE " +
			"X-ORIGIN ( 'Alder test harness' 'user defined' ) )",
		// Every field at once.
		"( 1.2.3 NAME ( 'a' 'b' ) DESC 'd' OBSOLETE SUP name EQUALITY caseIgnoreMatch " +
			"ORDERING integerOrderingMatch SUBSTR caseIgnoreSubstringsMatch " +
			"SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{64} SINGLE-VALUE COLLECTIVE " +
			"NO-USER-MODIFICATION USAGE directoryOperation X-ORIGIN 'somewhere' )",
		// The minimum.
		"( 1.2.4 NAME 'plain' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		// One that declares nothing but a superior, so every effective value it
		// has is inherited. Writing those back would redeclare them here.
		"( 1.2.5 NAME 'inherits' SUP alderTeam )",
	}

	for _, def := range definitions {
		t.Run(shortName(def), func(t *testing.T) {
			original, err := schema.ParseAttributeType(def)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			// Out to the editor and back, exactly as the UI does it.
			rebuilt := attributeTypeFrom(attributeTypeEditForm(original))
			written, err := rebuilt.Definition()
			if err != nil {
				t.Fatalf("rendering the round-tripped form: %v", err)
			}
			reparsed, err := schema.ParseAttributeType(written)
			if err != nil {
				t.Fatalf("the round-tripped definition does not parse: %v", err)
			}
			original.Raw, reparsed.Raw = "", ""
			if !reflect.DeepEqual(original, reparsed) {
				t.Errorf("the round trip changed the definition\n  from: %s\n  to:   %s\n  %+v\n  %+v",
					def, written, original, reparsed)
			}
		})
	}
}

func TestEditFormRoundTripsAnObjectClass(t *testing.T) {
	definitions := []string{
		"( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' DESC 'Alder test employee' " +
			"SUP top AUXILIARY MUST alderTeam MAY ( alderOnCall $ alderBadgePhoto $ alderNote ) " +
			"X-ORIGIN ( 'Alder test harness' 'user defined' ) )",
		"( 1.2.6 NAME 'bare' STRUCTURAL )",
		"( 1.2.7 NAME ( 'x' 'y' ) DESC 'd' OBSOLETE SUP ( top $ person ) ABSTRACT " +
			"MUST ( cn $ sn ) MAY description X-ORIGIN 'elsewhere' )",
	}

	for _, def := range definitions {
		t.Run(shortName(def), func(t *testing.T) {
			original, err := schema.ParseObjectClass(def)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			rebuilt := objectClassFrom(objectClassEditForm(original))
			written, err := rebuilt.Definition()
			if err != nil {
				t.Fatalf("rendering: %v", err)
			}
			reparsed, err := schema.ParseObjectClass(written)
			if err != nil {
				t.Fatalf("does not parse: %v", err)
			}
			original.Raw, reparsed.Raw = "", ""
			if !reflect.DeepEqual(original, reparsed) {
				t.Errorf("the round trip changed the definition\n  from: %s\n  to:   %s",
					def, written)
			}
		})
	}
}

// The editable form must report what the definition declares, not what it
// effectively has. An attribute inheriting its syntax declares none, and an
// editor told otherwise would write the inherited value in as its own.
func TestEditFormReportsWhatIsDeclaredNotWhatIsInherited(t *testing.T) {
	at, err := schema.ParseAttributeType("( 1.2.5 NAME 'inherits' SUP alderTeam )")
	if err != nil {
		t.Fatal(err)
	}
	form := attributeTypeEditForm(at)
	if form.Syntax != nil {
		t.Errorf("syntax is reported as %q, but the definition declares none", *form.Syntax)
	}
	if form.Equality != nil {
		t.Errorf("equality is reported as %q, but the definition declares none", *form.Equality)
	}
	if form.SingleValue != nil {
		t.Error("single-valuedness is reported, but the definition declares none")
	}
	if form.SuperName == nil || *form.SuperName != "alderTeam" {
		t.Errorf("the superior is %v, want alderTeam", form.SuperName)
	}
}

// Absent is not the same as false. A definition that says nothing about
// OBSOLETE must not come back asserting it is not obsolete, or every edit would
// add noise to the stored value.
func TestEditFormOmitsFalseFlags(t *testing.T) {
	at, err := schema.ParseAttributeType(
		"( 1.2.4 NAME 'plain' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
	if err != nil {
		t.Fatal(err)
	}
	form := attributeTypeEditForm(at)
	for name, got := range map[string]*bool{
		"obsolete":           form.Obsolete,
		"singleValue":        form.SingleValue,
		"collective":         form.Collective,
		"noUserModification": form.NoUserModification,
	} {
		if got != nil {
			t.Errorf("%s is present as %v, and the definition says nothing about it", name, *got)
		}
	}
	if form.Extensions != nil {
		t.Error("extensions are present, and the definition has none")
	}
}

// shortName labels a subtest without assuming the definition is long.
func shortName(def string) string {
	if len(def) > 40 {
		return def[:40]
	}
	return def
}
