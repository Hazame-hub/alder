package outcome

import (
	"errors"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// What Alder is allowed to conclude from looking.
//
// Every case here is a sentence somebody will read after a change went out
// and nothing came back. The danger is not getting a verdict wrong in the
// abstract; it is saying "not applied" about a change that landed, because
// the next thing that happens is somebody applies it again.

type fakeDirectory struct {
	entries map[string]*directory.Entry
	err     error
	reads   []string
}

func (f *fakeDirectory) Read(target string, _ []string) (*directory.Entry, error) {
	f.reads = append(f.reads, target)
	if f.err != nil {
		return nil, f.err
	}
	return f.entries[strings.ToLower(target)], nil
}

func holding(target string, attrs ...directory.Attribute) *fakeDirectory {
	parsed, err := dn.Parse(target)
	if err != nil {
		panic(err)
	}
	e := directory.NewEntry(parsed)
	for _, a := range attrs {
		e.Set(a.Name, a.Values)
	}
	return &fakeDirectory{entries: map[string]*directory.Entry{strings.ToLower(target): e}}
}

func empty() *fakeDirectory { return &fakeDirectory{entries: map[string]*directory.Entry{}} }

func attr(name string, values ...string) directory.Attribute {
	out := make([][]byte, 0, len(values))
	for _, v := range values {
		out = append(out, []byte(v))
	}
	return directory.Attribute{Name: name, Values: out}
}

func mod(op directory.ModOp, name string, values ...string) directory.Mod {
	out := make([][]byte, 0, len(values))
	for _, v := range values {
		out = append(out, []byte(v))
	}
	return directory.Mod{Op: op, Name: name, Values: out}
}

const target = "uid=alice,ou=people,dc=alder,dc=test"

func parsed(t *testing.T, s string) dn.DN {
	t.Helper()
	d, err := dn.Parse(s)
	if err != nil {
		t.Fatalf("dn %q: %v", s, err)
	}
	return d
}

func TestADeleteIsTheEasyCase(t *testing.T) {
	ch := directory.ChangeRecord{DN: parsed(t, target), Type: directory.ChangeDelete}

	if got := Determine(empty(), ch, nil); got.Verdict != Applied {
		t.Errorf("an entry that is gone means the delete landed, got %s: %s", got.Verdict, got.Reason)
	}
	live := holding(target, attr("uid", "alice"))
	if got := Determine(live, ch, nil); got.Verdict != NotApplied {
		t.Errorf("an entry still there means the delete did not land, got %s: %s", got.Verdict, got.Reason)
	}
}

func TestAnAddThatIsThereAndMatchesIsApplied(t *testing.T) {
	ch := directory.ChangeRecord{
		DN:   parsed(t, target),
		Type: directory.ChangeAdd,
		Attrs: []directory.Attribute{
			attr("objectClass", "top", "person"),
			attr("cn", "Alice"),
			attr("sn", "Fournier"),
		},
	}
	// The server adds superclasses of its own; the entry still holds every
	// class the change named, which is what matters.
	live := holding(target,
		attr("objectClass", "top", "person", "organizationalPerson"),
		attr("cn", "Alice"), attr("sn", "Fournier"))

	got := Determine(live, ch, nil)
	if got.Verdict != Applied {
		t.Errorf("got %s (%s); extra object classes are the server's doing, not a difference",
			got.Verdict, got.Reason)
	}
}

func TestAnAddMissingOneOfItsObjectClassesIsAConflict(t *testing.T) {
	// The server may add classes of its own, so extras are not a
	// difference -- but a class the change named and the entry does not
	// hold means this is not the entry the change described.
	ch := directory.ChangeRecord{
		DN: parsed(t, target), Type: directory.ChangeAdd,
		Attrs: []directory.Attribute{attr("objectClass", "top", "person", "alderTeamMember")},
	}
	live := holding(target, attr("objectClass", "top", "person"))

	got := Determine(live, ch, nil)
	if got.Verdict != Conflicted {
		t.Errorf("a missing object class: %s (%s)", got.Verdict, got.Reason)
	}
}

func TestAnAddWhoseEntryIsMissingDidNotLand(t *testing.T) {
	ch := directory.ChangeRecord{
		DN: parsed(t, target), Type: directory.ChangeAdd,
		Attrs: []directory.Attribute{attr("cn", "Alice")},
	}
	got := Determine(empty(), ch, nil)
	if got.Verdict != NotApplied {
		t.Errorf("got %s: %s", got.Verdict, got.Reason)
	}
}

func TestAnAddWhoseEntryHoldsSomethingElseIsAConflict(t *testing.T) {
	// Somebody else created this entry. Re-applying would be a change
	// reviewed against a directory that no longer exists.
	ch := directory.ChangeRecord{
		DN: parsed(t, target), Type: directory.ChangeAdd,
		Attrs: []directory.Attribute{attr("cn", "Alice"), attr("sn", "Fournier")},
	}
	live := holding(target, attr("cn", "Alice"), attr("sn", "Someone Else"))

	got := Determine(live, ch, nil)
	if got.Verdict != Conflicted {
		t.Fatalf("got %s: %s", got.Verdict, got.Reason)
	}
	if got.Attribute != "sn" {
		t.Errorf("the verdict should name the attribute that differs, got %q", got.Attribute)
	}
}

func TestAReplaceIsCheckedAgainstWhatTheEntryEndsUpHolding(t *testing.T) {
	ch := directory.ChangeRecord{
		DN: parsed(t, target), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModReplace, "title", "Lead")},
	}

	if got := Determine(holding(target, attr("title", "Lead")), ch, nil); got.Verdict != Applied {
		t.Errorf("the new value is there: %s (%s)", got.Verdict, got.Reason)
	}
	if got := Determine(holding(target, attr("title", "Engineer")), ch, nil); got.Verdict != NotApplied {
		t.Errorf("the old value is still there: %s (%s)", got.Verdict, got.Reason)
	}
}

func TestAnAddedValueNeedOnlyBePresent(t *testing.T) {
	// Adding one member to a group says nothing about the others, so the
	// check is presence, not equality.
	ch := directory.ChangeRecord{
		DN: parsed(t, "cn=platform,ou=groups,dc=alder,dc=test"), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModAdd, "member", "uid=bob,ou=people,dc=alder,dc=test")},
	}
	live := holding("cn=platform,ou=groups,dc=alder,dc=test",
		attr("member", "uid=alice,ou=people,dc=alder,dc=test", "uid=bob,ou=people,dc=alder,dc=test"))

	if got := Determine(live, ch, nil); got.Verdict != Applied {
		t.Errorf("the value is present among others: %s (%s)", got.Verdict, got.Reason)
	}

	// And the half that matters: the value is not there, so the add did not
	// happen, however many other members the group holds.
	without := holding("cn=platform,ou=groups,dc=alder,dc=test",
		attr("member", "uid=alice,ou=people,dc=alder,dc=test"))
	got := Determine(without, ch, nil)
	if got.Verdict != NotApplied {
		t.Errorf("the value is absent: %s (%s)", got.Verdict, got.Reason)
	}
	if got.Attribute != "member" {
		t.Errorf("the verdict should name the attribute, got %q", got.Attribute)
	}
}

func TestADeletedValueMustBeGone(t *testing.T) {
	base := "cn=platform,ou=groups,dc=alder,dc=test"
	ch := directory.ChangeRecord{
		DN: parsed(t, base), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModDelete, "member", "uid=bob,ou=people,dc=alder,dc=test")},
	}
	still := holding(base, attr("member", "uid=bob,ou=people,dc=alder,dc=test"))
	if got := Determine(still, ch, nil); got.Verdict != NotApplied {
		t.Errorf("the value is still there: %s (%s)", got.Verdict, got.Reason)
	}
	gone := holding(base, attr("member", "uid=alice,ou=people,dc=alder,dc=test"))
	if got := Determine(gone, ch, nil); got.Verdict != Applied {
		t.Errorf("the value is gone: %s (%s)", got.Verdict, got.Reason)
	}
}

func TestDeletingAWholeAttributeChecksTheAttributeIsEmpty(t *testing.T) {
	ch := directory.ChangeRecord{
		DN: parsed(t, target), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModDelete, "description")},
	}
	if got := Determine(holding(target, attr("description", "anything")), ch, nil); got.Verdict != NotApplied {
		t.Errorf("the attribute is still held: %s (%s)", got.Verdict, got.Reason)
	}
	if got := Determine(holding(target, attr("cn", "Alice")), ch, nil); got.Verdict != Applied {
		t.Errorf("the attribute is gone: %s (%s)", got.Verdict, got.Reason)
	}
}

func TestAPasswordCanNeverBeCheckedByLooking(t *testing.T) {
	// The whole point of the four-state verdict. Alder never holds the
	// value, so no read will ever settle this, and pretending otherwise
	// would be the most dangerous answer in the package.
	ch := directory.ChangeRecord{
		DN: parsed(t, target), Type: directory.ChangeSetPassword, NewPassword: "irrelevant",
	}
	got := Determine(holding(target, attr("cn", "Alice")), ch, nil)
	if got.Verdict != Undeterminable {
		t.Fatalf("a password change must stay unresolved, got %s: %s", got.Verdict, got.Reason)
	}
	if !strings.Contains(got.Reason, "cannot be read back") {
		t.Errorf("the reason should say why, and says: %s", got.Reason)
	}
}

func TestAModificationTouchingASecretIsUndeterminable(t *testing.T) {
	// userPassword written through an ordinary modify is the same problem
	// wearing different clothes.
	ch := directory.ChangeRecord{
		DN: parsed(t, target), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModReplace, "userPassword", "secret")},
	}
	got := Determine(holding(target, attr("cn", "Alice")), ch, nil)
	if got.Verdict != Undeterminable {
		t.Fatalf("got %s: %s", got.Verdict, got.Reason)
	}
	if got.Attribute != "userPassword" {
		t.Errorf("the reason should name the attribute, got %q", got.Attribute)
	}
}

func TestAnEntryThatCannotBeReadLeavesTheOutcomeUnresolved(t *testing.T) {
	// The failure mode that matters most: a bind that may write but not read
	// back, or a directory that is still unreachable. "Not applied" here
	// would send somebody to apply a change that may already be in place.
	broken := &fakeDirectory{err: errors.New("insufficient access rights")}
	for _, ch := range []directory.ChangeRecord{
		{DN: parsed(t, target), Type: directory.ChangeDelete},
		{DN: parsed(t, target), Type: directory.ChangeAdd, Attrs: []directory.Attribute{attr("cn", "A")}},
		{DN: parsed(t, target), Type: directory.ChangeModify,
			Mods: []directory.Mod{mod(directory.ModReplace, "title", "Lead")}},
	} {
		got := Determine(broken, ch, nil)
		if got.Verdict != Undeterminable {
			t.Errorf("%s: a read that failed cannot settle anything, got %s", ch.Type, got.Verdict)
		}
		if !strings.Contains(got.Reason, "insufficient access rights") {
			t.Errorf("the reason should carry the cause: %s", got.Reason)
		}
	}
}

func TestAModificationWithNothingCheckableSaysSo(t *testing.T) {
	ch := directory.ChangeRecord{DN: parsed(t, target), Type: directory.ChangeModify}
	got := Determine(holding(target, attr("cn", "Alice")), ch, nil)
	if got.Verdict != Undeterminable {
		t.Errorf("got %s: %s", got.Verdict, got.Reason)
	}
}

func TestAModifiedEntryThatHasVanishedIsAConflict(t *testing.T) {
	// Not "not applied": the change cannot be applied now either, and
	// offering it again would fail against a directory somebody else moved.
	ch := directory.ChangeRecord{
		DN: parsed(t, target), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModReplace, "title", "Lead")},
	}
	if got := Determine(empty(), ch, nil); got.Verdict != Conflicted {
		t.Errorf("got %s: %s", got.Verdict, got.Reason)
	}
}

func TestARenameIsJudgedByBothNames(t *testing.T) {
	from := "uid=alice,ou=people,dc=alder,dc=test"
	to := "uid=alice.f,ou=people,dc=alder,dc=test"
	ch := directory.ChangeRecord{
		DN: parsed(t, from), Type: directory.ChangeModRDN,
		NewRDN: "uid=alice.f", DeleteOldRDN: true,
	}

	moved := holding(to, attr("uid", "alice.f"))
	if got := Determine(moved, ch, nil); got.Verdict != Applied {
		t.Errorf("at the new name only: %s (%s)", got.Verdict, got.Reason)
	}

	stayed := holding(from, attr("uid", "alice"))
	if got := Determine(stayed, ch, nil); got.Verdict != NotApplied {
		t.Errorf("at the old name only: %s (%s)", got.Verdict, got.Reason)
	}

	both := holding(from, attr("uid", "alice"))
	toDN := parsed(t, to)
	both.entries[strings.ToLower(to)] = directory.NewEntry(toDN)
	if got := Determine(both, ch, nil); got.Verdict != Conflicted {
		t.Errorf("at both names, which a rename cannot produce: %s (%s)", got.Verdict, got.Reason)
	}

	if got := Determine(empty(), ch, nil); got.Verdict != Conflicted {
		t.Errorf("at neither name: %s (%s)", got.Verdict, got.Reason)
	}
}

func TestEveryVerdictCarriesAReason(t *testing.T) {
	// A verdict without a reason is a verdict nobody can check.
	cases := []directory.ChangeRecord{
		{DN: parsed(t, target), Type: directory.ChangeDelete},
		{DN: parsed(t, target), Type: directory.ChangeAdd, Attrs: []directory.Attribute{attr("cn", "A")}},
		{DN: parsed(t, target), Type: directory.ChangeModify,
			Mods: []directory.Mod{mod(directory.ModReplace, "title", "Lead")}},
		{DN: parsed(t, target), Type: directory.ChangeModRDN, NewRDN: "uid=b"},
		{DN: parsed(t, target), Type: directory.ChangeSetPassword},
		{DN: parsed(t, target), Type: "something-else"},
	}
	for _, ch := range cases {
		for _, d := range []Reader{empty(), holding(target, attr("cn", "Alice"))} {
			got := Determine(d, ch, nil)
			if strings.TrimSpace(got.Reason) == "" {
				t.Errorf("%s produced %s with no reason", ch.Type, got.Verdict)
			}
			switch got.Verdict {
			case Applied, NotApplied, Conflicted, Undeterminable:
			default:
				t.Errorf("%s produced an unknown verdict %q", ch.Type, got.Verdict)
			}
		}
	}
}

func TestNothingHereWritesAnything(t *testing.T) {
	// The Reader interface has one method and it reads. This is a statement
	// about the shape of the package rather than a behaviour, and it is
	// here because the one thing this code must never grow is the ability
	// to put the change right by itself.
	var _ interface {
		Read(string, []string) (*directory.Entry, error)
	} = (*fakeDirectory)(nil)
}
