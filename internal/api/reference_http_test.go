package api

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hazame-hub/alder/internal/allowlist"
	"github.com/hazame-hub/alder/internal/directory"
)

// A second server inside one session (B6).
//
// What has to hold: the reference is opened exactly as a session is, it can
// never be written to, it never outlives its session, and it is a connection
// --max-sessions counts. Each test below is one of those, with the reference
// a fake distinct from the session's so that a read from the wrong server
// shows.

// referenceDriver connects to the one fake it was given, counting dials and
// closes.
type referenceDriver struct {
	fake   *fakeSession
	dials  atomic.Int32
	closed atomic.Int32
}

func (d *referenceDriver) Connect(context.Context, directory.ConnConfig) (directory.Session, error) {
	d.dials.Add(1)
	return closingSession{fakeSession: d.fake, closed: &d.closed}, nil
}

const referenceBody = `{"host":"ldap-replica.example.test","port":636,"tls":"ldaps",` +
	`"bindDn":"cn=admin,dc=alder,dc=test","bindPassword":"` + referencePassword + `"}`

// referencePassword is distinctive, so finding it in a response is unambiguous.
const referencePassword = "reference-only-secret-5d7e"

// referenceRig is a rig whose driver connects to a separate fake, the
// reference, with its own vendor name so a response can be told apart.
func referenceRig(t *testing.T, cfg Config) (*testRig, *referenceDriver) {
	t.Helper()
	rig := newRig(t, cfg, &fakeSession{caps: defaultCaps(), sch: testSchema(t)})
	caps := defaultCaps()
	caps.VendorName = "Reference Directory"
	driver := &referenceDriver{fake: &fakeSession{caps: caps, sch: testSchema(t)}}
	rig.server.driver = driver
	return rig, driver
}

func (r *testRig) attach(t *testing.T) response {
	t.Helper()
	return r.do(t, http.MethodPost, "/api/v1/session/reference", strings.NewReader(referenceBody))
}

func TestAReferenceIsAttachedDescribedAndDetached(t *testing.T) {
	rig, driver := referenceRig(t, Config{})

	res := rig.attach(t)
	if res.Status != http.StatusCreated {
		t.Fatalf("attaching: %d\n%s", res.Status, res.Body)
	}
	info := decode[SessionInfo](t, res)
	if info.Role == nil || *info.Role != ServerSideReference {
		t.Errorf("role = %v, want reference", info.Role)
	}
	if info.ReadOnly == nil || !*info.ReadOnly {
		t.Error("the reference is not described as read-only")
	}
	if deref(info.Host) != "ldap-replica.example.test" || deref(info.VendorName) != "Reference Directory" {
		t.Errorf("described as %s (%s), not the reference", deref(info.Host), deref(info.VendorName))
	}
	if strings.Contains(res.Body, referencePassword) {
		t.Fatal("the reference's password came back in the response")
	}

	if got := rig.do(t, http.MethodGet, "/api/v1/session/reference", nil); got.Status != http.StatusOK ||
		!strings.Contains(got.Body, "ldap-replica.example.test") {
		t.Errorf("describing it: %d\n%s", got.Status, got.Body)
	}
	// The session itself is still the session's server.
	own := decode[SessionInfo](t, rig.do(t, http.MethodGet, "/api/v1/session", nil))
	if deref(own.Host) != "ldap.example.test" || own.Role == nil || *own.Role != ServerSideSession {
		t.Errorf("the session now describes %s as %v", deref(own.Host), own.Role)
	}

	if got := rig.do(t, http.MethodDelete, "/api/v1/session/reference", nil); got.Status != http.StatusNoContent {
		t.Fatalf("detaching: %d\n%s", got.Status, got.Body)
	}
	if n := driver.closed.Load(); n != 1 {
		t.Errorf("the reference connection was closed %d times, want 1", n)
	}
	if got := rig.do(t, http.MethodGet, "/api/v1/session/reference", nil); got.Status != http.StatusNotFound {
		t.Errorf("after detaching: %d, want 404", got.Status)
	}
	// Detaching again is not an error: there is nothing to let go of.
	if got := rig.do(t, http.MethodDelete, "/api/v1/session/reference", nil); got.Status != http.StatusNoContent {
		t.Errorf("detaching twice: %d", got.Status)
	}
}

func TestASecondReferenceIsRefusedBeforeDialling(t *testing.T) {
	rig, driver := referenceRig(t, Config{})
	if res := rig.attach(t); res.Status != http.StatusCreated {
		t.Fatalf("attaching: %d\n%s", res.Status, res.Body)
	}
	res := rig.attach(t)
	if res.Status != http.StatusConflict {
		t.Fatalf("a second attach answered %d, want 409\n%s", res.Status, res.Body)
	}
	if n := driver.dials.Load(); n != 1 {
		t.Errorf("dialled %d times; the second attach must be refused before connecting", n)
	}
}

func TestAttachingNeedsASession(t *testing.T) {
	rig, driver := referenceRig(t, Config{})
	res := rig.anonymousJSON(t, http.MethodPost, "/api/v1/session/reference", referenceBody)
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("an anonymous attach answered %d, want 401", res.Status)
	}
	if n := driver.dials.Load(); n != 0 {
		t.Errorf("an unauthenticated caller made this Alder dial %d times", n)
	}
}

func TestAttachingIsHeldToTheAllowlist(t *testing.T) {
	permitted, err := allowlist.Parse("ldap.example.test:636")
	if err != nil {
		t.Fatal(err)
	}
	rig, driver := referenceRig(t, Config{AllowedTargets: permitted})
	res := rig.attach(t)
	if res.Status != http.StatusForbidden || !strings.Contains(res.Body, "target_not_allowed") {
		t.Fatalf("a reference outside the allowlist answered %d\n%s", res.Status, res.Body)
	}
	if n := driver.dials.Load(); n != 0 {
		t.Errorf("dialled %d times; the allowlist is checked before anything is", n)
	}
}

// --max-sessions counts connections. Without that, attaching a reference to
// every session doubles the ceiling an operator set.
func TestAReferenceCountsAgainstTheSessionLimit(t *testing.T) {
	rig, driver := referenceRig(t, Config{MaxSessions: 1})
	res := rig.attach(t)
	if res.Status != http.StatusServiceUnavailable {
		t.Fatalf("attaching with the store full answered %d, want 503\n%s", res.Status, res.Body)
	}
	if n := driver.dials.Load(); n != 0 {
		t.Errorf("dialled %d times; a full server refuses before connecting", n)
	}

	rig, _ = referenceRig(t, Config{MaxSessions: 2})
	if res := rig.attach(t); res.Status != http.StatusCreated {
		t.Fatalf("attaching with room: %d\n%s", res.Status, res.Body)
	}
	if got := rig.server.sessions.Connections(); got != 2 {
		t.Errorf("connections = %d, want 2: a session and its reference", got)
	}
	if res := rig.anonymousJSON(t, http.MethodPost, "/api/v1/session", sessionBody); res.Status != http.StatusServiceUnavailable {
		t.Errorf("a new session beside a session and its reference answered %d, want 503", res.Status)
	}
}

// Connecting again replaces the browser's session and closes its reference,
// so at the limit those two connections are about to be freed: the new
// session takes their place instead of being refused.
func TestReconnectingAtTheLimitReplacesTheSessionAndItsReference(t *testing.T) {
	rig, driver := referenceRig(t, Config{MaxSessions: 2})
	if res := rig.attach(t); res.Status != http.StatusCreated {
		t.Fatalf("attaching: %d\n%s", res.Status, res.Body)
	}
	res := rig.withCookie(t, http.MethodPost, "/api/v1/session", sessionBody, rig.cookie)
	if res.Status != http.StatusCreated {
		t.Fatalf("reconnecting from the same browser answered %d, want 201\n%s", res.Status, res.Body)
	}
	if got := rig.server.sessions.Connections(); got != 1 {
		t.Errorf("connections = %d after reconnecting, want 1", got)
	}
	if n := driver.closed.Load(); n != 1 {
		t.Errorf("the replaced session's reference was closed %d times, want 1", n)
	}
	// A browser with no session still finds the store as full as it is.
	if res := rig.anonymousJSON(t, http.MethodPost, "/api/v1/session", sessionBody); res.Status != http.StatusCreated {
		t.Fatalf("a second session with one place left: %d\n%s", res.Status, res.Body)
	}
	if res := rig.anonymousJSON(t, http.MethodPost, "/api/v1/session", sessionBody); res.Status != http.StatusServiceUnavailable {
		t.Errorf("a third session against a limit of two answered %d, want 503", res.Status)
	}
}

// The reference never outlives its session.
func TestDisconnectingClosesTheReference(t *testing.T) {
	rig, driver := referenceRig(t, Config{})
	if res := rig.attach(t); res.Status != http.StatusCreated {
		t.Fatalf("attaching: %d\n%s", res.Status, res.Body)
	}
	rig.do(t, http.MethodDelete, "/api/v1/session", nil)
	if n := driver.closed.Load(); n != 1 {
		t.Errorf("disconnecting closed the reference %d times, want 1", n)
	}
	if got := rig.server.sessions.Connections(); got != 0 {
		t.Errorf("%d connections left after disconnecting", got)
	}
}

// Every write goes to the session's own server. Nothing in a change request
// can name the reference, and the reference is held as a type with no write.
func TestAWriteWithAReferenceAttachedGoesToTheSessionsServer(t *testing.T) {
	rig, driver := referenceRig(t, Config{})
	rig.fake.entry = entryFixture(t)
	if res := rig.attach(t); res.Status != http.StatusCreated {
		t.Fatalf("attaching: %d\n%s", res.Status, res.Body)
	}
	change := `{"dn":"uid=alice,ou=people,dc=alder,dc=test","type":"modify",` +
		`"mods":[{"op":"replace","name":"mail","values":[{"text":"a@alder.test"}]}]}`
	res := rig.do(t, http.MethodPost, "/api/v1/changes/apply", strings.NewReader(change))
	if res.Status != http.StatusOK {
		t.Fatalf("applying: %d\n%s", res.Status, res.Body)
	}
	if len(rig.fake.applied) != 1 {
		t.Errorf("the session's server received %d writes, want 1", len(rig.fake.applied))
	}
	if len(driver.fake.applied) != 0 {
		t.Errorf("the reference received %d writes", len(driver.fake.applied))
	}
}

func TestReadsNamingAnAbsentReferenceAreRefused(t *testing.T) {
	rig, _ := referenceRig(t, Config{})
	for name, req := range map[string]struct{ method, target, body string }{
		"schema":      {http.MethodGet, "/api/v1/schema?on=reference", ""},
		"replication": {http.MethodGet, "/api/v1/replication?on=reference", ""},
		"compare":     {http.MethodGet, "/api/v1/compare?left=dc%3Dalder%2Cdc%3Dtest&right=dc%3Dalder%2Cdc%3Dtest&rightOn=reference", ""},
		"capture":     {http.MethodPost, "/api/v1/snapshots/capture", `{"kind":"schema","on":"reference"}`},
		"diff":        {http.MethodPost, "/api/v1/diff", `{"source":{"live":{"base":"dc=alder,dc=test"}},"target":{"reference":{}}}`},
	} {
		var res response
		if req.body == "" {
			res = rig.do(t, req.method, req.target, nil)
		} else {
			res = rig.do(t, req.method, req.target, strings.NewReader(req.body))
		}
		if res.Status != http.StatusConflict {
			t.Errorf("%s: %d, want 409 with no reference attached\n%s", name, res.Status, res.Body)
		}
	}
}

func TestTheReferencesSchemaIsReadFromTheReference(t *testing.T) {
	rig, driver := referenceRig(t, Config{})
	driver.fake.sch = testSchema(t, "( 1.3.6.1.4.1.99999.2 NAME 'onlyOnTheReference' SUP top AUXILIARY )")
	if res := rig.attach(t); res.Status != http.StatusCreated {
		t.Fatalf("attaching: %d\n%s", res.Status, res.Body)
	}
	if res := rig.do(t, http.MethodGet, "/api/v1/schema?on=reference", nil); !strings.Contains(res.Body, "onlyOnTheReference") {
		t.Errorf("the reference's schema does not hold its own class: %d", res.Status)
	}
	if res := rig.do(t, http.MethodGet, "/api/v1/schema", nil); strings.Contains(res.Body, "onlyOnTheReference") {
		t.Error("the session's schema holds a class only the reference has")
	}
}

func TestAnEntryIsComparedAcrossTheTwoServers(t *testing.T) {
	rig, driver := referenceRig(t, Config{})
	left := entryFixture(t)
	right := entryFixture(t)
	right.Set("mail", [][]byte{[]byte("alice@replica.alder.test")})
	rig.fake.entry, driver.fake.entry = left, right
	if res := rig.attach(t); res.Status != http.StatusCreated {
		t.Fatalf("attaching: %d\n%s", res.Status, res.Body)
	}

	target := "uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest"
	res := rig.do(t, http.MethodGet, "/api/v1/compare?left="+target+"&right="+target+"&rightOn=reference", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("comparing: %d\n%s", res.Status, res.Body)
	}
	got := decode[EntryComparison](t, res)
	if got.Counts.Differs == 0 {
		t.Error("the same entry with a different mail on the reference compared as identical")
	}
	if deref(got.Left.Server) != "ldap.example.test:636" || deref(got.Right.Server) != "ldap-replica.example.test:636" {
		t.Errorf("the sides are labelled %q and %q", deref(got.Left.Server), deref(got.Right.Server))
	}
	if len(driver.fake.readDNs) != 1 || len(rig.fake.readDNs) != 1 {
		t.Errorf("reads: session %v, reference %v; want one each", rig.fake.readDNs, driver.fake.readDNs)
	}
	if strings.Contains(res.Body, "averyrealsecret") {
		t.Fatal("a password value reached the browser from the reference")
	}
}

// The comparison an operator wants: this server against the reference, with
// the changes that would make this one match. The reference is never the
// one a change is proposed for.
func TestADiffAgainstTheReferenceProposesChangesOnlyToTheSessionsServer(t *testing.T) {
	rig, driver := referenceRig(t, Config{})
	own := entryFixture(t)
	other := entryFixture(t)
	other.Set("mail", [][]byte{[]byte("alice@replica.alder.test")})
	rig.fake.entries, driver.fake.entries = []*directory.Entry{own}, []*directory.Entry{other}
	rig.fake.entry, driver.fake.entry = own, other
	if res := rig.attach(t); res.Status != http.StatusCreated {
		t.Fatalf("attaching: %d\n%s", res.Status, res.Body)
	}

	res := rig.do(t, http.MethodPost, "/api/v1/diff", strings.NewReader(
		`{"source":{"live":{"base":"ou=people,dc=alder,dc=test"}},"target":{"reference":{}}}`))
	if res.Status != http.StatusOK {
		t.Fatalf("comparing: %d\n%s", res.Status, res.Body)
	}
	d := decode[Diff](t, res)
	if d.Source.Kind != DiffSideKindLive || d.Target.Kind != DiffSideKindReference {
		t.Errorf("sides are %s and %s, want live and reference", d.Source.Kind, d.Target.Kind)
	}
	if deref(d.Target.Server) != "ldap-replica.example.test:636" || deref(d.Source.Server) != "ldap.example.test:636" {
		t.Errorf("the sides say they were read from %q and %q", deref(d.Source.Server), deref(d.Target.Server))
	}
	if d.Target.Base != "ou=people,dc=alder,dc=test" {
		t.Errorf("the reference was read at %q; an omitted base is the other side's", d.Target.Base)
	}
	if d.Counts.Modified != 1 {
		t.Fatalf("modified = %d, want 1\n%s", d.Counts.Modified, res.Body)
	}
	if d.Items[0].Candidate == nil || len(d.Items[0].Candidate.Changes) == 0 {
		t.Error("no change was proposed for the session's server")
	}

	// The other way round compares the same two servers, and proposes
	// nothing: the source is the reference, and nothing writes to it.
	res = rig.do(t, http.MethodPost, "/api/v1/diff", strings.NewReader(
		`{"source":{"reference":{"base":"ou=people,dc=alder,dc=test"}},"target":{"live":{}}}`))
	if res.Status != http.StatusOK {
		t.Fatalf("comparing the other way: %d\n%s", res.Status, res.Body)
	}
	d = decode[Diff](t, res)
	for _, item := range d.Items {
		if item.Candidate != nil && len(item.Candidate.Changes) > 0 {
			t.Errorf("a change was proposed with the reference as source: %+v", item.Candidate.Changes)
		}
	}

	res = rig.do(t, http.MethodPost, "/api/v1/diff", strings.NewReader(
		`{"source":{"reference":{"base":"ou=people,dc=alder,dc=test"}},"target":{"reference":{}}}`))
	if res.Status != http.StatusBadRequest {
		t.Errorf("both sides on the reference answered %d, want 400", res.Status)
	}
	res = rig.do(t, http.MethodPost, "/api/v1/diff", strings.NewReader(
		`{"source":{"live":{}},"target":{"reference":{}}}`))
	if res.Status != http.StatusBadRequest {
		t.Errorf("two read sides and no base answered %d, want 400", res.Status)
	}
}

func TestASnapshotIsCapturedFromTheReference(t *testing.T) {
	rig, driver := referenceRig(t, Config{})
	driver.fake.entries = []*directory.Entry{entryFixture(t)}
	if res := rig.attach(t); res.Status != http.StatusCreated {
		t.Fatalf("attaching: %d\n%s", res.Status, res.Body)
	}
	res := rig.do(t, http.MethodPost, "/api/v1/snapshots/capture", strings.NewReader(
		`{"base":"ou=people,dc=alder,dc=test","on":"reference"}`))
	if res.Status != http.StatusOK {
		t.Fatalf("capturing: %d\n%s", res.Status, res.Body)
	}
	if !strings.Contains(res.Body, "Reference Directory") || !strings.Contains(res.Body, "uid=alice") {
		t.Errorf("the capture is not the reference's:\n%.400s", res.Body)
	}
	if rig.fake.searchCount() != 0 {
		t.Errorf("the session's server was searched %d times for a capture of the reference", rig.fake.searchCount())
	}
}
