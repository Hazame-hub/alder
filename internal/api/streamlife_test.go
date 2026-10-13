package api

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// A streamed response is written by a goroutine fasthttp starts, and it is
// still running after the handler has returned. Two things outlived their
// welcome there: the --max-in-flight slot was let go while the writer was still
// paging the directory, and the writer read from a *fiber.Ctx that Fiber had
// already handed to the next request.

// waitForPages waits until the fake has been asked for n pages. The handler
// asks for one before it returns, so a third can only come from the writer.
func waitForPages(t *testing.T, rig *testRig, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for rig.fake.searchCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d pages were asked for in five seconds", rig.fake.searchCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The pages after the first are fetched by the stream writer, so that is
// directory work, and it has to hold the slot --max-in-flight hands out for
// directory work. It did not: the gate let go when the handler returned, and a
// search of ten thousand entries spent all but its first page outside the cap.
func TestAStreamedSearchHoldsItsSlotUntilTheLastPage(t *testing.T) {
	rig := newRig(t, Config{MaxInFlight: 1}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 60), pageSize: 10,
		searchDelay: 40 * time.Millisecond,
	})

	done := make(chan response, 1)
	go func() {
		done <- rig.do(t, http.MethodPost, "/api/v1/search", strings.NewReader(searchBody(1000, 10)))
	}()

	waitForPages(t, rig, 3)
	if got := rig.server.gate.inFlight(); got != 1 {
		t.Errorf("%d slots held while the stream writer was still searching, want 1", got)
	}

	res := <-done
	if out := decode[SearchResponse](t, res); len(out.Entries) != 60 {
		t.Errorf("%d entries came back, want 60", len(out.Entries))
	}
	if got := rig.server.gate.inFlight(); got != 0 {
		t.Errorf("%d slots still held after the stream finished", got)
	}
}

// The same for the LDIF export, whose writer pages the directory the same way.
func TestAStreamedExportHoldsItsSlotUntilTheLastPage(t *testing.T) {
	rig := newRig(t, Config{MaxInFlight: 1}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 60), pageSize: 10,
		searchDelay: 40 * time.Millisecond,
	})

	done := make(chan response, 1)
	go func() {
		done <- rig.do(t, http.MethodGet,
			"/api/v1/export/ldif?dn=dc%3Dalder%2Cdc%3Dtest&scope=sub&limit=1000", nil)
	}()

	waitForPages(t, rig, 3)
	if got := rig.server.gate.inFlight(); got != 1 {
		t.Errorf("%d slots held while the stream writer was still searching, want 1", got)
	}

	res := <-done
	if !strings.Contains(res.Body, "# 60 entries") {
		t.Errorf("the export did not finish:\n%s", lastLines(res.Body, 4))
	}
	if got := rig.server.gate.inFlight(); got != 0 {
		t.Errorf("%d slots still held after the stream finished", got)
	}
}

// Fiber puts its *fiber.Ctx back in a pool the moment the handler returns, and
// the next request is given it. The writer runs later, so whatever it reads
// from the Ctx belongs by then to someone else's request -- which is what
// abandon did, asking the Ctx for the path to log.
//
// The other requests have to arrive while the writer is still waiting on its
// failing page. One that arrives after the response has finished is ordered
// after the writer by the pipe the body went through, and the race detector
// rightly sees nothing wrong with it. Under -race, the ones that arrive in the
// window pick up the Ctx the failed search let go of, and the detector reports
// their writes to it and the writer's read of it as unordered.
func TestAnAbandonedSearchDoesNotTouchARecycledContext(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 50), pageSize: 10,
		failAfterSearches: 1,
		searchDelay:       20 * time.Millisecond,
	})

	// Several at once, because one alone keeps being handed back the Ctx it
	// just released and never reaches the one the search let go of.
	stop := make(chan struct{})
	var others sync.WaitGroup
	for range 8 {
		others.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					rig.do(t, http.MethodGet, "/api/v1/session", nil)
				}
			}
		})
	}

	// Forty rounds because whether the Ctx is reused inside the window is up
	// to sync.Pool. Against the old abandon this reported the race on six runs
	// out of six; at ten rounds of a longer delay it was one run in five.
	for range 40 {
		rig.fake.searches.Store(0)
		res := rig.do(t, http.MethodPost, "/api/v1/search", strings.NewReader(searchBody(1000, 10)))
		if !strings.Contains(res.Body, "this search failed after 10 entries") {
			t.Errorf("the search was not abandoned:\n%s", lastLines(res.Body, 4))
		}
	}
	close(stop)
	others.Wait()
}
