//go:build conformance

package conformance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/filter"
)

// 1.24: the harness replicates.
//
// Everything Alder will say about replication is a claim about a directory
// that is replicating, and until now the harness had none: two servers, each
// alone, each answering "no" to every question worth asking. So it grew a
// consumer for each -- openldap-replica over syncrepl, ds389-replica over a
// replication agreement -- and this file is the assertion that they really
// are consumers rather than two more empty servers.
//
// It tests the harness, not Alder. That is deliberate and it is where this
// belongs: a replication view proved against a harness whose replication
// quietly stopped working would be a view proving nothing, and the failure
// would look like a product bug for as long as it took someone to check.

// countUnder is how many entries this session can see below a base.
func countUnder(t *testing.T, sess directory.Session, base string) int {
	t.Helper()
	all, err := filter.Parse("(objectClass=*)")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	var cookie []byte
	for {
		res, err := sess.Search(ctx(t), directory.SearchRequest{
			BaseDN: mustDN(t, base), Scope: directory.ScopeSubtree, Filter: all,
			Attributes: []string{"1.1"}, Limit: directory.MaxPageSize, PageSize: 200, Cookie: cookie,
		})
		if err != nil {
			t.Fatalf("counting below %s: %v", base, err)
		}
		total += len(res.Entries)
		if len(res.Cookie) == 0 {
			return total
		}
		cookie = res.Cookie
	}
}

func TestTheReplicaHoldsWhatTheSupplierHolds(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		if s.replicaPort == 0 {
			t.Skip("this server has no consumer in the harness")
		}
		replica := connect(t, replicaOf(s), false)

		supplied := countUnder(t, sess, suffix)
		received := countUnder(t, replica, suffix)
		if supplied == 0 {
			t.Fatalf("%s holds nothing below %s", s.name, suffix)
		}
		if received != supplied {
			t.Fatalf("%s holds %d entries below %s and its consumer holds %d",
				s.name, supplied, suffix, received)
		}
		t.Logf("%s: %d entries, and its consumer has all of them", s.name, supplied)
	})
}

func TestAChangeOnTheSupplierReachesTheReplica(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		if s.replicaPort == 0 {
			t.Skip("this server has no consumer in the harness")
		}
		replica := connect(t, replicaOf(s), false)

		// An entry the seed owns, and an attribute nothing else in the suite
		// reads. The value carries the run's clock so that a leftover from a
		// crashed run cannot be mistaken for this run's change arriving.
		target := mustDN(t, "uid=user0100,ou=people,"+suffix)
		written := "replicated at " + time.Now().UTC().Format(time.RFC3339Nano)

		t.Cleanup(func() {
			_ = sess.Apply(ctx(t), directory.ChangeRecord{
				DN: target, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModDelete, Name: "description"}},
			})
		})
		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN: target, Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: "description",
				Values: [][]byte{[]byte(written)}}},
		}); err != nil {
			t.Fatalf("%s: writing to the supplier: %v", s.name, err)
		}

		// Replication is asynchronous on both, so this waits rather than
		// asserting immediately. Thirty seconds is far longer than either
		// server takes and short enough that a broken agreement fails the
		// suite rather than hanging it.
		deadline := time.Now().Add(30 * time.Second)
		for {
			entry, err := replica.Read(ctx(t), target, []string{"description"})
			if err == nil {
				for _, got := range entry.GetStrings("description") {
					if got == written {
						t.Logf("%s: the change reached the consumer", s.name)
						return
					}
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the change never reached the consumer", s.name)
			}
			time.Sleep(250 * time.Millisecond)
		}
	})
}

func TestAReplicaWillNotBeWrittenTo(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		if s.replicaPort == 0 {
			t.Skip("this server has no consumer in the harness")
		}
		_ = sess
		replica := connect(t, replicaOf(s), false)

		// A consumer is not a place changes are made, and both servers say so
		// -- by referring the writer to the supplier, or by refusing outright.
		// Which of the two is the server's business; that the write does not
		// silently succeed and then vanish at the next refresh is the point.
		target := mustDN(t, "uid=user0101,ou=people,"+suffix)
		err := replica.Apply(ctx(t), directory.ChangeRecord{
			DN: target, Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: "description",
				Values: [][]byte{[]byte("written to a consumer")}}},
		})
		if err == nil {
			t.Fatalf("%s's consumer accepted a write", s.name)
		}
		var refused *ldapdriver.Error
		if errors.As(err, &refused) {
			t.Logf("%s's consumer refused with code %d: %s", s.name, refused.Code, refused.Message)
		} else {
			t.Logf("%s's consumer refused: %v", s.name, err)
		}

		// And it did not keep it. A consumer that answered with a referral and
		// applied the change anyway would be the worst of both.
		entry, readErr := replica.Read(ctx(t), target, []string{"description"})
		if readErr != nil {
			t.Fatalf("%s: reading the consumer back: %v", s.name, readErr)
		}
		for _, got := range entry.GetStrings("description") {
			if strings.Contains(got, "written to a consumer") {
				t.Fatalf("%s's consumer refused the write and kept it anyway", s.name)
			}
		}
	})
}
