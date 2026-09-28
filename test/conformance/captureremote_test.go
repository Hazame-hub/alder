//go:build conformance

package conformance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/allowlist"
	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Capturing the server you are not connected to.
//
// A configuration snapshot exists to be compared against another server, and
// Alder could only capture the one it was connected to -- so the comparison
// an operator actually wants, this replica against its primary, meant
// capturing one, disconnecting, connecting to the other, and comparing. A UI
// audit measured forty-six interactions before the drift appeared, and lost
// its capture to the disconnect on the way.
//
// The harness has the real shape of the problem: two OpenLDAPs and two 389
// Directory Servers, a primary and its consumer, whose configurations differ
// in ways somebody would want to see.

func captureFrom(t *testing.T, client *http.Client, base string, from server) (string, int) {
	t.Helper()
	body := map[string]any{
		"kind": "config",
		"from": map[string]any{
			"host":               from.host,
			"port":               from.replicaPort,
			"tls":                "ldaps",
			"bindDn":             from.bindDN,
			"bindPassword":       from.bindPW,
			"insecureSkipVerify": true,
			"configBindDn":       from.schemaBindDN,
			"configBindPassword": from.schemaBindPW,
		},
	}
	res := post(t, client, base+"/snapshots/capture/from", mustEncode(t, body))
	return res.body, res.status
}

func TestAnotherServersConfigurationIsCapturedWithoutLeavingThisOne(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.replicaPort == 0 {
			t.Skip("this server has no consumer in the harness, or no readable configuration")
		}
		client, base := alderSession(t, s, true)

		// Connected to the primary, capturing the consumer. No second
		// session, no disconnect, and the session in hand is untouched.
		raw, status := captureFrom(t, client, base, s)
		if status != http.StatusOK {
			t.Fatalf("%s: capturing the consumer: %d\n%s", s.name, status, raw)
		}
		snap, _, err := snapshot.DecodeConfig([]byte(raw))
		if err != nil {
			t.Fatalf("%s: the captured document does not decode: %v", s.name, err)
		}
		if snap.Source.Provider == "" || len(snap.Settings) == 0 {
			t.Fatalf("%s: the capture is empty: %s", s.name, mustEncode(t, snap.Source))
		}

		// It is the *other* server's configuration, which is the whole
		// point. The two differ -- that is why the harness has both -- so a
		// capture identical to this server's own would mean the remote
		// connection was never used.
		own := captureConfig(t, client, base)
		ownSnap, _, err := snapshot.DecodeConfig([]byte(own))
		if err != nil {
			t.Fatal(err)
		}
		if snap.Checksum == ownSnap.Checksum {
			t.Errorf("%s: the capture of the consumer matches this server's own, so it captured the wrong directory",
				s.name)
		}

		// And the session is still the one it was: a remote capture opens no
		// session and replaces nothing.
		still := get(t, client, base+"/session")
		if !strings.Contains(still.body, `"connected":true`) {
			t.Errorf("%s: the session did not survive a remote capture: %s", s.name, still.body)
		}
		t.Logf("%s: captured the consumer on port %d from a session on %d, %d settings",
			s.name, s.replicaPort, s.port, len(snap.Settings))
	})
}

func TestARemoteCaptureIsRefusedForATargetTheOperatorHasNotPermitted(t *testing.T) {
	// The allowlist is the operator's rule about which directories this
	// instance may reach. An endpoint that dials outward and does not
	// consult it is a way around it.
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.replicaPort == 0 {
			t.Skip("this server has no consumer in the harness, or no readable configuration")
		}
		// An Alder that may reach this server and nothing else.
		permitted, err := allowlist.Parse(fmt.Sprintf("%s:%d", s.host, s.port))
		if err != nil {
			t.Fatalf("building the allowlist: %v", err)
		}
		client, base := alderSessionWith(t, s, true, api.Config{AllowedTargets: permitted})

		raw, status := captureFrom(t, client, base, s)
		if status != http.StatusForbidden {
			t.Fatalf("%s: capturing a target outside the allowlist answered %d, want 403\n%s",
				s.name, status, raw)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("decoding the refusal: %v\n%s", err, raw)
		}
		if body["error"] != "target_not_allowed" {
			t.Errorf("%s: refused with %v, want target_not_allowed", s.name, body["error"])
		}
		t.Logf("%s: a target outside the allowlist is refused before anything is dialled", s.name)
	})
}
