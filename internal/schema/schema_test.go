package schema

import (
	"slices"
	"testing"
)

// testSchema is a small but realistic slice of the schema both target servers
// publish: the person hierarchy, plus the harness's own auxiliary class.
func testSchema(t *testing.T) *Schema {
	t.Helper()
	return Load("cn=subschema", map[string][]string{
		"objectClasses": {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) MAY ( userPassword $ telephoneNumber $ seeAlso $ description ) )",
			"( 2.5.6.7 NAME 'organizationalPerson' SUP person STRUCTURAL MAY ( title $ ou $ l ) )",
			"( 2.16.840.1.113730.3.2.2 NAME 'inetOrgPerson' SUP organizationalPerson STRUCTURAL MAY ( mail $ uid $ displayName $ jpegPhoto ) )",
			"( 1.3.6.1.4.1.99999.2.1 NAME 'alderEmployee' SUP top AUXILIARY MUST ( alderTeam ) MAY ( alderNote $ alderOnCall ) )",
			"( 2.5.6.5 NAME 'organizationalUnit' SUP top STRUCTURAL MUST ou MAY description )",
		},
		"attributeTypes": {
			"( 2.5.4.0 NAME 'objectClass' EQUALITY objectIdentifierMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.41 NAME 'name' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{32768} )",
			"( 2.5.4.3 NAME ( 'cn' 'commonName' ) SUP name )",
			"( 2.5.4.4 NAME ( 'sn' 'surname' ) SUP name )",
			"( 2.5.4.11 NAME 'ou' SUP name )",
			"( 2.5.4.13 NAME 'description' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{1024} )",
			"( 2.5.4.35 NAME 'userPassword' EQUALITY octetStringMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.40{128} )",
			"( 0.9.2342.19200300.100.1.3 NAME 'mail' EQUALITY caseIgnoreIA5Match SYNTAX 1.3.6.1.4.1.1466.115.121.1.26{256} )",
			"( 2.5.4.49 NAME 'dn' EQUALITY distinguishedNameMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
			"( 0.9.2342.19200300.100.1.60 NAME 'jpegPhoto' SYNTAX 1.3.6.1.4.1.1466.115.121.1.28 )",
			"( 2.5.18.1 NAME 'createTimestamp' SYNTAX 1.3.6.1.4.1.1466.115.121.1.24 SINGLE-VALUE NO-USER-MODIFICATION USAGE directoryOperation )",
			"( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' SUP name SINGLE-VALUE )",
			"( 1.3.6.1.4.1.99999.1.2 NAME 'alderNote' EQUALITY caseExactMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.40 )",
			"( 1.3.6.1.4.1.99999.1.3 NAME 'alderOnCall' EQUALITY booleanMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.7 SINGLE-VALUE )",
		},
		"ldapSyntaxes": {
			"( 1.3.6.1.4.1.1466.115.121.1.15 DESC 'Directory String' )",
			"( 1.3.6.1.4.1.1466.115.121.1.7 DESC 'Boolean' )",
			"( 1.3.6.1.4.1.1466.115.121.1.40 DESC 'Octet String' )",
		},
		"matchingRules": {
			"( 2.5.13.2 NAME 'caseIgnoreMatch' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		},
	})
}

func TestLoadIndexesByNameAndOID(t *testing.T) {
	s := testSchema(t)
	if len(s.Errors) != 0 {
		t.Fatalf("Load reported errors: %v", s.Errors)
	}
	if s.IsEmpty() {
		t.Fatal("IsEmpty = true on a populated schema")
	}
	// Every one of these spellings must reach the same definition.
	for _, spelling := range []string{"cn", "CN", "commonName", "COMMONNAME", "2.5.4.3", "cn;lang-de"} {
		at := s.AttributeType(spelling)
		if at == nil {
			t.Fatalf("AttributeType(%q) = nil", spelling)
		}
		if at.OID != "2.5.4.3" {
			t.Errorf("AttributeType(%q).OID = %q, want 2.5.4.3", spelling, at.OID)
		}
	}
	if s.ObjectClass("INETORGPERSON") == nil {
		t.Error("object class lookup is case-sensitive")
	}
	if s.Syntax("1.3.6.1.4.1.1466.115.121.1.15") == nil {
		t.Error("syntax lookup by OID failed")
	}
	if s.MatchingRule("caseIgnoreMatch") == nil {
		t.Error("matching rule lookup by name failed")
	}
}

func TestLoadCollectsParseErrorsWithoutFailing(t *testing.T) {
	s := Load("cn=subschema", map[string][]string{
		"objectClasses": {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"this is not a definition",
			"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) )",
		},
	})
	if len(s.ObjectClasses) != 2 {
		t.Errorf("kept %d object classes, want the 2 that parsed", len(s.ObjectClasses))
	}
	if len(s.Errors) != 1 {
		t.Fatalf("collected %d errors, want 1", len(s.Errors))
	}
	if s.Errors[0].Attribute != "objectClasses" {
		t.Errorf("error attribute = %q", s.Errors[0].Attribute)
	}
	if s.Errors[0].Error() == "" {
		t.Error("ParseError.Error() is empty")
	}
}

func TestLoadMatchesAttributeNamesCaseInsensitively(t *testing.T) {
	// 389 DS and OpenLDAP disagree about the case of these attribute names in
	// the subschema entry, and neither is wrong.
	s := Load("cn=schema", map[string][]string{
		"OBJECTCLASSES":  {"( 2.5.6.0 NAME 'top' ABSTRACT )"},
		"attributetypes": {"( 2.5.4.3 NAME 'cn' )"},
	})
	if s.ObjectClass("top") == nil || s.AttributeType("cn") == nil {
		t.Error("Load did not match subschema attribute names case-insensitively")
	}
}

func TestSupers(t *testing.T) {
	s := testSchema(t)
	iop := s.ObjectClass("inetOrgPerson")
	got := s.Supers(iop)
	want := []string{"organizationalPerson", "person", "top"}
	if len(got) != len(want) {
		t.Fatalf("Supers = %v, want %v", names(got), want)
	}
	for i := range want {
		if got[i].Name() != want[i] {
			t.Errorf("Supers[%d] = %q, want %q", i, got[i].Name(), want[i])
		}
	}
}

func TestSupersSurvivesACycle(t *testing.T) {
	// A malformed schema must not hang the process. Both target servers reject
	// a cycle, but the schema arrives from a server Alder does not control.
	s := Load("cn=subschema", map[string][]string{
		"objectClasses": {
			"( 1.1 NAME 'a' SUP b STRUCTURAL )",
			"( 1.2 NAME 'b' SUP a STRUCTURAL )",
		},
	})
	got := s.Supers(s.ObjectClass("a"))
	if len(got) != 1 || got[0].Name() != "b" {
		t.Errorf("Supers = %v, want just [b] with the cycle broken", names(got))
	}
}

func TestSuperTypesAndInheritance(t *testing.T) {
	s := testSchema(t)
	cn := s.AttributeType("cn")

	// cn declares no syntax of its own; it inherits name's.
	if got := s.EffectiveSyntax(cn); got != "1.3.6.1.4.1.1466.115.121.1.15" {
		t.Errorf("EffectiveSyntax(cn) = %q, want the inherited Directory String OID", got)
	}
	if got := s.EffectiveEquality(cn); got != "caseIgnoreMatch" {
		t.Errorf("EffectiveEquality(cn) = %q, want the inherited caseIgnoreMatch", got)
	}
	if s.EffectiveSingleValue(cn) {
		t.Error("cn reported as single-valued")
	}
	if !s.EffectiveSingleValue(s.AttributeType("alderTeam")) {
		t.Error("alderTeam is SINGLE-VALUE and was not reported as such")
	}
	if !s.EffectiveNoUserModification(s.AttributeType("createTimestamp")) {
		t.Error("createTimestamp is NO-USER-MODIFICATION and was not reported as such")
	}
	if !s.EffectiveUsage(s.AttributeType("createTimestamp")).Operational() {
		t.Error("createTimestamp should be operational")
	}
}

func TestRequirements(t *testing.T) {
	s := testSchema(t)
	req := s.Requirements([]string{"top", "person", "organizationalPerson", "inetOrgPerson", "alderEmployee"})

	if !slices.Contains(req.Must, "cn") || !slices.Contains(req.Must, "sn") || !slices.Contains(req.Must, "objectClass") {
		t.Errorf("Must = %v, want cn, sn and objectClass", req.Must)
	}
	if !slices.Contains(req.Must, "alderTeam") {
		t.Errorf("Must = %v, want alderTeam from the auxiliary class", req.Must)
	}
	if !slices.Contains(req.May, "mail") || !slices.Contains(req.May, "title") || !slices.Contains(req.May, "alderNote") {
		t.Errorf("May = %v, want mail, title and alderNote", req.May)
	}
	// An attribute required by one class is required, even where another class
	// lists it as optional.
	for _, m := range req.Must {
		if slices.Contains(req.May, m) {
			t.Errorf("%q appears in both Must and May", m)
		}
	}
	if req.Structural == nil || req.Structural.Name() != "inetOrgPerson" {
		t.Errorf("Structural = %v, want inetOrgPerson as the most specific", req.Structural)
	}
	if len(req.Unknown) != 0 {
		t.Errorf("Unknown = %v, want none", req.Unknown)
	}
}

func TestRequirementsReportsUnknownClasses(t *testing.T) {
	s := testSchema(t)
	req := s.Requirements([]string{"person", "notAClass"})
	if !slices.Contains(req.Unknown, "notAClass") {
		t.Errorf("Unknown = %v, want notAClass", req.Unknown)
	}
	if !slices.Contains(req.Must, "cn") {
		t.Error("an unknown class should not lose the requirements of the known ones")
	}
}

func TestRequirementsCanonicalisesNames(t *testing.T) {
	s := testSchema(t)
	// The class names commonName and surname in MUST; the editor must see the
	// schema's preferred spellings so its field keys match the entry's.
	req := s.Requirements([]string{"person"})
	if !slices.Contains(req.Must, "cn") || slices.Contains(req.Must, "commonName") {
		t.Errorf("Must = %v, want the canonical spelling cn", req.Must)
	}
}

func TestKindOf(t *testing.T) {
	s := testSchema(t)
	tests := []struct {
		attr          string
		wantKind      ValueKind
		wantSingle    bool
		wantReadOnly  bool
		wantSensitive bool
		wantKnown     bool
	}{
		{attr: "cn", wantKind: KindString, wantKnown: true},
		{attr: "description", wantKind: KindString, wantKnown: true},
		{attr: "alderOnCall", wantKind: KindBoolean, wantSingle: true, wantKnown: true},
		{attr: "alderNote", wantKind: KindBinary, wantKnown: true},
		{attr: "jpegPhoto", wantKind: KindImage, wantKnown: true},
		{attr: "dn", wantKind: KindDN, wantKnown: true},
		{attr: "userPassword", wantKind: KindPassword, wantSensitive: true, wantKnown: true},
		{attr: "createTimestamp", wantKind: KindTime, wantSingle: true, wantReadOnly: true, wantKnown: true},
		{attr: "objectClass", wantKind: KindOID, wantKnown: true},
		{attr: "notInTheSchema", wantKind: KindString, wantKnown: false},
	}
	for _, tt := range tests {
		t.Run(tt.attr, func(t *testing.T) {
			got := s.KindOf(tt.attr)
			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tt.wantKind)
			}
			if got.SingleValue != tt.wantSingle {
				t.Errorf("SingleValue = %v, want %v", got.SingleValue, tt.wantSingle)
			}
			if got.ReadOnly != tt.wantReadOnly {
				t.Errorf("ReadOnly = %v, want %v", got.ReadOnly, tt.wantReadOnly)
			}
			if got.Sensitive != tt.wantSensitive {
				t.Errorf("Sensitive = %v, want %v", got.Sensitive, tt.wantSensitive)
			}
			if got.Known != tt.wantKnown {
				t.Errorf("Known = %v, want %v", got.Known, tt.wantKnown)
			}
		})
	}
}

func TestKindOfBinaryOptionOverridesSyntax(t *testing.T) {
	s := testSchema(t)
	// cn is a Directory String, but ";binary" means the value arrives as bytes.
	if got := s.KindOf("cn;binary").Kind; got != KindBinary {
		t.Errorf("KindOf(cn;binary) = %q, want binary", got)
	}
	if got := s.KindOf("cn;binary").Name; got != "cn" {
		t.Errorf("Name = %q, want the option stripped", got)
	}
}

func TestKindOfInheritsTheAdvisoryLength(t *testing.T) {
	s := testSchema(t)
	// cn has no SYNTAX of its own, so its length comes from name's {32768}.
	if got := s.KindOf("cn").MaxLength; got != 32768 {
		t.Errorf("MaxLength = %d, want the inherited 32768", got)
	}
}

func TestIsSensitive(t *testing.T) {
	for _, attr := range []string{"userPassword", "USERPASSWORD", "userPassword;binary", "nsslapd-rootpw"} {
		if !IsSensitive(attr) {
			t.Errorf("IsSensitive(%q) = false, want true", attr)
		}
	}
	if IsSensitive("cn") {
		t.Error("IsSensitive(cn) = true")
	}
	if len(SensitiveAttributeNames()) == 0 {
		t.Error("SensitiveAttributeNames is empty")
	}
}

func TestUsedByCrossLinks(t *testing.T) {
	s := testSchema(t)
	must, may := s.UsedBy("cn")
	if !slices.Contains(must, s.ObjectClass("person")) {
		t.Errorf("UsedBy(cn) must = %v, want person", names(must))
	}
	_, mayMail := s.UsedBy("mail")
	if !slices.Contains(mayMail, s.ObjectClass("inetOrgPerson")) {
		t.Errorf("UsedBy(mail) may = %v, want inetOrgPerson", names(mayMail))
	}
	if len(may) > 0 && slices.Contains(may, s.ObjectClass("person")) {
		t.Error("person lists cn in MUST, so it must not also appear in may")
	}
}

func TestSubclassesOf(t *testing.T) {
	s := testSchema(t)
	subs := s.SubclassesOf(s.ObjectClass("person"))
	if !slices.Contains(subs, s.ObjectClass("organizationalPerson")) {
		t.Errorf("SubclassesOf(person) = %v, want organizationalPerson", names(subs))
	}
	if slices.Contains(subs, s.ObjectClass("inetOrgPerson")) {
		t.Error("SubclassesOf returns direct subclasses only")
	}
}

func TestStructuralAndAuxiliaryClasses(t *testing.T) {
	s := testSchema(t)
	if !slices.Contains(s.StructuralClasses(), s.ObjectClass("inetOrgPerson")) {
		t.Error("inetOrgPerson missing from StructuralClasses")
	}
	if !slices.Contains(s.AuxiliaryClasses(), s.ObjectClass("alderEmployee")) {
		t.Error("alderEmployee missing from AuxiliaryClasses")
	}
	if slices.Contains(s.StructuralClasses(), s.ObjectClass("top")) {
		t.Error("top is abstract and must not be offered as structural")
	}
}

func TestAttributesWithSyntax(t *testing.T) {
	s := testSchema(t)
	got := s.AttributesWithSyntax("1.3.6.1.4.1.1466.115.121.1.7")
	if len(got) != 1 || got[0].Name() != "alderOnCall" {
		t.Errorf("AttributesWithSyntax(Boolean) = %v, want [alderOnCall]", attrNames(got))
	}
}

func TestSyntaxLabel(t *testing.T) {
	s := testSchema(t)
	if got := s.SyntaxLabel("1.3.6.1.4.1.1466.115.121.1.15"); got != "Directory String" {
		t.Errorf("SyntaxLabel = %q, want the server's DESC", got)
	}
	// Not published by this schema, but in the built-in table.
	if got := s.SyntaxLabel("1.3.6.1.4.1.1466.115.121.1.24"); got != "Generalized Time" {
		t.Errorf("SyntaxLabel = %q, want the built-in fallback", got)
	}
	if got := s.SyntaxLabel("9.9.9"); got != "9.9.9" {
		t.Errorf("SyntaxLabel = %q, want the OID itself", got)
	}
}

func TestBaseNameAndOptions(t *testing.T) {
	if got := BaseName("userCertificate;binary"); got != "userCertificate" {
		t.Errorf("BaseName = %q", got)
	}
	if got := Options("cn;lang-de;binary"); !equalStrings(got, []string{"lang-de", "binary"}) {
		t.Errorf("Options = %v", got)
	}
	if Options("cn") != nil {
		t.Error("Options on a bare attribute should be nil")
	}
}

func TestCanonicalAttrNamePreservesOptions(t *testing.T) {
	s := testSchema(t)
	if got := s.CanonicalAttrName("COMMONNAME;binary"); got != "cn;binary" {
		t.Errorf("CanonicalAttrName = %q, want cn;binary", got)
	}
	if got := s.CanonicalAttrName("notInTheSchema"); got != "notInTheSchema" {
		t.Errorf("CanonicalAttrName = %q, want the input unchanged", got)
	}
}

func TestCounts(t *testing.T) {
	s := testSchema(t)
	c := s.Counts()
	if c.ObjectClasses != 6 || c.AttributeTypes != 14 || c.Syntaxes != 3 {
		t.Errorf("Counts = %+v", c)
	}
}

func names(list []*ObjectClass) []string {
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.Name()
	}
	return out
}

func attrNames(list []*AttributeType) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.Name()
	}
	return out
}
