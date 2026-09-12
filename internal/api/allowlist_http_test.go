package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/allowlist"
	"github.com/hazame-hub/alder/internal/directory"
)

// The allowlist is enforced in the handler, so these are about the handler:
// that the refusal happens before anything is dialled, that it is the right
// status and code, and that a malformed target is still told apart from a
// forbidden one once the list is on.

// countingDriver records whether a connection was attempted. It never
// succeeds -- what matters here is only whether Connect was reached at all.
type countingDriver struct{ calls int }

func (d *countingDriver) Connect(context.Context, directory.ConnConfig) (directory.Session, error) {
	d.calls++
	return nil, errNoDirectory
}

type noDirectoryError struct{}

func (noDirectoryError) Error() string { return "no directory in this test" }

var errNoDirectory = noDirectoryError{}

// connectRig is a server with an allowlist and a driver that counts calls.
func connectRig(t *testing.T, list string) (*testRig, *countingDriver) {
	t.Helper()
	parsed, err := allowlist.Parse(list)
	if err != nil {
		t.Fatalf("parsing the allowlist %q: %v", list, err)
	}
	rig := newRig(t, Config{AllowedTargets: parsed}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
	})
	driver := &countingDriver{}
	rig.server.driver = driver
	return rig, driver
}

// connect sends what the connection screen sends, including a password, so
// every assertion below is also an assertion that it did not come back.
func connect(t *testing.T, rig *testRig, host string, port int) response {
	t.Helper()
	body := fmt.Sprintf(
		`{"host":%q,"port":%d,"tls":"ldaps","bindDn":"cn=admin,dc=alder,dc=test","bindPassword":%q}`,
		host, port, allowlistTestPassword)
	return rig.do(t, http.MethodPost, "/api/v1/session", strings.NewReader(body))
}

const allowlistTestPassword = "correct-horse-battery-staple"

func TestConnectIsRefusedForATargetOutsideTheAllowlist(t *testing.T) {
	rig, driver := connectRig(t, "ldap1.example.com:636")

	res := connect(t, rig, "evil.example.test", 636)

	if res.Status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403\nbody: %s", res.Status, res.Body)
	}
	body := decode[Error](t, res)
	if body.Error != ErrorErrorTargetNotAllowed {
		t.Errorf("error = %q, want %q", body.Error, ErrorErrorTargetNotAllowed)
	}
	// The whole point of enforcing before Connect: the refusal is what stops
	// Alder reaching a host of the caller's choosing, so a refusal that dialled
	// first would not be one.
	if driver.calls != 0 {
		t.Errorf("the driver was called %d times for a refused target", driver.calls)
	}
	// An operator reading the refusal should be able to see what to add.
	if body.Detail == nil || !strings.Contains(*body.Detail, "ldap1.example.com:636") {
		t.Errorf("the refusal does not name what is permitted: %+v", body.Detail)
	}
}

func TestConnectReachesTheDriverForAnAllowedTarget(t *testing.T) {
	rig, driver := connectRig(t, "ldap1.example.com:636, ldap2.example.com")

	for _, tc := range []struct {
		name string
		host string
		port int
	}{
		{"the exact entry", "ldap1.example.com", 636},
		{"folded case and a trailing dot", "LDAP1.Example.com.", 636},
		{"an entry without a port, on one port", "ldap2.example.com", 389},
		{"the same entry on another", "ldap2.example.com", 1636},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := driver.calls
			res := connect(t, rig, tc.host, tc.port)
			// The driver in this test never succeeds, so reaching it is a 502.
			// That is the assertion: the allowlist let it past.
			if res.Status == http.StatusForbidden {
				t.Fatalf("an allowed target was refused: %s", res.Body)
			}
			if driver.calls != before+1 {
				t.Errorf("the driver was not reached for %s:%d", tc.host, tc.port)
			}
		})
	}
}

// Malformed and forbidden are different answers. Turning the allowlist on must
// change which destinations are reachable, not which inputs parse.
func TestAMalformedTargetIsABadRequestNotARefusal(t *testing.T) {
	for _, list := range []string{"", "ldap1.example.com:636"} {
		name := "with an allowlist"
		if list == "" {
			name = "without an allowlist"
		}
		t.Run(name, func(t *testing.T) {
			rig, driver := connectRig(t, list)
			res := connect(t, rig, "ldap://ldap1.example.com", 636)
			if res.Status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400\nbody: %s", res.Status, res.Body)
			}
			if driver.calls != 0 {
				t.Errorf("the driver was called %d times for a malformed target", driver.calls)
			}
		})
	}
}

// The default has to stay the default: an operator who has configured nothing
// still gets a tool that connects where they point it.
func TestWithoutAnAllowlistAnyTargetReachesTheDriver(t *testing.T) {
	rig, driver := connectRig(t, "")
	res := connect(t, rig, "anything.example.test", 389)
	if res.Status == http.StatusForbidden {
		t.Fatalf("an unconfigured Alder refused a target: %s", res.Body)
	}
	if driver.calls != 1 {
		t.Errorf("the driver was called %d times, want 1", driver.calls)
	}
}

// Rule 6, at the one endpoint that receives a password.
func TestARefusalNeverEchoesTheBindPassword(t *testing.T) {
	rig, _ := connectRig(t, "ldap1.example.com:636")
	for _, host := range []string{"evil.example.test", "ldap://malformed"} {
		res := connect(t, rig, host, 636)
		if strings.Contains(res.Body, allowlistTestPassword) {
			t.Fatalf("the response to %s carried the bind password: %s", host, res.Body)
		}
	}
}

// The enum value is part of the API, so a client can switch on it. Pinned
// against the spec's spelling rather than against whatever the generator
// produced this week.
func TestTheTargetRefusalCodeIsStable(t *testing.T) {
	if got := string(ErrorErrorTargetNotAllowed); got != "target_not_allowed" {
		t.Errorf("the refusal code is %q, want \"target_not_allowed\"", got)
	}
	var e Error
	if err := json.Unmarshal(
		[]byte(`{"error":"target_not_allowed","message":"x"}`), &e); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !e.Error.Valid() {
		t.Error("target_not_allowed does not decode as a known error code")
	}
}
