package api

import (
	"fmt"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// benchEntries makes n entries that share their object classes, which is what a
// search over a directory of people actually returns.
func benchEntries(tb testing.TB, n int) []*directory.Entry {
	tb.Helper()
	out := make([]*directory.Entry, 0, n)
	for i := 0; i < n; i++ {
		e := directory.NewEntry(mustParseTB(tb,
			fmt.Sprintf("uid=bulk%06d,ou=people,dc=alder,dc=test", i)))
		e.Set("objectClass", [][]byte{
			[]byte("top"), []byte("person"), []byte("organizationalPerson"),
			[]byte("inetOrgPerson"),
		})
		e.Set("uid", [][]byte{[]byte(fmt.Sprintf("bulk%06d", i))})
		e.Set("cn", [][]byte{[]byte(fmt.Sprintf("Bulk User %d", i))})
		e.Set("sn", [][]byte{[]byte("User")})
		e.Set("givenName", [][]byte{[]byte("Bulk")})
		e.Set("mail", [][]byte{[]byte(fmt.Sprintf("bulk%06d@alder.test", i))})
		out = append(out, e)
	}
	return out
}

// The conversion a search response does per entry, as the handler does it.
func BenchmarkSearchEntryConversion(b *testing.B) {
	sch := testSchema(b)
	entries := benchEntries(b, 1000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := make([]SearchResultEntry, 0, len(entries))
		for _, e := range entries {
			out = append(out, SearchResultEntry{
				Dn:         e.DN.String(),
				Rdn:        ptr(rdnLabel(e.DN)),
				Attributes: ptr(entryAttributes(e, sch, sch.Requirements(e.ObjectClasses()))),
			})
		}
		_ = out
	}
}

// Requirements alone, to show how much of the above is it.
func BenchmarkRequirementsPerEntry(b *testing.B) {
	sch := testSchema(b)
	classes := []string{"top", "person", "organizationalPerson", "inetOrgPerson"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sch.Requirements(classes)
	}
}

func mustParseTB(tb testing.TB, s string) dn.DN {
	tb.Helper()
	d, err := dn.Parse(s)
	if err != nil {
		tb.Fatalf("parsing %q: %v", s, err)
	}
	return d
}
