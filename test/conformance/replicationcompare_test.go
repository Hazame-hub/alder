//go:build conformance

package conformance

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// 1.41: a supplier and its consumer, compared per origin. The harness's pairs
// are seeded and settled before the suite runs, so once replication has
// caught up the two must read as in step on both servers -- through two
// completely different records, contextCSN on one and the replica update
// vector on the other, which is what the comparison exists to make one
// answer.

func TestASupplierAndItsConsumerAreInStep(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := withReference(t, s, true)

		var cmp api.ReplicationComparison
		deadline := time.Now().Add(30 * time.Second)
		for {
			res := get(t, client, base+"/replication/compare")
			if res.status != http.StatusOK {
				t.Fatalf("%s: GET /replication/compare: %d\n%s", s.name, res.status, res.body)
			}
			cmp = decodeInto[api.ReplicationComparison](t, res)
			if cmp.Verdict == api.ReplicationInStep || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Second)
		}

		var found *api.ReplicationSuffixComparison
		for i := range cmp.Suffixes {
			if strings.EqualFold(cmp.Suffixes[i].Dn, suffix) {
				found = &cmp.Suffixes[i]
			}
		}
		if found == nil {
			t.Fatalf("%s: the comparison holds nothing about %s: %s", s.name, suffix, mustEncode(t, cmp))
		}
		if found.Verdict != api.ReplicationInStep || len(found.Origins) == 0 {
			t.Fatalf("%s: a settled supplier and consumer are %q: %s", s.name, found.Verdict, mustEncode(t, cmp))
		}
		for _, o := range found.Origins {
			if o.Verdict != api.ReplicationInStep || o.A == nil || o.B == nil || o.SpanSeconds != 0 {
				t.Errorf("%s: origin %s: %s", s.name, o.Origin, mustEncode(t, o))
			}
			if o.A != nil && (o.A.Csn == nil || *o.A.Csn == "") {
				t.Errorf("%s: origin %s was compared without its change sequence", s.name, o.Origin)
			}
		}
		if cmp.A == "" || cmp.B == "" || cmp.A == cmp.B || cmp.Caveat == "" {
			t.Errorf("%s: sides or caveat missing: %s", s.name, mustEncode(t, cmp))
		}
	})
}

func TestComparingNeedsAReference(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		client, base := alderSession(t, s, true)
		if res := get(t, client, base+"/replication/compare"); res.status != http.StatusConflict {
			t.Fatalf("%s: with no reference: %d\n%s", s.name, res.status, res.body)
		}
	})
}
