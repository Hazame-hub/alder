//go:build conformance

package conformance

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// 1.26: replication at entry scale, and what a server marks when two changes
// collide.
//
// "Has this change arrived there yet" is asked about one entry, and answered
// by comparing the same number on two servers -- so the interesting assertion
// is not that each server says something, but that a change written on a
// supplier moves the number on its consumer to match.

func entryReplication(t *testing.T, s server, target string) api.EntryReplication {
	t.Helper()
	client, base := alderSession(t, s, false)
	res := get(t, client, base+"/replication/entry?dn="+url.QueryEscape(target))
	if res.status != http.StatusOK {
		t.Fatalf("%s: GET /replication/entry: %d\n%s", s.name, res.status, res.body)
	}
	var state api.EntryReplication
	if err := json.Unmarshal([]byte(res.body), &state); err != nil {
		t.Fatalf("%s: decoding: %v\n%s", s.name, err, res.body)
	}
	return state
}

func TestAnEntrysChangeSequenceIsTheSameOnBothSidesOnceItHasArrived(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		if s.replicaPort == 0 {
			t.Skip("this server has no consumer in the harness")
		}
		target := "uid=user0102,ou=people," + suffix
		written := "compared at " + time.Now().UTC().Format(time.RFC3339Nano)

		t.Cleanup(func() {
			_ = sess.Apply(ctx(t), directory.ChangeRecord{
				DN: mustDN(t, target), Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModDelete, Name: "description"}},
			})
		})
		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN: mustDN(t, target), Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: "description",
				Values: [][]byte{[]byte(written)}}},
		}); err != nil {
			t.Fatalf("%s: writing: %v", s.name, err)
		}

		supplier := entryReplication(t, s, target)
		if supplier.Changed == nil {
			t.Fatalf("%s: the entry was just changed and the server records nothing about it: %s",
				s.name, mustEncode(t, supplier))
		}
		if supplier.Identity == nil || *supplier.Identity == "" {
			t.Errorf("%s: the entry has no identity that would survive a rename: %s",
				s.name, mustEncode(t, supplier))
		}

		// Wait for the change, then compare the two numbers. This is the
		// method the whole feature exists to support, so the test is the
		// method: write here, look there, and the numbers agree.
		deadline := time.Now().Add(30 * time.Second)
		for {
			consumer := entryReplication(t, replicaOf(s), target)
			if consumer.Changed != nil && consumer.Changed.Raw == supplier.Changed.Raw {
				t.Logf("%s: %s on both sides", s.name, consumer.Changed.Raw)
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the supplier says %q and its consumer says %q",
					s.name, supplier.Changed.Raw, rawOf(consumer.Changed))
			}
			time.Sleep(250 * time.Millisecond)
		}
	})
}

func rawOf(c *api.ReplicationCursor) string {
	if c == nil {
		return "nothing"
	}
	return c.Raw
}

// TestAServerThatMarksAConflictHasItFound proves the reporting path against a
// real server answering a real search.
//
// The marker is written by hand. A genuine conflict needs two suppliers
// changing the same entry in the same instant, which is a race no test can
// arrange and no harness should depend on; what can be pinned is that an
// entry the server has marked is found, read and explained. The entry is put
// back afterwards either way.
func TestAServerThatMarksAConflictHasItFound(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alderSession(t, s, false)
		res := get(t, client, base+"/replication/conflicts?dn="+url.QueryEscape(suffix))
		if res.status != http.StatusOK {
			t.Fatalf("%s: GET /replication/conflicts: %d\n%s", s.name, res.status, res.body)
		}
		var before api.ReplicationConflicts
		if err := json.Unmarshal([]byte(res.body), &before); err != nil {
			t.Fatalf("%s: decoding: %v\n%s", s.name, err, res.body)
		}
		if before.Why == "" {
			t.Errorf("%s: a conflict report with no explanation of what this server does", s.name)
		}
		if !before.Recorded {
			// Which is the honest answer for OpenLDAP: it discards the loser
			// and leaves nothing behind. The report has to say that, because
			// an empty list otherwise reads as good news.
			if len(before.Entries) != 0 {
				t.Errorf("%s: says it records no conflict and listed %d", s.name, len(before.Entries))
			}
			if !strings.Contains(before.Why, "discarded") && !strings.Contains(before.Why, "no model") {
				t.Errorf("%s: %q does not say what happens to the losing change", s.name, before.Why)
			}
			t.Logf("%s: records no conflict, and says so", s.name)
			return
		}

		target := mustDN(t, "uid=user0103,ou=people,"+suffix)
		const reason = "namingConflict uid=user0103,ou=people," + suffix
		t.Cleanup(func() {
			_ = sess.Apply(ctx(t), directory.ChangeRecord{
				DN: target, Type: directory.ChangeModify,
				Mods: []directory.Mod{
					{Op: directory.ModDelete, Name: "nsds5ReplConflict"},
					{Op: directory.ModDelete, Name: "objectClass", Values: [][]byte{[]byte("extensibleObject")}},
				},
			})
		})
		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN: target, Type: directory.ChangeModify,
			Mods: []directory.Mod{
				{Op: directory.ModAdd, Name: "objectClass", Values: [][]byte{[]byte("extensibleObject")}},
				{Op: directory.ModAdd, Name: "nsds5ReplConflict", Values: [][]byte{[]byte(reason)}},
			},
		}); err != nil {
			t.Fatalf("%s: marking an entry: %v", s.name, err)
		}

		res = get(t, client, base+"/replication/conflicts?dn="+url.QueryEscape(suffix))
		if res.status != http.StatusOK {
			t.Fatalf("%s: GET /replication/conflicts: %d\n%s", s.name, res.status, res.body)
		}
		var after api.ReplicationConflicts
		if err := json.Unmarshal([]byte(res.body), &after); err != nil {
			t.Fatalf("%s: decoding: %v\n%s", s.name, err, res.body)
		}
		found := false
		for _, conflict := range after.Entries {
			if strings.EqualFold(conflict.Dn, target.String()) {
				found = true
				if conflict.Kind != api.ConflictNaming {
					t.Errorf("%s: read as %q, want a naming collision", s.name, conflict.Kind)
				}
				if conflict.Reason == nil || *conflict.Reason != reason {
					t.Errorf("%s: the server's own words were not kept: %s", s.name, mustEncode(t, conflict))
				}
			}
		}
		if !found {
			t.Fatalf("%s: a marked entry was not found: %s", s.name, mustEncode(t, after))
		}

		// And the entry itself says so, which is where somebody looking at
		// that one account would see it.
		state := entryReplication(t, s, target.String())
		if state.Conflict == nil || *state.Conflict != reason {
			t.Errorf("%s: the entry does not report its own conflict: %s", s.name, mustEncode(t, state))
		}
		t.Logf("%s: the marked entry was found and explained", s.name)
	})
}
