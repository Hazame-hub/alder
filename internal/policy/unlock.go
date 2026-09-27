package policy

import (
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// Clearing a lock.
//
// The report already names the attribute that holds the lock -- it has to, to
// say the account is locked at all -- and until 1.29 that was where it
// stopped. The entry viewer filed the attribute under "operational, yours to
// set", no editor offered it, and the only way to clear it was to hand-write
// an LDIF modify into the import screen: ldapmodify with extra steps, for the
// commonest account-recovery operation there is. A black-box audit walked it
// and spent seventeen interactions between knowing the answer and applying it.
//
// The change is derived here rather than in the browser for the same reason
// every other change is: one code path builds what gets sent, and the LDIF the
// operator confirms is rendered by the server from that record.

// lockAttributes are the attributes that hold a lock, and what clearing each
// one means. Both servers are listed; whichever is present on the entry is
// what gets cleared.
var lockAttributes = []struct {
	key string
	// alsoClear are attributes that are part of the same lock and are
	// meaningless once it is gone.
	alsoClear []string
	why       string
}{
	{
		// OpenLDAP's ppolicy overlay, and the lock attribute alone.
		//
		// The first version of this also deleted pwdFailureTime, reasoning
		// that the overlay re-locks at pwdMaxFailure so the counted failures
		// should go too. That was wrong twice over, and a review proved both
		// against the running server. pwdFailureTime is
		// NO-USER-MODIFICATION -- the directory owns it -- so the server
		// refuses the modify with a constraint violation; and because a
		// modify is atomic, the lock was not cleared either. The button
		// failed in exactly the case it was built for, because an account
		// only carries pwdFailureTime when the overlay is what locked it.
		//
		// And it was unnecessary: deleting pwdAccountLockedTime alone
		// unlocks the account, and the overlay discards the failure times
		// itself. Verified -- a read immediately after the delete returns
		// neither attribute.
		key: "pwdaccountlockedtime",
		why: "clears the lock the password policy overlay applied; the overlay discards the failed binds with it",
	},
	{
		// 389 DS's own switch. Deleting the attribute is the unlock;
		// setting it to false would also work and leaves a value behind that
		// reads, to the next person, as though somebody meant something by
		// it.
		key: "nsaccountlock",
		why: "clears the administrative lock on the account",
	},
	{
		// 389 DS's automatic lockout: failed binds reached
		// passwordMaxFailure and the server set a time it will release
		// itself, which an administrator usually wants gone now.
		//
		// This was dead code until the state reader learned to call it a
		// lock. That was the worse half of the bug: the commonest lock on
		// 389 DS was reported as no lock at all, so the dialog said nothing
		// rather than saying it could not help.
		//
		// The retry count and its reset time go with it. Both are ordinary
		// writable attributes on 389 DS -- unlike OpenLDAP's pwdFailureTime
		// -- and leaving the count at the maximum means the next single
		// failure locks the account again.
		key:       "accountunlocktime",
		alsoClear: []string{"passwordretrycount", "retrycountresettime"},
		why:       "clears the automatic lockout and the failure count behind it",
	},
}

// Unlock is the change that would clear the lock on an entry.
type Unlock struct {
	// Attributes are the ones the change removes, in the order it removes
	// them, so a caller can say what it is about to do without reading LDIF.
	Attributes []string
	// Why is the sentence for the button.
	Why string
	// Record is the change itself. Every write in Alder is one of these
	// before it is anything else.
	Record directory.ChangeRecord
}

// unlockFor derives the change that clears whatever lock this entry carries,
// from the state already read. Nil when the entry is not locked, or when the
// lock is not one Alder knows how to clear -- which is a real answer, not a
// failure: a lock Alder invented a change for would be a change nobody asked
// for.
func unlockFor(target dn.DN, entry *directory.Entry, state *State) *Unlock {
	if state == nil || !state.Locked {
		return nil
	}
	held := map[string]bool{}
	for _, setting := range state.Attributes {
		if len(setting.Values) > 0 {
			held[strings.ToLower(setting.Key)] = true
		}
	}

	for _, lock := range lockAttributes {
		if !held[lock.key] {
			continue
		}
		// nsAccountLock is only a lock when it says true. The report already
		// decided that; this trusts it, because state.Locked is what the
		// report concluded from the same values.
		names := []string{canonicalName(entry, lock.key)}
		mods := []directory.Mod{{Op: directory.ModDelete, Name: names[0]}}
		for _, also := range lock.alsoClear {
			if !held[also] {
				continue
			}
			name := canonicalName(entry, also)
			names = append(names, name)
			mods = append(mods, directory.Mod{Op: directory.ModDelete, Name: name})
		}
		return &Unlock{
			Attributes: names,
			Why:        lock.why,
			Record:     directory.ChangeRecord{DN: target, Type: directory.ChangeModify, Mods: mods},
		}
	}
	return nil
}

// canonicalName is the attribute spelled the way the server spelled it, which
// is what the LDIF should carry: a server that returned nsAccountLock should
// not be sent nsaccountlock. Attribute names are case-insensitive on the
// wire, so this changes nothing the directory does -- it changes what the
// operator reads in the preview, and a preview that renames the attribute is
// a preview somebody has to think about.
//
// Read off the entry rather than off the report, because the report's keys
// come from Alder's own table and are lower-cased there.
func canonicalName(entry *directory.Entry, key string) string {
	if entry != nil {
		for _, name := range entry.Order {
			if strings.EqualFold(name, key) {
				return name
			}
		}
		for name := range entry.Attributes {
			if strings.EqualFold(name, key) {
				return name
			}
		}
	}
	return key
}
