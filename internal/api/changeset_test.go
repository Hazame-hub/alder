package api

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/ldif"
)

func mustDN(t *testing.T, s string) dn.DN {
	t.Helper()
	d, err := dn.Parse(s)
	if err != nil {
		t.Fatalf("dn.Parse(%q): %v", s, err)
	}
	return d
}

// The changeset document is offered to the user as a file to keep, hand over,
// or feed back in through Import. That is only true if it is a valid multi-
// record LDIF document, so the test reads it back with Alder's own reader
// rather than asserting on the text.
func TestChangesetLDIFRoundTrips(t *testing.T) {
	records := []directory.ChangeRecord{
		{
			DN:   mustDN(t, "ou=platform,dc=alder,dc=test"),
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("organizationalUnit")}},
				{Name: "ou", Values: [][]byte{[]byte("platform")}},
			},
		},
		{
			DN:   mustDN(t, "uid=bob,ou=platform,dc=alder,dc=test"),
			Type: directory.ChangeModify,
			Mods: []directory.Mod{
				{Op: directory.ModReplace, Name: "cn", Values: [][]byte{[]byte("Bob Adler")}},
			},
		},
		{
			DN:           mustDN(t, "uid=old,ou=people,dc=alder,dc=test"),
			Type:         directory.ChangeModRDN,
			NewRDN:       "uid=new",
			DeleteOldRDN: true,
		},
		{
			DN:   mustDN(t, "uid=gone,ou=people,dc=alder,dc=test"),
			Type: directory.ChangeDelete,
		},
	}

	got, err := ldif.Unmarshal([]byte(changesetLDIF(records)))
	if err != nil {
		t.Fatalf("the changeset document does not parse: %v", err)
	}
	if len(got) != len(records) {
		t.Fatalf("got %d records back, want %d", len(got), len(records))
	}
	for i, r := range got {
		if r.DN.String() != records[i].DN.String() {
			t.Errorf("record %d: DN %q, want %q", i, r.DN, records[i].DN)
		}
	}
}

// A password change has no LDIF form. It still has to appear in the document,
// as comments, or the file would describe fewer steps than the run performs.
func TestChangesetLDIFKeepsPasswordChangeVisible(t *testing.T) {
	records := []directory.ChangeRecord{
		{
			DN:          mustDN(t, "uid=bob,ou=people,dc=alder,dc=test"),
			Type:        directory.ChangeSetPassword,
			NewPassword: "hunter2",
		},
		{
			DN:   mustDN(t, "uid=bob,ou=people,dc=alder,dc=test"),
			Type: directory.ChangeDelete,
		},
	}

	doc := changesetLDIF(records)
	if !strings.Contains(doc, "uid=bob,ou=people,dc=alder,dc=test") {
		t.Error("the password change left no trace in the document")
	}
	if strings.Contains(doc, "hunter2") {
		t.Fatal("the new password appears in the rendered document")
	}
	// The comments must not stop the rest of the document parsing.
	got, err := ldif.Unmarshal([]byte(doc))
	if err != nil {
		t.Fatalf("document does not parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d parsed records, want 1 (the delete)", len(got))
	}
}

func TestChangesetWarnings(t *testing.T) {
	add := func(s string) directory.ChangeRecord {
		return directory.ChangeRecord{DN: mustDN(t, s), Type: directory.ChangeAdd}
	}
	del := func(s string) directory.ChangeRecord {
		return directory.ChangeRecord{DN: mustDN(t, s), Type: directory.ChangeDelete}
	}
	mod := func(s string) directory.ChangeRecord {
		return directory.ChangeRecord{DN: mustDN(t, s), Type: directory.ChangeModify}
	}

	tests := []struct {
		name    string
		records []directory.ChangeRecord
		want    string // a substring the warning must contain; empty means none
	}{
		{
			name:    "parent first is fine",
			records: []directory.ChangeRecord{add("ou=a,dc=x"), add("cn=b,ou=a,dc=x")},
		},
		{
			name:    "parent created later",
			records: []directory.ChangeRecord{add("cn=b,ou=a,dc=x"), add("ou=a,dc=x")},
			want:    "its parent is created later, at change 2",
		},
		{
			name:    "acting on a deleted entry",
			records: []directory.ChangeRecord{del("cn=b,dc=x"), mod("cn=b,dc=x")},
			want:    "which change 1 deletes first",
		},
		{
			// Case folding matters: a DN differing only in case is the same
			// entry, and a warning that missed it would be worse than none.
			name:    "the same entry twice, spelled differently",
			records: []directory.ChangeRecord{mod("CN=b,DC=x"), mod("cn=b,dc=x")},
			want:    "is changed 2 times",
		},
		{
			name:    "an entry at the top of the tree has no parent to warn about",
			records: []directory.ChangeRecord{add("dc=x")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := changesetWarnings(tc.records)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("want no warnings, got %q", got)
				}
				return
			}
			for _, w := range got {
				if strings.Contains(w, tc.want) {
					return
				}
			}
			t.Fatalf("no warning contained %q; got %q", tc.want, got)
		})
	}
}

func TestJoinInts(t *testing.T) {
	for _, tc := range []struct {
		in   []int
		want string
	}{
		{[]int{1}, "1"},
		{[]int{1, 2}, "1 and 2"},
		{[]int{1, 2, 5}, "1, 2 and 5"},
	} {
		if got := joinInts(tc.in); got != tc.want {
			t.Errorf("joinInts(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
