//go:build conformance

package conformance

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// A configuration entry's secrets never reach the browser.
//
// This is rule 6 of the charter checked where it is easiest to lose: not in
// the feature that knows a particular attribute is a secret, but in the
// generic path every entry goes through. A snapshot withheld these and the
// replication view stripped them, and the entry viewer served them anyway --
// because the shared list they all should have gated on did not have them.
//
// The case that made it reachable is the harness's own replica: an
// olcSyncrepl value carries the consumer's bind password in the clear, inside
// an ordinary configuration attribute, on an entry any configuration
// administrator can open.

// secrets are the harness's own, plus the attribute names that carry one.
// None of them may appear in a response, in any encoding a browser would
// render.
var secrets = []string{
	"alder-admin", "alder-config", "alder-directory-manager", "alder-replication",
	"credentials=",
}

func TestAConfigurationEntryNeverServesItsSecrets(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		// Every server in the harness, including the replicas: the consumer
		// is the one whose configuration holds a password in the clear.
		targets := []server{s}
		if s.replicaPort != 0 {
			targets = append(targets, replicaOf(s))
		}
		for _, target := range targets {
			client, base := alderSession(t, target, true)

			for _, entry := range configEntriesHoldingSecrets(t, target, sess) {
				where := url.QueryEscape(entry)
				for _, path := range []string{
					"/entry?dn=" + where,
					"/export/ldif?dn=" + where + "&scope=base",
				} {
					res := get(t, client, base+path)
					if res.status != http.StatusOK {
						// An export the server will not give is not a leak.
						continue
					}
					for _, secret := range secrets {
						if strings.Contains(res.body, secret) {
							t.Errorf("%s: %s served %q from %s", target.name, path, secret, entry)
						}
					}
				}
			}
		}
	})
}

// configEntriesHoldingSecrets are the entries in this harness known to carry
// one. Named rather than searched for: a search for them would have to name
// the attributes, and the point is to check the entries an operator opens.
func configEntriesHoldingSecrets(t *testing.T, s server, _ directory.Session) []string {
	t.Helper()
	if strings.HasPrefix(s.name, "openldap") {
		// olcRootPW, and on the replica olcSyncrepl with credentials= in it.
		return []string{"olcDatabase={1}mdb,cn=config", "cn=config"}
	}
	// 389 DS: nsslapd-rootpw on the global entry, and the agreement's
	// encrypted credential on the supplier.
	return []string{"cn=config"}
}
