//go:build conformance

package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/plan"
)

// Planning, against real directories.
//
// The unit tests classify records against a map. These ask the two servers, and
// the reason they are worth having separately is that "there is no such entry"
// is a protocol answer: 389 DS and OpenLDAP both have to produce a result code
// that Alder reads as absence, and a planner that mistook either for a hard
// failure would classify every add as an error against that server.
//
// Drift is the other half. Between the plan and the apply, this test is the
// other administrator.

func notFound(err error) bool {
	var ldapErr *ldapdriver.Error
	return errors.As(err, &ldapErr) && ldapErr.IsNoSuchObject()
}

func planner(t *testing.T) *plan.Planner {
	t.Helper()
	p, err := plan.NewPlanner(notFound)
	if err != nil {
		t.Fatalf("building a planner: %v", err)
	}
	return p
}

func mustDN(t *testing.T, s string) dn.DN {
	t.Helper()
	d, err := dn.Parse(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return d
}

// planReader is what the API layer hands the planner: reads and child counts,
// and nothing that writes.
type planReader struct{ sess directory.Session }

func (p planReader) Read(c context.Context, target dn.DN, attrs []string) (*directory.Entry, error) {
	return p.sess.Read(c, target, attrs)
}

// HasChildren is how a delete of a container is told from a delete of a leaf.
// The driver has it; the interface the planner takes does not require it.
func (p planReader) HasChildren(c context.Context, target dn.DN) (bool, error) {
	type browser interface {
		HasChildren(context.Context, dn.DN) (bool, error)
	}
	b, ok := p.sess.(browser)
	if !ok {
		return false, nil
	}
	return b.HasChildren(c, target)
}

// TestPlanClassifiesAgainstBothServers walks the classifications that depend on
// what the directory says rather than on what the record contains.
func TestPlanClassifiesAgainstBothServers(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		existing := mustDN(t, "uid=user0001,ou=people,"+suffix)
		absent := mustDN(t, "uid=nobody-plans-this,ou=people,"+suffix)

		// Read one attribute the fixture holds, so the "already true" case can
		// be built from the directory rather than guessed at.
		live, err := sess.Read(ctx(t), existing, []string{"sn"})
		if err != nil {
			t.Fatalf("reading the fixture: %v", err)
		}
		currentSN := live.Get("sn")
		if len(currentSN) == 0 {
			t.Fatalf("%s has no sn to plan against", existing)
		}

		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("reading the schema: %v", err)
		}

		records := []directory.ChangeRecord{
			// Already true: the value it sets is the value the entry holds.
			{DN: existing, Type: directory.ChangeModify, Mods: []directory.Mod{
				{Op: directory.ModReplace, Name: "sn", Values: currentSN}}},
			// A real modification.
			{DN: existing, Type: directory.ChangeModify, Mods: []directory.Mod{
				{Op: directory.ModReplace, Name: "description",
					Values: [][]byte{[]byte("planned, not applied")}}}},
			// An add of an entry that is not there.
			{DN: absent, Type: directory.ChangeAdd, Attrs: []directory.Attribute{
				{Name: "objectClass", Values: [][]byte{
					[]byte("top"), []byte("person"), []byte("inetOrgPerson")}},
				{Name: "cn", Values: [][]byte{[]byte("Nobody")}},
				{Name: "sn", Values: [][]byte{[]byte("Nobody")}},
			}},
			// A modification of one that is not.
			{DN: absent, Type: directory.ChangeModify, Mods: []directory.Mod{
				{Op: directory.ModReplace, Name: "sn", Values: [][]byte{[]byte("x")}}}},
			// A delete of a container, which every LDAP server refuses.
			{DN: mustDN(t, "ou=people,"+suffix), Type: directory.ChangeDelete},
		}

		computed, err := planner(t).Compute(ctx(t), planReader{sess}, sch, records, plan.Options{})
		if err != nil {
			t.Fatalf("planning: %v", err)
		}

		want := []plan.Action{
			plan.ActionUnchanged,
			plan.ActionModify,
			plan.ActionAdd,
			plan.ActionConflict,
			plan.ActionConflict,
		}
		if len(computed.Items) != len(want) {
			t.Fatalf("%d items, want %d", len(computed.Items), len(want))
		}
		for i, expected := range want {
			if got := computed.Items[i].Action; got != expected {
				t.Errorf("item %d (%s): action = %q, want %q\nreason: %s",
					i, computed.Items[i].DN, got, expected, computed.Items[i].Reason)
			}
		}

		// And the plan applied nothing: the description it said it would set is
		// not set.
		after, err := sess.Read(ctx(t), existing, []string{"description"})
		if err != nil {
			t.Fatalf("re-reading: %v", err)
		}
		for _, v := range after.Get("description") {
			if string(v) == "planned, not applied" {
				t.Fatal("planning wrote to the directory")
			}
		}
	})
}

// The two halves of the promise, against a real server: a plan verifies while
// nothing has moved, and stops verifying the moment somebody else writes.
func TestPlanDetectsAnotherAdministrator(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		target := mustDN(t, "uid=user0002,ou=people,"+suffix)
		pl := planner(t)

		before, err := sess.Read(ctx(t), target, []string{"description"})
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		original := before.Get("description")
		t.Cleanup(func() {
			restore := directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModReplace, Name: "description", Values: original}}}
			if len(original) == 0 {
				restore.Mods[0].Op = directory.ModDelete
				restore.Mods[0].Values = nil
			}
			if err := sess.Apply(ctx(t), restore); err != nil {
				t.Logf("restoring description: %v", err)
			}
		})

		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("reading the schema: %v", err)
		}
		record := directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: "description",
				Values: [][]byte{[]byte("what the plan intends")}}}}

		computed, err := pl.Compute(ctx(t), planReader{sess}, sch,
			[]directory.ChangeRecord{record}, plan.Options{})
		if err != nil {
			t.Fatalf("planning: %v", err)
		}
		baseline := computed.Items[0].Baseline

		// Nothing has happened yet, so it still holds.
		if err := pl.Verify(ctx(t), planReader{sess}, record, baseline); err != nil {
			t.Fatalf("a fresh plan did not verify: %v", err)
		}

		// Somebody else writes the very attribute this plan depends on.
		other := directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: "description",
				Values: [][]byte{[]byte("somebody else got here first")}}}}
		if err := sess.Apply(ctx(t), other); err != nil {
			t.Fatalf("the other administrator's write failed: %v", err)
		}

		err = pl.Verify(ctx(t), planReader{sess}, record, baseline)
		if err == nil {
			t.Fatal("the plan still verified after the entry was changed underneath it")
		}
		if !plan.IsStale(err) {
			t.Errorf("Verify returned %v, want a stale error", err)
		}
	})
}

// A reconciled round trip against a real directory: read an entry, offer it
// back as an add, and the plan should say there is nothing to do.
//
// This is the export-edit-import loop with the edit left out, and it is the
// case that has to be silent on both servers -- the values come back from the
// server byte for byte, so anything that normalised differently between them
// would show up here as a phantom modification.
func TestPlanReconcilesAnUnchangedRoundTripToNothing(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		target := mustDN(t, "uid=user0003,ou=people,"+suffix)

		live, err := sess.Read(ctx(t), target, []string{"*"})
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("reading the schema: %v", err)
		}

		// Offer every attribute back exactly as it came, which is what an
		// unedited export re-imported amounts to.
		record := directory.ChangeRecord{DN: target, Type: directory.ChangeAdd}
		for _, name := range live.Order {
			record.Attrs = append(record.Attrs,
				directory.Attribute{Name: name, Values: live.Get(name)})
		}

		computed, err := planner(t).Compute(ctx(t), planReader{sess}, sch,
			[]directory.ChangeRecord{record}, plan.Options{Reconcile: true})
		if err != nil {
			t.Fatalf("planning: %v", err)
		}
		item := computed.Items[0]
		if item.Action != plan.ActionUnchanged {
			t.Errorf("an unedited round trip planned as %q, not unchanged\nreason: %s\nrecord: %s",
				item.Action, item.Reason, item.Record.LDIF())
		}
		if len(computed.Applicable()) != 0 {
			t.Errorf("%d changes would be applied for an unedited round trip",
				len(computed.Applicable()))
		}
	})
}
