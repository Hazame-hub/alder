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
// for distinguishedNameMatch and the caseIgnore family, and bytes otherwise.
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
