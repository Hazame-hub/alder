package api

import (
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

// limit returns middleware holding the gate. A nil gate is no middleware at
// all, which is what a configuration of zero means.
func (g *gate) limit() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if g == nil {
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

		// Released when the handler returns.
		//
		// A streamed response is written after that, by the connection
		// goroutine, so a search or an export holds its slot for the part that
		// talks to the directory and not for the part that writes bytes out.
		// That is the honest place for it: the directory is what is being
		// protected, and the writing is the client's own pace.
		defer func() { <-g.slots }()
		return c.Next()
	}
}

// inFlight is how many slots are taken, for tests.
func (g *gate) inFlight() int {
	if g == nil {
		return 0
	}
	return len(g.slots)
}
