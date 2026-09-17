package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/changepkg"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Preflight at the size of real work.
//
//	go test -run '^$' -bench Preflight -benchmem ./internal/preflight/
//
// A package of 100 and 1000 changes (schema first, entries that use it, and a
// group per twenty entries naming them), a data snapshot of 1k and 10k entries
// whose target already holds half, and a schema snapshot of 1000 definitions.
// Each reports the report's JSON size and the reads the target was asked for.

func benchPackage(b *testing.B, n int) *changepkg.Package {
	b.Helper()
	var items []changepkg.Item
	groups := n / 20
	if groups == 0 {
		groups = 1
	}
	for g := 0; g < groups; g++ {
		items = append(items,
			schemaChange(fmt.Sprintf("a%d", g), changepkg.ElementAttributeType, changepkg.SchemaAdd, fmt.Sprintf("1.3.6.1.4.1.99999.90.1.%d", g),
				fmt.Sprintf("( 1.3.6.1.4.1.99999.90.1.%d NAME 'alderBench%dTeam' EQUALITY caseIgnoreMatch SYNTAX %s )", g, g, syntaxString)),
			schemaChange(fmt.Sprintf("o%d", g), changepkg.ElementObjectClass, changepkg.SchemaAdd, fmt.Sprintf("1.3.6.1.4.1.99999.90.2.%d", g),
				fmt.Sprintf("( 1.3.6.1.4.1.99999.90.2.%d NAME 'alderBench%dClass' SUP top AUXILIARY MUST alderBench%dTeam )", g, g, g)))
	}
	for i := 0; len(items) < n; i++ {
		g := i % groups
		if i%20 == 19 {
			var members []string
			for m := i - 19; m < i; m++ {
				members = append(members, fmt.Sprintf("uid=bench%d,ou=people,dc=alder,dc=test", m))
			}
			items = append(items, addEntry(fmt.Sprintf("g%d", i), fmt.Sprintf("cn=bench%d,ou=groups,dc=alder,dc=test", i),
				attr("objectClass", "top", "groupOfNames"), attr("cn", fmt.Sprintf("bench%d", i)), attr("member", members...)))
			continue
		}
		items = append(items, addEntry(fmt.Sprintf("e%d", i), fmt.Sprintf("uid=bench%d,ou=people,dc=alder,dc=test", i),
			attr("objectClass", "top", "person", fmt.Sprintf("alderBench%dClass", g)), attr("cn", "Bench"), attr("sn", "B"),
			attr(fmt.Sprintf("alderBench%dTeam", g), "platform"), attr("manager", managerDN)))
	}
	return buildPackage(b, items[:n]...)
}

func reportSize(b *testing.B, r *Report) int {
	b.Helper()
	out, err := json.Marshal(r)
	if err != nil {
		b.Fatal(err)
	}
	return len(out)
}

func BenchmarkPreflightPackage(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			p := benchPackage(b, n)
			var size, reads int
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				target := withManager(b)
				r, err := Package(context.Background(), p, "verified", target, Options{NotFound: notFound, Now: fixedClock})
				if err != nil {
					b.Fatal(err)
				}
				if r.Overall != Compatible {
					b.Fatalf("overall %s\n%s", r.Overall, describe(r))
				}
				reads = target.reads
				if i == 0 {
					b.StopTimer()
					size = reportSize(b, r)
					b.StartTimer()
				}
			}
			b.ReportMetric(float64(size), "report-bytes")
			b.ReportMetric(float64(reads), "reads/op")
		})
	}
}

func BenchmarkPreflightDataSnapshot(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			sch := targetSchema(b, nil, nil)
			entries := []*directory.Entry{entry(b, "ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "organizationalUnit", "ou", "people")}
			held := baseEntries(b)
			for i := 0; i < n; i++ {
				e := entry(b, fmt.Sprintf("uid=bench%d,ou=people,dc=alder,dc=test", i), "objectClass", "top", "objectClass", "person",
					"cn", fmt.Sprintf("Bench %d", i), "sn", "B", "uid", fmt.Sprintf("bench%d", i), "manager", managerDN)
				entries = append(entries, e)
				if i%2 == 0 {
					held = append(held, e)
				}
			}
			held = append(held, entry(b, managerDN, "objectClass", "top", "objectClass", "person", "cn", "Boss", "sn", "B"))
			s, err := snapshot.Build(snapshot.Capture{Base: mustDN(b, "ou=people,dc=alder,dc=test"), Scope: "sub", Filter: "(objectClass=*)",
				Vendor: "Source", CreatedAt: time.Unix(0, 0)}, sch, entries)
			if err != nil {
				b.Fatal(err)
			}
			var buf bytes.Buffer
			if err := snapshot.Encode(&buf, s); err != nil {
				b.Fatal(err)
			}
			doc := buf.Bytes()
			var size, reads int
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				target := newTarget(b, sch, held...)
				decoded, integrity, err := snapshot.Decode(doc)
				if err != nil {
					b.Fatal(err)
				}
				r, err := DataSnapshot(context.Background(), decoded, string(integrity), target,
					Options{NotFound: notFound, Now: fixedClock, Capture: captureFrom(target)})
				if err != nil {
					b.Fatal(err)
				}
				if r.Overall != CompatibleWithPrerequisite && r.Overall != Compatible {
					b.Fatalf("overall %s", r.Overall)
				}
				reads = target.reads
				if i == 0 {
					b.StopTimer()
					size = reportSize(b, r)
					b.StartTimer()
				}
			}
			b.ReportMetric(float64(size), "report-bytes")
			b.ReportMetric(float64(reads), "reads/op")
			b.ReportMetric(float64(len(doc)), "artifact-bytes")
		})
	}
}

func BenchmarkPreflightSchemaSnapshot(b *testing.B) {
	const n = 1000
	var ats, ocs []string
	for i := 0; i < n/2; i++ {
		ats = append(ats, userDefined(fmt.Sprintf("( 1.3.6.1.4.1.99999.91.1.%d NAME 'alderSchemaBench%d' EQUALITY caseIgnoreMatch SYNTAX %s )", i, i, syntaxString)))
		ocs = append(ocs, userDefined(fmt.Sprintf("( 1.3.6.1.4.1.99999.91.2.%d NAME 'alderSchemaBenchClass%d' SUP top AUXILIARY MAY alderSchemaBench%d )", i, i, i)))
	}
	source := targetSchema(b, ats, ocs)
	s := schemaSnapshotOf(b, "Source", source)
	var buf bytes.Buffer
	if err := snapshot.EncodeSchema(&buf, s); err != nil {
		b.Fatal(err)
	}
	doc := buf.Bytes()
	// The target already holds a quarter of them.
	held := targetSchema(b, ats[:n/8], ocs[:n/8])
	var size int
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decoded, integrity, err := snapshot.DecodeSchema(doc)
		if err != nil {
			b.Fatal(err)
		}
		r, err := SchemaSnapshot(context.Background(), decoded, string(integrity), newTarget(b, held), Options{NotFound: notFound, Now: fixedClock})
		if err != nil {
			b.Fatal(err)
		}
		if r.Overall != Compatible {
			b.Fatalf("overall %s", r.Overall)
		}
		if i == 0 {
			b.StopTimer()
			size = reportSize(b, r)
			b.StartTimer()
		}
	}
	b.ReportMetric(float64(size), "report-bytes")
	b.ReportMetric(float64(len(doc)), "artifact-bytes")
}
