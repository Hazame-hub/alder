package api

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

func cmpEntry(t *testing.T, d string, pairs ...[]string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(mustParse(t, d))
	for _, p := range pairs {
		vals := make([][]byte, 0, len(p)-1)
		for _, v := range p[1:] {
			vals = append(vals, []byte(v))
		}
		e.Set(p[0], vals)
	}
	return e
}

func rowFor(c comparison, name string) *AttributeComparison {
	for i := range c.Attributes {
		if strings.EqualFold(c.Attributes[i].Name, name) {
			return &c.Attributes[i]
		}
	}
	return nil
}

const (
	leftDN  = "uid=alice,ou=people,dc=alder,dc=test"
	rightDN = "uid=bob,ou=people,dc=alder,dc=test"
)

func TestCompareClassifiesEachAttribute(t *testing.T) {
	sch := testSchema(t)
	left := cmpEntry(t, leftDN,
		[]string{"objectClass", "top", "inetOrgPerson"},
		[]string{"cn", "Same Name"},
		[]string{"mail", "left@alder.test"},
		[]string{"title", "Engineer"},
	)
	right := cmpEntry(t, rightDN,
		[]string{"objectClass", "top", "inetOrgPerson"},
		[]string{"cn", "Same Name"},
		[]string{"mail", "right@alder.test"},
		[]string{"l", "Berlin"},
	)

	got := compareEntries(left, right, sch, 100)

	if r := rowFor(got, "cn"); r == nil || r.Status != Same {
		t.Errorf("cn is %v, want same", r)
	}
	if r := rowFor(got, "mail"); r == nil || r.Status != Differs {
		t.Errorf("mail is %v, want differs", r)
	}
	if r := rowFor(got, "title"); r == nil || r.Status != LeftOnly {
		t.Errorf("title is %v, want leftOnly", r)
	}
	if r := rowFor(got, "l"); r == nil || r.Status != RightOnly {
		t.Errorf("l is %v, want rightOnly", r)
	}
	if got.Counts.Differs != 1 || got.Counts.LeftOnly != 1 || got.Counts.RightOnly != 1 {
		t.Errorf("counts are %+v", got.Counts)
	}
}

// An attribute is a set; the order a directory returns values in is its own.
func TestCompareTreatsMultiValuedAttributesAsSets(t *testing.T) {
	sch := testSchema(t)
	left := cmpEntry(t, leftDN, []string{"objectClass", "top", "person", "inetOrgPerson"})
	right := cmpEntry(t, rightDN, []string{"objectClass", "inetOrgPerson", "top", "person"})

	if r := rowFor(compareEntries(left, right, sch, 100), "objectClass"); r == nil || r.Status != Same {
		t.Errorf("reordered values were read as a difference: %v", r)
	}
}

// Rule 6, on the surface whose whole instinct is to show both sides.
func TestCompareWithholdsSensitiveValues(t *testing.T) {
	sch := testSchema(t)
	left := cmpEntry(t, leftDN,
		[]string{"objectClass", "top", "person"},
		[]string{"userPassword", "{SSHA}leftsecretvalue"})
	right := cmpEntry(t, rightDN,
		[]string{"objectClass", "top", "person"},
		[]string{"userPassword", "{SSHA}rightsecretvalue"})

	got := compareEntries(left, right, sch, 100)
	r := rowFor(got, "userPassword")
	if r == nil {
		t.Fatal("userPassword is absent entirely; presence is the useful half")
	}
	if r.Withheld == nil || !*r.Withheld {
		t.Error("userPassword was not marked withheld")
	}
	if r.Values != nil && len(*r.Values) > 0 {
		t.Errorf("values crossed the wire: %+v", *r.Values)
	}
}

// The call recorded in the decisions log: presence is reported, equality is not.
// Reporting whether two entries hold the same hash would be an oracle about
// password material the product offers nowhere else.
func TestCompareNeverReportsWhetherTwoHashesMatch(t *testing.T) {
	sch := testSchema(t)
	same := func(l, r string) *AttributeComparison {
		left := cmpEntry(t, leftDN,
			[]string{"objectClass", "top", "person"}, []string{"userPassword", l})
		right := cmpEntry(t, rightDN,
			[]string{"objectClass", "top", "person"}, []string{"userPassword", r})
		return rowFor(compareEntries(left, right, sch, 100), "userPassword")
	}

	identical := same("{SSHA}thesamehash", "{SSHA}thesamehash")
	different := same("{SSHA}onehash", "{SSHA}anotherhash")

	if identical.Status != different.Status {
		t.Errorf("the verdict differs by hash equality (%v vs %v), which makes the "+
			"comparison an oracle about password material",
			identical.Status, different.Status)
	}
	if identical.Comparable == nil || *identical.Comparable {
		t.Error("a withheld attribute held by both sides should be marked not comparable")
	}
}

// Each side is annotated from its own classes, which the two entries need not
// share: sn is required on an inetOrgPerson and not permitted on an account.
func TestCompareAnnotatesEachSidesOwnRequirements(t *testing.T) {
	sch := testSchema(t)
	left := cmpEntry(t, leftDN,
		[]string{"objectClass", "top", "person", "inetOrgPerson"},
		[]string{"sn", "Liddell"})
	right := cmpEntry(t, rightDN,
		[]string{"objectClass", "top", "organizationalUnit"},
		[]string{"ou", "somewhere"})

	r := rowFor(compareEntries(left, right, sch, 100), "sn")
	if r == nil {
		t.Fatal("sn is missing from the comparison")
	}
	if !r.Left.Required || !r.Left.Permitted {
		t.Errorf("left says required=%v permitted=%v, want both true",
			r.Left.Required, r.Left.Permitted)
	}
	if r.Right.Permitted {
		t.Error("sn is not permitted by an organizationalUnit and was reported as permitted")
	}
}

// The case a diff of present values cannot see, and which is often the answer.
func TestCompareFlagsARequiredAttributeThatIsAbsent(t *testing.T) {
	sch := testSchema(t)
	left := cmpEntry(t, leftDN,
		[]string{"objectClass", "top", "person"},
		[]string{"cn", "Alice"}, []string{"sn", "Liddell"})
	// Carries the class that requires sn, and does not hold it.
	right := cmpEntry(t, rightDN,
		[]string{"objectClass", "top", "person"},
		[]string{"cn", "Bob"})

	r := rowFor(compareEntries(left, right, sch, 100), "sn")
	if r == nil {
		t.Fatal("a required-but-absent attribute did not appear at all")
	}
	if !r.Right.Required || r.Right.Present {
		t.Errorf("right says required=%v present=%v, want required and absent",
			r.Right.Required, r.Right.Present)
	}
}

// Two spellings of one DN name the same entry, and the directory agrees.
func TestCompareComparesDNValuedAttributesAsDNs(t *testing.T) {
	sch := testSchema(t)
	left := cmpEntry(t, "cn=a,ou=groups,dc=alder,dc=test",
		[]string{"objectClass", "top", "groupOfNames"},
		[]string{"member", "CN=Bob,OU=People,DC=Alder,DC=Test"})
	right := cmpEntry(t, "cn=b,ou=groups,dc=alder,dc=test",
		[]string{"objectClass", "top", "groupOfNames"},
		[]string{"member", "cn=bob,ou=people,dc=alder,dc=test"})

	if r := rowFor(compareEntries(left, right, sch, 100), "member"); r == nil || r.Status != Same {
		t.Errorf("the same reference spelled differently is %v, want same", r)
	}
}

// uniqueMember carries Name-and-Optional-UID, which maps to KindString — so a
// Kind == KindDN test misses exactly the attribute most likely to differ only
// in spelling.
func TestCompareComparesUniqueMemberAsADN(t *testing.T) {
	sch := testSchema(t)
	left := cmpEntry(t, "cn=a,ou=groups,dc=alder,dc=test",
		[]string{"objectClass", "top", "groupOfNames"},
		[]string{"uniqueMember", "CN=Bob,DC=Alder,DC=Test"})
	right := cmpEntry(t, "cn=b,ou=groups,dc=alder,dc=test",
		[]string{"objectClass", "top", "groupOfNames"},
		[]string{"uniqueMember", "cn=bob,dc=alder,dc=test"})

	if r := rowFor(compareEntries(left, right, sch, 100), "uniqueMember"); r == nil || r.Status != Same {
		t.Errorf("uniqueMember is %v, want same", r)
	}
}

// Everything else is byte-exact, for the reason reconcile.go already settled:
// implementing the matching rules means getting one wrong and lying about the
// thing the feature exists to show.
func TestCompareComparesEverythingElseByteForByte(t *testing.T) {
	sch := testSchema(t)
	left := cmpEntry(t, leftDN,
		[]string{"objectClass", "top", "person"}, []string{"description", "Ops"})
	right := cmpEntry(t, rightDN,
		[]string{"objectClass", "top", "person"}, []string{"description", "ops"})

	if r := rowFor(compareEntries(left, right, sch, 100), "description"); r == nil || r.Status != Differs {
		t.Errorf("description is %v, want differs", r)
	}
}

// The test that fails if the union is keyed on foldName, which drops options.
func TestCompareDistinguishesAttributeOptions(t *testing.T) {
	sch := testSchema(t)
	left := cmpEntry(t, leftDN,
		[]string{"objectClass", "top", "person"},
		[]string{"description;lang-en", "hello"})
	right := cmpEntry(t, rightDN,
		[]string{"objectClass", "top", "person"},
		[]string{"description", "hello"})

	got := compareEntries(left, right, sch, 100)
	withOption := rowFor(got, "description;lang-en")
	plain := rowFor(got, "description")
	if withOption == nil || plain == nil {
		t.Fatalf("the two descriptions were merged into one row: %v", got.Attributes)
	}
	if withOption.Status != LeftOnly || plain.Status != RightOnly {
		t.Errorf("statuses are %v and %v, want leftOnly and rightOnly",
			withOption.Status, plain.Status)
	}
}

// What the entry is, first; what the directory owns, last.
func TestCompareOrdersObjectClassFirstAndOperationalLast(t *testing.T) {
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrObjectClasses: {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) MAY description )",
		},
		schema.AttrAttributeTypes: {
			"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.3 NAME 'cn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.4 NAME 'sn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.13 NAME 'description' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 1.3.6.1.1.16.4 NAME 'entryUUID' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 " +
				"NO-USER-MODIFICATION SINGLE-VALUE USAGE directoryOperation )",
		},
	})
	if len(sch.Errors) != 0 {
		t.Fatalf("the test schema does not parse: %v", sch.Errors)
	}

	e := func(d, uuid string) *directory.Entry {
		return cmpEntry(t, d,
			[]string{"entryUUID", uuid},
			[]string{"description", "x"},
			[]string{"objectClass", "top", "person"},
			[]string{"cn", "n"}, []string{"sn", "s"})
	}
	got := compareEntries(e(leftDN, "aaa"), e(rightDN, "bbb"), sch, 100)

	if got.Attributes[0].Name != "objectClass" {
		t.Errorf("the first row is %q, want objectClass", got.Attributes[0].Name)
	}
	last := got.Attributes[len(got.Attributes)-1].Name
	if !strings.EqualFold(last, "entryUUID") {
		t.Errorf("the last row is %q, want the operational attribute", last)
	}
}

func TestCompareOfAnEntryWithItselfReportsNoDifferences(t *testing.T) {
	sch := testSchema(t)
	e := cmpEntry(t, leftDN,
		[]string{"objectClass", "top", "person", "inetOrgPerson"},
		[]string{"cn", "Alice"}, []string{"sn", "Liddell"},
		[]string{"mail", "a@x.test", "b@x.test"})

	got := compareEntries(e, e, sch, 100)
	if got.Counts.Differs != 0 || got.Counts.LeftOnly != 0 || got.Counts.RightOnly != 0 {
		t.Errorf("an entry differs from itself: %+v", got.Counts)
	}
}

// A comparison of two large groups is otherwise thousands of DNs across the
// wire to be read by nobody.
func TestCompareCapsValuesPerAttribute(t *testing.T) {
	sch := testSchema(t)
	many := make([]string, 0, 61)
	many = append(many, "member")
	for i := 0; i < 60; i++ {
		many = append(many, "uid=user"+string(rune('a'+i%26))+",dc=alder,dc=test")
	}
	left := cmpEntry(t, "cn=a,ou=groups,dc=alder,dc=test",
		[]string{"objectClass", "top", "groupOfNames"}, many)
	right := cmpEntry(t, "cn=b,ou=groups,dc=alder,dc=test",
		[]string{"objectClass", "top", "groupOfNames"}, []string{"member", "uid=other,dc=alder,dc=test"})

	got := compareEntries(left, right, sch, 10)
	r := rowFor(got, "member")
	if r == nil || r.Values == nil {
		t.Fatal("member has no values")
	}
	if len(*r.Values) > 10 {
		t.Errorf("returned %d values past a limit of 10", len(*r.Values))
	}
	if !got.Truncated {
		t.Error("the comparison was capped and did not say so")
	}
}
