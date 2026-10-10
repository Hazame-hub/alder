//go:build conformance

package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

// The tree no longer probes every child for children of its own. It skips the
// probe where the listing already carries the server's own "none"
// (hasSubordinates FALSE, numSubordinates 0), and probes the rest exactly as
// before. That is only a speed-up if it changes no answer -- in particular
// for a bind the access rules restrict, where the server's counts ignore what
// the bind may see and the probe does not.
//
// So: for the administrator and for the delegated account, on both servers,
// every node the tree draws must say what the old per-child probe says, asked
// with the same identity.
func TestTheTreeAgreesWithTheProbeItNoLongerRunsEverywhere(t *testing.T) {
	parents := []string{suffix, "ou=people," + suffix, "ou=groups," + suffix}

	for _, s := range servers {
		for _, who := range []struct {
			name string
			as   server
		}{
			{"administrator", s},
			{"delegated account", restrictedAs(s)},
		} {
			t.Run(s.name+"/"+who.name, func(t *testing.T) {
				client, base := alder(t, who.as)
				sess := connect(t, who.as, false)
				probe, ok := sess.(interface {
					HasChildren(context.Context, dn.DN) (bool, error)
				})
				if !ok {
					t.Fatal("the driver session does not answer HasChildren")
				}

				drawn := 0
				for _, parent := range parents {
					res := get(t, client, base+"/tree?limit=1000&dn="+url.QueryEscape(parent))
					if res.status != http.StatusOK {
						t.Fatalf("listing %s: status %d\n%s", parent, res.status, res.body)
					}
					var page struct {
						Nodes []struct {
							Dn          string `json:"dn"`
							HasChildren bool   `json:"hasChildren"`
						} `json:"nodes"`
					}
					if err := json.Unmarshal([]byte(res.body), &page); err != nil {
						t.Fatalf("decoding the listing of %s: %v", parent, err)
					}
					for _, node := range page.Nodes {
						want, err := probe.HasChildren(ctx(t), mustDN(t, node.Dn))
						if err != nil {
							// What the tree does with a probe it may not make.
							want = false
						}
						if node.HasChildren != want {
							t.Errorf("%s: the tree says hasChildren=%v, the probe says %v",
								node.Dn, node.HasChildren, want)
						}
						drawn++
					}
				}
				// ou=people alone holds hundreds of leaves; a near-empty
				// comparison would prove nothing.
				if drawn < 300 {
					t.Fatalf("compared only %d nodes", drawn)
				}
			})
		}
	}
}

func restrictedAs(s server) server {
	r := s
	r.bindDN, r.bindPW = s.restrictedDN, s.restrictedPW
	return r
}
