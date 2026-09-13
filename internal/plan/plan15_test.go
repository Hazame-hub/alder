package plan

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// The 1.5 behaviour: intent, typed problems, schema validation, membership,
// subtrees, and the operation half of the token.

func person(t *testing.T, d *fakeDirectory, target string, pairs ...[]string) {
	t.Helper()
	all := append([][]string{{"objectClass", "top", "person", "inetOrgPerson"}}, pairs...)
	d.put(t, target, all...)
}

func personAdd(t *testing.T, target string, pairs ...[]string) directory.ChangeRecord {
	t.Helper()
	all := append([][]string{{"objectClass", "top", "person", "inetOrgPerson"}}, pairs...)
	return addRecord(t, target, all...)
}

func only(t *testing.T, p Plan) Item {
	t.Helper()
	if len(p.Items) != 1 {
		t.Fatalf("%d items, want 1", len(p.Items))
	}
	return p.Items[0]
}

// --- intent ------------------------------------------------------------------

// An explicit add of an entry that exists is a conflict, whatever else is true.
// Rewriting it into a modification is the reinterpretation 1.5 exists to stop.
func TestAnExactAddOfAnExistingEntryIsNeverReconciled(t *testing.T) {
	d := &fakeDirectory{}
	person(t, d, alice, []string{"cn", "Alice"}, []string{"sn", "L"})

	record := personAdd(t, alice, []string{"cn", "Alice Liddell"}, []string{"sn", "L"})
	p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, testSchema(t),
		[]Proposal{{Record: record, Intent: IntentExact}}, Options{})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	item := only(t, p)
	if item.Action != ActionConflict {
		t.Fatalf("action = %q, want conflict", item.Action)
	}
	if item.Problem == nil || item.Problem.Code != ProblemEntryExists {
		t.Errorf("problem = %+v, want entry_exists", item.Problem)
	}
	if len(p.Applicable()) != 0 {
		t.Error("a conflicting exact add was handed on to apply")
	}
}

func TestADesiredStateRecordIsReconciled(t *testing.T) {
	d := &fakeDirectory{}
	person(t, d, alice, []string{"cn", "Alice"}, []string{"sn", "L"})

	record := personAdd(t, alice, []string{"cn", "Alice Liddell"}, []string{"sn", "L"})
	p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, testSchema(t),
		[]Proposal{{Record: record, Intent: IntentDesired}}, Options{})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	item := only(t, p)
	if item.Action != ActionModify || item.Intent != IntentDesired {
		t.Fatalf("action = %q intent = %v, want a desired-state modify", item.Action, item.Intent)
	}
	if len(item.Record.Mods) != 1 || !strings.EqualFold(item.Record.Mods[0].Name, "cn") {
		t.Errorf("reconciled mods = %+v, want only cn", item.Record.Mods)
	}
}

// A desired-state set that already matches is a successful plan of nothing.
func TestANoOpPlanIsASuccessWithNothingToApply(t *testing.T) {
	d := &fakeDirectory{}
	var proposals []Proposal
	for _, uid := range []string{"a", "b", "c"} {
		target := "uid=" + uid + ",ou=people,dc=alder,dc=test"
		person(t, d, target, []string{"cn", uid}, []string{"sn", uid})
		proposals = append(proposals, Proposal{
			Record: personAdd(t, target, []string{"cn", uid}, []string{"sn", uid}),
			Intent: IntentDesired,
		})
	}
	p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, testSchema(t), proposals, Options{})
	if err != nil {
		t.Fatalf("a no-op plan returned an error: %v", err)
	}
	if p.Counts.Unchanged != 3 || p.Counts.Examined != 3 {
		t.Errorf("counts = %+v, want three unchanged", p.Counts)
	}
	if n := len(p.Applicable()); n != 0 {
		t.Errorf("%d changes would be applied for a plan of nothing", n)
	}
	for _, item := range p.Items {
		if item.Baseline != "" {
			t.Errorf("an unchanged item carries a baseline: %s", item.DN)
		}
	}
}

// --- typed problems -----------------------------------------------------------

func TestProblemsAreTyped(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, d *fakeDirectory)
		record func(t *testing.T) directory.ChangeRecord
		action Action
		code   ProblemCode
		attr   string
	}{
		{
			name: "modify of a missing entry",
			record: func(t *testing.T) directory.ChangeRecord {
				return modifyRecord(t, alice, directory.ModReplace, "sn", "x")
			},
			action: ActionConflict, code: ProblemEntryMissing,
		},
		{
			name: "rename onto a taken name",
			setup: func(t *testing.T, d *fakeDirectory) {
				person(t, d, alice, []string{"cn", "a"}, []string{"sn", "a"})
				person(t, d, "uid=bob,ou=people,dc=alder,dc=test", []string{"cn", "b"}, []string{"sn", "b"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{DN: mustParse(t, alice), Type: directory.ChangeModRDN,
					NewRDN: "uid=bob", DeleteOldRDN: true}
			},
			action: ActionConflict, code: ProblemRenameTargetExists,
		},
		{
			name: "an attribute the schema does not define",
			setup: func(t *testing.T, d *fakeDirectory) {
				person(t, d, alice, []string{"cn", "a"}, []string{"sn", "a"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return modifyRecord(t, alice, directory.ModReplace, "favouriteColour", "teal")
			},
			action: ActionInvalid, code: ProblemAttributeUndefined, attr: "favouriteColour",
		},
		{
			name: "an attribute no class on the entry permits",
			setup: func(t *testing.T, d *fakeDirectory) {
				d.put(t, "ou=people,dc=alder,dc=test",
					[]string{"objectClass", "top", "organizationalUnit"}, []string{"ou", "people"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return modifyRecord(t, "ou=people,dc=alder,dc=test", directory.ModReplace, "mail", "x@alder.test")
			},
			action: ActionInvalid, code: ProblemAttributeNotPermitted, attr: "mail",
		},
		{
			name: "a second value for a single-valued attribute",
			setup: func(t *testing.T, d *fakeDirectory) {
				person(t, d, alice, []string{"cn", "a"}, []string{"sn", "a"})
			},
			record: func(t *testing.T) directory.ChangeRecord {
				return modifyRecord(t, alice, directory.ModAdd, "entryUUID", "1", "2")
			},
			// entryUUID is operational, so it is exempt from the class rule and
			// caught by the single-value one.
			action: ActionInvalid, code: ProblemSingleValue, attr: "entryUUID",
		},
		{
			name: "an add missing a required attribute",
			record: func(t *testing.T) directory.ChangeRecord {
				return personAdd(t, alice, []string{"cn", "Alice"})
			},
			action: ActionInvalid, code: ProblemMissingRequired, attr: "sn",
		},
		{
			name: "an undefined object class",
			record: func(t *testing.T) directory.ChangeRecord {
				return addRecord(t, alice, []string{"objectClass", "top", "wizard"},
					[]string{"cn", "a"})
			},
			action: ActionInvalid, code: ProblemObjectClassUndefined, attr: "wizard",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDirectory{}
			if tc.setup != nil {
				tc.setup(t, d)
			}
			p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, testSchema(t),
				Exact(tc.record(t)), Options{})
			if err != nil {
				t.Fatalf("planning: %v", err)
			}
			item := only(t, p)
			if item.Action != tc.action {
				t.Fatalf("action = %q, want %q (reason: %s)", item.Action, tc.action, item.Reason)
			}
			if item.Problem == nil || item.Problem.Code != tc.code {
				t.Fatalf("problem = %+v, want %s", item.Problem, tc.code)
			}
			if tc.attr != "" && !strings.EqualFold(item.Problem.Attribute, tc.attr) {
				t.Errorf("problem attribute = %q, want %q", item.Problem.Attribute, tc.attr)
			}
			if len(p.Applicable()) != 0 || item.Baseline != "" {
				t.Error("a refused change is still applicable")
			}
		})
	}
}

// The RDN attribute is present by virtue of the name, and a delegated bind that
// cannot read objectClass must not see every change as invalid.
func TestValidationDoesNotRefuseWhatItCannotJudge(t *testing.T) {
	t.Run("the RDN attribute counts as present", func(t *testing.T) {
		d := &fakeDirectory{}
		record := addRecord(t, "cn=Alice,ou=people,dc=alder,dc=test",
			[]string{"objectClass", "top", "person"}, []string{"sn", "L"})
		p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, testSchema(t), Exact(record), Options{})
		if err != nil {
			t.Fatalf("planning: %v", err)
		}
		if item := only(t, p); item.Action != ActionAdd {
			t.Errorf("action = %q (%s), want add", item.Action, item.Reason)
		}
	})
	t.Run("no object classes visible", func(t *testing.T) {
		d := &fakeDirectory{}
		d.put(t, alice, []string{"mail", "old@alder.test"})
		p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, testSchema(t),
			Exact(modifyRecord(t, alice, directory.ModReplace, "mail", "new@alder.test")), Options{})
		if err != nil {
			t.Fatalf("planning: %v", err)
		}
		if item := only(t, p); item.Action != ActionModify {
			t.Errorf("action = %q (%s), want modify", item.Action, item.Reason)
		}
	})
}

// --- membership ----------------------------------------------------------------

const platform = "cn=platform,ou=groups,dc=alder,dc=test"

func group(t *testing.T, d *fakeDirectory, members ...string) {
	t.Helper()
	d.put(t, platform, []string{"objectClass", "top", "groupOfNames"},
		append([]string{"member"}, members...), []string{"cn", "platform"})
}

func membershipOf(t *testing.T, item Item) MembershipChange {
	t.Helper()
	if len(item.Membership) != 1 {
		t.Fatalf("membership = %+v, want one attribute", item.Membership)
	}
	return item.Membership[0]
}

func TestMembershipGainedAndRemoved(t *testing.T) {
	const a = "uid=alice,ou=people,dc=alder,dc=test"
	const b = "uid=bob,ou=people,dc=alder,dc=test"
	const c = "uid=carol,ou=people,dc=alder,dc=test"
	opts := Options{MembershipAttributes: []string{"member", "uniqueMember", "memberUid"}}

	t.Run("an add of a member", func(t *testing.T) {
		d := &fakeDirectory{}
		group(t, d, a)
		p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, testSchemaWithGroups(t),
			Exact(modifyRecord(t, platform, directory.ModAdd, "member", b)), opts)
		if err != nil {
			t.Fatalf("planning: %v", err)
		}
		m := membershipOf(t, only(t, p))
		if len(m.Gained) != 1 || m.Gained[0] != b || len(m.Removed) != 0 {
			t.Errorf("membership = %+v, want bob gained", m)
		}
	})
	t.Run("a replace that swaps members", func(t *testing.T) {
		d := &fakeDirectory{}
		group(t, d, a, b)
		p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, testSchemaWithGroups(t),
			Exact(modifyRecord(t, platform, directory.ModReplace, "member", b, c)), opts)
		if err != nil {
			t.Fatalf("planning: %v", err)
		}
		m := membershipOf(t, only(t, p))
		if len(m.Gained) != 1 || m.Gained[0] != c || len(m.Removed) != 1 || m.Removed[0] != a {
			t.Errorf("membership = %+v, want carol gained and alice removed", m)
		}
	})
	t.Run("a member spelled differently is the same member", func(t *testing.T) {
		d := &fakeDirectory{}
		group(t, d, a)
		upper := "UID=ALICE,OU=People,DC=alder,DC=test"
		p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, testSchemaWithGroups(t),
			Exact(modifyRecord(t, platform, directory.ModReplace, "member", upper, b)), opts)
		if err != nil {
			t.Fatalf("planning: %v", err)
		}
		m := membershipOf(t, only(t, p))
		if len(m.Removed) != 0 || len(m.Gained) != 1 || m.Gained[0] != b {
			t.Errorf("membership = %+v, want only bob gained", m)
		}
	})
	t.Run("deleting a group removes every membership it held", func(t *testing.T) {
		d := &fakeDirectory{}
		group(t, d, a, b)
		p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, testSchemaWithGroups(t),
			Exact(directory.ChangeRecord{DN: mustParse(t, platform), Type: directory.ChangeDelete}), opts)
		if err != nil {
			t.Fatalf("planning: %v", err)
		}
		m := membershipOf(t, only(t, p))
		if len(m.Removed) != 2 || len(m.Gained) != 0 {
			t.Errorf("membership = %+v, want both removed", m)
		}
	})
}

// --- subtrees -------------------------------------------------------------------

func TestADeletedBranchIsReportedAsOne(t *testing.T) {
	d := &fakeDirectory{}
	ou := "ou=contractors,dc=alder,dc=test"
	d.put(t, ou, []string{"objectClass", "top", "organizationalUnit"}, []string{"ou", "contractors"})
	var records []directory.ChangeRecord
	for _, uid := range []string{"x", "y", "z"} {
		target := "uid=" + uid + "," + ou
		person(t, d, target, []string{"cn", uid}, []string{"sn", uid})
		records = append(records, directory.ChangeRecord{DN: mustParse(t, target), Type: directory.ChangeDelete})
	}
	// The container last, deepest first, as the subtree stager orders it.
	records = append(records, directory.ChangeRecord{DN: mustParse(t, ou), Type: directory.ChangeDelete})
	// And one unrelated leaf, which is not a subtree.
	person(t, d, alice, []string{"cn", "a"}, []string{"sn", "a"})
	records = append(records, directory.ChangeRecord{DN: mustParse(t, alice), Type: directory.ChangeDelete})

	p, err := newTestPlanner(t).ComputeProposals(t.Context(), d, testSchema(t), Exact(records...), Options{})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	if len(p.Subtrees) != 1 {
		t.Fatalf("subtrees = %+v, want one", p.Subtrees)
	}
	if got := p.Subtrees[0]; !strings.EqualFold(got.Root.String(), ou) || got.Entries != 4 {
		t.Errorf("subtree = %+v, want %s with 4 entries", got, ou)
	}
}

// --- the operation half of the token ---------------------------------------------

func TestVerifyTellsAMismatchFromAStalePlan(t *testing.T) {
	d := &fakeDirectory{}
	person(t, d, alice, []string{"cn", "a"}, []string{"sn", "a"}, []string{"mail", "old@alder.test"})
	pl := newTestPlanner(t)

	planned := modifyRecord(t, alice, directory.ModReplace, "mail", "new@alder.test")
	p, err := pl.Compute(t.Context(), d, testSchema(t), []directory.ChangeRecord{planned}, Options{})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	token := only(t, p).Baseline

	// The same entry, the same attribute, a different value: a baseline lifted
	// from one plan and attached to a change nobody planned.
	substituted := modifyRecord(t, alice, directory.ModReplace, "mail", "attacker@alder.test")
	if err := pl.Verify(t.Context(), d, substituted, token); !IsMismatch(err) {
		t.Errorf("a substituted operation verified as %v, want a mismatch", err)
	}
	// The planned operation still verifies.
	if err := pl.Verify(t.Context(), d, planned, token); err != nil {
		t.Errorf("the planned operation did not verify: %v", err)
	}
	// And once the directory moves, the planned operation is stale, not a mismatch.
	person(t, d, alice, []string{"cn", "a"}, []string{"sn", "a"}, []string{"mail", "moved@alder.test"})
	if err := pl.Verify(t.Context(), d, planned, token); !IsStale(err) {
		t.Errorf("after the directory moved, Verify returned %v, want stale", err)
	}
}

// The reconciled case is the one where the operation and the proposal's
// attributes differ, and where verification has to read what the token names.
type attributeHonouringDirectory struct{ *fakeDirectory }

func (a attributeHonouringDirectory) Read(ctx contextT, target dnT, attrs []string) (*directory.Entry, error) {
	full, err := a.fakeDirectory.Read(ctx, target, attrs)
	if err != nil || full == nil {
		return full, err
	}
	// Return only what was asked for, the way a real directory does.
	out := directory.NewEntry(full.DN)
	for _, name := range attrs {
		if values := full.Get(name); len(values) > 0 {
			out.Set(name, values)
		}
	}
	return out, nil
}

func TestAReconciledPlanVerifiesAgainstADirectoryThatReturnsOnlyWhatItIsAskedFor(t *testing.T) {
	base := &fakeDirectory{}
	person(t, base, alice, []string{"cn", "Alice"}, []string{"sn", "L"}, []string{"mail", "old@alder.test"})
	d := attributeHonouringDirectory{base}
	pl := newTestPlanner(t)

	record := personAdd(t, alice, []string{"cn", "Alice"}, []string{"sn", "L"},
		[]string{"mail", "new@alder.test"})
	p, err := pl.ComputeProposals(t.Context(), d, testSchema(t),
		[]Proposal{{Record: record, Intent: IntentDesired}}, Options{})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	item := only(t, p)
	if item.Action != ActionModify {
		t.Fatalf("action = %q, want modify", item.Action)
	}
	if err := pl.Verify(t.Context(), d, item.Record, item.Baseline); err != nil {
		t.Fatalf("a reconciled plan did not verify against an unchanged directory: %v", err)
	}
	// cn is not in the reconciled operation, but it decided the plan, so a
	// change to it must still make the plan stale.
	person(t, base, alice, []string{"cn", "Someone Else"}, []string{"sn", "L"}, []string{"mail", "old@alder.test"})
	if err := pl.Verify(t.Context(), d, item.Record, item.Baseline); !IsStale(err) {
		t.Errorf("a change to an attribute the plan depended on verified as %v, want stale", err)
	}
}

// A password change binds the password without carrying it.
//
// The token holds a keyed MAC of the new password under a key derived from the
// process's fingerprint key and the session, so: no plaintext, no digest anyone
// can recompute, a different password of the same length is a mismatch, and the
// same password planned in another session does not reproduce the token.
func TestAPasswordChangeTokenBindsThePasswordWithoutCarryingIt(t *testing.T) {
	d := &fakeDirectory{}
	person(t, d, alice, []string{"cn", "a"}, []string{"sn", "a"})
	pl := newTestPlanner(t)
	const secret = "correct-horse-battery-staple"

	record := directory.ChangeRecord{DN: mustParse(t, alice), Type: directory.ChangeSetPassword,
		NewPassword: secret}
	session := pl.ForSession([]byte("session-a"))
	p, err := session.ComputeProposals(t.Context(), d, testSchema(t), Exact(record), Options{})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	item := only(t, p)
	token := string(item.Baseline)

	// Nothing recoverable: not the password, and not an unkeyed digest of it in
	// any of the encodings a token could plausibly carry.
	sum := sha256.Sum256([]byte(secret))
	for _, leak := range []string{secret, hex.EncodeToString(sum[:]),
		base64.RawURLEncoding.EncodeToString(sum[:]), base64.StdEncoding.EncodeToString(sum[:])} {
		if strings.Contains(token, leak) {
			t.Fatalf("the token carries %q", leak)
		}
	}

	if err := session.Verify(t.Context(), d, record, item.Baseline); err != nil {
		t.Errorf("the planned password did not verify: %v", err)
	}

	other := record
	other.NewPassword = "correct-horse-battery-stapl3"
	if len(other.NewPassword) != len(secret) {
		t.Fatal("the substitute must have the same length, or this proves nothing")
	}
	if err := session.Verify(t.Context(), d, other, item.Baseline); !IsMismatch(err) {
		t.Errorf("a different password of the same length verified as %v, want a mismatch", err)
	}

	// The same password, verified from another session, is not the planned
	// operation: a token is not a guessing oracle outside the session that
	// typed the value.
	if err := pl.ForSession([]byte("session-b")).Verify(t.Context(), d, record, item.Baseline); !IsMismatch(err) {
		t.Errorf("the token verified in another session as %v, want a mismatch", err)
	}
}

// A sensitive attribute written by a modify is bound the same way: a different
// value of the same length is not the planned change.
func TestASensitiveValueIsBoundWithoutBeingCarried(t *testing.T) {
	d := &fakeDirectory{}
	person(t, d, alice, []string{"cn", "a"}, []string{"sn", "a"}, []string{"userPassword", "{SSHA}old"})
	pl := newTestPlanner(t).ForSession([]byte("session-a"))

	planned := directory.ChangeRecord{DN: mustParse(t, alice), Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModReplace, Name: "userPassword", Values: [][]byte{[]byte("{SSHA}AAAAAAAA")}}}}
	p, err := pl.ComputeProposals(t.Context(), d, testSchema(t), Exact(planned), Options{})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	item := only(t, p)
	if strings.Contains(string(item.Baseline), "AAAAAAAA") {
		t.Fatal("the token carries the value")
	}
	if err := pl.Verify(t.Context(), d, planned, item.Baseline); err != nil {
		t.Errorf("the planned value did not verify: %v", err)
	}
	substituted := planned
	substituted.Mods = []directory.Mod{{Op: directory.ModReplace, Name: "userPassword", Values: [][]byte{[]byte("{SSHA}BBBBBBBB")}}}
	if err := pl.Verify(t.Context(), d, substituted, item.Baseline); !IsMismatch(err) {
		t.Errorf("a substituted value of the same length verified as %v, want a mismatch", err)
	}
}
