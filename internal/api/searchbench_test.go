package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/session"
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

// searchRig is the whole request path -- router, handler, schema, session --
// with a directory of n entries behind it that pages the way a real one does,
// served over a real socket.
//
// A real socket rather than the in-memory transport the tests use, because that
// transport collects the whole response before it hands back any of it. A
// streamed body read through it is indistinguishable from a materialised one,
// and the client's copy of a fifteen-megabyte document lands in the very heap
// being measured. Here the client reads and discards as the server writes, so
// what is left on the heap is the server's doing.
func searchRig(tb testing.TB, n int) (*http.Request, int) {
	tb.Helper()
	rig := newRig(tb, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(tb),
		entries: benchEntries(tb, n),
		// pageSize alone models a server, which is below the Session a handler
		// talks to; driverPaging puts the driver's own page loop back on top of
		// it. Without that a handler asking for ten thousand at once would be
		// handed one page and look as frugal as one that asks page by page.
		pageSize: directory.MaxPageSize, driverPaging: true,
	})

	var lc net.ListenConfig
	ln, err := lc.Listen(tb.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listening: %v", err)
	}
	go func() { _ = rig.app.Listener(ln) }()
	tb.Cleanup(func() { _ = rig.app.Shutdown() })

	body := fmt.Sprintf(
		`{"baseDn":"dc=alder,dc=test","scope":"sub","filter":"(objectClass=*)","limit":%d,"pageSize":%d}`,
		n, directory.MaxPageSize)
	req, err := http.NewRequestWithContext(tb.Context(), http.MethodPost,
		"http://"+ln.Addr().String()+"/api/v1/search", nil)
	if err != nil {
		tb.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: session.CookieNameInsecure, Value: rig.cookie})
	// A fresh reader per attempt, so the request can be sent more than once.
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(body)), nil
	}
	return req, n
}

func runSearch(tb testing.TB, req *http.Request, n int) {
	tb.Helper()
	send := req.Clone(req.Context())
	rc, err := req.GetBody()
	if err != nil {
		tb.Fatalf("rewinding the body: %v", err)
	}
	send.Body = rc

	res, err := http.DefaultClient.Do(send)
	if err != nil {
		tb.Fatalf("searching: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != fiber.StatusOK {
		tb.Fatalf("got %d", res.StatusCode)
	}
	size, err := io.Copy(io.Discard, res.Body)
	if err != nil {
		tb.Fatalf("reading the response: %v", err)
	}
	// A response that came back short would make every figure below a
	// measurement of something else.
	if size < int64(n)*100 {
		tb.Fatalf("%d bytes for %d entries: the response is short", size, n)
	}
}

// BenchmarkSearchResponse drives the real handler through the real router at
// the documented maximum of 10,000 entries.
func BenchmarkSearchResponse(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			req, want := searchRig(b, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				runSearch(b, req, want)
			}
		})
	}
}

// BenchmarkSearchResponsePeakHeap reports how far the live heap rises while one
// search is answered.
//
// Separate from the benchmark above because the sampling costs more than the
// work it watches, so the two figures cannot be taken at once: this one's ns/op
// is the sampler and means nothing. Live heap rather than bytes allocated,
// because the fault is liveness and not volume -- building the whole response
// holds every entry, every wire type derived from it and the marshalled
// document at the same instant, and a total-allocated figure cannot tell that
// apart from the same work done a page at a time and dropped as it goes.
func BenchmarkSearchResponsePeakHeap(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			req, want := searchRig(b, n)
			var worst float64
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if over := watchHeap(func() { runSearch(b, req, want) }); over > worst {
					worst = over
				}
			}
			b.StopTimer()
			b.ReportMetric(worst, "peak-MB")
		})
	}
}

// watchHeap samples the live heap while fn runs and returns, in MB, how far the
// largest sample rose above what was already live when it started.
//
// Above a settled baseline, because the benchmark's own fixture -- ten thousand
// entries the fake hands out -- is live throughout and is not what is being
// measured. Sampled rather than read afterwards: by the time fn returns the
// response has been written and the collector has had the whole of it back, so
// a reading taken at the end shows nothing whichever way the handler works.
func watchHeap(fn func()) float64 {
	runtime.GC()
	debug.FreeOSMemory()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc

	var peak atomic.Uint64
	peak.Store(base)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var sample runtime.MemStats
		for {
			select {
			case <-stop:
				return
			// Throttled, because ReadMemStats stops the world: sampled flat out
			// it costs sixty times the work it is watching, and a server whose
			// every write is interrupted is not the server being measured. A
			// tenth of a millisecond still takes thousands of samples across one
			// search.
			case <-time.After(100 * time.Microsecond):
			}
			runtime.ReadMemStats(&sample)
			for {
				old := peak.Load()
				if sample.HeapAlloc <= old || peak.CompareAndSwap(old, sample.HeapAlloc) {
					break
				}
			}
		}
	}()
	fn()
	close(stop)
	<-done
	return float64(peak.Load()-base) / (1 << 20)
}

func mustParseTB(tb testing.TB, s string) dn.DN {
	tb.Helper()
	d, err := dn.Parse(s)
	if err != nil {
		tb.Fatalf("parsing %q: %v", s, err)
	}
	return d
}
