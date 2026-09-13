package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

func testSchema(t testing.TB) *schema.Schema {
	t.Helper()
	return schema.Load("cn=schema", map[string][]string{
		"objectClasses": {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) MAY ( userPassword $ description $ telephoneNumber ) )",
			"( 2.16.840.1.113730.3.2.2 NAME 'inetOrgPerson' SUP person STRUCTURAL MAY ( uid $ mail $ title ) )",
			"( 2.5.6.9 NAME 'groupOfNames' SUP top STRUCTURAL MUST ( member $ cn ) )",
		},
		"attributeTypes": {
			"( 2.5.4.0 NAME 'objectClass' EQUALITY objectIdentifierMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.41 NAME 'name' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.3 NAME ( 'cn' 'commonName' ) SUP name )",
			"( 2.5.4.4 NAME 'sn' SUP name )",
			"( 2.5.4.12 NAME 'title' SUP name )",
			"( 2.5.4.13 NAME 'description' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 0.9.2342.19200300.100.1.1 NAME 'uid' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 0.9.2342.19200300.100.1.3 NAME 'mail' EQUALITY caseIgnoreIA5Match SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
			"( 2.5.4.20 NAME 'telephoneNumber' EQUALITY telephoneNumberMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.50 )",
			"( 2.5.4.31 NAME 'member' EQUALITY distinguishedNameMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
			"( 2.5.4.35 NAME 'userPassword' EQUALITY octetStringMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.40 )",
			"( 2.5.18.2 NAME 'modifyTimestamp' EQUALITY generalizedTimeMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.24 SINGLE-VALUE NO-USER-MODIFICATION USAGE directoryOperation )",
			"( 1.3.6.1.1.16.4 NAME 'entryUUID' EQUALITY uuidMatch SYNTAX 1.3.6.1.1.16.1 SINGLE-VALUE NO-USER-MODIFICATION USAGE directoryOperation )",
		},
	})
}

func entry(t testing.TB, d string, pairs ...[]string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(dn.MustParse(d))
	for _, p := range pairs {
		values := make([][]byte, 0, len(p)-1)
		for _, v := range p[1:] {
			values = append(values, []byte(v))
		}
		e.Set(p[0], values)
	}
	return e
}

var captured = time.Date(2026, 9, 13, 22, 5, 0, 0, time.UTC)

func capture(ops bool) Capture {
	return Capture{Base: dn.MustParse("dc=alder,dc=test"), Scope: "sub", Filter: "(objectClass=*)",
		Vendor: "389 Project", VendorVersion: "2.6.1", Operational: ops, CreatedAt: captured}
}

func fixture(t testing.TB) []*directory.Entry {
	return []*directory.Entry{
		entry(t, "uid=bob,ou=people,dc=alder,dc=test",
			[]string{"objectClass", "top", "person", "inetOrgPerson"},
			[]string{"uid", "bob"}, []string{"sn", "B"}, []string{"cn", "Bob"},
			[]string{"userPassword", "{SSHA}c2VjcmV0LWJvYg=="},
			[]string{"modifyTimestamp", "20260913120000Z"},
			[]string{"entryUUID", "5e7d4c1e-0000-4000-8000-000000000002"}),
		entry(t, "ou=people,dc=alder,dc=test", []string{"objectClass", "top", "organizationalUnit"}, []string{"ou", "people"}),
		entry(t, "dc=alder,dc=test", []string{"objectClass", "top", "domain"}, []string{"dc", "alder"}),
		entry(t, "cn=admins,ou=groups,dc=alder,dc=test",
			[]string{"objectClass", "top", "groupOfNames"}, []string{"cn", "admins"},
			[]string{"member", "uid=bob,ou=people,dc=alder,dc=test", "uid=alice,ou=people,dc=alder,dc=test"}),
		entry(t, "uid=alice,ou=people,dc=alder,dc=test",
			[]string{"objectClass", "top", "person", "inetOrgPerson"},
			[]string{"uid", "alice"}, []string{"sn", "A"}, []string{"cn", "Alice"},
			[]string{"title", "Engineer", "Architect"}),
		entry(t, "ou=groups,dc=alder,dc=test", []string{"objectClass", "top", "organizationalUnit"}, []string{"ou", "groups"}),
	}
}

func encode(t testing.TB, s *Snapshot) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, s); err != nil {
		t.Fatalf("encoding: %v", err)
	}
	return buf.Bytes()
}

func TestTheSameStateIsTheSameDocument(t *testing.T) {
	a, err := Build(capture(false), testSchema(t), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	// The same entries in another order, attributes and values shuffled.
	shuffled := fixture(t)
	for i, j := 0, len(shuffled)-1; i < j; i, j = i+1, j-1 {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}
	for _, e := range shuffled {
		for i, j := 0, len(e.Order)-1; i < j; i, j = i+1, j-1 {
			e.Order[i], e.Order[j] = e.Order[j], e.Order[i]
		}
		for name, values := range e.Attributes {
			for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
				values[i], values[j] = values[j], values[i]
			}
			e.Attributes[name] = values
		}
	}
	b, err := Build(capture(false), testSchema(t), shuffled)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encode(t, a), encode(t, b)) {
		t.Fatalf("the same state produced different documents:\n%s\n---\n%s", encode(t, a), encode(t, b))
	}

	// A later capture differs only in its creation time; the checksum does not.
	later := capture(false)
	later.CreatedAt = captured.Add(time.Hour)
	c, err := Build(later, testSchema(t), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Checksum != a.Checksum {
		t.Error("the checksum depends on the capture time")
	}
	if c.CreatedAt == a.CreatedAt {
		t.Error("creation time not recorded")
	}
}

func TestCanonicalOrder(t *testing.T) {
	s, err := Build(capture(false), testSchema(t), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	var dns []string
	for _, e := range s.Entries {
		dns = append(dns, e.DN)
	}
	want := []string{
		"dc=alder,dc=test",
		"ou=groups,dc=alder,dc=test",
		"cn=admins,ou=groups,dc=alder,dc=test",
		"ou=people,dc=alder,dc=test",
		"uid=alice,ou=people,dc=alder,dc=test",
		"uid=bob,ou=people,dc=alder,dc=test",
	}
	if strings.Join(dns, "|") != strings.Join(want, "|") {
		t.Errorf("entry order:\n got %v\nwant %v", dns, want)
	}
	alice, _, _ := s.EntryByKey("uid=alice,ou=people,dc=alder,dc=test")
	var names []string
	for _, a := range alice.Attributes {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "objectClass,cn,sn,title,uid" {
		t.Errorf("attribute order = %v", names)
	}
	title := alice.Attributes[3]
	if *title.Values[0].Text != "Architect" || *title.Values[1].Text != "Engineer" {
		t.Errorf("values not in key order: %+v", title.Values)
	}
}

func TestSensitiveValuesAreNeverCaptured(t *testing.T) {
	s, err := Build(capture(true), testSchema(t), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	doc := encode(t, s)
	secret := "{SSHA}c2VjcmV0LWJvYg=="
	sum := sha256.Sum256([]byte(secret))
	for _, leak := range []string{secret, "c2VjcmV0LWJvYg", hex.EncodeToString(sum[:]),
		base64.StdEncoding.EncodeToString([]byte(secret))} {
		if bytes.Contains(doc, []byte(leak)) {
			t.Fatalf("the snapshot carries %q", leak)
		}
	}
	bob, _, _ := s.EntryByKey("uid=bob,ou=people,dc=alder,dc=test")
	for _, a := range bob.Attributes {
		if a.Name == "userPassword" && (a.Withheld != 1 || len(a.Values) != 0) {
			t.Errorf("userPassword = %+v, want withheld 1 and no values", a)
		}
	}
}

func TestOperationalAttributesAreExcludedUnlessAsked(t *testing.T) {
	without, _ := Build(capture(false), testSchema(t), fixture(t))
	with, _ := Build(capture(true), testSchema(t), fixture(t))
	has := func(s *Snapshot, name string) bool {
		bob, _, _ := s.EntryByKey("uid=bob,ou=people,dc=alder,dc=test")
		for _, a := range bob.Attributes {
			if strings.EqualFold(a.Name, name) {
				return true
			}
		}
		return false
	}
	if has(without, "modifyTimestamp") || has(without, "entryUUID") {
		t.Error("operational attributes captured by default")
	}
	if !has(with, "modifyTimestamp") {
		t.Error("operational attributes missing when asked for")
	}
	for _, s := range []*Snapshot{without, with} {
		bob, _, _ := s.EntryByKey("uid=bob,ou=people,dc=alder,dc=test")
		if bob.ID != "entryUUID=5e7d4c1e-0000-4000-8000-000000000002" {
			t.Errorf("id = %q; identity is recorded either way", bob.ID)
		}
	}
	if strings.Join(without.Excluded, ",") != "operational-attributes,sensitive-values" {
		t.Errorf("excluded = %v", without.Excluded)
	}
	if strings.Join(with.Excluded, ",") != "sensitive-values" {
		t.Errorf("excluded = %v", with.Excluded)
	}
}

func TestDecodeRoundTripsAndVerifies(t *testing.T) {
	s, _ := Build(capture(false), testSchema(t), fixture(t))
	doc := encode(t, s)
	back, integrity, err := Decode(doc)
	if err != nil {
		t.Fatalf("decoding its own output: %v", err)
	}
	if integrity != IntegrityVerified {
		t.Errorf("integrity = %s", integrity)
	}
	if !bytes.Equal(encode(t, back), doc) {
		t.Error("decode and encode is not the identity")
	}
}

func codeOf(err error) string {
	var se *Error
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

func TestDecodeRefusesWhatIsNotAUsableSnapshot(t *testing.T) {
	s, _ := Build(capture(false), testSchema(t), fixture(t))
	good := string(encode(t, s))
	edit := func(old, new string) string {
		if !strings.Contains(good, old) {
			t.Fatalf("fixture lacks %q", old)
		}
		return strings.Replace(good, old, new, 1)
	}
	cases := []struct {
		name, doc, code string
	}{
		{"not JSON", "dn: uid=bob,dc=alder,dc=test\n", CodeNotSnapshot},
		{"other JSON", `{"entries": []}`, CodeNotSnapshot},
		{"future version", edit(`"version": 1`, `"version": 2`), CodeUnsupportedVersion},
		{"version zero", edit(`"version": 1`, `"version": 0`), CodeUnsupportedVersion},
		{"unknown field", edit(`"kind": "data",`, `"kind": "data", "colour": "blue",`), CodeInvalid},
		{"schema kind", edit(`"kind": "data"`, `"kind": "schema"`), CodeInvalid},
		{"partial", edit(`"completeness": "complete"`, `"completeness": "partial"`), CodeInvalid},
		{"malformed DN", edit(`"dn": "uid=bob,ou=people,dc=alder,dc=test"`, `"dn": "uid=bob,,dc=alder"`), CodeInvalid},
		{"outside the scope", edit(`"dn": "uid=bob,ou=people,dc=alder,dc=test"`, `"dn": "uid=bob,dc=elsewhere"`), CodeInvalid},
		{"bad base64", edit(`"text": "Bob"`, `"base64": "%%%"`), CodeInvalid},
		{"both encodings", edit(`"text": "Bob"`, `"text": "Bob", "base64": "Qm9i"`), CodeInvalid},
		{"a sensitive value", edit(`"withheld": 1`, `"values": [{"text": "{SSHA}x"}]`), CodeInvalid},
		{"wrong count", edit(`"entryCount": 6`, `"entryCount": 7`), CodeInvalid},
		{"edited content", edit(`"text": "Bob"`, `"text": "Robert"`), CodeChecksumMismatch},
		{"trailing content", good + `{}`, CodeInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Decode([]byte(tc.doc)); codeOf(err) != tc.code {
				t.Errorf("err = %v, want code %s", err, tc.code)
			}
		})
	}

	t.Run("without a checksum it is readable and unverified", func(t *testing.T) {
		doc := edit(`"checksum": "`+s.Checksum+`",`, "")
		_, integrity, err := Decode([]byte(doc))
		if err != nil || integrity != IntegrityUnverified {
			t.Errorf("integrity = %s, err = %v", integrity, err)
		}
	})
}

func TestEqualityKeys(t *testing.T) {
	cases := []struct {
		rule, a, b string
		equal      bool
	}{
		{"caseIgnoreMatch", "Senior  Engineer ", "senior engineer", true},
		{"caseIgnoreMatch", "Engineer", "Engineers", false},
		{"caseExactMatch", " Engineer", "Engineer", true},
		{"caseExactMatch", "engineer", "Engineer", false},
		{"distinguishedNameMatch", "UID=Bob, OU=People,DC=alder,DC=test", "uid=bob,ou=people,dc=alder,dc=test", true},
		{"uniqueMemberMatch", "UID=Bob,DC=test#'0101'B", "uid=bob,dc=test#'0101'B", true},
		{"integerMatch", "0042", "42", true},
		{"booleanMatch", "true", "TRUE", true},
		{"generalizedTimeMatch", "20260913120000Z", "20260913140000+0200", true},
		{"generalizedTimeMatch", "20260913120000.0Z", "20260913120000Z", true},
		{"telephoneNumberMatch", "+1 555-0100", "+15550100", true},
		{"numericStringMatch", "1 234", "1234", true},
		{"objectIdentifierMatch", "inetOrgPerson", "inetorgperson", true},
		{"octetStringMatch", "A", "a", false},
		{"someVendorMatch", "A", "a", false},
	}
	for _, tc := range cases {
		keyer, _ := KeyerForRule(tc.rule)
		if got := keyer([]byte(tc.a)) == keyer([]byte(tc.b)); got != tc.equal {
			t.Errorf("%s: %q vs %q equal = %v, want %v", tc.rule, tc.a, tc.b, got, tc.equal)
		}
	}
	if _, known := KeyerForRule("someVendorMatch"); known {
		t.Error("an unknown rule claims to be modelled")
	}
}

func TestBuildRefusesTooManyEntries(t *testing.T) {
	many := make([]*directory.Entry, MaxEntries+1)
	if _, err := Build(capture(false), nil, many); codeOf(err) != CodeTooLarge {
		t.Errorf("err = %v, want too_large", err)
	}
}
