package plan

import (
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

func planOne(t *testing.T, d *fakeDirectory, proposal Proposal) (*Planner, Item) {
	t.Helper()
	pl := newTestPlanner(t)
	p, err := pl.ComputeProposals(t.Context(), d, testSchema(t), []Proposal{proposal}, Options{})
	if err != nil {
		t.Fatalf("ComputeProposals: %v", err)
	}
	return pl, p.Items[0]
}

func expectation(exhaustive bool, pairs ...[]string) *Expectation {
	e := &Expectation{Exhaustive: exhaustive}
	for _, p := range pairs {
		a := directory.Attribute{Name: p[0]}
		for _, v := range p[1:] {
			a.Values = append(a.Values, []byte(v))
		}
		e.Attributes = append(e.Attributes, a)
	}
	return e
}

func TestAnExpectationThatHoldsPlansNormally(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, alice, []string{"mail", "new@alder.test"}, []string{"description", "Kept"})
	record := modifyRecord(t, alice, directory.ModReplace, "mail", "old@alder.test")

	_, item := planOne(t, d, Proposal{Record: record, Intent: IntentExact,
		// An expected absence holds for an attribute the entry does not have.
		Expect: expectation(false, []string{"mail", "new@alder.test"}, []string{"telephoneNumber"})})
	if item.Action != ActionModify {
		t.Fatalf("action %s (%s: %s), want modify", item.Action, item.Problem, item.Reason)
	}
}

func TestAnExpectationThatNoLongerHoldsIsAConflict(t *testing.T) {
	cases := []struct {
		name   string
		expect *Expectation
		attr   string
	}{
		{"a value changed", expectation(false, []string{"mail", "other@alder.test"}), "mail"},
		{"an expected absence", expectation(false, []string{"description"}), "description"},
		{"an extra value", expectation(false, []string{"description", "Kept"}, []string{"mail"}), "mail"},
		{"an attribute nobody expected", expectation(true, []string{"mail", "new@alder.test"}), "description"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDirectory{}
			d.put(t, alice, []string{"mail", "new@alder.test"}, []string{"description", "Kept"})
			record := modifyRecord(t, alice, directory.ModReplace, "mail", "old@alder.test")
			_, item := planOne(t, d, Proposal{Record: record, Intent: IntentExact, Expect: tc.expect})
			if item.Action != ActionConflict || item.Problem == nil || item.Problem.Code != ProblemExpectedStateDiffers {
				t.Fatalf("action %s problem %s, want a conflict: expected_state_differs", item.Action, item.Problem)
			}
			if item.Baseline != "" {
				t.Error("a conflict carries a token that would let it be applied")
			}
		})
	}
}

func TestAnExhaustiveExpectationIgnoresWhatTheServerOwns(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, alice,
		[]string{"objectClass", "top", "person", "inetOrgPerson"},
		[]string{"sn", "Liddell"},
		[]string{"cn", "alice"},
		[]string{"entryUUID", "5f1d"},
		[]string{"modifyTimestamp", "20260101000000Z"},
		[]string{"userPassword", "{SSHA}hash"},
	)
	record := directory.ChangeRecord{DN: mustParse(t, alice), Type: directory.ChangeDelete}
	_, item := planOne(t, d, Proposal{Record: record, Intent: IntentExact,
		Expect: expectation(true, []string{"sn", "Liddell"}, []string{"cn", "alice"})})
	if item.Action != ActionDelete {
		t.Fatalf("action %s (%s: %s), want delete", item.Action, item.Problem, item.Reason)
	}
}

func TestAnExpectationBindsTheBaseline(t *testing.T) {
	// The token covers what the expectation names, so a change to it between
	// plan and apply is stale even though the operation never touches it.
	d := &fakeDirectory{}
	d.put(t, alice, []string{"mail", "new@alder.test"}, []string{"description", "Kept"})
	record := modifyRecord(t, alice, directory.ModReplace, "mail", "old@alder.test")
	pl, item := planOne(t, d, Proposal{Record: record, Intent: IntentExact,
		Expect: expectation(false, []string{"mail", "new@alder.test"}, []string{"description", "Kept"})})
	if item.Action != ActionModify {
		t.Fatalf("action %s", item.Action)
	}
	if err := pl.Verify(t.Context(), d, record, item.Baseline); err != nil {
		t.Fatalf("Verify of an unchanged entry: %v", err)
	}
	d.put(t, alice, []string{"mail", "new@alder.test"}, []string{"description", "Changed by somebody"})
	if err := pl.Verify(t.Context(), d, record, item.Baseline); !IsStale(err) {
		t.Errorf("Verify after the expected description changed: %v, want stale", err)
	}
}
