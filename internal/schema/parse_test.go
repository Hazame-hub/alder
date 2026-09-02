package schema

import (
	"strings"
	"testing"
)

func TestParseObjectClass(t *testing.T) {
	tests := []struct {
		name string
		def  string
		want ObjectClass
	}{
		{
			name: "inetOrgPerson as OpenLDAP publishes it",
			def: "( 2.16.840.1.113730.3.2.2 NAME 'inetOrgPerson' DESC 'RFC2798: Internet Organizational Person' " +
				"SUP organizationalPerson STRUCTURAL MAY ( audio $ businessCategory $ carLicense $ departmentNumber $ " +
				"displayName $ employeeNumber $ employeeType $ givenName $ homePhone $ homePostalAddress $ initials $ " +
				"jpegPhoto $ labeledURI $ mail $ manager $ mobile $ o $ pager $ photo $ roomNumber $ secretary $ uid $ " +
				"userCertificate $ x500uniqueIdentifier $ preferredLanguage $ userSMIMECertificate $ userPKCS12 ) )",
			want: ObjectClass{
				OID:        "2.16.840.1.113730.3.2.2",
				Names:      []string{"inetOrgPerson"},
				Desc:       "RFC2798: Internet Organizational Person",
				SuperNames: []string{"organizationalPerson"},
				Kind:       KindStructural,
			},
		},
		{
			name: "abstract with multiple names and an extension",
			def:  "( 2.5.6.0 NAME ( 'top' 'topClass' ) ABSTRACT MUST objectClass X-ORIGIN 'RFC 4512' )",
			want: ObjectClass{
				OID:   "2.5.6.0",
				Names: []string{"top", "topClass"},
				Kind:  KindAbstract,
				Must:  []string{"objectClass"},
			},
		},
		{
			name: "auxiliary, obsolete, several superiors",
			def:  "( 1.2.3.4 NAME 'alderEmployee' OBSOLETE SUP ( top $ person ) AUXILIARY MUST ( alderTeam ) MAY alderNote )",
			want: ObjectClass{
				OID:        "1.2.3.4",
				Names:      []string{"alderEmployee"},
				Obsolete:   true,
				SuperNames: []string{"top", "person"},
				Kind:       KindAuxiliary,
				Must:       []string{"alderTeam"},
				May:        []string{"alderNote"},
			},
		},
		{
			name: "no kind keyword defaults to structural",
			def:  "( 1.2.3.5 NAME 'implicit' )",
			want: ObjectClass{OID: "1.2.3.5", Names: []string{"implicit"}, Kind: KindStructural},
		},
		{
			name: "escaped apostrophe in a description",
			def:  `( 1.2.3.6 NAME 'quoted' DESC 'it\27s escaped' )`,
			want: ObjectClass{OID: "1.2.3.6", Names: []string{"quoted"}, Desc: "it's escaped", Kind: KindStructural},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseObjectClass(tt.def)
			if err != nil {
				t.Fatalf("ParseObjectClass: %v", err)
			}
			if got.OID != tt.want.OID {
				t.Errorf("OID = %q, want %q", got.OID, tt.want.OID)
			}
			if !equalStrings(got.Names, tt.want.Names) {
				t.Errorf("Names = %v, want %v", got.Names, tt.want.Names)
			}
			if got.Desc != tt.want.Desc {
				t.Errorf("Desc = %q, want %q", got.Desc, tt.want.Desc)
			}
			if got.Obsolete != tt.want.Obsolete {
				t.Errorf("Obsolete = %v, want %v", got.Obsolete, tt.want.Obsolete)
			}
			if !equalStrings(got.SuperNames, tt.want.SuperNames) {
				t.Errorf("SuperNames = %v, want %v", got.SuperNames, tt.want.SuperNames)
			}
			if got.Kind != tt.want.Kind {
				t.Errorf("Kind = %v, want %v", got.Kind, tt.want.Kind)
			}
			if tt.want.Must != nil && !equalStrings(got.Must, tt.want.Must) {
				t.Errorf("Must = %v, want %v", got.Must, tt.want.Must)
			}
			if tt.want.May != nil && !equalStrings(got.May, tt.want.May) {
				t.Errorf("May = %v, want %v", got.May, tt.want.May)
			}
			if got.Raw != strings.TrimSpace(tt.def) {
				t.Errorf("Raw did not round-trip the definition")
			}
		})
	}
}

func TestParseObjectClassMAYList(t *testing.T) {
	def := "( 2.16.840.1.113730.3.2.2 NAME 'inetOrgPerson' SUP organizationalPerson STRUCTURAL " +
		"MAY ( audio $ businessCategory $ carLicense ) )"
	oc, err := ParseObjectClass(def)
	if err != nil {
		t.Fatalf("ParseObjectClass: %v", err)
	}
	want := []string{"audio", "businessCategory", "carLicense"}
	if !equalStrings(oc.May, want) {
		t.Errorf("May = %v, want %v", oc.May, want)
	}
}

func TestParseAttributeType(t *testing.T) {
	def := "( 2.5.4.3 NAME ( 'cn' 'commonName' ) DESC 'RFC4519: common name(s)' SUP name " +
		"EQUALITY caseIgnoreMatch SUBSTR caseIgnoreSubstringsMatch " +
		"SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{32768} X-ORIGIN 'RFC 4519' )"
	at, err := ParseAttributeType(def)
	if err != nil {
		t.Fatalf("ParseAttributeType: %v", err)
	}
	if at.OID != "2.5.4.3" {
		t.Errorf("OID = %q", at.OID)
	}
	if !equalStrings(at.Names, []string{"cn", "commonName"}) {
		t.Errorf("Names = %v", at.Names)
	}
	if at.SuperName != "name" {
		t.Errorf("SuperName = %q", at.SuperName)
	}
	if at.Equality != "caseIgnoreMatch" {
		t.Errorf("Equality = %q", at.Equality)
	}
	if at.Substr != "caseIgnoreSubstringsMatch" {
		t.Errorf("Substr = %q", at.Substr)
	}
	if at.Syntax != "1.3.6.1.4.1.1466.115.121.1.15" {
		t.Errorf("Syntax = %q, want the OID without the length suffix", at.Syntax)
	}
	if at.SyntaxLen != 32768 {
		t.Errorf("SyntaxLen = %d, want 32768", at.SyntaxLen)
	}
	if got := at.Extensions["X-ORIGIN"]; !equalStrings(got, []string{"RFC 4519"}) {
		t.Errorf("X-ORIGIN = %v", got)
	}
}

func TestParseAttributeTypeFlags(t *testing.T) {
	def := "( 2.5.18.1 NAME 'createTimestamp' EQUALITY generalizedTimeMatch " +
		"ORDERING generalizedTimeOrderingMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.24 " +
		"SINGLE-VALUE NO-USER-MODIFICATION USAGE directoryOperation )"
	at, err := ParseAttributeType(def)
	if err != nil {
		t.Fatalf("ParseAttributeType: %v", err)
	}
	if !at.SingleValue {
		t.Error("SingleValue = false, want true")
	}
	if !at.NoUserModification {
		t.Error("NoUserModification = false, want true")
	}
	if at.Usage != UsageDirectoryOperation {
		t.Errorf("Usage = %v, want directoryOperation", at.Usage)
	}
	if !at.Usage.Operational() {
		t.Error("directoryOperation should report as operational")
	}
}

func TestParseSyntaxAndMatchingRule(t *testing.T) {
	sy, err := ParseSyntax("( 1.3.6.1.4.1.1466.115.121.1.15 DESC 'Directory String' )")
	if err != nil {
		t.Fatalf("ParseSyntax: %v", err)
	}
	if sy.OID != "1.3.6.1.4.1.1466.115.121.1.15" || sy.Desc != "Directory String" {
		t.Errorf("got %+v", sy)
	}

	mr, err := ParseMatchingRule("( 2.5.13.2 NAME 'caseIgnoreMatch' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")
	if err != nil {
		t.Fatalf("ParseMatchingRule: %v", err)
	}
	if mr.Name() != "caseIgnoreMatch" || mr.Syntax != "1.3.6.1.4.1.1466.115.121.1.15" {
		t.Errorf("got %+v", mr)
	}
}

func TestParseMatchingRuleUse(t *testing.T) {
	mru, err := ParseMatchingRuleUse("( 2.5.13.2 NAME 'caseIgnoreMatch' APPLIES ( cn $ sn $ description ) )")
	if err != nil {
		t.Fatalf("ParseMatchingRuleUse: %v", err)
	}
	if !equalStrings(mru.Applies, []string{"cn", "sn", "description"}) {
		t.Errorf("Applies = %v", mru.Applies)
	}
}

func TestParseNameForm(t *testing.T) {
	nf, err := ParseNameForm("( 1.2.3.7 NAME 'personNameForm' OC person MUST ( cn ) MAY ( sn ) )")
	if err != nil {
		t.Fatalf("ParseNameForm: %v", err)
	}
	if nf.ObjectClass != "person" || !equalStrings(nf.Must, []string{"cn"}) {
		t.Errorf("got %+v", nf)
	}
}

func TestParseDITContentRule(t *testing.T) {
	cr, err := ParseDITContentRule("( 2.16.840.1.113730.3.2.2 NAME 'inetOrgPersonRule' " +
		"AUX ( alderEmployee ) MUST ( cn ) MAY ( description ) NOT ( audio ) )")
	if err != nil {
		t.Fatalf("ParseDITContentRule: %v", err)
	}
	if !equalStrings(cr.Aux, []string{"alderEmployee"}) || !equalStrings(cr.Not, []string{"audio"}) {
		t.Errorf("got %+v", cr)
	}
}

// TestParseTolerance covers the shapes real servers emit that RFC 4512 does not
// strictly permit. Each one has broken a schema browser somewhere.
func TestParseTolerance(t *testing.T) {
	tests := []struct {
		name string
		def  string
	}{
		{"no space inside the parens", "(1.2.3.8 NAME 'tight' STRUCTURAL)"},
		{"newlines instead of spaces", "( 1.2.3.9\n  NAME 'folded'\n  STRUCTURAL )"},
		{"a non-numeric leading OID", "( alderThing NAME 'named-oid' STRUCTURAL )"},
		{"an unknown keyword with a quoted value", "( 1.2.3.10 NAME 'unknown-kw' FUTURE-THING 'value' STRUCTURAL )"},
		{"an unknown keyword with a list value", "( 1.2.3.11 NAME 'unknown-list' FUTURE-THING ( a $ b ) STRUCTURAL )"},
		{"a quoted OID list", "( 1.2.3.12 NAME 'quoted-list' MUST ( 'cn' $ 'sn' ) )"},
		{"trailing whitespace", "( 1.2.3.13 NAME 'trailing' STRUCTURAL )   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oc, err := ParseObjectClass(tt.def)
			if err != nil {
				t.Fatalf("ParseObjectClass(%q): %v", tt.def, err)
			}
			if oc.OID == "" {
				t.Error("OID is empty")
			}
		})
	}
}

func TestParseRejectsNonDefinitions(t *testing.T) {
	tests := []string{
		"",
		"not a definition",
		"( 1.2.3.14 NAME 'unterminated",
		"1.2.3.15 NAME 'no-parens'",
		// Found by FuzzParseObjectClass: an empty quoted OID, and a definition
		// that ends before its closing parenthesis. Both used to parse "fine"
		// and yield a definition with no OID and no name.
		"(''",
		"( 1.2.3.16 NAME 'no-close'",
		"( '' )",
	}
	for _, def := range tests {
		if _, err := ParseObjectClass(def); err == nil {
			t.Errorf("ParseObjectClass(%q) = nil error, want an error", def)
		}
	}
}

func TestSplitNOIDLen(t *testing.T) {
	tests := []struct {
		in      string
		wantOID string
		wantLen int
	}{
		{"1.3.6.1.4.1.1466.115.121.1.15", "1.3.6.1.4.1.1466.115.121.1.15", 0},
		{"1.3.6.1.4.1.1466.115.121.1.15{32768}", "1.3.6.1.4.1.1466.115.121.1.15", 32768},
		{"1.3.6.1.4.1.1466.115.121.1.15{bad}", "1.3.6.1.4.1.1466.115.121.1.15", 0},
		{"1.3.6.1.4.1.1466.115.121.1.15{", "1.3.6.1.4.1.1466.115.121.1.15{", 0},
	}
	for _, tt := range tests {
		gotOID, gotLen := splitNOIDLen(tt.in)
		if gotOID != tt.wantOID || gotLen != tt.wantLen {
			t.Errorf("splitNOIDLen(%q) = (%q, %d), want (%q, %d)", tt.in, gotOID, gotLen, tt.wantOID, tt.wantLen)
		}
	}
}

// FuzzParseObjectClass asserts the parser never panics and never loops, whatever
// a server sends. A schema definition is attacker-influenced input in exactly
// the same way a filter is: it arrives over the network from a server the user
// pointed us at, and this process must survive it.
func FuzzParseObjectClass(f *testing.F) {
	seeds := []string{
		"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
		"( 1.2.3 NAME ( 'a' 'b' ) SUP ( x $ y ) AUXILIARY MAY ( p $ q ) X-ORIGIN 'z' )",
		`( 1.2.3 DESC 'it\27s' )`,
		"(((((",
		"( 1.2.3 NAME '",
		"( 1.2.3 NAME 'a' " + strings.Repeat("SUP b ", 200) + ")",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, def string) {
		oc, err := ParseObjectClass(def)
		if err != nil {
			return
		}
		// A successful parse must produce something the rest of the program can
		// use: Name never panics and never returns empty, since it falls back
		// to the OID and the OID is required to reach here.
		if oc.Name() == "" {
			t.Errorf("ParseObjectClass(%q) succeeded with an empty name and OID", def)
		}
	})
}

// FuzzParseAttributeType is FuzzParseObjectClass for the other half of the
// grammar, where the interesting input is the SYNTAX length suffix.
func FuzzParseAttributeType(f *testing.F) {
	seeds := []string{
		"( 2.5.4.3 NAME 'cn' SUP name SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{32768} )",
		"( 1.1 SYNTAX 1.2{99999999999999999999} )",
		"( 1.1 USAGE dSAOperation SINGLE-VALUE NO-USER-MODIFICATION COLLECTIVE )",
		"( 1.1 SYNTAX {} )",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, def string) {
		at, err := ParseAttributeType(def)
		if err != nil {
			return
		}
		if at.SyntaxLen < 0 {
			t.Errorf("ParseAttributeType(%q) produced a negative SyntaxLen %d", def, at.SyntaxLen)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
