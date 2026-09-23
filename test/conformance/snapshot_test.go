//go:build conformance

package conformance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// 1.7: snapshots and comparisons against both servers, over HTTP.

const snapshotOU = "ou=alder-snapshot," + suffix

func snapshotDN(rdn string) string { return rdn + "," + snapshotOU }

func person(t *testing.T, dnText, uid string, extra ...[2]string) directory.ChangeRecord {
	t.Helper()
	rec := directory.ChangeRecord{DN: mustDN(t, dnText), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
		{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("person"), []byte("organizationalPerson"), []byte("inetOrgPerson")}},
		{Name: "uid", Values: [][]byte{[]byte(uid)}},
		{Name: "cn", Values: [][]byte{[]byte("Snapshot " + uid)}},
		{Name: "sn", Values: [][]byte{[]byte("Snapshot")}},
	}}
	for _, kv := range extra {
		rec.Attrs = append(rec.Attrs, directory.Attribute{Name: kv[0], Values: [][]byte{[]byte(kv[1])}})
	}
	return rec
}

func clearSnapshotOU(t *testing.T, sess directory.Session) {
	t.Helper()
	for _, rdn := range []string{"cn=snap-group", "uid=snap-a", "uid=snap-b", "uid=snap-c"} {
		_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, snapshotDN(rdn)), Type: directory.ChangeDelete})
	}
	_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, snapshotOU), Type: directory.ChangeDelete})
}

func mustApply(t *testing.T, sess directory.Session, rec directory.ChangeRecord) {
	t.Helper()
	if err := sess.Apply(ctx(t), rec); err != nil {
		t.Fatalf("%s %s: %v", rec.Type, rec.DN, err)
	}
}

func diffOverHTTP(t *testing.T, client *http.Client, base, body string) api.Diff {
	t.Helper()
	res := post(t, client, base+"/diff", body)
	if res.status != http.StatusOK {
		t.Fatalf("diff: status %d\n%s", res.status, res.body)
	}
	return decodeInto[api.Diff](t, res)
}

// planAndApplyChanges plans a set, then applies it with the plan's tokens.
func planAndApplyChanges(t *testing.T, client *http.Client, base string, changes []api.ChangeRequest) {
	t.Helper()
	encoded, _ := json.Marshal(changes)
	res := post(t, client, base+"/plan", `{"reconcile":false,"changes":`+string(encoded)+`}`)
	if res.status != http.StatusOK {
		t.Fatalf("plan: status %d\n%s", res.status, res.body)
	}
	p := decodeInto[api.Plan](t, res)
	for i, item := range p.Items {
		if item.Baseline == nil {
			t.Fatalf("change %d planned as %s (%s), not as something to apply", i, item.Action, mustEncode(t, item))
		}
		changes[i].Baseline = item.Baseline
	}
	encoded, _ = json.Marshal(changes)
	applied := post(t, client, base+"/changeset/apply", `{"changes":`+string(encoded)+`}`)
	if applied.status != http.StatusOK {
		t.Fatalf("apply: status %d\n%s", applied.status, applied.body)
	}
	if r := decodeInto[api.ChangesetResult](t, applied); r.AppliedCount != len(changes) {
		t.Fatalf("applied %d of %d: %s", r.AppliedCount, len(changes), mustEncode(t, r.Outcomes))
	}
}

func differences(d api.Diff) int {
	return d.Counts.Added + d.Counts.Removed + d.Counts.Modified + d.Counts.Renamed + d.Counts.Unknown
}

func TestSnapshotDiffPlanApplyOverHTTP(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		clearSnapshotOU(t, sess)
		t.Cleanup(func() { clearSnapshotOU(t, sess) })

		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, snapshotOU), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("organizationalUnit")}},
			{Name: "ou", Values: [][]byte{[]byte("alder-snapshot")}},
		}})
		mustApply(t, sess, person(t, snapshotDN("uid=snap-a"), "snap-a", [2]string{"title", "Engineer"}))
		mustApply(t, sess, person(t, snapshotDN("uid=snap-b"), "snap-b"))
		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, snapshotDN("cn=snap-group")), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("groupOfNames")}},
			{Name: "cn", Values: [][]byte{[]byte("snap-group")}},
			{Name: "member", Values: [][]byte{[]byte(snapshotDN("uid=snap-a")), []byte(snapshotDN("uid=snap-b"))}},
		}})

		// 1. capture S1.
		captured := post(t, client, base+"/snapshots/capture", fmt.Sprintf(`{"base":%q,"scope":"sub"}`, snapshotOU))
		if captured.status != http.StatusOK {
			t.Fatalf("capture: status %d\n%s", captured.status, captured.body)
		}
		s1 := captured.body
		if !strings.Contains(s1, `"entryCount": 4`) {
			t.Fatalf("S1 does not hold the four entries:\n%s", s1)
		}

		// 2. the directory drifts.
		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, snapshotDN("uid=snap-a")), Type: directory.ChangeModify, Mods: []directory.Mod{
			{Op: directory.ModReplace, Name: "title", Values: [][]byte{[]byte("Senior Engineer")}},
			{Op: directory.ModAdd, Name: "description", Values: [][]byte{[]byte("added after the snapshot")}},
		}})
		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, snapshotDN("cn=snap-group")), Type: directory.ChangeModify, Mods: []directory.Mod{
			{Op: directory.ModDelete, Name: "member", Values: [][]byte{[]byte(snapshotDN("uid=snap-b"))}},
		}})
		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, snapshotDN("uid=snap-b")), Type: directory.ChangeDelete})
		mustApply(t, sess, person(t, snapshotDN("uid=snap-c"), "snap-c"))

		// 3-4. diff live -> S1 sees each difference.
		body := `{"source":{"live":{}},"target":{"snapshot":` + s1 + `}}`
		d := diffOverHTTP(t, client, base, body)
		if !d.Complete || d.Counts.Modified != 2 || d.Counts.Added != 1 || d.Counts.Removed != 1 {
			t.Fatalf("complete = %v, counts = %+v, reasons = %+v\nitems: %+v", d.Complete, d.Counts, d.Reasons, d.Items)
		}

		// 5-7. select the non-destructive changes, plan, apply.
		var restore []api.ChangeRequest
		var deletion []api.ChangeRequest
		for _, it := range d.Items {
			if it.Candidate == nil || it.Candidate.Blocked != nil {
				t.Fatalf("no usable candidate for %+v", it)
			}
			if it.Candidate.Destructive {
				deletion = append(deletion, it.Candidate.Changes...)
				continue
			}
			restore = append(restore, it.Candidate.Changes...)
		}
		// The add of snap-b before the group names it again.
		sortAddsFirst(restore)
		planAndApplyChanges(t, client, base, restore)

		// 8a. only the unselected deletion remains.
		d = diffOverHTTP(t, client, base, body)
		if differences(d) != 1 || d.Counts.Removed != 1 {
			t.Fatalf("after restoring, counts = %+v\nitems: %+v", d.Counts, d.Items)
		}
		if got := readOne(t, sess, snapshotDN("uid=snap-a"), "title"); got != "Engineer" {
			t.Errorf("snap-a title = %q after the planned restore", got)
		}

		// The deletion is separate and explicit.
		if len(deletion) != 1 || deletion[0].Type != api.ChangeRequestTypeDelete {
			t.Fatalf("deletion candidates = %+v", deletion)
		}
		planAndApplyChanges(t, client, base, deletion)

		// 8b. capture and compare again: nothing differs.
		d = diffOverHTTP(t, client, base, body)
		if !d.Complete || differences(d) != 0 {
			t.Errorf("after applying, complete = %v, counts = %+v\nitems: %+v", d.Complete, d.Counts, d.Items)
		}
	})
}

func sortAddsFirst(changes []api.ChangeRequest) {
	adds := changes[:0:0]
	var rest []api.ChangeRequest
	for _, c := range changes {
		if c.Type == api.ChangeRequestTypeAdd {
			adds = append(adds, c)
		} else {
			rest = append(rest, c)
		}
	}
	copy(changes, append(adds, rest...))
}

// The same seeded people, captured from both servers, compared with each other.
func TestEquivalentDataOnBothServersComparesEqual(t *testing.T) {
	snapshots := map[string]string{}
	var client *http.Client
	var base string
	for _, s := range servers {
		c, b := alder(t, s)
		res := post(t, c, b+"/snapshots/capture",
			`{"base":"ou=people,dc=alder,dc=test","scope":"one","filter":"(uid=user00*)"}`)
		if res.status != http.StatusOK {
			t.Fatalf("%s: capture status %d\n%s", s.name, res.status, res.body)
		}
		snapshots[s.name] = res.body
		client, base = c, b
	}
	d := diffOverHTTP(t, client, base, `{"source":{"snapshot":`+snapshots["openldap"]+`},"target":{"snapshot":`+snapshots["389ds"]+`}}`)
	if !d.CrossVendor {
		t.Error("a comparison across the two servers does not say so")
	}
	if d.Counts.Compared == 0 {
		t.Fatal("nothing compared")
	}
	if differences(d) != 0 {
		t.Errorf("equivalent seed data differs across servers: counts = %+v\nitems: %+v\nrule differences: %v",
			d.Counts, d.Items, d.RuleDifferences)
	}
}
