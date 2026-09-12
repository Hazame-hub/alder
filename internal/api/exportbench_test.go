package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/session"
)

// benchTree makes the shape the exports are actually asked for: a base, one
// container, and n entries under it.
//
// benchEntries is not reused because it returns leaves whose parent is not in
// the set, and a set of n orphans renders as n roots -- the flat case, which is
// the one the tree exports exist to avoid measuring.
func benchTree(tb testing.TB, n int) []*directory.Entry {
	tb.Helper()
	out := make([]*directory.Entry, 0, n+2)

	base := directory.NewEntry(mustParseTB(tb, "dc=alder,dc=test"))
	base.Set("objectClass", [][]byte{[]byte("top"), []byte("domain")})
	base.Set("dc", [][]byte{[]byte("alder")})
	out = append(out, base)

	container := directory.NewEntry(mustParseTB(tb, "ou=bulk,dc=alder,dc=test"))
	container.Set("objectClass", [][]byte{[]byte("top"), []byte("organizationalUnit")})
	container.Set("ou", [][]byte{[]byte("bulk")})
	out = append(out, container)

	for i := 0; i < n; i++ {
		uid := fmt.Sprintf("bulk%07d", i)
		e := directory.NewEntry(mustParseTB(tb, "uid="+uid+",ou=bulk,dc=alder,dc=test"))
		e.Set("objectClass", [][]byte{
			[]byte("top"), []byte("person"), []byte("organizationalPerson"),
			[]byte("inetOrgPerson"),
		})
		e.Set("uid", [][]byte{[]byte(uid)})
		e.Set("cn", [][]byte{[]byte("Bulk User " + uid)})
		e.Set("sn", [][]byte{[]byte("User")})
		e.Set("givenName", [][]byte{[]byte("Bulk")})
		e.Set("mail", [][]byte{[]byte(uid + "@bulk.alder.test")})
		out = append(out, e)
	}
	return out
}

// exportRig serves the export endpoints over a real socket, for searchRig's
// reason: the in-memory transport collects the whole response before handing
// back any of it, so a streamed body measured through it is indistinguishable
// from a materialised one and the client's copy lands in the heap being
// measured.
func exportRig(tb testing.TB, n int, path string) *http.Request {
	tb.Helper()
	rig := newRig(tb, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(tb),
		entries:      benchTree(tb, n),
		pageSize:     directory.MaxPageSize,
		driverPaging: true,
	})

	var lc net.ListenConfig
	ln, err := lc.Listen(tb.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listening: %v", err)
	}
	go func() { _ = rig.app.Listener(ln) }()
	tb.Cleanup(func() { _ = rig.app.Shutdown() })

	q := url.Values{}
	q.Set("dn", "dc=alder,dc=test")
	q.Set("scope", "sub")
	q.Set("limit", fmt.Sprint(directory.MaxResults))
	target := "http://" + ln.Addr().String() + "/api/v1/" + path + "?" + q.Encode()

	req, err := http.NewRequestWithContext(tb.Context(), http.MethodGet, target, nil)
	if err != nil {
		tb.Fatalf("building the request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: session.CookieNameInsecure, Value: rig.cookie})
	return req
}

// runExport reads the whole body and discards it as it arrives, so what stays
// on the heap is the server's doing and not the client's.
func runExport(tb testing.TB, req *http.Request, min int64) {
	tb.Helper()
	res, err := http.DefaultClient.Do(req.Clone(req.Context()))
	if err != nil {
		tb.Fatalf("exporting: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != fiber.StatusOK {
		tb.Fatalf("got %d", res.StatusCode)
	}
	size, err := io.Copy(io.Discard, res.Body)
	if err != nil {
		tb.Fatalf("reading the response: %v", err)
	}
	// A short response would make every figure a measurement of something else.
	if size < min {
		tb.Fatalf("%d bytes, expected at least %d: the response is short", size, min)
	}
}

// exportCases are the three subtree exports, with a floor on the document each
// produces. The outline's floor is far lower because it writes one line per
// entry rather than every value.
var exportCases = []struct {
	name string
	path string
	min  int64
}{
	{"ldif", "export/ldif", 200 * 10000},
	{"outline", "export/outline", 30 * 10000},
	{"yaml", "export/yaml", 500 * 10000},
}

func BenchmarkExportResponse(b *testing.B) {
	for _, tc := range exportCases {
		b.Run(tc.name, func(b *testing.B) {
			req := exportRig(b, 10000, tc.path)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				runExport(b, req, tc.min)
			}
		})
	}
}

// BenchmarkExportPeakHeap reports how far the live heap rises while one export
// is answered. Separate from the benchmark above for watchHeap's reason: the
// sampling costs more than the work it watches, so this one's ns/op is the
// sampler and means nothing.
func BenchmarkExportPeakHeap(b *testing.B) {
	for _, tc := range exportCases {
		b.Run(tc.name, func(b *testing.B) {
			req := exportRig(b, 10000, tc.path)
			var worst float64
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if over := watchHeap(func() { runExport(b, req, tc.min) }); over > worst {
					worst = over
				}
			}
			b.StopTimer()
			b.ReportMetric(worst, "peak-MB")
		})
	}
}
