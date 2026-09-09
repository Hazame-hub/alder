package api

import (
	"strings"
	"testing"
)

const resolveBase = "dc=alder,dc=test"

func kinds(ds []Destination) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, string(d.Kind))
	}
	return out
}

func firstOf(ds []Destination, kind DestinationKind) *Destination {
	for i := range ds {
		if ds[i].Kind == kind {
			return &ds[i]
		}
	}
	return nil
}

// A DN with more than one component is a DN. Nobody types this meaning
// anything else.
func TestARootedDNResolvesToTheEntryAlone(t *testing.T) {
	got := resolveQuery("uid=alice,ou=people,dc=alder,dc=test", resolveBase)

	if len(got) != 1 || got[0].Kind != Entry {
		t.Fatalf("got %v, want one entry destination", kinds(got))
	}
	if got[0].Dn == nil || *got[0].Dn != "uid=alice,ou=people,dc=alder,dc=test" {
		t.Errorf("dn is %v", got[0].Dn)
	}
}

// The ambiguity the design turns on. "cn=platform" is a valid one-component DN
// and almost certainly a request to find something called platform, so both are
// offered rather than one being guessed.
func TestAOneComponentDNIsAmbiguousAndOffersBoth(t *testing.T) {
	got := resolveQuery("cn=platform", resolveBase)

	if len(got) != 2 {
		t.Fatalf("got %v, want both an entry and a search", kinds(got))
	}
	if firstOf(got, Entry) == nil || firstOf(got, Search) == nil {
		t.Fatalf("destinations are %v", kinds(got))
	}

	// The search is for what the DN *names*, not for the text of the DN.
	// Without this the filter looked for entries whose cn contains the literal
	// string "cn=platform", which nobody has ever wanted — and the first
	// version did exactly that while passing the assertion above.
	f := *firstOf(got, Search).Filter
	if f != "(cn=platform)" {
		t.Errorf("the search filter is %s, want an exact match on the RDN", f)
	}
}

// A multi-valued RDN is a DN and nothing else: there is no single name in it.
func TestAMultiValuedRDNOffersOnlyTheEntry(t *testing.T) {
	got := resolveQuery("cn=alice+ou=people", resolveBase)
	if len(got) != 1 || got[0].Kind != Entry {
		t.Errorf("got %v, want the entry alone", kinds(got))
	}
}

func TestAFilterResolvesToASearch(t *testing.T) {
	got := resolveQuery("(objectClass=inetOrgPerson)", resolveBase)

	if len(got) != 1 || got[0].Kind != Search {
		t.Fatalf("got %v, want one search", kinds(got))
	}
	if got[0].Filter == nil || *got[0].Filter != "(objectClass=inetOrgPerson)" {
		t.Errorf("filter is %v", got[0].Filter)
	}
	if got[0].Base == nil || *got[0].Base != resolveBase {
		t.Errorf("base is %v", got[0].Base)
	}
}

// A half-typed filter is not a name to search for. Offering to search for the
// literal text "(objectClass=" would be nonsense.
func TestAHalfTypedFilterOffersNothing(t *testing.T) {
	for _, q := range []string{"(objectClass=", "(&(a=b)", "((("} {
		if got := resolveQuery(q, resolveBase); len(got) != 0 {
			t.Errorf("%q offered %v", q, kinds(got))
		}
	}
}

func TestABareNameResolvesToASearch(t *testing.T) {
	got := resolveQuery("platform", resolveBase)

	if len(got) != 1 || got[0].Kind != Search {
		t.Fatalf("got %v, want one search", kinds(got))
	}
	f := *got[0].Filter
	for _, want := range []string{"cn=*platform*", "uid=*platform*", "mail=*platform*"} {
		if !strings.Contains(f, want) {
			t.Errorf("the filter does not look in %q: %s", want, f)
		}
	}
}

// Rule 3, on a box a user types in. "*)(objectClass=*" pasted into a filter is
// a filter that returns the whole directory.
func TestABareNameIsEscapedIntoTheFilter(t *testing.T) {
	got := resolveQuery("*)(objectClass=*", resolveBase)
	if len(got) == 0 {
		t.Fatal("nothing came back at all")
	}
	f := *firstOf(got, Search).Filter

	// The asterisk and the parentheses must have become escaped assertion
	// values rather than structure.
	if strings.Contains(f, "*)(objectClass=*)") {
		t.Errorf("the input reached the filter as structure: %s", f)
	}
	if !strings.Contains(f, `\2a`) || !strings.Contains(f, `\29`) {
		t.Errorf("the metacharacters were not escaped: %s", f)
	}
}

func TestAnEmptyQueryOffersNothing(t *testing.T) {
	for _, q := range []string{"", "   ", "\t"} {
		if got := resolveQuery(q, resolveBase); len(got) != 0 {
			t.Errorf("%q offered %v", q, kinds(got))
		}
	}
}

// An empty list serialises as [], and the client renders "nothing matches that"
// rather than crashing on null.
func TestDestinationsAreNeverNil(t *testing.T) {
	if resolveQuery("", resolveBase) == nil {
		t.Error("got nil rather than an empty list")
	}
}

// A session with no naming context still resolves; the search simply has no
// base to offer.
func TestNoNamingContextStillResolves(t *testing.T) {
	got := resolveQuery("platform", "")
	if len(got) != 1 {
		t.Fatalf("got %v", kinds(got))
	}
	if got[0].Base != nil {
		t.Errorf("base is %v, want none", got[0].Base)
	}
}
