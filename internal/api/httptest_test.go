package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/session"
)

// The HTTP layer, tested.
//
// Until this file, nothing exercised a handler. The conformance suite sits
// below them — it drives the Driver against two real servers — and the unit
// tests sit beside them, covering the pure functions handlers call. Between the
// two was the layer that actually decides what a browser receives: routing,
// parameter parsing, the guards, and the shape of the JSON.
//
// That gap is not theoretical. A correct back end has been invisible three
// times in this codebase because a mapping between it and the wire was wrong or
// missing, and each time the tests were green. These run without Docker, so
// they run on every `go test ./...` rather than only where a harness exists.

// fakeSession is a directory.Session whose answers the test decides.
//
// A fake rather than a mock: handlers should be judged by what they return, not
// by which methods they happened to call.
type fakeSession struct {
	caps    directory.Capabilities
	sch     *schema.Schema
	entries []*directory.Entry
	entry   *directory.Entry

	// byDN answers Read per DN. Without it Read returns the same entry for
	// every DN, and a handler that read one entry twice instead of two
	// different ones would pass every test.
	byDN map[string]*directory.Entry

	searchErr error
	readErr   error
	applyErr  error

	// readDNs records what was asked for, in order.
	readDNs []string

	// applied records every write, so a test can assert that a handler sent
	// what the preview promised.
	applied []directory.ChangeRecord
	// lastSearch is the request as the handler built it, which is where
	// parameter parsing either worked or quietly did not.
	//
	// Guarded, because the concurrency tests run two requests against one fake
	// and the race detector is right that this is shared. Read it through
	// last().
	searchMu   sync.Mutex
	lastSearch *directory.SearchRequest

	// visibility answers the access probe, keyed "dn|attribute" in lower case.
	// Anything not named answers Absent, which is what a directory with no
	// access rules in the way would say.
	visibility map[string]directory.AttributeVisibility
	// visibilityAsked records every probe, so a test can assert that a handler
	// did not spend a round trip it did not need.
	visibilityAsked []string

	// pageSize makes Search page rather than return everything at once, so a
	// handler that consumes a search incrementally can be tested. Zero keeps
	// the old all-at-once behaviour that every other test relies on.
	pageSize int
	// driverPaging makes one Search call fill req.Limit by looping its own
	// pages, which is what the real driver does and what pageSize alone does
	// not: pageSize models a server, and a server is below the Session the
	// handlers talk to. Only a measurement needs the difference -- a handler
	// that asks for ten thousand at once has to be given ten thousand at once,
	// or it looks as frugal as one that asks a page at a time.
	driverPaging bool
	// searches counts the calls, which is how a test tells one round trip from
	// several.
	//
	// Atomic because the disconnect tests read it from the test goroutine while
	// a handler is still in its page loop, which is the whole question those
	// tests ask.
	searches atomic.Int64
	// failAfterSearches makes Search fail once that many have succeeded, which
	// is the only way to reach the failure a streamed response cannot report as
	// a status code.
	failAfterSearches int
	// referrals rides on every page, so a handler that keeps only the last
	// page's is caught rather than looking right on a single-page result.
	referrals []string
	// searchDelay makes each page take long enough for a test to interfere
	// partway through -- to close the connection, or to fill the concurrency
	// limit -- rather than racing a response that is already finished.
	searchDelay time.Duration
}

// searchCount is how many pages have been asked for, readable while a handler
// is still running.
func (f *fakeSession) searchCount() int { return int(f.searches.Load()) }

// last is the most recent search request the handler built.
func (f *fakeSession) last() *directory.SearchRequest {
	f.searchMu.Lock()
	defer f.searchMu.Unlock()
	return f.lastSearch
}

func (f *fakeSession) Capabilities() directory.Capabilities { return f.caps }

func (f *fakeSession) Schema(context.Context) (*schema.Schema, error) { return f.sch, nil }

func (f *fakeSession) Search(ctx context.Context, req directory.SearchRequest) (*directory.SearchResult, error) {
	f.searchMu.Lock()
	f.lastSearch = &req
	f.searchMu.Unlock()
	n := f.searches.Add(1)
	if f.searchDelay > 0 {
		select {
		case <-time.After(f.searchDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// Honoured rather than ignored, because a handler that streams its response
	// runs after its own handler function has returned -- and one that cancels
	// its context on the way out cuts the stream off at the first page. A fake
	// that never looks at the context cannot catch that, and did not: the
	// export shipped it, and only a real directory showed it.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	if f.failAfterSearches > 0 && int(n) > f.failAfterSearches {
		return nil, errors.New("the directory hung up partway through")
	}

	// With pageSize set the fake pages the way a server does: the cookie is an
	// offset, and a caller that ignores it sees only the first page. Nothing
	// could test the streaming tally without that, because a fake that returns
	// everything at once makes a loop that runs exactly once look correct.
	if f.pageSize > 0 {
		size := f.pageSize
		if req.Limit > 0 && req.Limit < size {
			size = req.Limit
		}
		start := 0
		if len(req.Cookie) > 0 {
			if n, err := strconv.Atoi(string(req.Cookie)); err == nil {
				start = n
			}
		}
		if start > len(f.entries) {
			start = len(f.entries)
		}
		if f.driverPaging && req.Limit > size {
			size = req.Limit
		}
		end := start + size
		if end > len(f.entries) {
			end = len(f.entries)
		}
		out := &directory.SearchResult{Entries: f.entries[start:end], Referrals: f.referrals}
		if end < len(f.entries) {
			out.Cookie = []byte(strconv.Itoa(end))
			out.Truncated = true
		}
		return out, nil
	}

	return &directory.SearchResult{Entries: f.entries, Referrals: f.referrals}, nil
}

func (f *fakeSession) Read(_ context.Context, target dn.DN, _ []string) (*directory.Entry, error) {
	f.readDNs = append(f.readDNs, target.String())
	if f.readErr != nil {
		return nil, f.readErr
	}
	if f.byDN != nil {
		if e, ok := f.byDN[strings.ToLower(target.String())]; ok {
			return e, nil
		}
		return nil, &ldapdriver.Error{Code: 32, Message: "No Such Object"}
	}
	return f.entry, nil
}

func (f *fakeSession) VisibilityOf(_ context.Context, target dn.DN, attribute string) (directory.AttributeVisibility, error) {
	key := strings.ToLower(target.String() + "|" + attribute)
	f.visibilityAsked = append(f.visibilityAsked, key)
	if v, ok := f.visibility[key]; ok {
		return v, nil
	}
	return directory.VisibilityAbsent, nil
}

func (f *fakeSession) SchemaDefinitions(context.Context, string, directory.SchemaDefKind) ([]string, error) {
	return nil, nil
}

func (f *fakeSession) Apply(_ context.Context, ch directory.ChangeRecord) error {
	if f.applyErr != nil {
		return f.applyErr
	}
	f.applied = append(f.applied, ch)
	return nil
}

func (f *fakeSession) Close() error { return nil }

// testRig is a server with its routes mounted and one session already open.
type testRig struct {
	app    *fiber.App
	server *Server
	fake   *fakeSession
	cookie string
}

// testing.TB rather than *testing.T so a benchmark can drive the real handler
// through the real router. A benchmark that reimplements the handler measures
// the reimplementation.
func newRig(t testing.TB, cfg Config, fake *fakeSession) *testRig {
	t.Helper()
	if fake.sch == nil {
		fake.sch = testSchema(t)
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = time.Minute
	}
	if cfg.MaxLifetime == 0 {
		cfg.MaxLifetime = time.Hour
	}

	// Built directly rather than through NewServer, which would dial a real
	// directory. Same package, so no test-only door has to exist in production
	// code for this.
	s := &Server{
		sessions: session.NewStore(slog.New(slog.DiscardHandler), cfg.IdleTimeout, cfg.MaxLifetime),
		logger:   slog.New(slog.DiscardHandler),
		cfg:      cfg,
		// Through the same helper NewServer uses. This rig builds a Server by
		// hand, so anything NewServer does that is not repeated here is a thing
		// no test can see -- which is how the gate came to be nil in every test
		// that was meant to exercise it.
		gate: gateFor(cfg),
	}
	t.Cleanup(s.sessions.Close)

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	s.Register(app)

	sess, err := s.sessions.Add(fake, directory.ConnConfig{
		Host: "ldap.example.test", Port: 636, TLS: directory.TLSModeLDAPS,
		BindDN: "cn=admin,dc=alder,dc=test",
		// A real value, so tests can assert on the secret itself rather than
		// on field names — "passwordModify" is a capability, not a leak.
		BindPassword: sentinelPassword,
	}, cfg.ReadOnly)
	if err != nil {
		t.Fatalf("opening a session: %v", err)
	}

	return &testRig{app: app, server: s, fake: fake, cookie: sess.ID}
}

// response is a request's outcome, already read and closed.
//
// The helpers hand back this rather than *http.Response so that no test has to
// remember to close a body — and so the linter can see that none is left open,
// which it cannot when the closing happens behind a helper.
type response struct {
	Status int
	Body   string
	Header http.Header
}

func (r *testRig) send(t testing.TB, req *http.Request) response {
	t.Helper()
	res, err := r.app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the response to %s %s: %v", req.Method, req.URL, err)
	}
	return response{Status: res.StatusCode, Body: string(body), Header: res.Header}
}

// do sends a request carrying the session cookie.
func (r *testRig) do(t testing.TB, method, target string, body io.Reader) response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: session.CookieNameInsecure, Value: r.cookie})
	return r.send(t, req)
}

// anonymous sends the same request with no session at all.
func (r *testRig) anonymous(t *testing.T, method, target string) response {
	t.Helper()
	return r.send(t, httptest.NewRequestWithContext(t.Context(), method, target, nil))
}

func decode[T any](t *testing.T, res response) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil {
		t.Fatalf("decoding the response: %v\nbody: %s", err, res.Body)
	}
	return out
}

// entryFixture is an ordinary person, including a password so that tests can
// assert it does not travel.
func entryFixture(t *testing.T) *directory.Entry {
	t.Helper()
	d, err := dn.Parse("uid=alice,ou=people,dc=alder,dc=test")
	if err != nil {
		t.Fatalf("parsing the fixture DN: %v", err)
	}
	e := directory.NewEntry(d)
	e.Set("objectClass", [][]byte{[]byte("top"), []byte("inetOrgPerson")})
	e.Set("cn", [][]byte{[]byte("Alice Liddell")})
	e.Set("sn", [][]byte{[]byte("Liddell")})
	e.Set("uid", [][]byte{[]byte("alice")})
	e.Set("mail", [][]byte{[]byte("alice@alder.test")})
	e.Set("userPassword", [][]byte{[]byte("{SSHA}averyrealsecret")})
	return e
}

// sentinelPassword is distinctive enough that finding it anywhere in a
// response body is unambiguous.
const sentinelPassword = "correct-horse-battery-staple-8f21"

func defaultCaps() directory.Capabilities {
	return directory.Capabilities{
		NamingContexts:    []string{"dc=alder,dc=test"},
		SubschemaSubentry: "cn=subschema",
		Paging:            true,
		WhoAmI:            true,
		PasswordModify:    true,
	}
}
