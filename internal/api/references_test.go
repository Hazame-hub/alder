package api

import (
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// The failure that matters here is under-reporting.
//
// A reference this misses is a reference nobody is warned about before deleting
// the entry it points at, and the deletion then leaves a group holding a DN
// that resolves to nothing. So every case below is a stored value that is the
// subject, spelled in a way a string comparison would miss.

func mustParse(t *testing.T, s string) dn.DN {
	t.Helper()
	d, err := dn.Parse(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return d
}

func entryWith(t *testing.T, at string, values ...string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(mustParse(t, "cn=group,ou=groups,dc=alder,dc=test"))
	raw := make([][]byte, 0, len(values))
	for _, v := range values {
		raw = append(raw, []byte(v))
	}
	e.Set(at, raw)
	return e
}

func TestReferencesAreComparedAsDNsNotStrings(t *testing.T) {
	subject := mustParse(t, "uid=alice,ou=people,dc=alder,dc=test")

	for _, tc := range []struct {
		name  string
		value string
		want  bool
	}{
		{"exactly as written", "uid=alice,ou=people,dc=alder,dc=test", true},
		// A directory is free to return a different case for the attribute
		// names in a DN, and it is the same entry.
		{"different case in the attribute names", "UID=alice,OU=people,DC=alder,DC=test", true},
		{"spaces after the commas", "uid=alice, ou=people, dc=alder, dc=test", true},
		// uniqueMember's syntax appends an optional UID that is not part of
		// the DN. The entry being named is the same entry.
		{"a uniqueMember with a UID suffix", "uid=alice,ou=people,dc=alder,dc=test#'01'B", true},
		{"somebody else", "uid=bob,ou=people,dc=alder,dc=test", false},
		{"a prefix of the subject", "ou=people,dc=alder,dc=test", false},
		{"not a DN at all", "alice", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := referencesSubject(tc.value, subject); got != tc.want {
				t.Errorf("referencesSubject(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestMatchReferencesReportsTheAttributeEachOneUses(t *testing.T) {
	subject := mustParse(t, "uid=alice,ou=people,dc=alder,dc=test")
	attrs := []string{"member", "owner", "uniqueMember"}

	entries := []*directory.Entry{
		entryWith(t, "member", "uid=bob,ou=people,dc=alder,dc=test", subject.String()),
		entryWith(t, "owner", subject.String()),
	}
	got := matchReferences(entries, subject, attrs)
	if len(got) != 2 {
		t.Fatalf("got %d references, want 2: %+v", len(got), got)
	}
	// Removing one means deleting a value of a named attribute, so the name is
	// the load-bearing half of the answer.
	if got[0].Attribute != "member" || got[1].Attribute != "owner" {
		t.Errorf("attributes are %q and %q, want member and owner",
			got[0].Attribute, got[1].Attribute)
	}
}

// One entry can name the subject twice — a group holding somebody as both a
// member and its owner — and each is a separate thing to remove.
func TestOneEntryCanReferenceBySeveralAttributes(t *testing.T) {
	subject := mustParse(t, "uid=alice,ou=people,dc=alder,dc=test")
	e := entryWith(t, "member", subject.String())
	e.Set("owner", [][]byte{[]byte(subject.String())})

	got := matchReferences([]*directory.Entry{e}, subject, []string{"member", "owner"})
	if len(got) != 2 {
		t.Fatalf("got %d rows, want one per attribute: %+v", len(got), got)
	}
	if got[0].Dn != got[1].Dn {
		t.Error("the two rows should be the same entry")
	}
}

// The same attribute holding the subject twice is still one thing to remove.
func TestARepeatedValueIsReportedOnce(t *testing.T) {
	subject := mustParse(t, "uid=alice,ou=people,dc=alder,dc=test")
	e := entryWith(t, "member", subject.String(), "UID=alice,OU=people,DC=alder,DC=test")
	got := matchReferences([]*directory.Entry{e}, subject, []string{"member"})
	if len(got) != 1 {
		t.Errorf("got %d rows for one attribute, want 1: %+v", len(got), got)
	}
}

// The server's spelling of an attribute name is its own; the lookup must fold.
func TestTheAttributeLookupFoldsCase(t *testing.T) {
	subject := mustParse(t, "uid=alice,ou=people,dc=alder,dc=test")
	e := entryWith(t, "MEMBER", subject.String())
	got := matchReferences([]*directory.Entry{e}, subject, []string{"member"})
	if len(got) != 1 {
		t.Fatalf("got %d rows; the server returned MEMBER and member was asked for", len(got))
	}
	// Reported in the spelling that was asked for, which is the schema's.
	if got[0].Attribute != "member" {
		t.Errorf("attribute is %q, want the canonical member", got[0].Attribute)
	}
}

// The removal is a delete of a value, so the value has to come back exactly as
// the directory holds it. uniqueMemberMatch compares the UID part as well, and
// a delete of the bare DN would match nothing while reporting success.
func TestTheStoredValueIsReportedVerbatim(t *testing.T) {
	subject := mustParse(t, "uid=alice,ou=people,dc=alder,dc=test")
	stored := "UID=alice,OU=people,DC=alder,DC=test#'01'B"
	e := entryWith(t, "uniqueMember", stored)

	got := matchReferences([]*directory.Entry{e}, subject, []string{"uniqueMember"})
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if got[0].Value != stored {
		t.Errorf("value is %q, want the stored %q", got[0].Value, stored)
	}
}

func TestNoReferencesIsAnEmptyListNotNil(t *testing.T) {
	subject := mustParse(t, "uid=alice,ou=people,dc=alder,dc=test")
	got := matchReferences(nil, subject, []string{"member"})
	if got == nil {
		t.Error("got nil; an empty list serialises as [] and nil as null")
	}
	if len(got) != 0 {
		t.Errorf("got %d rows from no entries", len(got))
	}
}

// The search has to start from a suffix that contains the subject. Starting
// from the subject itself would find only the subject, and a reference points
// down from somewhere else in the tree.
func TestReferenceSearchBasePicksTheContainingContext(t *testing.T) {
	caps := directory.Capabilities{
		NamingContexts: []string{"dc=other,dc=test", "dc=alder,dc=test"},
	}
	base, name := referenceSearchBase(caps, mustParse(t, "uid=alice,ou=people,dc=alder,dc=test"))
	if name != "dc=alder,dc=test" {
		t.Errorf("searched %q, want the context containing the subject", name)
	}
	if base.IsEmpty() {
		t.Error("no base was chosen")
	}

	// An entry outside every naming context — the schema entry, say — gets no
	// search rather than a search of a suffix that does not contain it.
	outside, _ := referenceSearchBase(caps, mustParse(t, "cn=schema"))
	if !outside.IsEmpty() {
		t.Errorf("got base %v for an entry outside every naming context", outside)
	}
}
