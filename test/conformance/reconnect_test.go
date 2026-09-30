//go:build conformance

package conformance

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/filter"
)

// 1.35: a session that survives losing its connection.
//
// A UI audit watched every directory call fail while the session endpoint
// reported a healthy bind, and disconnect-and-reconnect cured it completely.
// The trigger was never reproduced. These proofs do not reproduce it either.
// They cut the connection deliberately -- which is the part that can be made
// deterministic -- and assert what happens next.
//
// The cut is a TCP proxy rather than a stopped container. Restarting 389 DS
// takes most of a minute and leaves the rest of the suite waiting for
// replication to settle; a proxy severs the socket immediately, does it to
// exactly one session, and leaves every other test's server alone. What
// reaches the client is the same either way: a connection that is gone.

// cutProxy forwards TCP to a directory and can sever everything through it.
type cutProxy struct {
	ln      net.Listener
	backend string

	mu     sync.Mutex
	live   []net.Conn
	closed bool
	// accepted counts connections ever made through this proxy. It is how a
	// test can tell a write that was retried from one that simply landed
	// before the cut: a retry has to dial, and dialling is visible here even
	// though the traffic itself is encrypted.
	accepted int
}

func newCutProxy(t *testing.T, backend string) *cutProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening for the proxy: %v", err)
	}
	p := &cutProxy{ln: ln, backend: backend}
	go p.serve()
	t.Cleanup(p.stop)
	return p
}

// opened is how many connections have been made through the proxy so far.
func (p *cutProxy) opened() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.accepted
}

func (p *cutProxy) addr() (string, int) {
	return "127.0.0.1", p.ln.Addr().(*net.TCPAddr).Port
}

func (p *cutProxy) serve() {
	for {
		client, err := p.ln.Accept()
		if err != nil {
			return
		}
		server, dialErr := net.DialTimeout("tcp", p.backend, 10*time.Second)
		if dialErr != nil {
			_ = client.Close()
			continue
		}
		p.mu.Lock()
		p.accepted++
		p.mu.Unlock()
		p.track(client, server)
		go func() { _, _ = io.Copy(server, client) }()
		go func() { _, _ = io.Copy(client, server) }()
	}
}

func (p *cutProxy) track(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		for _, c := range conns {
			_ = c.Close()
		}
		return
	}
	p.live = append(p.live, conns...)
}

// cut closes every connection passing through and lets new ones be made
// afterwards: a directory that went away and came back.
func (p *cutProxy) cut() {
	p.mu.Lock()
	live := p.live
	p.live = nil
	p.mu.Unlock()
	for _, c := range live {
		_ = c.Close()
	}
}

// stop closes the listener as well, so nothing can reconnect: a directory
// that is still gone.
func (p *cutProxy) stop() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	live := p.live
	p.live = nil
	p.mu.Unlock()

	_ = p.ln.Close()
	for _, c := range live {
		_ = c.Close()
	}
}

// connectThrough opens a session by way of a proxy, so a test can cut it.
//
// The harness certificate is verified, not skipped: a recovery proof that
// turned verification off would not show that the reconnect verifies either.
// The certificate is issued for localhost and the proxy listens on
// localhost, so the name still checks out.
func connectThrough(t *testing.T, s server, p *cutProxy, withConfig bool) directory.Session {
	t.Helper()
	host, port := p.addr()
	cfg := directory.ConnConfig{
		Host:           host,
		Port:           port,
		TLS:            directory.TLSModeLDAPS,
		CACertificates: caPool(t),
		ServerName:     "localhost",
		BindDN:         s.bindDN,
		BindPassword:   s.bindPW,
		Timeout:        15 * time.Second,
	}
	if withConfig {
		cfg.ConfigBindDN, cfg.ConfigBindPassword = s.schemaBindDN, s.schemaBindPW
	}
	drv := ldapdriver.New(slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	dialCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := drv.Connect(dialCtx, cfg)
	if err != nil {
		t.Fatalf("connecting to %s through the proxy: %v", s.name, err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func backendOf(s server) string { return net.JoinHostPort(s.host, strconv.Itoa(s.port)) }

func vals(items ...string) [][]byte {
	out := make([][]byte, 0, len(items))
	for _, v := range items {
		out = append(out, []byte(v))
	}
	return out
}

// removeIfPresent deletes an entry through a session that is not the one
// under test, so cleanup does not depend on the thing being proved.
func removeIfPresent(t *testing.T, sess directory.Session, target string) {
	t.Helper()
	err := sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, target), Type: directory.ChangeDelete})
	if err != nil && !notFound(err) {
		t.Fatalf("removing %s: %v", target, err)
	}
}

func readThrough(t *testing.T, sess directory.Session, target string) (*directory.Entry, error) {
	t.Helper()
	return sess.Read(ctx(t), mustDN(t, target), []string{"cn", "uid"})
}

// A read survives the connection being cut underneath it.
func TestAReadRecoversFromALostConnection(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		p := newCutProxy(t, backendOf(s))
		sess := connectThrough(t, s, p, false)
		target := "uid=user0002,ou=people," + suffix

		if _, err := readThrough(t, sess, target); err != nil {
			t.Fatalf("%s: the first read should work: %v", s.name, err)
		}

		p.cut()

		// The same read again. The driver finds the socket gone, dials
		// again, binds again and runs it once more; the caller sees an
		// entry, not an error.
		entry, err := readThrough(t, sess, target)
		if err != nil {
			t.Fatalf("%s: a read after the connection was cut should recover: %v", s.name, err)
		}
		if !strings.EqualFold(entry.DN.String(), target) {
			t.Errorf("%s: recovered the wrong entry: %s", s.name, entry.DN)
		}

		// And it keeps working, rather than recovering once and then being
		// subtly wrong.
		for i := range 3 {
			if _, err := readThrough(t, sess, target); err != nil {
				t.Fatalf("%s: read %d after recovery failed: %v", s.name, i+1, err)
			}
		}
		t.Logf("%s: the read recovered after the connection was severed", s.name)
	})
}

// A search recovers too, which is the path the tree and every list take.
func TestASearchRecoversFromALostConnection(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		p := newCutProxy(t, backendOf(s))
		sess := connectThrough(t, s, p, false)

		req := directory.SearchRequest{
			BaseDN: mustDN(t, suffix),
			Scope:  directory.ScopeSubtree,
			Filter: filter.Equal("objectClass", "organizationalUnit"),
			Limit:  50,
		}
		before, err := sess.Search(ctx(t), req)
		if err != nil {
			t.Fatalf("%s: the first search should work: %v", s.name, err)
		}

		p.cut()

		after, err := sess.Search(ctx(t), req)
		if err != nil {
			t.Fatalf("%s: a search after the cut should recover: %v", s.name, err)
		}
		if len(after.Entries) != len(before.Entries) {
			t.Errorf("%s: recovered a different answer: %d entries before, %d after",
				s.name, len(before.Entries), len(after.Entries))
		}
	})
}

// The one read that is not repeated: a page whose cookie belonged to the
// connection that just died.
func TestAnInterruptedPageIsNotSilentlyResumed(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, base directory.Session) {
		if !base.Capabilities().Paging {
			t.Skip("this server does not offer paged results")
		}
		p := newCutProxy(t, backendOf(s))
		sess := connectThrough(t, s, p, false)

		req := directory.SearchRequest{
			BaseDN:   mustDN(t, suffix),
			Scope:    directory.ScopeSubtree,
			Filter:   filter.Present("objectClass"),
			Limit:    10,
			PageSize: 5,
		}
		first, err := sess.Search(ctx(t), req)
		if err != nil {
			t.Fatalf("%s: the first page should work: %v", s.name, err)
		}
		if len(first.Cookie) == 0 {
			t.Skipf("%s: the server finished the search in one page", s.name)
		}

		p.cut()

		// Continuing with that cookie on a new connection asks for a page
		// the server never issued. The honest outcome is a failure saying
		// so, not a different page presented as the next one -- which the
		// caller would splice onto the first and never know.
		req.Cookie = first.Cookie
		next, err := sess.Search(ctx(t), req)
		if err == nil {
			t.Fatalf("%s: a continuation page after a cut returned %d entries instead of failing",
				s.name, len(next.Entries))
		}
		if !strings.Contains(err.Error(), "run the search again") {
			t.Errorf("%s: the failure should say what to do about it: %v", s.name, err)
		}
		t.Logf("%s: %v", s.name, err)
	})
}

// The rule that matters most: a write interrupted in flight is never sent
// again by Alder, and says so.
func TestAnInterruptedWriteIsNotRepeated(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, control directory.Session) {
		p := newCutProxy(t, backendOf(s))
		sess := connectThrough(t, s, p, false)

		target := "uid=reconnect-proof,ou=people," + suffix
		removeIfPresent(t, control, target)
		t.Cleanup(func() { removeIfPresent(t, control, target) })

		add := directory.ChangeRecord{
			DN:   mustDN(t, target),
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: vals("top", "person", "organizationalPerson", "inetOrgPerson")},
				{Name: "uid", Values: vals("reconnect-proof")},
				{Name: "cn", Values: vals("Reconnect Proof")},
				{Name: "sn", Values: vals("Proof")},
			},
		}

		// Cut first, so the change goes out on a connection the driver does
		// not yet know is dead. This is the ambiguous case the rule exists
		// for: the request may or may not have reached the server.
		p.cut()
		dialled := p.opened()
		err := sess.Apply(ctx(t), add)

		// The assertion that does the work. A retry cannot reuse the socket
		// it just lost, so it has to dial -- and this session has not yet
		// had an operation that would legitimately reopen the link. A new
		// connection during Apply means the change was sent twice.
		if p.opened() != dialled {
			t.Fatalf("%s: applying opened %d new connection(s) after the cut, so the change was "+
				"sent again; a change whose outcome is unknown must never be repeated",
				s.name, p.opened()-dialled)
		}

		if err == nil {
			// A legitimate outcome of the race -- the write landed before
			// the cut did -- but it proves little on its own, so say so
			// rather than passing quietly. The connection count above is
			// asserted either way, which is the part that matters.
			t.Skipf("%s: the add completed before the cut landed (and was not resent)", s.name)
		}
		if !errors.Is(err, ldapdriver.ErrWriteOutcomeUnknown) {
			t.Fatalf("%s: an interrupted write must report an unknown outcome, and reported: %v",
				s.name, err)
		}
		for _, want := range []string{"unknown", "will not send it again"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the message should contain %q: %v", s.name, want, err)
			}
		}

		// Exactly one attempt was made, however it landed. A driver that
		// retried would have met "already exists" on the second attempt and
		// reported that instead of an unknown outcome.
		if _, readErr := control.Read(ctx(t), mustDN(t, target), []string{"uid"}); readErr == nil {
			t.Logf("%s: the change reached the server before the cut, and was reported unknown", s.name)
		} else {
			t.Logf("%s: the change never reached the server, and was reported unknown", s.name)
		}
	})
}

// After an interrupted write the session is usable again.
func TestTheSessionKeepsWorkingAfterAnInterruptedWrite(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, control directory.Session) {
		p := newCutProxy(t, backendOf(s))
		sess := connectThrough(t, s, p, false)

		target := "ou=reconnect-proof-2," + suffix
		removeIfPresent(t, control, target)
		t.Cleanup(func() { removeIfPresent(t, control, target) })

		p.cut()
		// May fail as unknown, may have slipped through. Either way the
		// session has to be usable next.
		_ = sess.Apply(ctx(t), directory.ChangeRecord{
			DN:   mustDN(t, target),
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: vals("top", "organizationalUnit")},
				{Name: "ou", Values: vals("reconnect-proof-2")},
			},
		})

		if _, err := readThrough(t, sess, "uid=user0002,ou=people,"+suffix); err != nil {
			t.Fatalf("%s: the session should still work after an interrupted write: %v", s.name, err)
		}
	})
}

// When the directory is really gone, say so plainly.
func TestReconnectFailureIsReportedClearly(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		p := newCutProxy(t, backendOf(s))
		sess := connectThrough(t, s, p, false)
		if _, err := readThrough(t, sess, "uid=user0002,ou=people,"+suffix); err != nil {
			t.Fatalf("%s: the first read should work: %v", s.name, err)
		}

		// Not a cut: the listener goes too, so nothing can reconnect.
		p.stop()

		_, err := readThrough(t, sess, "uid=user0002,ou=people,"+suffix)
		if err == nil {
			t.Fatalf("%s: a read with no directory to reach must fail", s.name)
		}
		if !strings.Contains(err.Error(), "the directory connection") {
			t.Errorf("%s: the failure should name which connection was lost: %v", s.name, err)
		}
		if !strings.Contains(err.Error(), "could not be reopened") {
			t.Errorf("%s: the failure should say the reconnect was tried and failed: %v", s.name, err)
		}
		t.Logf("%s: %v", s.name, err)
	})
}

// The two connections recover as themselves.
func TestTheConfigurationConnectionRecoversAsItself(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, base directory.Session) {
		if !base.Capabilities().Config.Readable {
			t.Skip("this session cannot read the configuration tree")
		}
		p := newCutProxy(t, backendOf(s))
		sess := connectThrough(t, s, p, true)
		configDN := sess.Capabilities().Config.DN
		if configDN == "" {
			t.Skip("no configuration tree on this server")
		}

		if _, err := readThrough(t, sess, "uid=user0002,ou=people,"+suffix); err != nil {
			t.Fatalf("%s: reading data: %v", s.name, err)
		}
		if _, err := sess.Read(ctx(t), mustDN(t, configDN), []string{"objectClass"}); err != nil {
			t.Fatalf("%s: reading the configuration tree: %v", s.name, err)
		}

		// One proxy, so the cut takes both. What matters is that each comes
		// back as itself: the configuration read still succeeds, which it
		// could not if that link had rebound as the data identity on a
		// server where the data identity cannot read the tree.
		p.cut()

		if _, err := sess.Read(ctx(t), mustDN(t, configDN), []string{"objectClass"}); err != nil {
			t.Fatalf("%s: the configuration connection should recover as itself: %v", s.name, err)
		}
		if _, err := readThrough(t, sess, "uid=user0002,ou=people,"+suffix); err != nil {
			t.Fatalf("%s: the data connection should recover as itself: %v", s.name, err)
		}
		t.Logf("%s: both connections recovered, each as its own identity", s.name)
	})
}
