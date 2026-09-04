package schema

import (
	"reflect"
	"strings"
	"testing"
)

// The property that matters is that the parser reads back what the writer
// produced, unchanged. Asserting on the exact text would test the formatting;
// this tests that a definition Alder builds means what Alder thinks it means.
func TestAttributeTypeRoundTrips(t *testing.T) {
	tests := []struct {
		name string
		in   AttributeType
	}{
		{
			name: "the minimum a server will accept",
			in:   AttributeType{OID: "1.3.6.1.4.1.99999.1.1", Names: []string{"alderTeam"}},
		},
		{
			name: "every field set",
			in: AttributeType{
				OID:                "1.3.6.1.4.1.99999.1.2",
				Names:              []string{"alderOnCall", "alderRota"},
				Desc:               "Whether the person is in the on-call rotation",
				Obsolete:           true,
				SuperName:          "name",
				Equality:           "booleanMatch",
				Ordering:           "integerOrderingMatch",
				Substr:             "caseIgnoreSubstringsMatch",
				Syntax:             "1.3.6.1.4.1.1466.115.121.1.7",
				SyntaxLen:          64,
				SingleValue:        true,
				Collective:         true,
				NoUserModification: true,
				Usage:              UsageDirectoryOperation,
				Extensions:         Extensions{"X-ORIGIN": {"Alder test harness"}},
			},
		},
		{
			// The two escapes of RFC 4512 section 4.1, in the one field a
			// person types free text into.
			name: "a description containing a quote and a backslash",
			in: AttributeType{
				OID:   "1.3.6.1.4.1.99999.1.3",
				Names: []string{"alderNote"},
				Desc:  `it's a note \ with both escapes`,
			},
		},
		{
			name: "several extension values",
			in: AttributeType{
				OID:        "1.3.6.1.4.1.99999.1.4",
				Names:      []string{"alderTag"},
				Extensions: Extensions{"X-ORIGIN": {"one", "two"}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			def, err := tc.in.Definition()
			if err != nil {
				t.Fatalf("Definition(): %v", err)
			}
			got, err := ParseAttributeType(def)
			if err != nil {
				t.Fatalf("ParseAttributeType(%q): %v", def, err)
			}
			// Raw is what the parser was handed, and has no counterpart on the
			// way in. Everything else has to survive the trip.
			got.Raw = ""
			if !reflect.DeepEqual(*got, tc.in) {
				t.Errorf("round trip changed the definition\n  wrote: %s\n  in:    %+v\n  out:   %+v",
					def, tc.in, *got)
			}
		})
	}
}

func TestObjectClassRoundTrips(t *testing.T) {
	tests := []struct {
		name string
		in   ObjectClass
	}{
		{
			name: "the minimum",
			in: ObjectClass{
				OID: "1.3.6.1.4.1.99999.2.1", Names: []string{"alderEmployee"},
				Kind: KindStructural,
			},
		},
		{
			name: "auxiliary, with everything",
			in: ObjectClass{
				OID:        "1.3.6.1.4.1.99999.2.2",
				Names:      []string{"alderEmployee", "alderStaff"},
				Desc:       "Alder test employee",
				Obsolete:   true,
				SuperNames: []string{"top", "person"},
				Kind:       KindAuxiliary,
				Must:       []string{"alderTeam"},
				May:        []string{"alderOnCall", "alderBadgePhoto", "alderNote"},
				Extensions: Extensions{"X-ORIGIN": {"Alder test harness"}},
			},
		},
		{
			name: "abstract with one superior",
			in: ObjectClass{
				OID: "1.3.6.1.4.1.99999.2.3", Names: []string{"alderThing"},
				SuperNames: []string{"top"}, Kind: KindAbstract,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			def, err := tc.in.Definition()
			if err != nil {
				t.Fatalf("Definition(): %v", err)
			}
			got, err := ParseObjectClass(def)
			if err != nil {
				t.Fatalf("ParseObjectClass(%q): %v", def, err)
			}
			got.Raw = ""
			if !reflect.DeepEqual(*got, tc.in) {
				t.Errorf("round trip changed the definition\n  wrote: %s\n  in:    %+v\n  out:   %+v",
					def, tc.in, *got)
			}
		})
	}
}

// A definition that cannot be rendered must be refused here, where the message
// can say which field is wrong, rather than by the directory as a syntax error
// naming only the value.
func TestDefinitionRefusesWhatServersWouldReject(t *testing.T) {
	tests := []struct {
		name string
		in   AttributeType
		want string
	}{
		{"no OID", AttributeType{Names: []string{"a"}}, "OID is empty"},
		{"an OID that is a name", AttributeType{OID: "alderThing", Names: []string{"a"}}, "not a numeric OID"},
		{"a leading zero", AttributeType{OID: "1.3.06.1", Names: []string{"a"}}, "leading zero"},
		{"no name", AttributeType{OID: "1.2.3"}, "at least one name"},
		{"a name starting with a digit", AttributeType{OID: "1.2.3", Names: []string{"1bad"}}, "must start with a letter"},
		{"a name with a space", AttributeType{OID: "1.2.3", Names: []string{"bad name"}}, "not allowed in a name"},
		{"a syntax that is not an OID", AttributeType{OID: "1.2.3", Names: []string{"a"}, Syntax: "Directory String"}, "SYNTAX"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.in.Definition()
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}

	if _, err := (ObjectClass{OID: "1.2.3", Names: []string{"a"}, Must: []string{"not a name"}}).Definition(); err == nil {
		t.Error("an object class with an unusable MUST was rendered anyway")
	}
}

// Extensions come out of a map, and a definition that renders differently on
// each call would show a spurious difference against the stored one every time.
func TestExtensionOrderIsStable(t *testing.T) {
	a := AttributeType{
		OID: "1.2.3", Names: []string{"alderThing"},
		Extensions: Extensions{"X-ORIGIN": {"a"}, "X-DEPRECATED": {"b"}, "X-OTHER": {"c"}},
	}
	first, err := a.Definition()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		again, err := a.Definition()
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("rendering is not stable:\n  %s\n  %s", first, again)
		}
	}
}

// Every definition the harness servers publish must survive being read and
// written: if Alder cannot reproduce a real definition it has no business
// offering to edit one.
func FuzzDefinitionRoundTrip(f *testing.F) {
	f.Add("( 1.2.3 NAME 'x' )")
	f.Add("( 1.2.3 NAME ( 'x' 'y' ) DESC 'a' OBSOLETE SUP name SINGLE-VALUE )")
	f.Add("( 1.2.3 NAME 'x' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{64} USAGE dSAOperation )")

	f.Fuzz(func(t *testing.T, def string) {
		parsed, err := ParseAttributeType(def)
		if err != nil {
			return // not a definition; nothing to round-trip
		}
		written, err := parsed.Definition()
		if err != nil {
			// Refusing to render is allowed — a server may publish something
			// this package will not author — but it must not then be rendered
			// into something that parses differently, which is the point below.
			return
		}
		again, err := ParseAttributeType(written)
		if err != nil {
			t.Fatalf("wrote a definition that does not parse\n  from: %q\n  wrote: %q\n  %v", def, written, err)
		}
		parsed.Raw, again.Raw = "", ""
		if !reflect.DeepEqual(parsed, again) {
			t.Fatalf("writing changed the meaning\n  from:  %q\n  wrote: %q\n  %+v\n  %+v",
				def, written, parsed, again)
		}
	})
}
