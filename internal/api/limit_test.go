package api

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// The gate is the only bound on how much work Alder does at once, so what it
// admits and what it refuses are both worth pinning.

func TestTheGateAdmitsUpToItsSize(t *testing.T) {
	rig := newRig(t, Config{MaxInFlight: 4}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entry: entryFixture(t),
	})
	// Four at a time, several times over: a gate that leaked a slot per request
	// would stop admitting after the fourth.
	for i := 0; i < 12; i++ {
		res := rig.do(t, http.MethodGet, "/api/v1/session", nil)
		if res.Status != http.StatusOK {
			t.Fatalf("request %d: status %d, want 200", i, res.Status)
		}
	}
	if got := rig.server.gate.inFlight(); got != 0 {
		t.Errorf("%d slots still held after every request finished", got)
	}
}

// A slot is held for the handler, so requests that overlap contend and requests
// that do not, do not.
func TestTheGateHoldsASlotForTheDurationOfAHandler(t *testing.T) {
	rig := newRig(t, Config{MaxInFlight: 2}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries:     benchTree(t, 400),
		pageSize:    100,
		searchDelay: 150 * time.Millisecond,
	})

	const body = `{"baseDn":"dc=alder,dc=test","scope":"sub","attribute":"sn","limit":400}`

	var wg sync.WaitGroup
	peak := make(chan int, 8)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rig.do(t, http.MethodPost, "/api/v1/inventory", strings.NewReader(body))
		}()
	}
	// Sample while they run. Two slow requests against a gate of two should
	// occupy both.
	time.Sleep(120 * time.Millisecond)
	peak <- rig.server.gate.inFlight()
	wg.Wait()
	close(peak)

	seen := <-peak
	if seen == 0 {
		t.Error("no slot was held while two slow requests were in flight")
	}
	if got := rig.server.gate.inFlight(); got != 0 {
		t.Errorf("%d slots still held after both requests finished", got)
	}
}

// A full gate refuses rather than queueing forever, and says so in the shape
// the rest of the API uses.
func TestAFullGateRefusesWithRetryAfter(t *testing.T) {
	rig := newRig(t, Config{MaxInFlight: 1}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
	})

	// Fill it by hand rather than by racing a handler: what is under test is
	// the refusal, and a test that has to win a race to reach it is a test that
	// will one day not.
	rig.server.gate.slots <- struct{}{}
	t.Cleanup(func() { <-rig.server.gate.slots })

	done := make(chan response, 1)
	go func() { done <- rig.do(t, http.MethodGet, "/api/v1/session", nil) }()

	select {
	case res := <-done:
		if res.Status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503\nbody: %s", res.Status, res.Body)
		}
		if got := res.Header.Get("Retry-After"); got == "" {
			t.Error("a 503 from the gate carries no Retry-After")
		}
	case <-time.After(inFlightWait + 3*time.Second):
		t.Fatal("a request against a full gate never returned")
	}
}

// Zero means the default, and a negative number means no gate at all. Both are
// configuration an operator can reach, so both are pinned.
func TestGateConfiguration(t *testing.T) {
	t.Run("zero takes the default", func(t *testing.T) {
		rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), sch: testSchema(t)})
		if rig.server.gate == nil {
			t.Fatal("a default configuration has no gate")
		}
		if got := cap(rig.server.gate.slots); got != DefaultMaxInFlight {
			t.Errorf("gate size = %d, want %d", got, DefaultMaxInFlight)
		}
	})
	t.Run("negative turns it off", func(t *testing.T) {
		rig := newRig(t, Config{MaxInFlight: -1}, &fakeSession{caps: defaultCaps(), sch: testSchema(t)})
		if rig.server.gate != nil {
			t.Fatal("a negative configuration still built a gate")
		}
		// And the API still answers, which is the point of allowing it.
		if res := rig.do(t, http.MethodGet, "/api/v1/session", nil); res.Status != http.StatusOK {
			t.Errorf("status = %d with no gate, want 200", res.Status)
		}
	})
}
