package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// countingDirectory is a fakeDirectory that also counts what is below an
// entry, from the entries it holds.
type countingDirectory struct {
	fakeDirectory
	counts int
}

func (c *countingDirectory) HasChildren(_ context.Context, target dn.DN) (bool, error) {
	n, _, _ := c.below(target, 1)
	return n > 0, nil
}

func (c *countingDirectory) CountBelow(_ context.Context, target dn.DN, limit int) (int, bool, error) {
	c.counts++
	n, truncated, _ := c.below(target, limit)
	return n, truncated, nil
}

func (c *countingDirectory) below(target dn.DN, limit int) (int, bool, error) {
	n := 0
	for key := range c.entries {
		d, err := dn.Parse(key)
		if err != nil || d.Equal(target) || !d.HasSuffix(target) {
			continue
		}
		n++
		if n >= limit {
			return limit, true, nil
		}
	}
	return n, false, nil
}

const (
	people = "ou=people,dc=alder,dc=test"
	staff  = "ou=staff,dc=alder,dc=test"
)

func moveRecord(t *testing.T, from, newRDN, newSuperior string) directory.ChangeRecord {
	t.Helper()
	r := directory.ChangeRecord{DN: mustParse(t, from), Type: directory.ChangeModRDN, NewRDN: newRDN}
	if newSuperior != "" {
		r.NewSuperior = mustParse(t, newSuperior)
	}
	return r
}

func TestAMoveUnderAMissingParentIsAConflict(t *testing.T) {
	d := &countingDirectory{}
	d.put(t, alice, []string{"cn", "Alice"})
	p, err := newTestPlanner(t).Compute(context.Background(), d, nil,
		[]directory.ChangeRecord{moveRecord(t, alice, "uid=alice", staff)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	item := p.Items[0]
	if item.Action != ActionConflict || item.Problem == nil || item.Problem.Code != ProblemParentMissing {
		t.Fatalf("got %s %+v, want a parent_missing conflict", item.Action, item.Problem)
	}
}

func TestAMoveUnderAParentAddedEarlierInTheSetIsPlanned(t *testing.T) {
	d := &countingDirectory{}
	d.put(t, alice, []string{"cn", "Alice"})
	d.put(t, "uid=bob,"+people, []string{"cn", "Bob"})
	records := []directory.ChangeRecord{
		addRecord(t, staff, []string{"objectClass", "organizationalUnit"}, []string{"ou", "staff"}),
		moveRecord(t, alice, "uid=alice", staff),
		// Moved under a parent that the set itself moved into place.
		moveRecord(t, "uid=bob,"+people, "uid=bob", "uid=alice,"+staff),
	}
	p, err := newTestPlanner(t).Compute(context.Background(), d, nil, records, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []Action{ActionAdd, ActionRename, ActionRename} {
		if got := p.Items[i].Action; got != want {
			t.Errorf("item %d: %s %+v, want %s", i, got, p.Items[i].Problem, want)
		}
	}
}

func TestARenameInPlaceDoesNotReadItsParent(t *testing.T) {
	d := &countingDirectory{}
	d.put(t, alice, []string{"cn", "Alice"})
	p, err := newTestPlanner(t).Compute(context.Background(), d, nil,
		[]directory.ChangeRecord{moveRecord(t, alice, "uid=alicia", "")}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Items[0].Action != ActionRename {
		t.Fatalf("got %s %+v", p.Items[0].Action, p.Items[0].Problem)
	}
	for _, r := range d.reads {
		if strings.EqualFold(r, people) {
			t.Errorf("read the parent %s for a rename that keeps it", r)
		}
	}
}

func TestARenameSaysHowManyEntriesMoveWithIt(t *testing.T) {
	d := &countingDirectory{}
	d.put(t, people, []string{"ou", "people"})
	d.put(t, staff, []string{"ou", "staff"})
	d.put(t, alice, []string{"cn", "Alice"})
	d.put(t, "uid=bob,"+people, []string{"cn", "Bob"})
	d.put(t, "cn=x,uid=bob,"+people, []string{"cn", "x"})

	p, err := newTestPlanner(t).Compute(context.Background(), d, nil, []directory.ChangeRecord{
		moveRecord(t, people, "ou=people", staff),
		moveRecord(t, alice, "uid=alicia", ""),
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	branch, leaf := p.Items[0], p.Items[1]
	if branch.Subordinates == nil || branch.Subordinates.Count != 3 || branch.Subordinates.Truncated {
		t.Errorf("branch move subordinates = %+v, want 3", branch.Subordinates)
	}
	if leaf.Subordinates != nil {
		t.Errorf("leaf rename subordinates = %+v, want none", leaf.Subordinates)
	}
}

func TestASavedRenameExpectsItsSubordinates(t *testing.T) {
	d := &countingDirectory{}
	d.put(t, people, []string{"ou", "people"})
	d.put(t, alice, []string{"cn", "Alice"})
	planner := newTestPlanner(t)
	move := moveRecord(t, people, "ou=folks", "")

	plan := func(want int) Item {
		t.Helper()
		p, err := planner.ComputeProposals(context.Background(), d, nil,
			[]Proposal{{Record: move, Intent: IntentExact, Expect: &Expectation{Subordinates: &want}}}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		return p.Items[0]
	}

	held := plan(1)
	if held.Action != ActionRename {
		t.Fatalf("expecting the one entry below: %s %+v", held.Action, held.Problem)
	}
	if got := plan(0); got.Action != ActionConflict || got.Problem.Code != ProblemExpectedStateDiffers ||
		got.Problem.Attribute != "subordinates" {
		t.Errorf("expecting none below: %s %+v, want expected_state_differs on subordinates", got.Action, got.Problem)
	}

	// A child added after planning makes the apply stale.
	one := 1
	if _, _, err := planner.VerifyRead(context.Background(), d, nil, held.Record, held.Baseline,
		&Expectation{Subordinates: &one}, nil); err != nil {
		t.Fatalf("verify before the change: %v", err)
	}
	d.put(t, "uid=bob,"+people, []string{"cn", "Bob"})
	_, _, err := planner.VerifyRead(context.Background(), d, nil, held.Record, held.Baseline,
		&Expectation{Subordinates: &one}, nil)
	if !IsStale(err) {
		t.Errorf("verify after a child was added: %v, want stale", err)
	}
}

func TestASubordinateExpectationNeedsACounter(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, alice, []string{"cn", "Alice"})
	zero := 0
	_, err := newTestPlanner(t).ComputeProposals(context.Background(), d, nil,
		[]Proposal{{Record: moveRecord(t, alice, "uid=alicia", ""), Intent: IntentExact,
			Expect: &Expectation{Subordinates: &zero}}}, Options{})
	if err == nil || !strings.Contains(err.Error(), "cannot count") {
		t.Errorf("err = %v, want one saying the entries cannot be counted", err)
	}
}
