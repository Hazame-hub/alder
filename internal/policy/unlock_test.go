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

	// OpenLDAP's ppolicy. The failure times go with the lock: the overlay
	// re-locks at pwdMaxFailure, so leaving them means the next single
	// failure locks it again, which nobody expects after an unlock.
	entry, state := lockedEntry(t, "pwdAccountLockedTime", "000001010000Z", "pwdFailureTime", "20260927080000Z")
	unlock := unlockFor(target, entry, state)
	if unlock == nil {
		t.Fatal("an account locked by ppolicy was offered no way out")
	}
	if len(unlock.Record.Mods) != 2 {
		t.Fatalf("the change is %+v, want the lock and the failure count", unlock.Record.Mods)
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
