package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/session"
)

// closingSession is a fake whose Close is counted.
type closingSession struct {
	*fakeSession
	closed *atomic.Int32
}

func (c closingSession) Close() error { c.closed.Add(1); return nil }

// openingDriver connects every time, to a fake that counts its closes.
type openingDriver struct {
	t      *testing.T
	dials  atomic.Int32
	closed atomic.Int32
}

func (d *openingDriver) Connect(context.Context, directory.ConnConfig) (directory.Session, error) {
	d.dials.Add(1)
	return closingSession{fakeSession: &fakeSession{caps: defaultCaps(), sch: testSchema(d.t)}, closed: &d.closed}, nil
}

const sessionBody = `{"host":"ldap.example.test","port":636,"tls":"ldaps","bindDn":"cn=admin,dc=alder,dc=test","bindPassword":"pw"}`

func TestConnectionsBeyondTheSessionLimitAreRefusedBeforeDialling(t *testing.T) {
	rig := newRig(t, Config{MaxSessions: 2}, &fakeSession{caps: defaultCaps(), sch: testSchema(t)})
	driver := &openingDriver{t: t}
	rig.server.driver = driver

	// The rig holds one session already; one more fits.
	if res := rig.anonymousJSON(t, http.MethodPost, "/api/v1/session", sessionBody); res.Status != http.StatusCreated {
		t.Fatalf("second session: status %d\n%s", res.Status, res.Body)
	}
	res := rig.anonymousJSON(t, http.MethodPost, "/api/v1/session", sessionBody)
	if res.Status != http.StatusServiceUnavailable {
		t.Fatalf("third session: status %d, want 503\n%s", res.Status, res.Body)
	}
	if !strings.Contains(res.Body, "--max-sessions") {
		t.Errorf("the refusal does not name the setting:\n%s", res.Body)
	}
	if n := driver.dials.Load(); n != 1 {
		t.Errorf("dialled %d times; a full server must refuse before opening a connection", n)
	}
}

// Connecting again from a browser that has a session replaces it, and the
// replaced session lets go of its connection and password at once instead
// of lingering, unreachable, until it times out.
func TestReconnectingClosesThePreviousSession(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), sch: testSchema(t)})
	driver := &openingDriver{t: t}
	rig.server.driver = driver

	first := rig.anonymousJSON(t, http.MethodPost, "/api/v1/session", sessionBody)
	if first.Status != http.StatusCreated {
		t.Fatalf("first connect: %d\n%s", first.Status, first.Body)
	}
	cookie := sessionCookie(t, first)
	before := rig.server.sessions.Len()

	second := rig.withCookie(t, http.MethodPost, "/api/v1/session", sessionBody, cookie)
	if second.Status != http.StatusCreated {
		t.Fatalf("second connect: %d\n%s", second.Status, second.Body)
	}
	if got := rig.server.sessions.Len(); got != before {
		t.Errorf("live sessions = %d after reconnecting, want %d: the old one was kept", got, before)
	}
	if n := driver.closed.Load(); n != 1 {
		t.Errorf("the replaced session's connection was closed %d times, want 1", n)
	}
}

func sessionCookie(t *testing.T, res response) string {
	t.Helper()
	for _, c := range (&http.Response{Header: res.Header}).Cookies() {
		if c.Value != "" {
			return c.Value
		}
	}
	t.Fatalf("no session cookie in %v", res.Header)
	return ""
}

// anonymousJSON sends JSON with no session cookie, as a first connection does.
func (r *testRig) anonymousJSON(t testing.TB, method, target, body string) response {
	t.Helper()
	return r.withCookie(t, method, target, body, "")
}

// withCookie sends JSON carrying the given session cookie, if any.
func (r *testRig) withCookie(t testing.TB, method, target, body, cookie string) response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: session.CookieNameInsecure, Value: cookie})
	}
	return r.send(t, req)
}
