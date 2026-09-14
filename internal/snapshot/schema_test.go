package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/schema"
)

func subschema(t testing.TB, ats, ocs []string, extra map[string][]string) *schema.Schema {
	t.Helper()
	attrs := map[string][]string{schema.AttrAttributeTypes: ats, schema.AttrObjectClasses: ocs}
	for k, v := range extra {
		attrs[k] = v
	}
	return schema.Load("cn=schema", attrs)
}

var testATs = []string{
	"( 2.5.4.41 NAME 'name' EQUALITY caseIgnoreMatch SUBSTR caseIgnoreSubstringsMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{32768} )",
	"( 2.5.4.3 NAME ( 'cn' 'commonName' ) DESC 'RFC4519: common name' SUP name )",
	"( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' DESC 'Team' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE X-ORIGIN ( 'Alder test harness' 'user defined' ) )",
	"( 1.3.6.1.4.1.99999.1.10 NAME 'alderTen' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
	"( 2.5.18.1 NAME 'createTimestamp' EQUALITY generalizedTimeMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.24 SINGLE-VALUE NO-USER-MODIFICATION USAGE directoryOperation )",
}

var testOCs = []string{
	"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
	"( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP top AUXILIARY MUST alderTeam MAY ( cn $ alderTen ) )",
}

var testContext = map[string][]string{
	schema.AttrLDAPSyntaxes:  {"( 1.3.6.1.4.1.1466.115.121.1.15 DESC 'Directory String' )"},
	schema.AttrMatchingRules: {"( 2.5.13.2 NAME 'caseIgnoreMatch' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"},
}

func schemaCapture(at time.Time) SchemaCapture {
	return SchemaCapture{Vendor: "Test", VendorVersion: "1", SubschemaEntry: "cn=schema", CreatedAt: at}
}

func buildTestSchema(t testing.TB) *SchemaSnapshot {
	t.Helper()
	s, err := BuildSchema(schemaCapture(captured), subschema(t, testATs, testOCs, testContext))
	if err != nil {
		t.Fatalf("BuildSchema: %v", err)
	}
	return s
}

func encodeSchema(t testing.TB, s *SchemaSnapshot) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := EncodeSchema(&buf, s); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAnUnchangedSchemaIsTheSameDocument(t *testing.T) {
	first := buildTestSchema(t)
	reversed := func(in []string) []string {
		out := make([]string, len(in))
		for i, v := range in {
			out[len(in)-1-i] = v
		}
		return out
	}
	// The same schema, published in another order and captured an hour later.
	second, err := BuildSchema(schemaCapture(captured.Add(time.Hour)), subschema(t, reversed(testATs), reversed(testOCs), testContext))
	if err != nil {
		t.Fatal(err)
	}
	if first.Checksum != second.Checksum {
		t.Errorf("checksums differ: %s and %s", first.Checksum, second.Checksum)
	}
	a := bytes.Replace(encodeSchema(t, first), []byte(first.CreatedAt), nil, 1)
	b := bytes.Replace(encodeSchema(t, second), []byte(second.CreatedAt), nil, 1)
	if !bytes.Equal(a, b) {
		t.Error("the documents differ apart from createdAt")
	}
}

func TestSchemaCanonicalForm(t *testing.T) {
	s := buildTestSchema(t)
	var oids []string
	for _, at := range s.AttributeTypes {
		oids = append(oids, at.OID)
	}
	// Numeric arc order: .1.1 before .1.10, and 2.5.4.3 before 2.5.4.41.
	want := "1.3.6.1.4.1.99999.1.1 1.3.6.1.4.1.99999.1.10 2.5.4.3 2.5.4.41 2.5.18.1"
	if got := strings.Join(oids, " "); got != want {
		t.Errorf("order %s, want %s", got, want)
	}
	cn := s.AttributeTypes[2]
	if strings.Join(cn.Names, ",") != "cn,commonName" || cn.Sup != "name" || cn.Usage != "userApplications" {
		t.Errorf("cn %+v", cn)
	}
	name := s.AttributeTypes[3]
	if name.Syntax != "1.3.6.1.4.1.1466.115.121.1.15" || name.SyntaxLength != 32768 {
		t.Errorf("name syntax %q length %d", name.Syntax, name.SyntaxLength)
	}
	team := s.AttributeTypes[0]
	if got := team.Extensions["X-ORIGIN"]; len(got) != 2 || got[1] != "user defined" {
		t.Errorf("X-ORIGIN %v, want both values in order", got)
	}
	emp := s.ObjectClasses[0]
	if strings.Join(emp.May, ",") != "alderTen,cn" || strings.Join(emp.Must, ",") != "alderTeam" || emp.Kind != "AUXILIARY" {
		t.Errorf("alderEmployee %+v", emp)
	}
	if s.Counts.LDAPSyntaxes != 1 || s.Counts.MatchingRules != 1 || s.Completeness != SchemaComplete {
		t.Errorf("counts %+v completeness %s", s.Counts, s.Completeness)
	}
	if strings.Join(s.Coverage.NotCaptured, ",") != "dITStructureRules" {
		t.Errorf("coverage %+v", s.Coverage)
	}
}

func TestSchemaDecodeRoundTripsAndVerifies(t *testing.T) {
	s := buildTestSchema(t)
	raw := encodeSchema(t, s)
	if kind, err := KindOf(raw); err != nil || kind != KindSchema {
		t.Fatalf("KindOf = %q, %v", kind, err)
	}
	got, integrity, err := DecodeSchema(raw)
	if err != nil {
		t.Fatalf("DecodeSchema: %v", err)
	}
	if integrity != IntegrityVerified || got.Checksum != s.Checksum {
		t.Errorf("integrity %s, checksum %s", integrity, got.Checksum)
	}
	// A data reader refuses it as the kind it is.
	if _, _, err := Decode(raw); err == nil || !strings.Contains(err.Error(), "not a data snapshot") {
		t.Errorf("data Decode of a schema snapshot: %v", err)
	}
}

func TestPartialAndDuplicateDefinitionsAreKeptVisible(t *testing.T) {
	ats := append(append([]string{}, testATs...),
		"( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeamAgain' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 1.2.3.4 NAME 'broken' SYNTAX",
	)
	s, err := BuildSchema(schemaCapture(captured), subschema(t, ats, testOCs, nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Completeness != SchemaPartial || len(s.Unparsed) != 2 || s.Counts.Unparsed != 2 {
		t.Fatalf("completeness %s, unparsed %+v", s.Completeness, s.Unparsed)
	}
	var reasons []string
	for _, u := range s.Unparsed {
		reasons = append(reasons, u.Error)
	}
	if !strings.Contains(strings.Join(reasons, "|"), "already has OID 1.3.6.1.4.1.99999.1.1") {
		t.Errorf("the duplicate is not reported as one: %v", reasons)
	}
	if _, _, err := DecodeSchema(encodeSchema(t, s)); err != nil {
		t.Errorf("a partial snapshot does not decode: %v", err)
	}
}

func TestUnrecognizedKeywordsAndHostileTextAreKept(t *testing.T) {
	rlo := string(rune(0x202e))
	ats := []string{
		"( 1.2.3.4 NAME 'odd' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 FUTURE-KEYWORD 'x' )",
		"( 1.2.3.5 NAME 'hostile' DESC 'report" + rlo + "txt.exe <script>' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 X-BAD 'a\tb' )",
		"( nsHost-oid NAME 'nsHost' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
	}
	s, err := BuildSchema(schemaCapture(captured), subschema(t, ats, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	byOID := map[string]SchemaAttributeType{}
	for _, at := range s.AttributeTypes {
		byOID[at.OID] = at
	}
	if got := byOID["1.2.3.4"].Unrecognized; len(got) != 1 || got[0] != "FUTURE-KEYWORD" {
		t.Errorf("unrecognized %v", got)
	}
	if got := byOID["1.2.3.5"].Desc; got != "report"+rlo+"txt.exe <script>" {
		t.Errorf("the description was altered: %q", got)
	}
	if _, ok := byOID["nsHost-oid"]; !ok {
		t.Error("a descriptor-style OID was not captured")
	}
	decoded, _, err := DecodeSchema(encodeSchema(t, s))
	if err != nil {
		t.Fatalf("DecodeSchema: %v", err)
	}
	if decoded.AttributeTypes[0].Desc != byOID[decoded.AttributeTypes[0].OID].Desc {
		t.Error("text changed on the round trip")
	}
}

func TestSchemaDecodeRefuses(t *testing.T) {
	good := buildTestSchema(t)
	raw := string(encodeSchema(t, good))

	// edited returns the document with a change applied and no checksum, so
	// the refusal comes from validation, not from the checksum.
	edited := func(edit func(s *SchemaSnapshot)) string {
		var s SchemaSnapshot
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Fatal(err)
		}
		edit(&s)
		s.Checksum = ""
		return string(encodeSchema(t, &s))
	}
	cases := []struct {
		name, input, code string
	}{
		{"not JSON", "nope", CodeNotSnapshot},
		{"a later version", strings.Replace(raw, `"version": 1`, `"version": 2`, 1), CodeUnsupportedVersion},
		{"a data snapshot", strings.Replace(raw, `"kind": "schema"`, `"kind": "data"`, 1), CodeInvalid},
		{"configuration", strings.Replace(raw, `"kind": "schema"`, `"kind": "config"`, 1), CodeInvalid},
		{"an unknown field", strings.Replace(raw, `"kind": "schema",`, `"kind": "schema", "apply": true,`, 1), CodeInvalid},
		{"trailing content", raw + "{}", CodeInvalid},
		{"a tampered checksum", strings.Replace(raw, `'Team'`, `'Tea m'`, 1), CodeInvalid},
		{"a tampered description with its fields", strings.Replace(strings.Replace(raw, `'Team'`, `'Teem'`, 1), `"desc": "Team"`, `"desc": "Teem"`, 1), CodeChecksumMismatch},
		{"a malformed OID", edited(func(s *SchemaSnapshot) { s.AttributeTypes[0].OID = "1..2 bad" }), CodeInvalid},
		{"a duplicate OID", edited(func(s *SchemaSnapshot) {
			s.AttributeTypes[1] = s.AttributeTypes[0]
		}), CodeInvalid},
		{"fields that disagree with the definition", edited(func(s *SchemaSnapshot) { s.AttributeTypes[0].SingleValue = false }), CodeInvalid},
		{"a definition that does not parse", edited(func(s *SchemaSnapshot) { s.AttributeTypes[0].Definition = "( 1.3.6.1.4.1.99999.1.1 NAME" }), CodeInvalid},
		{"a subschema entry that is not a DN", edited(func(s *SchemaSnapshot) { s.Source.SubschemaEntry = "not a dn" }), CodeInvalid},
		{"a completeness that is not true", edited(func(s *SchemaSnapshot) { s.Completeness = SchemaPartial }), CodeInvalid},
		{"counts that are not true", edited(func(s *SchemaSnapshot) { s.Counts.AttributeTypes++ }), CodeInvalid},
		{"a coverage claim", edited(func(s *SchemaSnapshot) { s.Coverage.Compared = append(s.Coverage.Compared, "dITStructureRules") }), CodeInvalid},
		{"an unparsed definition from nowhere", edited(func(s *SchemaSnapshot) {
			s.Unparsed = append(s.Unparsed, SchemaUnparsed{Attribute: "olcAccess", Definition: "x", Error: "y"})
			s.Counts.Unparsed++
			s.Completeness = SchemaPartial
		}), CodeInvalid},
		{"an inheritance cycle", edited(func(s *SchemaSnapshot) {
			for i := range s.ObjectClasses {
				if s.ObjectClasses[i].OID == "2.5.6.0" {
					s.ObjectClasses[i].Sup = []string{"alderEmployee"}
					s.ObjectClasses[i].Definition = "( 2.5.6.0 NAME 'top' SUP alderEmployee ABSTRACT MUST objectClass )"
				}
			}
		}), CodeInvalid},
		{"an attribute inheritance cycle", edited(func(s *SchemaSnapshot) {
			for i := range s.AttributeTypes {
				if s.AttributeTypes[i].OID == "2.5.4.41" {
					s.AttributeTypes[i].Sup = "cn"
					s.AttributeTypes[i].Definition = "( 2.5.4.41 NAME 'name' SUP cn EQUALITY caseIgnoreMatch SUBSTR caseIgnoreSubstringsMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{32768} )"
				}
			}
		}), CodeInvalid},
		{"a missing list", edited(func(s *SchemaSnapshot) { s.Context.NameForms = nil }), CodeInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.input == raw {
				t.Fatal("the case did not change the document")
			}
			_, _, err := DecodeSchema([]byte(tc.input))
			var se *Error
			if !errors.As(err, &se) {
				t.Fatalf("err = %v, want a snapshot error", err)
			}
			if se.Code != tc.code {
				t.Errorf("code %s (%s), want %s", se.Code, se.Detail, tc.code)
			}
		})
	}
}

func TestBuildSchemaRefusesTooManyDefinitions(t *testing.T) {
	ats := make([]string, MaxSchemaDefinitions+1)
	for i := range ats {
		ats[i] = fmt.Sprintf("( 1.2.3.%d NAME 'a%d' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )", i+1, i)
	}
	_, err := BuildSchema(schemaCapture(captured), subschema(t, ats, nil, nil))
	var se *Error
	if !errors.As(err, &se) || se.Code != CodeTooLarge {
		t.Errorf("err = %v, want too_large", err)
	}
}

func TestOIDOrderAndShape(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{
		{"1.2.3", "1.2.10", true}, {"2.5.4.41", "2.5.4.3", false}, {"1.2", "1.2.0", true},
		{"1.2.3", "nsHost-oid", true}, {"abc-oid", "Abd-oid", true},
	} {
		if got := OIDLess(c.a, c.b); got != c.less {
			t.Errorf("OIDLess(%s, %s) = %v", c.a, c.b, got)
		}
	}
	for s, want := range map[string]bool{"1.2.3": true, "2.5": true, "1": false, "1.02": false, "1..2": false, "a.b": false, "": false} {
		if IsNumericOID(s) != want {
			t.Errorf("IsNumericOID(%q) != %v", s, want)
		}
	}
}
