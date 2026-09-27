package policy

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// Which change clears which lock, without a directory.
//
// The two servers lock an account in different attributes with different
// meanings, and the wrong change is worse than none: clearing 389 DS's switch
// on an OpenLDAP entry does nothing and reports success.

func lockedEntry(t *testing.T, attrs ...string) (*directory.Entry, *State) {
	t.Helper()
	target := dn.MustParse("uid=a,ou=people,dc=alder,dc=test")
	entry := directory.NewEntry(target)
	state := &State{Locked: true}
	for i := 0; i+1 < len(attrs); i += 2 {
		name, value := attrs[i], attrs[i+1]
		entry.Set(name, [][]byte{[]byte(value)})
		state.Attributes = append(state.Attributes, Setting{Key: strings.ToLower(name), Values: []string{value}})
	}
	return entry, state
}

func TestTheUnlockClearsWhatThisServerLockedWith(t *testing.T) {
	target := dn.MustParse("uid=a,ou=people,dc=alder,dc=test")

	// OpenLDAP's ppolicy, with the failure times a real lock always carries.
	// The change must name the lock attribute and NOTHING ELSE:
	// pwdFailureTime is NO-USER-MODIFICATION, the server refuses a modify
	// that names it, and a modify is atomic -- so including it meant no
	// ppolicy-locked account could be unlocked at all. The overlay discards
	// the failure times itself when the lock goes.
	entry, state := lockedEntry(t, "pwdAccountLockedTime", "000001010000Z", "pwdFailureTime", "20260927080000Z")
	unlock := unlockFor(target, entry, state)
	if unlock == nil {
		t.Fatal("an account locked by ppolicy was offered no way out")
	}
	if len(unlock.Record.Mods) != 1 {
		t.Fatalf("the change is %+v, want the lock attribute alone", unlock.Record.Mods)
	}
	for _, mod := range unlock.Record.Mods {
		if strings.EqualFold(mod.Name, "pwdFailureTime") {
			t.Error("the change names pwdFailureTime, which the directory owns and will refuse")
		}
	}
	for _, mod := range unlock.Record.Mods {
		if mod.Op != directory.ModDelete {
			t.Errorf("%s is removed with %v, want a delete", mod.Name, mod.Op)
		}
		if len(mod.Values) != 0 {
			t.Errorf("%s names values; a delete of the whole attribute names none", mod.Name)
		}
	}
	// Spelled the way the server spelled it, so the preview does not rename
	// the attribute under the reader.
	if unlock.Attributes[0] != "pwdAccountLockedTime" {
		t.Errorf("the preview calls it %q", unlock.Attributes[0])
	}

	// 389 DS's switch, which stands alone.
	entry, state = lockedEntry(t, "nsAccountLock", "true")
	unlock = unlockFor(target, entry, state)
	if unlock == nil || len(unlock.Record.Mods) != 1 {
		t.Fatalf("an account locked by nsAccountLock: %+v", unlock)
	}
	if !strings.EqualFold(unlock.Record.Mods[0].Name, "nsAccountLock") {
		t.Errorf("the change removes %q", unlock.Record.Mods[0].Name)
	}
}

func TestNoUnlockIsInventedForAnAccountThatIsNotLocked(t *testing.T) {
	target := dn.MustParse("uid=a,ou=people,dc=alder,dc=test")
	entry, state := lockedEntry(t, "pwdChangedTime", "20260927080000Z")
	state.Locked = false
	if unlock := unlockFor(target, entry, state); unlock != nil {
		t.Errorf("an unlocked account was offered %+v", unlock)
	}
	if unlock := unlockFor(target, entry, nil); unlock != nil {
		t.Errorf("an entry with no state was offered %+v", unlock)
	}
}

func TestALockAlderDoesNotRecogniseGetsNoChange(t *testing.T) {
	// A server that locks an account some other way gets no invented change.
	// Offering one would be offering a write nobody asked for, against an
	// attribute Alder has never seen, on the screen whose whole point is that
	// it reports rather than decides.
	target := dn.MustParse("uid=a,ou=people,dc=alder,dc=test")
	entry, state := lockedEntry(t, "someVendorLockFlag", "yes")
	if unlock := unlockFor(target, entry, state); unlock != nil {
		t.Errorf("a lock Alder does not know was offered %+v", unlock)
	}
}

func TestOnlyTheAttributesTheEntryActuallyHoldsAreRemoved(t *testing.T) {
	// pwdFailureTime is cleared with the lock where it is there, and not
	// named where it is not: a delete of an absent attribute is an error on
	// both servers, so naming it would turn a working unlock into a refusal.
	target := dn.MustParse("uid=a,ou=people,dc=alder,dc=test")
	entry, state := lockedEntry(t, "pwdAccountLockedTime", "000001010000Z")
	unlock := unlockFor(target, entry, state)
	if unlock == nil || len(unlock.Record.Mods) != 1 {
		t.Fatalf("want one modification, got %+v", unlock)
	}
	if !strings.EqualFold(unlock.Record.Mods[0].Name, "pwdAccountLockedTime") {
		t.Errorf("removes %q", unlock.Record.Mods[0].Name)
	}
}

func TestTheAutomaticLockoutIsCleared(t *testing.T) {
	// 389 DS's own lockout, which used to be unreachable: the state reader
	// did not call it a lock, so unlockFor returned before the table entry
	// for it could match, and the commonest lock on that server was reported
	// as no lock at all.
	target := dn.MustParse("uid=a,ou=people,dc=alder,dc=test")
	entry, state := lockedEntry(t,
		"accountUnlockTime", "20260927090000Z",
		"passwordRetryCount", "3",
		"retryCountResetTime", "20260927093000Z")
	unlock := unlockFor(target, entry, state)
	if unlock == nil {
		t.Fatal("an account the server locked out was offered no way back")
	}
	if len(unlock.Record.Mods) != 3 {
		t.Fatalf("the change is %+v, want the lockout, the count and its reset", unlock.Record.Mods)
	}
	// The count goes with it, or the next single failure locks the account
	// again. Unlike OpenLDAP's, these are ordinary writable attributes.
	names := map[string]bool{}
	for _, mod := range unlock.Record.Mods {
		names[strings.ToLower(mod.Name)] = true
	}
	for _, want := range []string{"accountunlocktime", "passwordretrycount", "retrycountresettime"} {
		if !names[want] {
			t.Errorf("the change does not clear %s", want)
		}
	}
}
