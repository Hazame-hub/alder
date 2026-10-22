//go:build conformance

package conformance

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// One modification over selected entries (docs/proposals/bulk-attribute-edit.md),
// on both servers.
//
// The composition is the browser's (web/src/lib/bulk-edit.ts): one `modify`
// per selected DN, each carrying the same single modification. So this posts
// exactly what the composer writes, written out by hand as planRequest is, and
// holds the plan and the apply to what the proposal says the changeset does
// with it: an entry that already holds the value in another case is
// unchanged, an entry deleted after it was selected is a conflict
// (entry_missing) and never an add, an entry whose classes do not permit the
// attribute is invalid, and only the rest is written.

const bulkOU = "ou=alder-bulkedit," + suffix

func bulkDN(rdn string) string { return rdn + "," + bulkOU }

// bulkChange is one record as the composer writes it.
func bulkChange(target, op, attr string, values ...string) string {
	mod := map[string]any{"op": op, "name": attr}
	if len(values) > 0 {
		vs := make([]map[string]string, len(values))
		for i, v := range values {
			vs[i] = map[string]string{"text": v}
		}
		mod["values"] = vs
	}
	encoded, _ := json.Marshal(map[string]any{"dn": target, "type": "modify", "mods": []any{mod}})
	return string(encoded)
}

func clearBulkOU(t *testing.T, sess directory.Session) {
	t.Helper()
	for _, rdn := range []string{"uid=bulk-a", "uid=bulk-b", "uid=bulk-c", "uid=bulk-d", "cn=bulk-e"} {
		deleteQuietly(t, sess, bulkDN(rdn))
	}
	deleteQuietly(t, sess, bulkOU)
}

// bulkPlan plans a composed set and returns the plan.
func bulkPlan(t *testing.T, client *http.Client, base string, changes []string) api.Plan {
	t.Helper()
	res := post(t, client, base+"/plan", planRequest(false, changes...))
	if res.status != http.StatusOK {
		t.Fatalf("planning: status %d\n%s", res.status, res.body)
	}
	return decodeInto[api.Plan](t, res)
}

// bulkApply sends what the changeset sends after a plan: the plan's records
// that carry a baseline, in order, and nothing for the rest.
func bulkApply(t *testing.T, client *http.Client, base string, p api.Plan) int {
	t.Helper()
	var send []api.ChangeRequest
	for _, item := range p.Items {
		if item.Record == nil || item.Baseline == nil {
			continue
		}
		record := *item.Record
		record.Baseline = item.Baseline
		send = append(send, record)
	}
	encoded, err := json.Marshal(send)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	applied := post(t, client, base+"/changeset/apply", `{"changes":`+string(encoded)+`}`)
	if applied.status != http.StatusOK {
		t.Fatalf("applying: status %d\n%s", applied.status, applied.body)
	}
	r := decodeInto[api.ChangesetResult](t, applied)
	if r.AppliedCount != len(send) {
		t.Fatalf("applied %d of %d: %s", r.AppliedCount, len(send), mustEncode(t, r.Outcomes))
	}
	return r.AppliedCount
}

func TestABulkEditOverFiveEntriesPlansAndAppliesOnBothServers(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		clearBulkOU(t, sess)
		t.Cleanup(func() { clearBulkOU(t, sess) })

		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, bulkOU), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("organizationalUnit")}},
			{Name: "ou", Values: [][]byte{[]byte("alder-bulkedit")}},
		}})
		mustApply(t, sess, person(t, bulkDN("uid=bulk-a"), "bulk-a", [2]string{"displayName", "Old A"}, [2]string{"description", "temp"}))
		mustApply(t, sess, person(t, bulkDN("uid=bulk-b"), "bulk-b"))
		mustApply(t, sess, person(t, bulkDN("uid=bulk-c"), "bulk-c", [2]string{"departmentNumber", "Platform"}))
		mustApply(t, sess, person(t, bulkDN("uid=bulk-d"), "bulk-d"))
		// A person, not an inetOrgPerson: its classes do not permit departmentNumber.
		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, bulkDN("cn=bulk-e")), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("person")}},
			{Name: "cn", Values: [][]byte{[]byte("bulk-e")}},
			{Name: "sn", Values: [][]byte{[]byte("Bulk")}},
		}})

		selected := []string{bulkDN("uid=bulk-a"), bulkDN("uid=bulk-b"), bulkDN("uid=bulk-c"), bulkDN("uid=bulk-d"), bulkDN("cn=bulk-e")}
		// Deleted after the table was read and the rows selected.
		deleteQuietly(t, sess, bulkDN("uid=bulk-d"))

		t.Run("add a value to five entries", func(t *testing.T) {
			changes := make([]string, len(selected))
			for i, d := range selected {
				changes[i] = bulkChange(d, "add", "departmentNumber", "platform")
			}
			p := bulkPlan(t, client, base, changes)
			if len(p.Items) != len(selected) {
				t.Fatalf("items = %d, want %d", len(p.Items), len(selected))
			}
			want := []struct {
				action  api.PlanAction
				problem api.PlanProblemCode
			}{
				{api.PlanActionModify, ""},
				{api.PlanActionModify, ""},
				{api.PlanActionUnchanged, ""}, // holds "Platform": the same value under caseIgnoreMatch
				{api.PlanActionConflict, api.PlanProblemEntryMissing},
				{api.PlanActionInvalid, api.PlanProblemAttributeNotPermitted},
			}
			for i, item := range p.Items {
				if item.Action != want[i].action {
					t.Errorf("%s planned as %q, want %q (%s)", selected[i], item.Action, want[i].action, mustEncode(t, item))
					continue
				}
				if want[i].problem != "" && (item.Problem == nil || item.Problem.Code != want[i].problem) {
					t.Errorf("%s: problem %s, want %q", selected[i], mustEncode(t, item.Problem), want[i].problem)
				}
			}

			if n := bulkApply(t, client, base, p); n != 2 {
				t.Fatalf("applied %d changes, want 2", n)
			}
			for _, d := range selected[:2] {
				if got := readAll(t, sess, d, "departmentNumber"); !slices.Equal(got, []string{"platform"}) {
					t.Errorf("%s departmentNumber = %q, want [platform]", d, got)
				}
			}
			// The unchanged entry kept its own spelling: nothing was sent for it.
			if got := readAll(t, sess, selected[2], "departmentNumber"); !slices.Equal(got, []string{"Platform"}) {
				t.Errorf("%s departmentNumber = %q, want [Platform] untouched", selected[2], got)
			}
			// The deleted entry was not recreated by a modification.
			if _, err := sess.Read(ctx(t), mustDN(t, selected[3]), []string{"uid"}); err == nil {
				t.Errorf("%s exists again after the bulk edit", selected[3])
			}
			if got := readAll(t, sess, selected[4], "departmentNumber"); len(got) != 0 {
				t.Errorf("%s holds departmentNumber %q, which its classes do not permit", selected[4], got)
			}
		})

		t.Run("replace a single-valued attribute", func(t *testing.T) {
			two := selected[:2]
			changes := []string{
				bulkChange(two[0], "replace", "displayName", "Bulk Name"),
				bulkChange(two[1], "replace", "displayName", "Bulk Name"),
			}
			p := bulkPlan(t, client, base, changes)
			for i, item := range p.Items {
				if item.Action != api.PlanActionModify {
					t.Errorf("%s planned as %q, want modify", two[i], item.Action)
				}
			}
			bulkApply(t, client, base, p)
			for _, d := range two {
				if got := readAll(t, sess, d, "displayName"); !slices.Equal(got, []string{"Bulk Name"}) {
					t.Errorf("%s displayName = %q, want [Bulk Name]", d, got)
				}
			}
		})

		t.Run("remove a value one entry does not hold", func(t *testing.T) {
			two := selected[:2]
			changes := []string{
				bulkChange(two[0], "delete", "description", "temp"),
				bulkChange(two[1], "delete", "description", "temp"),
			}
			p := bulkPlan(t, client, base, changes)
			if p.Items[0].Action != api.PlanActionModify || p.Items[1].Action != api.PlanActionUnchanged {
				t.Fatalf("actions = %q, %q; want modify, unchanged", p.Items[0].Action, p.Items[1].Action)
			}
			if n := bulkApply(t, client, base, p); n != 1 {
				t.Fatalf("applied %d, want 1", n)
			}
			if got := readAll(t, sess, two[0], "description"); len(got) != 0 {
				t.Errorf("%s description = %q after removing it", two[0], got)
			}
		})

		t.Run("remove the attribute from an entry that holds it and one that does not", func(t *testing.T) {
			changes := []string{
				bulkChange(selected[0], "delete", "departmentNumber"),
				bulkChange(selected[1], "delete", "description"),
			}
			p := bulkPlan(t, client, base, changes)
			if p.Items[0].Action != api.PlanActionModify || p.Items[1].Action != api.PlanActionUnchanged {
				t.Fatalf("actions = %q, %q; want modify, unchanged", p.Items[0].Action, p.Items[1].Action)
			}
			bulkApply(t, client, base, p)
			if got := readAll(t, sess, selected[0], "departmentNumber"); len(got) != 0 {
				t.Errorf("%s departmentNumber = %q after removing the attribute", selected[0], got)
			}
		})
	})
}
