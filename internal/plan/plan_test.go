package plan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// notFound is the driver's "no such entry", modelled here as its own type so
// the planner is tested through the same seam the API wires up.
type notFoundError struct{}

func (notFoundError) Error() string { return "no such object" }

func isNotFound(err error) bool {
	var nf notFoundError
	return errors.As(err, &nf)
}

// fakeDirectory answers reads from a map and can report children.
type fakeDirectory struct {
	entries  map[string]*directory.Entry
	children map[string]bool
	readErr  error
	reads    []string
}

func (f *fakeDirectory) Read(_ context.Context, target dn.DN, _ []string) (*directory.Entry, error) {
	f.reads = append(f.reads, target.String())
	if f.readErr != nil {
		return nil, f.readErr
	}
	e, ok := f.entries[strings.ToLower(target.String())]
	if !ok {
		return nil, notFoundError{}
	}
	return e, nil
}

func (f *fakeDirectory) HasChildren(_ context.Context, target dn.DN) (bool, error) {
	return f.children[strings.ToLower(target.String())], nil
}

func (f *fakeDirectory) put(t *testing.T, d string, pairs ...[]string) {
	t.Helper()
	if f.entries == nil {
		f.entries = map[string]*directory.Entry{}
	}
	e := directory.NewEntry(mustParse(t, d))
	for _, p := range pairs {
		values := make([][]byte, 0, len(p)-1)
		for _, v := range p[1:] {
			values = append(values, []byte(v))
		}
		e.Set(p[0], values)
	}
	f.entries[strings.ToLower(d)] = e
}

func newTestPlanner(t *testing.T) *Planner {
	t.Helper()
	p, err := NewPlanner(isNotFound)
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}
	return p
}

func modifyRecord(t *testing.T, d string, op directory.ModOp, name string, values ...string) directory.ChangeRecord {
	t.Helper()
	vs := make([][]byte, 0, len(values))
	for _, v := range values {
		vs = append(vs, []byte(v))
	}
	return directory.ChangeRecord{
		DN:   mustParse(t, d),
		Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: op, Name: name, Values: vs}},
	}
}

// --- classification ----------------------------------------------------------

func TestClassification(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(t *testing.T, d *fakeDirectory)
		record     func(t *testing.T) directory.ChangeRecord
		reconcile  bool
		wantAction Action
	}{
		{
			name:       "an add of an entry that is not there",
			record:     func(t *testing.T) directory.ChangeRecord { return addRecord(t, alice, []string{"cn", "Alice"}) },
			wantAction: ActionAdd,
		},
		{
			name: "an add of one that is, without reconcile, cannot be applied",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"cn", "Alice"})
			},
			record:     func(t *testing.T) directory.ChangeRecord { return addRecord(t, alice, []string{"cn", "Alice"}) },
			wantAction: ActionConflict,
		},
		{
			name: "an add of one that is, reconciled, when it already matches",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"cn", "Alice"})
			},
			record:     func(t *testing.T) directory.ChangeRecord { return addRecord(t, alice, []string{"cn", "Alice"}) },
			reconcile:  true,
			wantAction: ActionUnchanged,
		},
		{
			name: "an add of one that is, reconciled, when it does not",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"cn", "Alice"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return addRecord(t, alice, []string{"cn", "Alice Liddell"})
			},
			reconcile:  true,
			wantAction: ActionModify,
		},
		{
			name: "a modify of an entry that is there and differs",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"mail", "old@alder.test"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return modifyRecord(t, alice, directory.ModReplace, "mail", "new@alder.test")
			},
			wantAction: ActionModify,
		},
		{
			name: "a modify that is already true",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"mail", "same@alder.test"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return modifyRecord(t, alice, directory.ModReplace, "mail", "same@alder.test")
			},
			wantAction: ActionUnchanged,
		},
		{
			name: "an add of a value the entry already holds",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"mail", "a@alder.test", "b@alder.test"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return modifyRecord(t, alice, directory.ModAdd, "mail", "b@alder.test")
			},
			wantAction: ActionUnchanged,
		},
		{
			name: "a delete of a value the entry does not hold",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"mail", "a@alder.test"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return modifyRecord(t, alice, directory.ModDelete, "mail", "gone@alder.test")
			},
			wantAction: ActionUnchanged,
		},
		{
			name: "a modify of an entry that is not there",
			record: func(t *testing.T) directory.ChangeRecord {
				return modifyRecord(t, alice, directory.ModReplace, "mail", "x@alder.test")
			},
			wantAction: ActionConflict,
		},
		{
			name: "a delete of a leaf",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"cn", "Alice"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{DN: mustParse(t, alice), Type: directory.ChangeDelete}
			},
			wantAction: ActionDelete,
		},
		{
			name: "a delete of a container",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, "ou=people,dc=alder,dc=test", []string{"ou", "people"})
				d.children = map[string]bool{"ou=people,dc=alder,dc=test": true}
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{
					DN: mustParse(t, "ou=people,dc=alder,dc=test"), Type: directory.ChangeDelete}
			},
			wantAction: ActionConflict,
		},
		{
			name: "a delete of something already gone",
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{DN: mustParse(t, alice), Type: directory.ChangeDelete}
			},
			wantAction: ActionUnchanged,
		},
		{
			name: "a rename of an entry that is there",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"cn", "Alice"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{
					DN: mustParse(t, alice), Type: directory.ChangeModRDN,
					NewRDN: "uid=alicel", DeleteOldRDN: true}
			},
			wantAction: ActionRename,
		},
		{
			name: "a rename of one that is not",
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{
					DN: mustParse(t, alice), Type: directory.ChangeModRDN,
					NewRDN: "uid=alicel", DeleteOldRDN: true}
			},
			wantAction: ActionConflict,
		},
		{
			name: "a password change on an entry that is there",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"cn", "Alice"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{
					DN: mustParse(t, alice), Type: directory.ChangeSetPassword,
					NewPassword: "hunter2"}
			},
			wantAction: ActionSetPassword,
		},
		{
			name: "a password change on one that is not",
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{
					DN: mustParse(t, alice), Type: directory.ChangeSetPassword,
					NewPassword: "hunter2"}
			},
			wantAction: ActionConflict,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDirectory{}
			if tc.setup != nil {
				tc.setup(t, d)
			}
			p, err := newTestPlanner(t).Compute(t.Context(), d, testSchema(t),
				[]directory.ChangeRecord{tc.record(t)}, Options{Reconcile: tc.reconcile})
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			if len(p.Items) != 1 {
				t.Fatalf("%d items, want 1", len(p.Items))
			}
			if got := p.Items[0].Action; got != tc.wantAction {
				t.Errorf("action = %q, want %q (reason: %s)", got, tc.wantAction, p.Items[0].Reason)
			}
		})
	}
}

// A change that does nothing must not reach Apply, and a conflict must not
// either. Everything else must.
func TestOnlyApplicableChangesAreHandedOn(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, alice, []string{"mail", "same@alder.test"})
	d.put(t, "uid=bob,ou=people,dc=alder,dc=test", []string{"mail", "old@alder.test"})

	records := []directory.ChangeRecord{
		modifyRecord(t, alice, directory.ModReplace, "mail", "same@alder.test"),                                // unchanged
		modifyRecord(t, "uid=bob,ou=people,dc=alder,dc=test", directory.ModReplace, "mail", "new@alder.test"),  // modify
		modifyRecord(t, "uid=nobody,ou=people,dc=alder,dc=test", directory.ModReplace, "mail", "x@alder.test"), // conflict
	}
	p, err := newTestPlanner(t).Compute(t.Context(), d, testSchema(t), records, Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if p.Counts.Unchanged != 1 || p.Counts.Modify != 1 || p.Counts.Conflict != 1 {
		t.Fatalf("counts = %+v", p.Counts)
	}
	applicable := p.Applicable()
	if len(applicable) != 1 {
		t.Fatalf("%d applicable, want 1", len(applicable))
	}
	if got := applicable[0].DN.String(); got != "uid=bob,ou=people,dc=alder,dc=test" {
		t.Errorf("the applicable change is %s", got)
	}
}

// The invariant the whole design rests on: what the plan says will be applied
// is the value that is applied, not something reconstructed from it.
func TestThePlanCarriesTheRecordApplyWillReceive(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, alice, []string{"cn", "Alice"}, []string{"mail", "old@alder.test"})

	// A reconcile is the case where the record the caller sent is *not* the
	// record that runs, so it is the case worth pinning.
	sent := addRecord(t, alice,
		[]string{"cn", "Alice"},
		[]string{"mail", "new@alder.test"})

	p, err := newTestPlanner(t).Compute(t.Context(), d, testSchema(t),
		[]directory.ChangeRecord{sent}, Options{Reconcile: true})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	item := p.Items[0]
	if item.Action != ActionModify {
		t.Fatalf("action = %q, want modify", item.Action)
	}

	// The planned record is a modify of exactly the attribute that differs.
	if item.Record.Type != directory.ChangeModify {
		t.Errorf("the planned record is a %q", item.Record.Type)
	}
	if len(item.Record.Mods) != 1 || !strings.EqualFold(item.Record.Mods[0].Name, "mail") {
		t.Fatalf("the planned mods are %+v; cn matches and should not be touched", item.Record.Mods)
	}
	// And Applicable hands back that same record, not the one that arrived.
	applicable := p.Applicable()
	if len(applicable) != 1 || applicable[0].Type != directory.ChangeModify {
		t.Fatalf("Applicable returned %+v", applicable)
	}
	if applicable[0].LDIF() != item.Record.LDIF() {
		t.Error("Applicable rendered differently from the item it came from")
	}
}

// --- drift -------------------------------------------------------------------

func TestVerifyAcceptsAnUnchangedDirectory(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, alice, []string{"mail", "old@alder.test"})
	pl := newTestPlanner(t)

	record := modifyRecord(t, alice, directory.ModReplace, "mail", "new@alder.test")
	p, err := pl.Compute(t.Context(), d, testSchema(t), []directory.ChangeRecord{record}, Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if err := pl.Verify(t.Context(), d, record, p.Items[0].Baseline); err != nil {
		t.Errorf("Verify refused an unchanged directory: %v", err)
	}
}

func TestVerifyDetectsDrift(t *testing.T) {
	cases := []struct {
		name  string
		drift func(t *testing.T, d *fakeDirectory)
	}{
		{
			name: "somebody else changed the attribute",
			drift: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"mail", "someone-else@alder.test"})
			},
		},
		{
			name: "somebody else deleted the entry",
			drift: func(t *testing.T, d *fakeDirectory) {
				delete(d.entries, alice)
			},
		},
		{
			name: "the attribute gained a second value",
			drift: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"mail", "old@alder.test", "extra@alder.test"})
			},
		},
		{
			name: "the attribute was removed",
			drift: func(t *testing.T, d *fakeDirectory) {
				d.put(t, alice, []string{"cn", "Alice"})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDirectory{}
			d.put(t, alice, []string{"mail", "old@alder.test"})
			pl := newTestPlanner(t)

			record := modifyRecord(t, alice, directory.ModReplace, "mail", "new@alder.test")
			p, err := pl.Compute(t.Context(), d, testSchema(t), []directory.ChangeRecord{record}, Options{})
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}

			tc.drift(t, d)

			err = pl.Verify(t.Context(), d, record, p.Items[0].Baseline)
			if err == nil {
				t.Fatal("Verify accepted a directory that had moved")
			}
			if !IsStale(err) {
				t.Errorf("Verify returned %v, want a stale error", err)
			}
		})
	}
}

// A change to an attribute this change does not touch must not invalidate it.
// A baseline over the whole entry would refuse it, and an operator who is
// refused for irrelevant reasons learns to bypass the check.
func TestVerifyIgnoresAnUnrelatedAttribute(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, alice, []string{"mail", "old@alder.test"}, []string{"telephoneNumber", "1234"})
	pl := newTestPlanner(t)

	record := modifyRecord(t, alice, directory.ModReplace, "mail", "new@alder.test")
	p, err := pl.Compute(t.Context(), d, testSchema(t), []directory.ChangeRecord{record}, Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	d.put(t, alice, []string{"mail", "old@alder.test"}, []string{"telephoneNumber", "5678"})

	if err := pl.Verify(t.Context(), d, record, p.Items[0].Baseline); err != nil {
		t.Errorf("Verify refused because an unrelated attribute moved: %v", err)
	}
}

// A baseline is per-process. One Alder's token must mean nothing to another,
// so a plan cannot outlive the server that made it.
func TestABaselineFromAnotherPlannerIsStale(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, alice, []string{"mail", "old@alder.test"})
	record := modifyRecord(t, alice, directory.ModReplace, "mail", "new@alder.test")

	first := newTestPlanner(t)
	p, err := first.Compute(t.Context(), d, testSchema(t), []directory.ChangeRecord{record}, Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	second := newTestPlanner(t)
	if err := second.Verify(t.Context(), d, record, p.Items[0].Baseline); !IsStale(err) {
		t.Errorf("a baseline from another process verified: %v", err)
	}
}

// Rule 6 reaches the fingerprint. A password is covered by presence and count,
// so a plan can still tell "somebody set one" from "nobody has", and the bytes
// never leave.
func TestAPasswordIsFingerprintedWithoutItsValue(t *testing.T) {
	fp, err := NewFingerprinter()
	if err != nil {
		t.Fatalf("NewFingerprinter: %v", err)
	}
	record := modifyRecord(t, alice, directory.ModReplace, "userPassword", "{SSHA}new")

	withOne := directory.NewEntry(mustParse(t, alice))
	withOne.Set("userPassword", [][]byte{[]byte("{SSHA}aaaa")})

	withAnother := directory.NewEntry(mustParse(t, alice))
	withAnother.Set("userPassword", [][]byte{[]byte("{SSHA}bbbb")})

	withNone := directory.NewEntry(mustParse(t, alice))

	// Two different hashes of the same shape are one baseline: the values are
	// not in it.
	if fp.Of(record, withOne) != fp.Of(record, withAnother) {
		t.Error("the fingerprint distinguishes two password values, so it contains them")
	}
	// Set and unset are still different, which is the part worth keeping.
	if fp.Of(record, withOne) == fp.Of(record, withNone) {
		t.Error("the fingerprint cannot tell a set password from an absent one")
	}
}

// A read that fails for any reason other than "not there" must stop the plan.
// Reporting the rest as absent would classify every one of them as an add.
func TestAFailedReadStopsThePlan(t *testing.T) {
	d := &fakeDirectory{readErr: errors.New("the directory hung up")}
	_, err := newTestPlanner(t).Compute(t.Context(), d, testSchema(t),
		[]directory.ChangeRecord{modifyRecord(t, alice, directory.ModReplace, "mail", "x@alder.test")},
		Options{})
	if err == nil {
		t.Fatal("Compute succeeded against a directory that would not answer")
	}
}

// An empty set plans to nothing rather than failing, because "nothing staged"
// is a state the UI reaches by ordinary use.
func TestAnEmptySetPlansToNothing(t *testing.T) {
	p, err := newTestPlanner(t).Compute(t.Context(), &fakeDirectory{}, testSchema(t), nil, Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if p.Counts.Examined != 0 || len(p.Applicable()) != 0 {
		t.Errorf("an empty set planned %+v", p.Counts)
	}
}

// The dependency list travels in the token, so it has to be covered by the MAC.
// A client that could narrow it could ask to be checked against nothing.
func TestABaselineWithATamperedDependencyListIsRejected(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, alice, []string{"mail", "old@alder.test"})
	pl := newTestPlanner(t)

	record := modifyRecord(t, alice, directory.ModReplace, "mail", "new@alder.test")
	p, err := pl.Compute(t.Context(), d, testSchema(t), []directory.ChangeRecord{record}, Options{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	good := p.Items[0].Baseline

	// Empty the list, keeping the MAC: an attacker asking to be checked against
	// nothing at all.
	_, mac, ok := strings.Cut(string(good), ".")
	if !ok {
		t.Fatalf("a baseline has no separator: %q", good)
	}
	narrowed := Baseline("." + mac)

	d.put(t, alice, []string{"mail", "someone-else@alder.test"})
	if err := pl.Verify(t.Context(), d, record, narrowed); !IsStale(err) {
		t.Error("a baseline with its dependency list emptied was accepted")
	}
	// And a malformed one is not a free pass either.
	for _, bad := range []Baseline{"", "nonsense", "no-separator", "..", "!!!.!!!"} {
		if err := pl.Verify(t.Context(), d, record, bad); !IsStale(err) {
			t.Errorf("the malformed baseline %q was accepted: %v", bad, err)
		}
	}
}
