//go:build conformance

package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/allowlist"
	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// A second server inside one session (B6).
//
// The harness has the real shape of the problem: each server has a consumer,
// and a session on the supplier attaches the consumer as its reference. What
// is held to both servers here is what the proposal promised: the reference
// is reached the way a session is and held to the same allowlist, every read
// that names it reads *it* -- the same answer a session of its own would give
// -- nothing proposes a change to it, and its password never reaches a
// response or the log.

// attachBody is the request that attaches a server as the reference, built
// the way the connection screen connects: LDAPS, the harness CA, verified.
func attachBody(t *testing.T, to server, withConfig bool) string {
	t.Helper()
	caPEM, err := os.ReadFile(filepath.Join("..", "compose", "certs", "ca.crt"))
	if err != nil {
		t.Fatalf("reading the harness CA: %v", err)
	}
	fields := map[string]any{
		"host":          to.host,
		"port":          to.port,
		"tls":           "ldaps",
		"caCertificate": string(caPEM),
		"serverName":    "localhost",
		"bindDn":        to.bindDN,
		"bindPassword":  to.bindPW,
	}
	if withConfig && to.schemaBindDN != "" {
		fields["configBindDn"] = to.schemaBindDN
		fields["configBindPassword"] = to.schemaBindPW
	}
	return mustEncode(t, fields)
}

// withReference is a session on s with s's consumer attached as its
// reference. It skips a server the harness gives no consumer.
func withReference(t *testing.T, s server, withConfig bool) (*http.Client, string) {
	t.Helper()
	if s.replicaPort == 0 {
		t.Skip("this server has no consumer in the harness")
	}
	client, base := alderSession(t, s, withConfig)
	res := post(t, client, base+"/session/reference", attachBody(t, replicaOf(s), withConfig))
	if res.status != http.StatusCreated {
		t.Fatalf("%s: attaching the consumer as the reference: %d\n%s", s.name, res.status, res.body)
	}
	return client, base
}

func TestTheConsumerIsAttachedAsAReadOnlyReference(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		client, base := withReference(t, s, false)

		info := decodeInto[api.SessionInfo](t, get(t, client, base+"/session/reference"))
		if info.Role == nil || *info.Role != api.ServerSideReference || info.ReadOnly == nil || !*info.ReadOnly {
			t.Errorf("%s: the reference is described as %+v", s.name, info)
		}
		if info.Port == nil || *info.Port != s.replicaPort {
			t.Errorf("%s: the reference is on port %v, want the consumer's %d", s.name, info.Port, s.replicaPort)
		}
		// The session is still the supplier.
		own := decodeInto[api.SessionInfo](t, get(t, client, base+"/session"))
		if own.Port == nil || *own.Port != s.port {
			t.Errorf("%s: the session moved to port %v", s.name, own.Port)
		}

		// A second attach is refused; a detach lets go; the session stays.
		if res := post(t, client, base+"/session/reference", attachBody(t, replicaOf(s), false)); res.status != http.StatusConflict {
			t.Errorf("%s: a second reference answered %d, want 409", s.name, res.status)
		}
		if res := del(t, client, base+"/session/reference"); res.status != http.StatusNoContent {
			t.Errorf("%s: detaching answered %d", s.name, res.status)
		}
		if res := get(t, client, base+"/session/reference"); res.status != http.StatusNotFound {
			t.Errorf("%s: after detaching, the reference answers %d", s.name, res.status)
		}
		if res := get(t, client, base+"/session"); !strings.Contains(res.body, `"connected":true`) {
			t.Errorf("%s: detaching the reference closed the session: %s", s.name, res.body)
		}
	})
}

func TestAttachingAReferenceIsHeldToTheAllowlist(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		if s.replicaPort == 0 {
			t.Skip("this server has no consumer in the harness")
		}
		// An Alder that may reach this server and nothing else.
		permitted, err := allowlist.Parse(fmt.Sprintf("%s:%d", s.host, s.port))
		if err != nil {
			t.Fatal(err)
		}
		client, base := alderSessionWith(t, s, false, api.Config{AllowedTargets: permitted})
		res := post(t, client, base+"/session/reference", attachBody(t, replicaOf(s), false))
		if res.status != http.StatusForbidden || !strings.Contains(res.body, "target_not_allowed") {
			t.Fatalf("%s: a reference outside the allowlist answered %d\n%s", s.name, res.status, res.body)
		}
	})
}

// Each read that names the reference gives the answer a session of its own
// on that server would have given.
func TestReadsOnTheReferenceAreTheReferencesOwn(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := withReference(t, s, true)
		direct, directBase := alderSession(t, replicaOf(s), true)

		onRef := decodeInto[api.SchemaView](t, get(t, client, base+"/schema?on=reference"))
		alone := decodeInto[api.SchemaView](t, get(t, direct, directBase+"/schema"))
		if onRef.SubschemaDn != alone.SubschemaDn || mustEncode(t, onRef.Counts) != mustEncode(t, alone.Counts) {
			t.Errorf("%s: the reference's schema (%s, %s) is not the consumer's (%s, %s)",
				s.name, onRef.SubschemaDn, mustEncode(t, onRef.Counts), alone.SubschemaDn, mustEncode(t, alone.Counts))
		}

		if sess.Capabilities().Config.Readable {
			refRepl := get(t, client, base+"/replication?on=reference")
			aloneRepl := get(t, direct, directBase+"/replication")
			if refRepl.status != http.StatusOK || aloneRepl.status != http.StatusOK {
				t.Fatalf("%s: replication on the reference %d, alone %d\n%s", s.name, refRepl.status, aloneRepl.status, refRepl.body)
			}
			ref, alone := decodeInto[api.ReplicationReport](t, refRepl), decodeInto[api.ReplicationReport](t, aloneRepl)
			if ref.Role != alone.Role {
				t.Errorf("%s: the reference reports role %s, the consumer on its own %s", s.name, ref.Role, alone.Role)
			}

			// A configuration captured from the reference is the one 1.33's
			// one-shot capture of the same server returns.
			refCapture := post(t, client, base+"/snapshots/capture", `{"kind":"config","on":"reference"}`)
			if refCapture.status != http.StatusOK {
				t.Fatalf("%s: capturing the reference's configuration: %d\n%s", s.name, refCapture.status, refCapture.body)
			}
			fromRef, _, err := snapshot.DecodeConfig([]byte(refCapture.body))
			if err != nil {
				t.Fatal(err)
			}
			fromAlone, _, err := snapshot.DecodeConfig([]byte(captureConfig(t, direct, directBase)))
			if err != nil {
				t.Fatal(err)
			}
			if fromRef.Checksum != fromAlone.Checksum {
				t.Errorf("%s: the reference's configuration capture differs from the consumer's own", s.name)
			}
		}
		t.Logf("%s: schema and replication read through the reference match the consumer read directly", s.name)
	})
}

// The comparison the audits asked for: this server against the reference,
// in one request, with changes proposed only for this server.
func TestTheSupplierIsComparedWithItsConsumerInOneSession(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := withReference(t, s, true)

		d := diffOverHTTP(t, client, base,
			`{"source":{"live":{"base":"ou=people,`+suffix+`","scope":"one"}},"target":{"reference":{}}}`)
		if d.Source.Kind != api.DiffSideKindLive || d.Target.Kind != api.DiffSideKindReference {
			t.Fatalf("%s: sides are %s and %s", s.name, d.Source.Kind, d.Target.Kind)
		}
		wantRef := fmt.Sprintf("%s:%d", s.host, s.replicaPort)
		if d.Target.Server == nil || *d.Target.Server != wantRef {
			t.Errorf("%s: the reference side says it was read from %v, want %s", s.name, d.Target.Server, wantRef)
		}
		if d.Target.EntryCount == 0 || d.Target.Base != d.Source.Base {
			t.Errorf("%s: the reference side read %d entries at %q", s.name, d.Target.EntryCount, d.Target.Base)
		}

		// The other way round proposes nothing: the reference is never written.
		back := diffOverHTTP(t, client, base,
			`{"source":{"reference":{"base":"ou=people,`+suffix+`","scope":"one"}},"target":{"live":{}}}`)
		for _, item := range back.Items {
			if item.Candidate != nil && len(item.Candidate.Changes) > 0 {
				t.Errorf("%s: a change was proposed for the reference: %+v", s.name, item.Candidate.Changes)
			}
		}

		if sess.Capabilities().Config.Readable {
			cfg := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"reference":{}}}`)
			if cfg.Config.ProviderMismatch {
				t.Errorf("%s: a supplier and its own consumer compared as different software", s.name)
			}
			if cfg.Counts.Unchanged == cfg.Counts.Compared {
				t.Errorf("%s: the supplier's and the consumer's configurations compared identical; "+
					"the harness configures them differently", s.name)
			}
		}

		// One entry, on both servers.
		entry := url.QueryEscape("ou=people," + suffix)
		cmp := get(t, client, base+"/compare?left="+entry+"&right="+entry+"&rightOn=reference")
		if cmp.status != http.StatusOK {
			t.Fatalf("%s: comparing an entry across the two: %d\n%s", s.name, cmp.status, cmp.body)
		}
		got := decodeInto[api.EntryComparison](t, cmp)
		if got.Right.Server == nil || *got.Right.Server != wantRef {
			t.Errorf("%s: the right entry says it came from %v", s.name, got.Right.Server)
		}
	})
}

// The reference never outlives its session, and it is a connection
// --max-sessions counts.
func TestAReferenceIsACountedConnectionThatClosesWithItsSession(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		if s.replicaPort == 0 {
			t.Skip("this server has no consumer in the harness")
		}
		client, base := alderSessionWith(t, s, false, api.Config{MaxSessions: 1})
		if res := post(t, client, base+"/session/reference", attachBody(t, replicaOf(s), false)); res.status != http.StatusServiceUnavailable {
			t.Errorf("%s: attaching with one connection allowed and one held answered %d, want 503", s.name, res.status)
		}

		client, base = withReference(t, s, false)
		del(t, client, base+"/session")
		if res := get(t, client, base+"/session/reference"); res.status != http.StatusUnauthorized {
			t.Errorf("%s: after disconnecting, the reference answers %d, want 401", s.name, res.status)
		}
	})
}

// del sends a DELETE carrying the client's session cookie.
func del(t *testing.T, client *http.Client, target string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, target, nil)
	if err != nil {
		t.Fatalf("building a request for %s: %v", target, err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("deleting %s: %v", target, err)
	}
	defer func() { _ = res.Body.Close() }()
	return httpResult{status: res.StatusCode}
}

// lockedBuffer is a log destination two goroutines may write to.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The reference's credentials never reach the log at any level, nor any
// response -- the check 1.33 made of a capture, made of an attach.
func TestAReferencesPasswordIsNeverLoggedOrReturned(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, _ directory.Session) {
		if s.replicaPort == 0 {
			t.Skip("this server has no consumer in the harness")
		}
		logs := &lockedBuffer{}
		srv := api.NewServer(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})), api.Config{})
		t.Cleanup(srv.Close)
		app := fiber.New(fiber.Config{DisableStartupMessage: true})
		srv.Register(app)
		var lc net.ListenConfig
		ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = app.Listener(ln) }()
		t.Cleanup(func() { _ = app.Shutdown() })
		base := "http://" + ln.Addr().String() + "/api/v1"
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Jar: jar}

		var connect map[string]any
		if err := json.Unmarshal([]byte(attachBody(t, s, true)), &connect); err != nil {
			t.Fatal(err)
		}
		if res := post(t, client, base+"/session", mustEncode(t, connect)); res.status != http.StatusCreated {
			t.Fatalf("%s: connecting: %d\n%s", s.name, res.status, res.body)
		}

		var bodies []string
		attached := post(t, client, base+"/session/reference", attachBody(t, replicaOf(s), true))
		if attached.status != http.StatusCreated {
			t.Fatalf("%s: attaching: %d\n%s", s.name, attached.status, attached.body)
		}
		bodies = append(bodies, attached.body, get(t, client, base+"/session/reference").body)
		// A refused attach carries the password too, and is logged.
		bad := strings.Replace(attachBody(t, replicaOf(s), true), `"host":"`+s.host+`"`, `"host":"`+s.host+`.invalid"`, 1)
		bodies = append(bodies, post(t, client, base+"/session/reference", bad).body)
		del(t, client, base+"/session/reference")
		bodies = append(bodies, post(t, client, base+"/session/reference", bad).body)
		del(t, client, base+"/session")

		log := logs.String()
		if !strings.Contains(log, "role=reference") {
			t.Errorf("%s: the reference's connection was never logged as one:\n%s", s.name, log)
		}
		for _, secret := range []string{s.bindPW, s.schemaBindPW} {
			if secret == "" {
				continue
			}
			if strings.Contains(log, secret) {
				t.Fatalf("%s: a reference password is in the log", s.name)
			}
			for _, b := range bodies {
				if strings.Contains(b, secret) {
					t.Fatalf("%s: a reference password came back in a response", s.name)
				}
			}
		}
	})
}
