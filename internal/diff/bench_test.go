package diff

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// People, and one group holding all of them: the multi-valued attribute that
// makes a naive comparison quadratic. Each benchmark runs at one and ten
// thousand, so a cost that grows faster than the directory shows as a ratio
// well above ten.

var benchSizes = []int{1000, 10000}

func benchEntries(b *testing.B, people int, drift bool) []*directory.Entry {
	b.Helper()
	entries := make([]*directory.Entry, 0, people+1)
	members := make([]string, 0, people)
	for i := 0; i < people; i++ {
		d := fmt.Sprintf("uid=user%05d,ou=people,dc=alder,dc=test", i)
		title := "Engineer"
		if drift && i%100 == 0 { // one in a hundred changed
			title = "Senior Engineer"
		}
		entries = append(entries, entry(d,
			[]string{"objectClass", "top", "person", "inetOrgPerson"},
			[]string{"uid", fmt.Sprintf("user%05d", i)}, []string{"cn", fmt.Sprintf("User %d", i)},
			[]string{"sn", "User"}, []string{"title", title},
			[]string{"mail", fmt.Sprintf("user%05d@alder.test", i)},
			[]string{"entryUUID", fmt.Sprintf("00000000-0000-4000-8000-%012d", i)}))
		if !drift || i%100 != 1 { // and one in a hundred left the group
			members = append(members, d)
		}
	}
	group := []string{"member"}
	group = append(group, members...)
	entries = append(entries, entry("cn=everyone,ou=groups,dc=alder,dc=test",
		[]string{"objectClass", "top", "groupOfNames"}, []string{"cn", "everyone"}, group))
	return entries
}

func BenchmarkSnapshotBuild(b *testing.B) {
	sch := testSchema(b)
	for _, n := range benchSizes {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			entries := benchEntries(b, n, false)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				snap(b, opts{sch: sch}, entries...)
			}
		})
	}
}

func BenchmarkSnapshotDecode(b *testing.B) {
	sch := testSchema(b)
	for _, n := range benchSizes {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			var buf bytes.Buffer
			if err := snapshot.Encode(&buf, snap(b, opts{sch: sch}, benchEntries(b, n, false)...)); err != nil {
				b.Fatal(err)
			}
			doc := buf.Bytes()
			var compact bytes.Buffer
			if err := json.Compact(&compact, doc); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := snapshot.Decode(doc); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(doc))/1e6, "MB-file")
			b.ReportMetric(float64(compact.Len())/1e6, "MB-compact")
		})
	}
}

func BenchmarkDiffWithLargeGroup(b *testing.B) {
	sch := testSchema(b)
	for _, n := range benchSizes {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			source := snap(b, opts{sch: sch}, benchEntries(b, n, false)...)
			target := snap(b, opts{sch: sch}, benchEntries(b, n, true)...)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r, err := Compare(context.Background(), Side{Snapshot: source, Live: true}, Side{Snapshot: target}, Options{})
				if err != nil {
					b.Fatal(err)
				}
				if r.Counts.Modified != n/100+1 {
					b.Fatalf("modified = %d", r.Counts.Modified)
				}
			}
		})
	}
}
