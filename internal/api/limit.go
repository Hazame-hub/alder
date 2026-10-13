package api

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

// How much work one Alder will do at once.
//
// Every other bound in the API limits one request: a page size, a result count,
// an export length, a document size. None of them limits how many requests
// there are. A directory search is cheap for the caller to ask for and
// expensive for the directory to answer, and a handful of subtree searches over
// a hundred thousand entries will occupy a directory for as long as they are
// allowed to.
//
// This is deliberately not a rate limiter. There is no per-caller accounting,
// no token bucket and no state to expire, because Alder has no users of its own
// to account to -- SECURITY.md says so, and a quota keyed on nothing is
// theatre. What it does is put a ceiling on concurrent work, which is the thing
// that actually protects the directory behind it.

// DefaultMaxInFlight is the number of API requests answered at once.
//
// Generous on purpose. The tree browser fires several requests per expansion
// and the entry view fetches schema, entry and references together, so a low
// number would queue an ordinary session against itself. It is a ceiling on
// abuse, not a throttle on use.
const DefaultMaxInFlight = 64

// DefaultMaxSessions is how many directory sessions are open at once by
// default. Each is a connection to a directory, held until it is closed or
// times out, and opening one needs no authentication beforehand, so the
// bound is what stops a loop of connection requests from holding hundreds.
// Far above what a team uses -- a session per browser tab or CLI run -- and
// a negative --max-sessions turns it off.
const DefaultMaxSessions = 256

// inFlightWait is how long a request waits for a slot before being refused.
//
// A short wait rather than none, because the burst that fills the last slot is
// usually a page loading, and refusing one of six requests that arrived
// together would make an ordinary interaction fail. A long wait would turn a
// full server into a slow one, which is worse to diagnose than a refused
// request.
const inFlightWait = 5 * time.Second

// gate admits a bounded number of requests at a time.
//
// A buffered channel rather than a counter and a condition variable: the
// channel is the counter, and the select below is the timeout.
type gate struct {
	slots chan struct{}
}

// gateFor is the gate a configuration asks for: zero takes the default, a
// negative number means none.
//
// It exists so that NewServer and the test rig cannot disagree about it. They
// did: the rig builds a Server as a struct literal rather than through
// NewServer, so every gate test passed against a nil gate until one of them
// dereferenced it.
func gateFor(cfg Config) *gate {
	n := cfg.MaxInFlight
	if n == 0 {
		n = DefaultMaxInFlight
	}
	return newGate(n)
}

func newGate(n int) *gate {
	if n <= 0 {
		return nil
	}
	return &gate{slots: make(chan struct{}, n)}
}

// answeredWithoutTheDirectory reports the requests that ask the directory
// nothing, and so have no business queueing behind a limit that exists to
// protect it.
//
// Letting go of a session is the one that matters. It is a delete from a map
// in Alder's own memory, and it was behind the gate -- so the moment Alder
// was busy, or the directory slow enough that every slot was held by a
// request waiting on it, Disconnect answered 503. An audit clicked it five
// times and watched nothing happen. Exactly when you most want to let go of
// a directory is when you could not.
//
// Reading the session is here for the same reason -- the header polls it,
// and a header that stops answering during load is the false-claim problem
// in reverse -- and the licence offer must work without a session at all.
func answeredWithoutTheDirectory(c *fiber.Ctx) bool {
	switch c.Path() {
	case "/api/v1/session":
		// Not POST: opening a session dials the directory and binds, which
		// is directory work and belongs in the queue.
		return c.Method() == fiber.MethodDelete || c.Method() == fiber.MethodGet
	case "/api/v1/source":
		return true
	}
	return false
}

// limit returns middleware holding the gate. A nil gate is no middleware at
// all, which is what a configuration of zero means.
func (g *gate) limit() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if g == nil || answeredWithoutTheDirectory(c) {
			return c.Next()
		}
		select {
		case g.slots <- struct{}{}:
		case <-time.After(inFlightWait):
			c.Set("Retry-After", "5")
			return writeError(c, fiber.StatusServiceUnavailable, ErrorErrorUpstream,
				"This Alder is already answering as many requests as it will run at once.",
				"Wait and try again. If this happens during ordinary use rather than "+
					"under load, --max-in-flight is too low for how the interface fetches.")
		}

		// Released when the handler returns, unless the handler kept it.
		//
		// A streamed response is written after that, by a goroutine fasthttp
		// starts, and for the search and the LDIF export that goroutine is
		// where every page after the first is asked for. Letting go here was
		// meant to hold the slot "for the part that talks to the directory and
		// not for the part that writes bytes out", and it did the opposite: a
		// ten-thousand-entry search spent all but its first page outside the
		// cap. Those handlers take the slot with keepSlot and give it back when
		// their writer finishes. The tree exports, which have finished with the
		// directory before they stream, do not, and still let go here.
		var once sync.Once
		held := &heldSlot{release: func() { once.Do(func() { <-g.slots }) }}
		c.Locals(slotKey{}, held)
		defer func() {
			if !held.kept {
				held.release()
			}
		}()
		return c.Next()
	}
}

// slotKey is where the gate leaves the slot it holds for a request, so a
// handler that streams can take it with keepSlot.
type slotKey struct{}

// heldSlot is one request's slot. kept is only touched by the request's own
// goroutine -- set by the handler, read by the gate after the handler returns --
// and release is safe to call from the writer because of the sync.Once.
type heldSlot struct {
	release func()
	kept    bool
}

// keepSlot takes the request's slot away from the gate, which would otherwise
// release it when the handler returns, and hands back the function that
// releases it. A streaming handler calls it before SetBodyStreamWriter and
// defers the release inside the writer, so the slot is held until the last
// page has been asked for and written.
//
// With no gate, or on a path the gate waves through, there is no slot, and the
// release does nothing.
func keepSlot(c *fiber.Ctx) func() {
	held, ok := c.Locals(slotKey{}).(*heldSlot)
	if !ok {
		return func() {}
	}
	held.kept = true
	return held.release
}

// inFlight is how many slots are taken, for tests.
func (g *gate) inFlight() int {
	if g == nil {
		return 0
	}
	return len(g.slots)
}
