package ldif

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

// Golden files for the LDIF a user actually receives.
//
// The tests around these assert behaviours -- that a value needing base64 gets
// it, that folding is reversible. This file asserts something different and
// duller: that the exact bytes do not move. Alder's pitch is that its output is
// code, which means people commit an export and diff the next one against it. A
// change that reorders attributes or shifts a fold column breaks every one of
// those diffs while every behavioural test stays green.
//
// Regenerate deliberately, never reflexively:
//
//	go test ./internal/ldif -update
//
// and read the resulting diff. A change here is a change to a promise.

var update = flag.Bool("update", false, "rewrite the golden files")

// assertGolden compares got against testdata/golden/<name>, or rewrites it
// under -update.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the golden directory: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(got))
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v\nrun \"go test ./internal/ldif -update\" to create it", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the rendering of %s changed.\n--- want ---\n%s\n--- got ---\n%s",
			name, want, got)
	}
}

func mustDN(t *testing.T, s string) dn.DN {
	t.Helper()
	parsed, err := dn.Parse(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return parsed
}

func vals(values ...string) [][]byte {
	out := make([][]byte, 0, len(values))
	for _, v := range values {
		out = append(out, []byte(v))
	}
	return out
}

func TestGoldenLDIF(t *testing.T) {
	cases := []struct {
		file    string
		records func(t *testing.T) []*Record
	}{
		{
			// The ordinary export. Attribute order is preserved on purpose, so
			// that re-exporting an unchanged entry diffs to nothing.
			file: "content-entry.ldif",
			records: func(t *testing.T) []*Record {
				return []*Record{{
					DN: mustDN(t, "uid=alice,ou=people,dc=alder,dc=test"),
					Attrs: []Attribute{
						{Name: "objectClass", Values: vals("top", "person", "organizationalPerson", "inetOrgPerson")},
						{Name: "uid", Values: vals("alice")},
						{Name: "cn", Values: vals("Alice Liddell")},
						{Name: "sn", Values: vals("Liddell")},
						{Name: "mail", Values: vals("alice@alder.test", "a.liddell@alder.test")},
						{Name: "uidNumber", Values: vals("10001")},
					},
				}}
			},
		},
		{
			// Two records in one document, which is what a subtree export is.
			file: "content-subtree.ldif",
			records: func(t *testing.T) []*Record {
				return []*Record{
					{
						DN: mustDN(t, "ou=people,dc=alder,dc=test"),
						Attrs: []Attribute{
							{Name: "objectClass", Values: vals("top", "organizationalUnit")},
							{Name: "ou", Values: vals("people")},
						},
					},
					{
						DN: mustDN(t, "uid=bob,ou=people,dc=alder,dc=test"),
						Attrs: []Attribute{
							{Name: "objectClass", Values: vals("top", "inetOrgPerson")},
							{Name: "uid", Values: vals("bob")},
							{Name: "sn", Values: vals("Adler")},
						},
					},
				}
			},
		},
		{
			// Every reason RFC 2849 requires base64, in one record: non-ASCII,
			// a leading space, a trailing space, a leading colon, a leading
			// less-than, and a byte that is not text at all.
			file: "base64-values.ldif",
			records: func(t *testing.T) []*Record {
				return []*Record{{
					DN: mustDN(t, "cn=awkward,dc=alder,dc=test"),
					Attrs: []Attribute{
						{Name: "cn", Values: vals("awkward")},
						{Name: "description", Values: vals("café")},
						{Name: "leadingSpace", Values: vals(" indented")},
						{Name: "trailingSpace", Values: vals("padded ")},
						{Name: "leadingColon", Values: vals(":colon")},
						{Name: "leadingLess", Values: vals("<less")},
						{Name: "binary", Values: [][]byte{{0x00, 0x01, 0xff, 0xfe}}},
						{Name: "empty", Values: vals("")},
					},
				}}
			},
		},
		{
			// Folding at column 76. The continuation is a single leading space,
			// and unfolding must return the original byte for byte.
			file: "folded-value.ldif",
			records: func(t *testing.T) []*Record {
				long := "This is a deliberately long description that runs past the seventy-six column fold and keeps going for a while yet."
				return []*Record{{
					DN: mustDN(t, "cn=long,dc=alder,dc=test"),
					Attrs: []Attribute{
						{Name: "cn", Values: vals("long")},
						{Name: "description", Values: vals(long)},
					},
				}}
			},
		},
		{
			// An attribute description carries its options. "userCertificate"
			// and "userCertificate;binary" are different attributes to a
			// server, and the writer must not collapse them.
			file: "attribute-options.ldif",
			records: func(t *testing.T) []*Record {
				return []*Record{{
					DN: mustDN(t, "cn=optioned,dc=alder,dc=test"),
					Attrs: []Attribute{
						{Name: "cn", Values: vals("optioned")},
						{Name: "cn;lang-fr", Values: vals("optionné")},
						{Name: "userCertificate;binary", Values: [][]byte{{0x30, 0x82, 0x01, 0x0a}}},
					},
				}}
			},
		},
		{
			// A DN that needs RFC 4514 escaping, and one with non-ASCII.
			file: "awkward-dns.ldif",
			records: func(t *testing.T) []*Record {
				return []*Record{
					{
						DN:    mustDN(t, `cn=Liddell\, Alice,ou=people,dc=alder,dc=test`),
						Attrs: []Attribute{{Name: "cn", Values: vals("Liddell, Alice")}},
					},
					{
						DN:    mustDN(t, "cn=café,ou=people,dc=alder,dc=test"),
						Attrs: []Attribute{{Name: "cn", Values: vals("café")}},
					},
				}
			},
		},
		{
			file: "change-add.ldif",
			records: func(t *testing.T) []*Record {
				return []*Record{{
					DN:     mustDN(t, "uid=carol,ou=people,dc=alder,dc=test"),
					Change: ChangeAdd,
					Attrs: []Attribute{
						{Name: "objectClass", Values: vals("top", "inetOrgPerson")},
						{Name: "uid", Values: vals("carol")},
						{Name: "cn", Values: vals("Carol")},
						{Name: "sn", Values: vals("Adler")},
					},
				}}
			},
		},
		{
			// Every modification operation in one record, including the
			// distinction the format makes and most tools lose: a delete with
			// no values drops the attribute, a delete with values drops only
			// those.
			file: "change-modify.ldif",
			records: func(t *testing.T) []*Record {
				return []*Record{{
					DN:     mustDN(t, "uid=alice,ou=people,dc=alder,dc=test"),
					Change: ChangeModify,
					Mods: []Mod{
						{Op: ModAdd, Name: "mail", Values: vals("alice@example.test")},
						{Op: ModDelete, Name: "telephoneNumber"},
						{Op: ModDelete, Name: "mail", Values: vals("old@alder.test")},
						{Op: ModReplace, Name: "description", Values: vals("Replaced wholesale")},
						{Op: ModIncrement, Name: "uidNumber", Values: vals("1")},
					},
				}}
			},
		},
		{
			file: "change-delete.ldif",
			records: func(t *testing.T) []*Record {
				return []*Record{{
					DN:     mustDN(t, "uid=departed,ou=people,dc=alder,dc=test"),
					Change: ChangeDelete,
				}}
			},
		},
		{
			// A rename in place, and a move to a new parent.
			file: "change-modrdn.ldif",
			records: func(t *testing.T) []*Record {
				return []*Record{
					{
						DN:           mustDN(t, "uid=alice,ou=people,dc=alder,dc=test"),
						Change:       ChangeModRDN,
						NewRDN:       "uid=alice.liddell",
						DeleteOldRDN: true,
					},
					{
						DN:           mustDN(t, "uid=bob,ou=people,dc=alder,dc=test"),
						Change:       ChangeModRDN,
						NewRDN:       "uid=bob",
						DeleteOldRDN: false,
						NewSuperior:  mustDN(t, "ou=alumni,dc=alder,dc=test"),
					},
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got, err := Marshal(tc.records(t))
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			assertGolden(t, tc.file, got)
		})
	}
}

// The golden documents are LDIF the reader accepts, which is the property that
// makes them worth freezing: an export nobody can read back is not an export.
//
// Delete and modrdn records carry no attributes to compare, so this checks that
// the document parses and yields the DNs it should.
func TestGoldenLDIFReadsBack(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "golden"))
	if err != nil {
		t.Skipf("no golden directory yet: %v", err)
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".ldif" {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			raw, readErr := os.ReadFile(filepath.Join("testdata", "golden", e.Name()))
			if readErr != nil {
				t.Fatal(readErr)
			}
			records, parseErr := Unmarshal(raw)
			if parseErr != nil {
				t.Fatalf("the golden document does not parse: %v", parseErr)
			}
			if len(records) == 0 {
				t.Fatal("the golden document parsed to no records")
			}
			for _, r := range records {
				if r.DN.String() == "" {
					t.Error("a record came back with an empty DN")
				}
			}
		})
	}
}
