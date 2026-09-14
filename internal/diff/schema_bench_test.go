package diff

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Schema snapshots and comparisons at the size of a real directory's schema
// and at five times it: 1,000 and 5,000 definitions, four attribute types to
// each object class. Run with:
//
//	go test -run '^$' -bench Schema -benchmem ./internal/diff/

var schemaBenchSizes = []int{1000, 5000}

// syntheticSchemaText is n definitions: attribute types in supertype chains of
// ten, object classes each requiring one attribute type and allowing two.
// edit changes one description in ten; drop leaves out one unreferenced
// attribute type in forty.
func syntheticSchemaText(n int, edit, drop bool) map[string][]string {
	ats := []string{"( 2.5.4.0 NAME 'objectClass' EQUALITY objectIdentifierMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )"}
	ocs := []string{"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )"}
	nAT := n * 4 / 5
	for i := 0; i < nAT; i++ {
		if drop && i%40 == 3 {
			continue
		}
		desc := "synthetic attribute"
		if edit && i%10 == 5 {
			desc = "edited attribute"
		}
		sup := ""
		if i%10 != 0 {
			sup = fmt.Sprintf(" SUP a%d", i-i%10)
		}
		ats = append(ats, fmt.Sprintf("( 1.3.6.1.4.1.99999.9.1.%d NAME 'a%d' DESC '%s'%s EQUALITY caseIgnoreMatch "+
			"SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 X-ORIGIN 'bench' )", i, i, desc, sup))
	}
	for i := 0; i < n-nAT; i++ {
		ocs = append(ocs, fmt.Sprintf("( 1.3.6.1.4.1.99999.9.2.%d NAME 'c%d' SUP top AUXILIARY MUST a%d MAY ( a%d $ a%d ) )",
			i, i, i*4, i*4+1, i*4+2))
	}
	return map[string][]string{schema.AttrAttributeTypes: ats, schema.AttrObjectClasses: ocs}
}

func benchSnapshot(b *testing.B, text map[string][]string) *snapshot.SchemaSnapshot {
	b.Helper()
	s, err := snapshot.BuildSchema(snapshot.SchemaCapture{Vendor: "bench", SubschemaEntry: "cn=schema",
		CreatedAt: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)}, schema.Load("cn=schema", text))
	if err != nil {
		b.Fatal(err)
	}
	return s
}

func BenchmarkSchemaCapture(b *testing.B) {
	for _, n := range schemaBenchSizes {
		text := syntheticSchemaText(n, false, false)
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for b.Loop() {
				benchSnapshot(b, text)
			}
		})
	}
}

func BenchmarkSchemaEncodeDecode(b *testing.B) {
	for _, n := range schemaBenchSizes {
		s := benchSnapshot(b, syntheticSchemaText(n, false, false))
		var buf bytes.Buffer
		if err := snapshot.EncodeSchema(&buf, s); err != nil {
			b.Fatal(err)
		}
		doc := buf.Bytes()
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportMetric(float64(len(doc)), "json-bytes")
			for b.Loop() {
				var out bytes.Buffer
				if err := snapshot.EncodeSchema(&out, s); err != nil {
					b.Fatal(err)
				}
				if _, _, err := snapshot.DecodeSchema(out.Bytes()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSchemaCompare(b *testing.B) {
	for _, n := range schemaBenchSizes {
		source := benchSnapshot(b, syntheticSchemaText(n, false, false))
		target := benchSnapshot(b, syntheticSchemaText(n, true, true))
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			var r *SchemaResult
			for b.Loop() {
				r = CompareSchema(SchemaSide{Snapshot: source, Live: true}, SchemaSide{Snapshot: target}, SchemaOptions{})
			}
			b.ReportMetric(float64(len(r.Items)), "differences")
			b.ReportMetric(float64(len(r.Order)), "ordered")
		})
	}
}

func BenchmarkSchemaDerive(b *testing.B) {
	for _, n := range schemaBenchSizes {
		source := benchSnapshot(b, syntheticSchemaText(n, false, false))
		target := benchSnapshot(b, syntheticSchemaText(n, true, true))
		r := CompareSchema(SchemaSide{Snapshot: source, Live: true}, SchemaSide{Snapshot: target}, SchemaOptions{})
		t := SchemaTarget{
			Write: directory.SchemaWrite{Style: directory.SchemaStyleSubschema,
				Targets:         []directory.SchemaTarget{{DN: "cn=schema", Name: "cn=schema"}},
				ObjectClassAttr: "objectClasses", AttributeTypeAttr: "attributeTypes"},
			Stored: func(string, directory.SchemaDefKind) ([]string, error) { return nil, nil },
			Usage:  func(string, string, []string) (bool, bool) { return false, false },
		}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for b.Loop() {
				for _, item := range r.Items {
					DeriveSchema(r, item, t)
				}
			}
		})
	}
}

// The benchmark schemas are what they claim to be.
func TestSyntheticSchemaIsWhatTheBenchmarksMeasure(t *testing.T) {
	for _, n := range schemaBenchSizes {
		text := syntheticSchemaText(n, false, false)
		sch := schema.Load("cn=schema", text)
		if len(sch.Errors) != 0 {
			t.Fatalf("%d: %v", n, sch.Errors[0])
		}
		if got := len(sch.AttributeTypes) + len(sch.ObjectClasses); got < n || got > n+2 {
			t.Errorf("%d: %d definitions", n, got)
		}
		edited := strings.Join(syntheticSchemaText(n, true, true)[schema.AttrAttributeTypes], "\n")
		if !strings.Contains(edited, "edited attribute") {
			t.Errorf("%d: no edits", n)
		}
	}
}
