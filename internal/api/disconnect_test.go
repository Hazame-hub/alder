package api

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/session"
	"github.com/valyala/fasthttp"
)

// What happens to the directory work when the browser goes away.
//
// A streamed search asks the directory for page after page and writes each one
// out. If nothing notices the client leaving, a caller who opens a
// ten-thousand-entry search and closes the tab leaves Alder paging a directory
// on behalf of nobody -- and can be made to do it as fast as the directory
// answers, which is the cheapest way to make Alder expensive.

// listening puts a rig behind a real socket. app.Test cannot be used here: the
// question is what a closed TCP connection does, and there is no connection.
func listening(t *testing.T, rig *testRig) net.Addr {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	go func() { _ = rig.app.Listener(ln) }()
	t.Cleanup(func() { _ = rig.app.Shutdown() })
	return ln.Addr()
}

// hangUpDuring sends one request, waits for the handler to be under way, closes
// the connection, and reports how many pages were served before and after.
func hangUpDuring(t *testing.T, path, body string, readFirst bool) (atHangup, after int) {
	t.Helper()
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries:     benchTree(t, 20000),
		pageSize:    200,
		searchDelay: 5 * time.Millisecond,
	})
	addr := listening(t, rig)

	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "tcp", addr.String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	const crlf = "\r\n"
	request := fmt.Sprintf(
		"POST %s HTTP/1.1"+crlf+
			"Host: x"+crlf+
			"Content-Type: application/json"+crlf+
			"Cookie: %s=%s"+crlf+
			"Content-Length: %d"+crlf+crlf+"%s",
		path, session.CookieNameInsecure, rig.cookie, len(body), body)
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("writing the request: %v", err)
	}

	if readFirst {
		// A streamed response starts arriving before it is finished, so reading
		// the first bytes is proof the handler is in its page loop.
		if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatalf("setting a deadline: %v", err)
		}
		if _, err := conn.Read(make([]byte, 4096)); err != nil {
			t.Fatalf("reading the first bytes: %v", err)
		}
	} else {
		// A bounded response writes nothing until it has finished, so wait for
		// the work itself instead.
		deadline := time.Now().Add(10 * time.Second)
		for rig.fake.searchCount() < 3 && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
	}

	atHangup = rig.fake.searchCount()
	if err := conn.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	// A hundred pages of five milliseconds are left. An unbothered handler
	// serves dozens more in this window.
	time.Sleep(500 * time.Millisecond)
	return atHangup, rig.fake.searchCount()
}

func TestAStreamedSearchStopsWhenTheClientGoesAway(t *testing.T) {
	const body = `{"baseDn":"dc=alder,dc=test","scope":"sub","filter":"(objectClass=*)","limit":10000,"pageSize":200}`
	atHangup, after := hangUpDuring(t, "/api/v1/search", body, true)
	t.Logf("pages: %d at hangup, %d half a second later", atHangup, after)
	if after > atHangup+2 {
		t.Errorf("the search kept paging after the client left: %d became %d. "+
			"A disconnected client should stop the directory work, not only the writing",
			atHangup, after)
	}
}

// The same question for a bounded handler, and the answer is different.
//
// A bounded handler writes nothing until it has finished, so a broken
// connection tells it nothing, and fasthttp does not tell it either: its
// request context is not cancelled when the peer goes away. Measured, an
// abandoned tally serves around ninety more pages before it stops -- and it
// stops because it runs out of entries, not because anybody asked it to.
//
// So what is pinned here is the bound rather than a cancellation Alder does not
// have. Abandoned work ends, and ends within the limits the request carried;
// what keeps a caller from turning that into real cost is the ceiling on how
// many requests run at once, not per-request cancellation.
//
// The obvious fix -- fasthttp's ConnState, a map from connection to cancel
// function, and the keep-alive distinction between idle and closed -- was
// built, and cancels nothing. Why is measured and pinned by
// TestFasthttpReportsNoDisconnectWhileAHandlerRuns below.
func TestAnAbandonedTallyIsBoundedEvenThoughItIsNotCancelled(t *testing.T) {
	const body = `{"baseDn":"dc=alder,dc=test","scope":"sub","attribute":"sn","limit":20000}`
	atHangup, after := hangUpDuring(t, "/api/v1/inventory", body, false)
	t.Logf("pages: %d at hangup, %d half a second later", atHangup, after)

	// 20,000 entries at 200 a page is a hundred pages. Ending near there is the
	// work finishing on its own; running past it would mean a loop with no
	// bound at all, which is the failure that would matter.
	const pagesInTheWholeSearch = 20000 / 200
	if after > pagesInTheWholeSearch+2 {
		t.Errorf("an abandoned tally served %d pages for a search of %d: "+
			"the loop is not bounded by the limit it was given", after, pagesInTheWholeSearch)
	}
}

// Why the obvious fix does not work, pinned so that the day it starts working
// is a test failure rather than a thing nobody thinks to retry.
//
// The mechanism a bounded handler would need is a signal that the peer has
// gone. fasthttp offers two candidates and neither delivers one:
//
//   - RequestCtx implements context.Context, but its Done channel is not closed
//     when the connection drops. Deriving the request context from it changes
//     nothing; measured, not assumed.
//   - Server.ConnState is the same hook net/http has, and it does fire on
//     close -- but not until after the handler has returned. fasthttp serves a
//     connection from one goroutine, so while a handler runs nothing is reading
//     the socket and there is nothing to notice the close.
//
// The only remaining option is a watchdog reading the socket underneath
// fasthttp, which would steal the bytes of a pipelined request from the server
// that needs them. That is a real hazard for a signal that buys a bound already
// enforced twice over, so Alder does not do it.
func TestFasthttpReportsNoDisconnectWhileAHandlerRuns(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries:     benchTree(t, 20000),
		pageSize:    200,
		searchDelay: 5 * time.Millisecond,
	})

	var mu sync.Mutex
	var closedAt time.Time
	server := rig.app.Server()
	server.ConnState = func(_ net.Conn, state fasthttp.ConnState) {
		if state == fasthttp.StateClosed {
			mu.Lock()
			closedAt = time.Now()
			mu.Unlock()
		}
	}

	addr := listening(t, rig)
	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "tcp", addr.String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	const body = `{"baseDn":"dc=alder,dc=test","scope":"sub","attribute":"sn","limit":20000}`
	const crlf = "\r\n"
	request := fmt.Sprintf("POST /api/v1/inventory HTTP/1.1"+crlf+"Host: x"+crlf+
		"Content-Type: application/json"+crlf+"Cookie: %s=%s"+crlf+
		"Content-Length: %d"+crlf+crlf+"%s",
		session.CookieNameInsecure, rig.cookie, len(body), body)
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("writing the request: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for rig.fake.searchCount() < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	pagesAtHangup := rig.fake.searchCount()
	if err := conn.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	hungUpAt := time.Now()

	// Long enough for the handler to finish on its own.
	time.Sleep(2 * time.Second)

	mu.Lock()
	fired := closedAt
	mu.Unlock()

	if fired.IsZero() {
		t.Fatal("ConnState never reported the close at all")
	}
	served := rig.fake.searchCount() - pagesAtHangup
	t.Logf("close reported %v after the hangup, by which time %d more pages had been served",
		fired.Sub(hungUpAt).Round(time.Millisecond), served)

	// The finding. If this ever fails because the notice arrived while there
	// was still work to stop, fasthttp has changed and the cancellation is
	// worth building.
	if served < 10 {
		t.Errorf("ConnState reported the close in time to stop the work: only %d more "+
			"pages were served. fasthttp may now notice a disconnect during a handler, "+
			"which would make per-request cancellation possible -- see the comment above",
			served)
	}
}
