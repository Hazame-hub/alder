//go:build conformance

package conformance

import (
	"errors"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/plan"
)

// Whether an entry already holds a value, asked of the planner and of the
// directory, on both servers.
//
// The planner compares an add or a delete under the attribute's equality rule
// for distinguishedNameMatch, uniqueMemberMatch and the caseIgnore family, and
// bytes otherwise.
// The point of running it here is the other half: for every pair the planner
// calls the same value, each server is asked too -- an add of the variant is
// refused with attributeOrValueExists, and a delete of it removes the stored
// value -- so the planner is held to what the directories actually do, not to
// what RFC 4517 says they should.

func TestPlanComparesAddAndDeleteUnderTheEqualityRule(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		target := "uid=user0004,ou=people," + suffix
		d := mustDN(t, target)
		restoreAttribute(t, sess, target, "description")
		restoreAttribute(t, sess, target, "mail")
		restoreAttribute(t, sess, target, "seeAlso")

		stored := map[string]string{
			"description": "Platform  Team",                  // caseIgnoreMatch
			"mail":        "Ana.Lima@Example.com",            // caseIgnoreIA5Match
			"seeAlso":     "cn=Platform,ou=Groups," + suffix, // distinguishedNameMatch
		}
		variant := map[string]string{
			"description": " platform team ",
			"mail":        "ana.lima@EXAMPLE.COM",
			"seeAlso":     "CN=platform, OU=groups," + suffixUpper(), // spacing and case in the name
		}
		for name, value := range stored {
			mustApply(t, sess, directory.ChangeRecord{DN: d, Type: directory.ChangeModify, Mods: []directory.Mod{
				{Op: directory.ModReplace, Name: name, Values: [][]byte{[]byte(value)}}}})
		}

		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("reading the schema: %v", err)
		}
		modify := func(op directory.ModOp, name, value string) directory.ChangeRecord {
			return directory.ChangeRecord{DN: d, Type: directory.ChangeModify, Mods: []directory.Mod{
				{Op: op, Name: name, Values: [][]byte{[]byte(value)}}}}
		}
		planOne := func(rec directory.ChangeRecord) plan.Action {
			t.Helper()
			computed, err := planner(t).Compute(ctx(t), planReader{sess}, sch, []directory.ChangeRecord{rec}, plan.Options{})
			if err != nil {
				t.Fatalf("planning: %v", err)
			}
			return computed.Items[0].Action
		}

		for name := range stored {
			t.Run(name, func(t *testing.T) {
				// The planner: an add of the variant is nothing to do.
				addVariant := modify(directory.ModAdd, name, variant[name])
				if got := planOne(addVariant); got != plan.ActionUnchanged {
					t.Errorf("adding %q to an entry holding %q planned as %q, want unchanged",
						variant[name], stored[name], got)
				}
				// The directory agrees: it refuses the same add as a value it holds.
				err := sess.Apply(ctx(t), addVariant)
				var ldapErr *ldapdriver.Error
				if !errors.As(err, &ldapErr) || ldapErr.Code != ldap.LDAPResultAttributeOrValueExists {
					t.Errorf("%s refused the add with %v, want attributeOrValueExists: the planner and the server disagree", s.name, err)
				}

				// The planner: a delete of the variant is a change...
				delVariant := modify(directory.ModDelete, name, variant[name])
				if got := planOne(delVariant); got != plan.ActionModify {
					t.Errorf("deleting %q from an entry holding %q planned as %q, want modify",
						variant[name], stored[name], got)
				}
				// ...and the directory removes the stored value for it.
				mustApply(t, sess, delVariant)
				if got := readAll(t, sess, target, name); len(got) != 0 {
					t.Errorf("after deleting the variant, %s still holds %q", name, got)
				}
				// Once it is gone, the same delete is nothing to do.
				if got := planOne(delVariant); got != plan.ActionUnchanged {
					t.Errorf("deleting a value the entry no longer holds planned as %q, want unchanged", got)
				}
			})
		}
	})
}

// suffixUpper is the harness suffix with its attribute types and values in
// capitals, a spelling distinguishedNameMatch treats as the same name.
func suffixUpper() string {
	out := []rune(suffix)
	for i, r := range out {
		if r >= 'a' && r <= 'z' {
			out[i] = r - 'a' + 'A'
		}
	}
	return string(out)
}

func readAll(t *testing.T, sess directory.Session, target, attr string) []string {
	t.Helper()
	e, err := sess.Read(ctx(t), mustDN(t, target), []string{attr})
	if err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	var out []string
	for _, v := range e.Get(attr) {
		out = append(out, string(v))
	}
	return out
}

// restoreAttribute puts an attribute back as it was at the end of a test.
func restoreAttribute(t *testing.T, sess directory.Session, target, attr string) {
	t.Helper()
	d := mustDN(t, target)
	before, err := sess.Read(ctx(t), d, []string{attr})
	if err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	original := before.Get(attr)
	t.Cleanup(func() {
		restore := directory.ChangeRecord{DN: d, Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: attr, Values: original}}}
		if len(original) == 0 {
			restore.Mods[0].Op = directory.ModDelete
			restore.Mods[0].Values = nil
		}
		if err := sess.Apply(ctx(t), restore); err != nil {
			var ldapErr *ldapdriver.Error
			if errors.As(err, &ldapErr) && ldapErr.Code == ldap.LDAPResultNoSuchAttribute {
				return // there was none, and there is none
			}
			t.Logf("restoring %s of %s: %v", attr, target, err)
		}
	})
}

// uniqueMember, matched by uniqueMemberMatch: the name compared as a name, and
// the optional #'...'B UID exactly as written.
//
// The pairs the planner calls the same value -- a bare name spelled in other
// case and spacing, and a name with the same UID spelled likewise -- are held
// to each server as above. The pairs it calls different, a bare name against
// the name with a UID and two different UIDs, are planned as before: an add
// is a change and a delete is nothing to do. What each server does with those
// is logged rather than asserted, because the planner does not lean on it --
// keeping the UID as written is what lets it not care.
const uniqueMemberGroup = "cn=alder-uniquemember-equality,ou=groups," + suffix

func TestPlanComparesUniqueMemberUnderUniqueMemberMatch(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		d := mustDN(t, uniqueMemberGroup)
		_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: d, Type: directory.ChangeDelete})
		t.Cleanup(func() { _ = sess.Apply(ctx(t), directory.ChangeRecord{DN: d, Type: directory.ChangeDelete}) })

		bare := "uid=user0001,ou=people," + suffix
		withUID := "uid=user0002,ou=people," + suffix + "#'0101'B"
		// A third member, so no case here leaves the group without the
		// uniqueMember its class requires.
		other := "uid=user0003,ou=people," + suffix
		mustApply(t, sess, directory.ChangeRecord{DN: d, Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("groupOfUniqueNames")}},
			{Name: "cn", Values: [][]byte{[]byte("alder-uniquemember-equality")}},
			{Name: "uniqueMember", Values: [][]byte{[]byte(bare), []byte(withUID), []byte(other)}},
		}})

		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("reading the schema: %v", err)
		}
		modify := func(op directory.ModOp, value string) directory.ChangeRecord {
			return directory.ChangeRecord{DN: d, Type: directory.ChangeModify, Mods: []directory.Mod{
				{Op: op, Name: "uniqueMember", Values: [][]byte{[]byte(value)}}}}
		}
		planOne := func(rec directory.ChangeRecord) plan.Action {
			t.Helper()
			computed, err := planner(t).Compute(ctx(t), planReader{sess}, sch, []directory.ChangeRecord{rec}, plan.Options{})
			if err != nil {
				t.Fatalf("planning: %v", err)
			}
			return computed.Items[0].Action
		}
		holds := func(value string) bool {
			t.Helper()
			for _, v := range readAll(t, sess, uniqueMemberGroup, "uniqueMember") {
				if v == value {
					return true
				}
			}
			return false
		}

		same := []struct{ name, stored, variant string }{
			{"a bare name", bare, "UID=user0001, OU=People," + suffixUpper()},
			{"a name with a UID", withUID, "UID=USER0002,OU=people," + suffixUpper() + "#'0101'B"},
		}
		for _, c := range same {
			t.Run(c.name, func(t *testing.T) {
				addVariant := modify(directory.ModAdd, c.variant)
				if got := planOne(addVariant); got != plan.ActionUnchanged {
					t.Errorf("adding %q to a group holding %q planned as %q, want unchanged", c.variant, c.stored, got)
				}
				err := sess.Apply(ctx(t), addVariant)
				var ldapErr *ldapdriver.Error
				if !errors.As(err, &ldapErr) || ldapErr.Code != ldap.LDAPResultAttributeOrValueExists {
					t.Errorf("%s refused the add with %v, want attributeOrValueExists: the planner and the server disagree", s.name, err)
				}

				delVariant := modify(directory.ModDelete, c.variant)
				if got := planOne(delVariant); got != plan.ActionModify {
					t.Errorf("deleting %q from a group holding %q planned as %q, want modify", c.variant, c.stored, got)
				}
				mustApply(t, sess, delVariant)
				if holds(c.stored) {
					t.Errorf("after deleting %q, %s still holds %q", c.variant, s.name, c.stored)
				}
				mustApply(t, sess, modify(directory.ModAdd, c.stored))
			})
		}

		different := []struct{ name, stored, variant string }{
			{"the name without its UID", withUID, "uid=user0002,ou=people," + suffix},
			{"the name with a UID it was not given", bare, bare + "#'1'B"},
			{"another UID", withUID, "uid=user0002,ou=people," + suffix + "#'0110'B"},
		}
		for _, c := range different {
			t.Run(c.name, func(t *testing.T) {
				addVariant := modify(directory.ModAdd, c.variant)
				if got := planOne(addVariant); got != plan.ActionModify {
					t.Errorf("adding %q to a group holding %q planned as %q, want modify", c.variant, c.stored, got)
				}
				delVariant := modify(directory.ModDelete, c.variant)
				if got := planOne(delVariant); got != plan.ActionUnchanged {
					t.Errorf("deleting %q from a group holding %q planned as %q, want unchanged", c.variant, c.stored, got)
				}

				// What the server makes of the pair, for the record.
				addErr := sess.Apply(ctx(t), addVariant)
				added := addErr == nil && holds(c.variant)
				if added {
					mustApply(t, sess, delVariant)
				}
				delErr := sess.Apply(ctx(t), delVariant)
				removed := !holds(c.stored)
				if removed {
					mustApply(t, sess, modify(directory.ModAdd, c.stored))
				}
				t.Logf("%s: add of %q beside %q: err=%v, added=%v; delete: err=%v, removed the stored value=%v",
					s.name, c.variant, c.stored, addErr, added, delErr, removed)
			})
		}
	})
}
