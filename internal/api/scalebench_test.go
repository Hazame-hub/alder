package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/session"
)

// The paths that were designed for a large directory and never measured on one.
//
// The export benchmarks next door cover the three subtree exports. These are
// the rest: the playbook export, which is bounded exactly the way the tree
// exports were; the inventory, which folds page by page and should therefore
// not grow with the directory at all; and the comparison, whose size comes not
// from the number of entries but from how much is in the two it reads.

// serve puts a rig behind a real socket. The in-memory transport collects the
// whole response before handing any of it back, which is the difference these
// benchmarks exist to see.
func serve(tb testing.TB, fake *fakeSession) (base, cookie string) {
	tb.Helper()
	rig := newRig(tb, Config{}, fake)

	var lc net.ListenConfig
	ln, err := lc.Listen(tb.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listening: %v", err)
	}
	go func() { _ = rig.app.Listener(ln) }()
	tb.Cleanup(func() { _ = rig.app.Shutdown() })

	return "http://" + ln.Addr().String() + "/api/v1", rig.cookie
}

func benchRequest(tb testing.TB, method, target, cookie, body string) *http.Request {
	tb.Helper()
	req, err := http.NewRequestWithContext(tb.Context(), method, target, nil)
	if err != nil {
		tb.Fatalf("building the request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
		// A fresh reader per attempt, so the request can be sent more than once.
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(body)), nil
		}
	}
	req.AddCookie(&http.Cookie{Name: session.CookieNameInsecure, Value: cookie})
	return req
}

// run sends the request and discards the body as it arrives, so what stays on
// the heap is the server's doing.
func run(tb testing.TB, req *http.Request, min int64) {
	tb.Helper()
	send := req.Clone(req.Context())
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil {
			tb.Fatalf("rewinding the body: %v", err)
		}
		send.Body = rc
	}
	res, err := http.DefaultClient.Do(send)
	if err != nil {
		tb.Fatalf("requesting: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != fiber.StatusOK {
		tb.Fatalf("got %d", res.StatusCode)
	}
	size, err := io.Copy(io.Discard, res.Body)
	if err != nil {
		tb.Fatalf("reading the response: %v", err)
	}
	// A short answer would make every figure below a measurement of something
	// else.
	if size < min {
		tb.Fatalf("%d bytes, expected at least %d: the response is short", size, min)
	}
}

// --- the playbook export -----------------------------------------------------

func ansibleRequest(tb testing.TB, n int) *http.Request {
	tb.Helper()
	base, cookie := serve(tb, &fakeSession{
		caps: defaultCaps(), sch: testSchema(tb),
		entries:      benchTree(tb, n),
		pageSize:     directory.MaxPageSize,
		driverPaging: true,
	})
	q := url.Values{}
	q.Set("dn", "dc=alder,dc=test")
	q.Set("scope", "sub")
	q.Set("limit", fmt.Sprint(directory.MaxResults))
	return benchRequest(tb, http.MethodGet, base+"/export/ansible?"+q.Encode(), cookie, "")
}

func BenchmarkAnsibleExport(b *testing.B) {
	req := ansibleRequest(b, 10000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		run(b, req, 300*10000)
	}
}

func BenchmarkAnsibleExportPeakHeap(b *testing.B) {
	req := ansibleRequest(b, 10000)
	var worst float64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if over := watchHeap(func() { run(b, req, 300*10000) }); over > worst {
			worst = over
		}
	}
	b.StopTimer()
	b.ReportMetric(worst, "peak-MB")
}

// --- the inventory -----------------------------------------------------------

// The tally folds each page as it arrives, so the claim under test is that a
// hundred thousand entries cost about what ten thousand do. A path that grew
// with the directory would show it here and nowhere else.
func inventoryRequest(tb testing.TB, n int) *http.Request {
	tb.Helper()
	base, cookie := serve(tb, &fakeSession{
		caps: defaultCaps(), sch: testSchema(tb),
		entries:      benchTree(tb, n),
		pageSize:     directory.MaxPageSize,
		driverPaging: true,
	})
	body := fmt.Sprintf(
		`{"baseDn":"dc=alder,dc=test","scope":"sub","attribute":"sn","limit":%d}`, n)
	return benchRequest(tb, http.MethodPost, base+"/inventory", cookie, body)
}

func BenchmarkInventory(b *testing.B) {
	for _, n := range []int{10000, 100000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			req := inventoryRequest(b, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				run(b, req, 20)
			}
		})
	}
}

func BenchmarkInventoryPeakHeap(b *testing.B) {
	for _, n := range []int{10000, 100000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			req := inventoryRequest(b, n)
			var worst float64
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if over := watchHeap(func() { run(b, req, 20) }); over > worst {
					worst = over
				}
			}
			b.StopTimer()
			b.ReportMetric(worst, "peak-MB")
		})
	}
}

// --- the comparison ----------------------------------------------------------

// benchGroup is one entry holding n members, which is the shape that makes a
// comparison large. The endpoint reads two entries, so it cannot grow with the
// size of the directory -- but it can grow with the size of what it reads, and
// a group of a hundred thousand is an ordinary thing to find in a directory of
// a hundred thousand.
func benchGroup(tb testing.TB, name string, n int) *directory.Entry {
	tb.Helper()
	e := directory.NewEntry(mustParseTB(tb, "cn="+name+",ou=groups,dc=alder,dc=test"))
	e.Set("objectClass", [][]byte{[]byte("top"), []byte("groupOfNames")})
	e.Set("cn", [][]byte{[]byte(name)})
	members := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		members = append(members, fmt.Appendf(nil,
			"uid=bulk%07d,ou=bulk,dc=alder,dc=test", i))
	}
	e.Set("member", members)
	return e
}

func compareRequest(tb testing.TB, n int) *http.Request {
	tb.Helper()
	left := benchGroup(tb, "left", n)
	right := benchGroup(tb, "right", n)
	base, cookie := serve(tb, &fakeSession{
		caps: defaultCaps(), sch: testSchema(tb),
		byDN: map[string]*directory.Entry{
			strings.ToLower(left.DN.String()):  left,
			strings.ToLower(right.DN.String()): right,
		},
	})
	q := url.Values{}
	q.Set("left", left.DN.String())
	q.Set("right", right.DN.String())
	return benchRequest(tb, http.MethodGet, base+"/compare?"+q.Encode(), cookie, "")
}

func BenchmarkCompareLargeGroups(b *testing.B) {
	req := compareRequest(b, 100000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		run(b, req, 20)
	}
}

func BenchmarkCompareLargeGroupsPeakHeap(b *testing.B) {
	req := compareRequest(b, 100000)
	var worst float64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if over := watchHeap(func() { run(b, req, 20) }); over > worst {
			worst = over
		}
	}
	b.StopTimer()
	b.ReportMetric(worst, "peak-MB")
}
