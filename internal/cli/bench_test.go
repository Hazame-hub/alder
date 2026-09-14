package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What the client adds on top of the server: moving a snapshot from the socket
// to disk, and passing a comparison through to standard output. Allocations per
// operation, against the size of the document, are the measure that matters: a
// client that held the snapshot in memory would allocate at least its size.

func bigSnapshot(entries int) string {
	var b strings.Builder
	b.WriteString(`{"format":"alder-snapshot","version":1,"kind":"data","createdAt":"2026-09-14T10:00:00Z",` +
		`"source":{"base":"ou=people,dc=example,dc=test","scope":"sub","filter":"(objectClass=*)"},` +
		`"operationalAttributes":false,"schemaAvailable":true,"excluded":["operational-attributes","sensitive-values"],` +
		`"completeness":"complete",`)
	fmt.Fprintf(&b, `"entryCount":%d,"attributes":[],"checksum":"sha256:0","entries":[`, entries)
	for i := 0; i < entries; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"dn":"uid=user%05d,ou=people,dc=example,dc=test","id":"entryUUID=%08d-0000-4000-8000-000000000000","attributes":[`+
			`{"name":"objectClass","values":[{"text":"inetOrgPerson"},{"text":"person"},{"text":"top"}]},`+
			`{"name":"cn","values":[{"text":"User %d"}]},{"name":"mail","values":[{"text":"user%05d@example.test"}]},`+
			`{"name":"sn","values":[{"text":"User"}]},{"name":"title","values":[{"text":"Engineer"}]},{"name":"userPassword","withheld":1}]}`,
			i, i, i, i)
	}
	b.WriteString("]}\n")
	return b.String()
}

func BenchmarkSnapshotToFile(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			doc := bigSnapshot(n)
			s := newStub(b)
			s.reply("POST /api/v1/snapshots/capture", http.StatusOK, doc)
			dir := b.TempDir()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out := filepath.Join(dir, fmt.Sprintf("s%d.json", i))
				if r := s.run(b, runOpts{}, "snapshot", "--base", "ou=people,dc=example,dc=test", "--output", out); r.code != 0 {
					b.Fatalf("exit %d: %s", r.code, r.stderr)
				}
				_ = os.Remove(out)
			}
			b.ReportMetric(float64(len(doc))/1e6, "MB-snapshot")
		})
	}
}

func BenchmarkDiffJSONPassThrough(b *testing.B) {
	items := make([]string, 0, 10000)
	for i := 0; i < 10000; i++ {
		items = append(items, fmt.Sprintf(`{"kind":"modified","sourceDn":"uid=user%05d,ou=people,dc=example,dc=test","targetDn":"uid=user%05d,ou=people,dc=example,dc=test",`+
			`"attributes":[{"name":"title","kind":"modified","removed":[{"text":"Engineer"}],"added":[{"text":"Senior Engineer"}]}]}`, i, i))
	}
	body := `{"source":{"kind":"live","base":"ou=people,dc=example,dc=test","scope":"sub","filter":"(objectClass=*)","operationalAttributes":false,"entryCount":10000},` +
		`"target":{"kind":"snapshot","base":"ou=people,dc=example,dc=test","scope":"sub","filter":"(objectClass=*)","operationalAttributes":false,"entryCount":10000},` +
		`"complete":true,"counts":{"compared":10000,"added":0,"removed":0,"modified":10000,"renamed":0,"unchanged":0,"unknown":0},` +
		`"crossVendor":false,"operationalIgnored":false,"items":[` + strings.Join(items, ",") + `]}`
	s := newStub(b)
	s.reply("POST /api/v1/diff", http.StatusOK, body)
	snap := filepath.Join(b.TempDir(), "s.json")
	_ = os.WriteFile(snap, []byte(snapshotDoc), 0o600)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if r := s.run(b, runOpts{}, "diff", "@live", snap, "--json"); r.code != ExitDifferences {
			b.Fatalf("exit %d: %s", r.code, r.stderr)
		}
	}
	b.ReportMetric(float64(len(body))/1e6, "MB-diff")
}

var _ = io.Discard
