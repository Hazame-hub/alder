//go:build conformance

package conformance

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

// Members nobody can find (docs/proposals/group-membership-tools.md, Part 2),
// on both servers.
//
// Everything is created below the case's own ou and removed after it, so the
// seed stays identical: groups naming one entry that exists and DNs that never
// did (both servers accept an unresolved DN-syntax value), a posixGroup, and a
// referral object -- an entry whose ref attribute says it is held elsewhere --
// with a group naming a DN below it.
//
// The referral object is written with a raw LDAP connection carrying the
// ManageDsaIT control, because without it both servers answer an operation on
// it with the referral rather than performing it. That is a fixture, not
// Alder: Alder never sends the control, and never follows a referral.
//
// The server limits the audit refuses on -- sizeLimitExceeded,
// timeLimitExceeded, adminLimitExceeded, and a truncated result -- cannot be
// made to happen on demand in this harness without configuring a limit for
// the bind, which would change every other case. They are tested against a
// driver double in internal/api/memberaudit_test.go, one case each.

const auditOU = "ou=alder-memberaudit," + suffix

func auditDN(rdn string) string { return rdn + "," + auditOU }

var auditGroups = []string{"cn=ma-mixed", "cn=ma-orphan", "cn=ma-posix", "cn=ma-remote"}

// manageDsaIT is a raw connection that operates on a referral object itself.
func manageDsaIT(t *testing.T, s server) *ldap.Conn {
	t.Helper()
	conn, err := ldap.DialURL(fmt.Sprintf("ldaps://%s:%d", s.host, s.port),
		ldap.DialWithTLSConfig(&tls.Config{RootCAs: caPool(t), ServerName: "localhost", MinVersion: tls.VersionTLS12}))
	if err != nil {
		t.Fatalf("dialling %s for the referral fixture: %v", s.name, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.Bind(s.bindDN, s.bindPW); err != nil {
		t.Fatalf("binding to %s for the referral fixture: %v", s.name, err)
	}
	return conn
}

func clearAuditOU(t *testing.T, sess directory.Session, raw *ldap.Conn) {
	t.Helper()
	_ = raw.Del(ldap.NewDelRequest(auditDN("ou=remote"), []ldap.Control{ldap.NewControlManageDsaIT(true)}))
	for _, rdn := range append(auditGroups, "uid=ma-a") {
		deleteQuietly(t, sess, auditDN(rdn))
	}
	deleteQuietly(t, sess, auditOU)
}

func runAudit(t *testing.T, client *http.Client, base string) (api.MemberAudit, httpResult) {
	t.Helper()
	res := post(t, client, base+"/members/audit", `{"base":"`+auditOU+`"}`)
	if res.status != http.StatusOK {
		return api.MemberAudit{}, res
	}
	return decodeInto[api.MemberAudit](t, res), res
}

func auditGroup(t *testing.T, audit api.MemberAudit, rdn string) api.MemberAuditGroup {
	t.Helper()
	for _, g := range audit.Groups {
		if strings.EqualFold(g.Dn, auditDN(rdn)) {
			return g
		}
	}
	t.Fatalf("%s is not in the audit: %s", rdn, mustEncode(t, audit.Groups))
	return api.MemberAuditGroup{}
}

func TestTheMembershipAuditOnBothServers(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		raw := manageDsaIT(t, s)
		clearAuditOU(t, sess, raw)
		t.Cleanup(func() { clearAuditOU(t, sess, raw) })

		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("reading the schema: %v", err)
		}
		requires := func(classes ...string) bool {
			return slices.ContainsFunc(sch.Requirements(classes).Must,
				func(name string) bool { return strings.EqualFold(name, "member") })
		}

		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, auditOU), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("organizationalUnit")}},
			{Name: "ou", Values: [][]byte{[]byte("alder-memberaudit")}},
		}})
		a := auditDN("uid=ma-a")
		ghost, ghost2 := auditDN("uid=ma-ghost"), auditDN("uid=ma-ghost2")
		remote := "uid=ma-x," + auditDN("ou=remote")
		mustApply(t, sess, person(t, a, "ma-a"))
		group := func(rdn string, members ...string) {
			values := make([][]byte, len(members))
			for i, m := range members {
				values[i] = []byte(m)
			}
			mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, auditDN(rdn)), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
				{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("groupOfNames")}},
				{Name: "cn", Values: [][]byte{[]byte(strings.TrimPrefix(rdn, "cn="))}},
				{Name: "member", Values: values},
			}})
		}
		group("cn=ma-mixed", a, ghost)
		group("cn=ma-orphan", ghost2)
		group("cn=ma-remote", remote, a)

		// posixGroup is structural on one server and auxiliary on the other,
		// so the entry is built from what this schema says, not from its name.
		posixClasses := [][]byte{[]byte("top"), []byte("posixGroup")}
		if oc := sch.ObjectClass("posixGroup"); oc != nil && oc.Kind != schema.KindStructural {
			posixClasses = append(posixClasses, []byte("device"))
		}
		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, auditDN("cn=ma-posix")), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: posixClasses},
			{Name: "cn", Values: [][]byte{[]byte("ma-posix")}},
			{Name: "gidNumber", Values: [][]byte{[]byte("7101")}},
			{Name: "memberUid", Values: [][]byte{[]byte("ma-a")}},
		}})

		referral := ldap.NewAddRequest(auditDN("ou=remote"), []ldap.Control{ldap.NewControlManageDsaIT(true)})
		referral.Attribute("objectClass", []string{"top", "referral", "extensibleObject"})
		referral.Attribute("ou", []string{"remote"})
		referral.Attribute("ref", []string{"ldap://elsewhere.invalid/" + auditDN("ou=remote")})
		if err := raw.Add(referral); err != nil {
			t.Fatalf("adding the referral object on %s: %v", s.name, err)
		}

		audit, res := runAudit(t, client, base)
		if res.status != http.StatusOK {
			t.Fatalf("audit: status %d\n%s", res.status, res.body)
		}

		t.Run("values never created are not found, and said to be not found by this bind", func(t *testing.T) {
			if !strings.EqualFold(audit.Bind, s.bindDN) {
				t.Errorf("bind = %q, want %q", audit.Bind, s.bindDN)
			}
			mixed := auditGroup(t, audit, "cn=ma-mixed")
			if len(mixed.NotFound) != 1 || !strings.EqualFold(mixed.NotFound[0].Value, ghost) || mixed.Remaining != 1 {
				t.Errorf("ma-mixed = %s", mustEncode(t, mixed))
			}
			orphan := auditGroup(t, audit, "cn=ma-orphan")
			if len(orphan.NotFound) != 1 || orphan.Remaining != 0 {
				t.Errorf("ma-orphan = %s", mustEncode(t, orphan))
			}
			// Asked of the schema: 389 DS publishes member as optional in
			// groupOfNames, and then the group can simply be emptied.
			if orphan.ClassesRequireMember != requires("top", "groupOfNames") {
				t.Errorf("ma-orphan classesRequireMember = %v, the schema says %v", orphan.ClassesRequireMember, requires("top", "groupOfNames"))
			}
			posix := auditGroup(t, audit, "cn=ma-posix")
			if posix.ClassesRequireMember || len(posix.NotChecked) != 1 || posix.NotChecked[0].Reason != api.UncheckedNotADn {
				t.Errorf("ma-posix = %s", mustEncode(t, posix))
			}
		})

		t.Run("a value below a referral is not checked, never not found", func(t *testing.T) {
			g := auditGroup(t, audit, "cn=ma-remote")
			if len(g.NotFound) != 0 || len(g.NotChecked) != 1 || g.NotChecked[0].Reason != api.UncheckedBelowReferral {
				t.Errorf("ma-remote = %s", mustEncode(t, g))
			}
			if len(audit.Referrals) == 0 {
				t.Errorf("no referral reported; the server returned one for %s", auditDN("ou=remote"))
			}
		})

		t.Run("the clean-up of one group applies, and the plan refuses to empty a group that requires a member", func(t *testing.T) {
			planAndApply(t, client, base, membersChange(auditDN("cn=ma-mixed"), "delete", ghost), api.PlanActionModify)
			if got := membersOf(t, sess, auditDN("cn=ma-mixed")); !slices.Equal(got, lowerSorted(a)) {
				t.Errorf("ma-mixed members = %q, want only ma-a", got)
			}
			item := bulkPlan(t, client, base, []string{membersChange(auditDN("cn=ma-orphan"), "delete", ghost2)}).Items[0]
			if requires("top", "groupOfNames") {
				if item.Action != api.PlanActionInvalid || item.Problem == nil ||
					item.Problem.Code != api.PlanProblemMissingRequiredAttribute {
					t.Errorf("removing ma-orphan's last value planned as %s", mustEncode(t, item))
				}
			} else if item.Action != api.PlanActionModify {
				t.Errorf("member is optional here, so emptying ma-orphan is a modify; planned as %s", mustEncode(t, item))
			}
		})

		t.Run("past the direct reads, existence is read under the names' parents only", func(t *testing.T) {
			values := make([][]byte, 0, 210)
			for i := range 210 {
				values = append(values, []byte(auditDN(fmt.Sprintf("uid=ma-many-%d", i))))
			}
			mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, auditDN("cn=ma-mixed")), Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModAdd, Name: "member", Values: values}}})

			again, res := runAudit(t, client, base)
			if res.status != http.StatusOK {
				t.Fatalf("audit: status %d\n%s", res.status, res.body)
			}
			mixed := auditGroup(t, again, "cn=ma-mixed")
			if len(mixed.NotFound) != 210 || mixed.Remaining != 1 {
				t.Errorf("ma-mixed: %d not found, %d remaining; want 210 and 1", len(mixed.NotFound), mixed.Remaining)
			}
			// The groups' own search, then the names' common parent, one level
			// down: a handful of entries, not the 320 the suffix holds.
			read := 0
			for _, r := range again.Read[1:] {
				if !strings.EqualFold(r.Base, auditOU) || r.Scope != api.MemberAuditReadScopeOne {
					t.Errorf("existence read %+v, want one level under %s", r, auditOU)
				}
				read += r.Entries
			}
			if read == 0 || read > 20 {
				t.Errorf("%d entries read to check 212 names; want the ou's few children", read)
			}
		})
	})
}
