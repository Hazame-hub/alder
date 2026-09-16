package changepkg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
)

// Packages at the size of real work: a hundred changes, and a thousand.
//
//	go test -run '^$' -bench Package -benchmem ./internal/changepkg/
//
// The shape is the one that matters: schema first, then entries that depend on
// it, so the dependency graph is not trivial and validation has to resolve
// references rather than walk a flat list.

var benchSizes = []int{100, 1000}

// benchItems is n changes: one attribute type and one class for every twenty
// entries, each entry depending on the class, and one modification each.
func benchItems(n int) []Item {
	var items []Item
	groups := n / 20
	if groups == 0 {
		groups = 1
	}
	for g := 0; g < groups; g++ {
		attr := fmt.Sprintf("( 1.3.6.1.4.1.99999.20.1.%d NAME 'alderBench%dTeam' EQUALITY caseIgnoreMatch "+
			"SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )", g, g)
		class := fmt.Sprintf("( 1.3.6.1.4.1.99999.20.2.%d NAME 'alderBench%dClass' SUP top AUXILIARY "+
			"MUST alderBench%dTeam )", g, g, g)
		attrID, classID := fmt.Sprintf("a%d", g), fmt.Sprintf("c%d", g)
		items = append(items,
			Item{ID: attrID, Kind: KindSchema, Schema: &SchemaChange{Element: ElementAttributeType,
				Op: SchemaAdd, OID: fmt.Sprintf("1.3.6.1.4.1.99999.20.1.%d", g), Definition: attr}},
			Item{ID: classID, Kind: KindSchema, DependsOn: []string{attrID},
				Schema: &SchemaChange{Element: ElementObjectClass, Op: SchemaAdd,
					OID: fmt.Sprintf("1.3.6.1.4.1.99999.20.2.%d", g), Definition: class}})
	}
	for i := 0; len(items) < n; i++ {
		g := i % groups
		dn := fmt.Sprintf("uid=bench%d,ou=people,dc=alder,dc=test", i)
		items = append(items, Item{ID: fmt.Sprintf("e%d", i), Kind: KindData,
			DependsOn: []string{fmt.Sprintf("c%d", g)},
			Data: &DataChange{DN: dn, Type: OpAdd, Attributes: []Attribute{
				{Name: "objectClass", Values: []Value{{Text: "top"}, {Text: "person"},
					{Text: fmt.Sprintf("alderBench%dClass", g)}}},
				{Name: "cn", Values: []Value{{Text: fmt.Sprintf("Bench %d", i)}}},
				{Name: "sn", Values: []Value{{Text: "Bench"}}},
				{Name: fmt.Sprintf("alderBench%dTeam", g), Values: []Value{{Text: "platform"}}},
			}}})
	}
	return items[:n]
}

func benchPackage(b *testing.B, n int) *Package {
	b.Helper()
	p, err := Build(&Package{ID: "bench", Title: "bench", Changes: Derive(benchItems(n))}, created)
	if err != nil {
		b.Fatal(err)
	}
	return p
}

func BenchmarkPackageBuild(b *testing.B) {
	for _, n := range benchSizes {
		items := Derive(benchItems(n))
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for b.Loop() {
				if _, err := Build(&Package{ID: "bench", Changes: items}, created); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPackageDecode(b *testing.B) {
	for _, n := range benchSizes {
		var buf bytes.Buffer
		if err := Encode(&buf, benchPackage(b, n)); err != nil {
			b.Fatal(err)
		}
		document := buf.Bytes()
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportMetric(float64(len(document)), "json-bytes")
			for b.Loop() {
				if _, _, err := Decode(document); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPackageValidate measures validation against a directory that has
// none of it: every entry is read once, every definition resolved once.
func BenchmarkPackageValidate(b *testing.B) {
	for _, n := range benchSizes {
		p := benchPackage(b, n)
		target := benchTarget(b)
		opts := Options{NotFound: func(err error) bool { return errors.Is(err, errNoSuchEntry) }}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			var res *Result
			for b.Loop() {
				var err error
				res, err = Validate(context.Background(), p, target, opts)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(res.Counts.Ready), "ready")
			b.ReportMetric(float64(target.reads)/float64(b.N), "reads/op")
		})
	}
}

// benchTarget is a directory holding the parent entry and nothing else.
func benchTarget(b *testing.B) *fakeTarget {
	b.Helper()
	return newTarget(b, targetSchema(b, nil, nil), entryAt(b, "ou=people,dc=alder,dc=test"))
}

func TestTheBenchmarkPackageIsWhatItClaims(t *testing.T) {
	for _, n := range benchSizes {
		items := Derive(benchItems(n))
		p, err := Build(&Package{ID: "bench", Changes: items}, created)
		if err != nil {
			t.Fatalf("%d: %v", n, err)
		}
		if p.Counts.Changes != n {
			t.Errorf("%d: %d changes", n, p.Counts.Changes)
		}
		// Every entry comes after the class it uses, which comes after its
		// attribute type: the order is the graph's, and it is not the order the
		// items were made in.
		position := map[string]int{}
		for i, item := range p.Changes {
			position[item.ID] = i
		}
		for _, item := range p.Changes {
			for _, needs := range item.DependsOn {
				if position[needs] > position[item.ID] {
					t.Fatalf("%d: %s comes before %s", n, item.ID, needs)
				}
			}
		}
	}
}
