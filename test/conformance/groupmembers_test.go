//go:build conformance

package conformance

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
)

// Many members at once (docs/proposals/group-membership-tools.md, Part 1),
// on both servers.
//
// The record is the browser's (web/src/lib/group-members.ts): one `modify`
// of the group, `add: member` with the values it does not hold or `delete:
// member` with the ones it does. This posts what the composer writes and
// holds the servers to the two facts the design rests on: an add naming a
// value already held is refused whole, which is why the composer leaves
// those out; and a member changed by somebody else between the plan and the
// apply refuses the record as plan_stale with nothing written, while a
// single-value add still applies.

const membersOU = "ou=alder-groupmembers," + suffix

func membersDN(rdn string) string { return rdn + "," + membersOU }

var membersPeople = []string{"uid=gm-a", "uid=gm-b", "uid=gm-c", "uid=gm-d", "uid=gm-e", "uid=gm-f"}

func clearMembersOU(t *testing.T, sess directory.Session) {
	t.Helper()
	deleteQuietly(t, sess, membersDN("cn=gm-group"))
	for _, rdn := range membersPeople {
		deleteQuietly(t, sess, membersDN(rdn))
	}
	deleteQuietly(t, sess, membersOU)
}

// membersChange is one record as the composer writes it.
func membersChange(group, op string, values ...string) string {
	vs := make([]map[string]string, len(values))
	for i, v := range values {
		vs[i] = map[string]string{"text": v}
	}
	encoded, _ := json.Marshal(map[string]any{
		"dn": group, "type": "modify",
		"mods": []any{map[string]any{"op": op, "name": "member", "values": vs}},
	})
	return string(encoded)
}

func membersOf(t *testing.T, sess directory.Session, group string) []string {
	t.Helper()
	out := readAll(t, sess, group, "member")
	for i := range out {
		out[i] = strings.ToLower(out[i])
	}
	slices.Sort(out)
	return out
}

func lowerSorted(dns ...string) []string {
	out := make([]string, len(dns))
	for i, d := range dns {
		out[i] = strings.ToLower(d)
	}
	slices.Sort(out)
	return out
}

func TestManyMembersAreOneRecordPerGroupOnBothServers(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		clearMembersOU(t, sess)
		t.Cleanup(func() { clearMembersOU(t, sess) })

		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, membersOU), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("organizationalUnit")}},
			{Name: "ou", Values: [][]byte{[]byte("alder-groupmembers")}},
		}})
		for _, rdn := range membersPeople {
			mustApply(t, sess, person(t, membersDN(rdn), strings.TrimPrefix(rdn, "uid=")))
		}
		a, b, c, d, e, f := membersDN("uid=gm-a"), membersDN("uid=gm-b"), membersDN("uid=gm-c"),
			membersDN("uid=gm-d"), membersDN("uid=gm-e"), membersDN("uid=gm-f")
		group := membersDN("cn=gm-group")
		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, group), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("groupOfNames")}},
			{Name: "cn", Values: [][]byte{[]byte("gm-group")}},
			{Name: "member", Values: [][]byte{[]byte(a)}},
		}})

		t.Run("an add naming a held value is refused whole, so it is left out", func(t *testing.T) {
			// The record the composer does not write: a's DN in capitals beside b.
			p := bulkPlan(t, client, base, []string{membersChange(group, "add", strings.ToUpper(a), b)})
			if p.Items[0].Action != api.PlanActionModify {
				t.Fatalf("planned as %q, want modify: the planner reports and does not narrow", p.Items[0].Action)
			}
			err := sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, group), Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModAdd, Name: "member", Values: [][]byte{[]byte(strings.ToUpper(a)), []byte(b)}}}})
			var ldapErr *ldapdriver.Error
			if !errors.As(err, &ldapErr) || ldapErr.Code != ldap.LDAPResultAttributeOrValueExists {
				t.Fatalf("%s answered %v, want attributeOrValueExists for the whole record", s.name, err)
			}
			if got := membersOf(t, sess, group); !slices.Equal(got, lowerSorted(a)) {
				t.Fatalf("members = %q after a refused add; b must not have been written", got)
			}
		})

		t.Run("the composed add carries only what the group lacks, and applies", func(t *testing.T) {
			p := bulkPlan(t, client, base, []string{membersChange(group, "add", b, c, d)})
			item := p.Items[0]
			if item.Action != api.PlanActionModify {
				t.Fatalf("planned as %q, want modify (%s)", item.Action, mustEncode(t, item))
			}
			if item.Membership == nil || len(*item.Membership) != 1 || len((*item.Membership)[0].Gained) != 3 {
				t.Errorf("membership = %s, want three gained in member", mustEncode(t, item.Membership))
			}
			bulkApply(t, client, base, p)
			if got := membersOf(t, sess, group); !slices.Equal(got, lowerSorted(a, b, c, d)) {
				t.Errorf("members = %q, want a, b, c, d", got)
			}
		})

		t.Run("a member changed by somebody else makes the record stale, and one value still applies", func(t *testing.T) {
			p := bulkPlan(t, client, base, []string{membersChange(group, "add", e, f)})
			item := p.Items[0]
			if item.Baseline == nil {
				t.Fatalf("no baseline: %s", mustEncode(t, item))
			}
			// The other administrator removes d with a single-value delete.
			mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, group), Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModDelete, Name: "member", Values: [][]byte{[]byte(d)}}}})

			refused := post(t, client, base+"/changeset/apply", applyBody(t, item))
			if refused.status != http.StatusConflict {
				t.Fatalf("status %d, want 409\n%s", refused.status, refused.body)
			}
			body := decodeInto[api.Error](t, refused)
			if body.Cause == nil || *body.Cause != api.ErrorCausePlanStale {
				t.Fatalf("refusal = %+v, want cause plan_stale", body)
			}
			if got := membersOf(t, sess, group); !slices.Equal(got, lowerSorted(a, b, c)) {
				t.Fatalf("members = %q; the stale record wrote something", got)
			}

			// The fallback: one value at a time, as the single-value action does.
			planAndApply(t, client, base, membersChange(group, "add", e), api.PlanActionModify)
			if got := membersOf(t, sess, group); !slices.Equal(got, lowerSorted(a, b, c, e)) {
				t.Errorf("members = %q after the single-value add", got)
			}
		})

		t.Run("a removal of several members is one delete of the values held", func(t *testing.T) {
			p := bulkPlan(t, client, base, []string{membersChange(group, "delete", b, c)})
			if p.Items[0].Action != api.PlanActionModify {
				t.Fatalf("planned as %q, want modify", p.Items[0].Action)
			}
			bulkApply(t, client, base, p)
			if got := membersOf(t, sess, group); !slices.Equal(got, lowerSorted(a, e)) {
				t.Errorf("members = %q, want a and e", got)
			}
		})

		t.Run("removing every member of a groupOfNames is invalid", func(t *testing.T) {
			p := bulkPlan(t, client, base, []string{membersChange(group, "delete", a, e)})
			item := p.Items[0]
			if item.Action != api.PlanActionInvalid || item.Problem == nil ||
				item.Problem.Code != api.PlanProblemMissingRequiredAttribute {
				t.Errorf("planned as %s, want invalid with missing_required_attribute", mustEncode(t, item))
			}
		})
	})
}
