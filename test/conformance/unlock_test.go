//go:build conformance

package conformance

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// 1.29: clearing a lock, against both servers.
//
// A black-box audit walked "this account cannot log in, why", found the answer
// in two clicks and then spent seventeen more applying it -- because the entry
// viewer filed the lock attribute under "operational, yours to set", no editor
// offered it, and the only way to clear it was hand-written LDIF in the import
// screen.
//
// The two servers lock an account in different attributes with different
// meanings, so the change is derived from whichever one the entry actually
// carries. This locks a real account on each server, reads the report, applies
// the change the report itself offered, and checks the server let go.

func policyOf(t *testing.T, client *http.Client, base, target string) api.PolicyReport {
	t.Helper()
	res := get(t, client, base+"/policy?dn="+url.QueryEscape(target))
	if res.status != http.StatusOK {
		t.Fatalf("GET /policy: %d\n%s", res.status, res.body)
	}
	var report api.PolicyReport
	if err := json.Unmarshal([]byte(res.body), &report); err != nil {
		t.Fatalf("decoding the policy report: %v\n%s", err, res.body)
	}
	return report
}

func TestALockedAccountCanBeUnlockedFromTheReportThatFoundIt(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alderSession(t, s, false)
		target := mustDN(t, "uid=user0105,ou=people,"+suffix)

		// Whatever this server locks with. The suite already names it per
		// server, because the two do not agree and never will.
		t.Cleanup(func() {
			_ = sess.Apply(ctx(t), directory.ChangeRecord{
				DN: target, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModDelete, Name: s.lockAttr}},
			})
		})
		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN: target, Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: s.lockAttr,
				Values: [][]byte{[]byte(s.lockValue)}}},
		}); err != nil {
			t.Fatalf("%s: locking the account: %v", s.name, err)
		}

		// The report says it is locked, and offers the way out.
		locked := policyOf(t, client, base, target.String())
		if !locked.State.Locked {
			t.Fatalf("%s: the account was locked and the report does not say so: %s",
				s.name, mustEncode(t, locked.State))
		}
		if locked.Unlock == nil {
			t.Fatalf("%s: the report names the lock and offers no way to clear it: %s",
				s.name, mustEncode(t, locked))
		}
		if len(locked.Unlock.Attributes) == 0 || locked.Unlock.Why == "" {
			t.Errorf("%s: an unlock that does not say what it removes: %s", s.name, mustEncode(t, locked.Unlock))
		}
		// It must name the attribute this server actually used, spelled the
		// way the server spelled it -- a change is sent, not guessed at.
		named := false
		for _, a := range locked.Unlock.Attributes {
			if strings.EqualFold(a, s.lockAttr) {
				named = true
			}
		}
		if !named {
			t.Errorf("%s: the unlock removes %v and the lock is in %s",
				s.name, locked.Unlock.Attributes, s.lockAttr)
		}
		if locked.Unlock.Change.Type != api.ChangeRequestTypeModify {
			t.Errorf("%s: the unlock is a %s, not a modify", s.name, locked.Unlock.Change.Type)
		}

		// And it works, through the ordinary plan and apply -- the same path
		// every other write in Alder takes.
		planAndApplyChanges(t, client, base, []api.ChangeRequest{locked.Unlock.Change})

		after := policyOf(t, client, base, target.String())
		if after.State.Locked {
			t.Fatalf("%s: the change the report offered was applied and the account is still locked: %s",
				s.name, mustEncode(t, after.State))
		}
		if after.Unlock != nil {
			t.Errorf("%s: an unlocked account is still offered an unlock", s.name)
		}
		// Read from the server rather than from Alder's own answer.
		entry, err := sess.Read(ctx(t), target, []string{s.lockAttr})
		if err != nil {
			t.Fatalf("%s: reading the entry back: %v", s.name, err)
		}
		if len(entry.GetStrings(s.lockAttr)) != 0 {
			t.Errorf("%s: %s is still on the entry: %v", s.name, s.lockAttr, entry.GetStrings(s.lockAttr))
		}
		t.Logf("%s: locked with %s, cleared by the report's own change (%v)",
			s.name, s.lockAttr, locked.Unlock.Attributes)
	})
}

func TestAnUnlockedAccountIsOfferedNoUnlock(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		client, base := alderSession(t, s, false)
		report := policyOf(t, client, base, "uid=user0106,ou=people,"+suffix)
		if report.State.Locked {
			t.Skipf("%s: uid=user0106 is locked, which this case assumes it is not", s.name)
		}
		if report.Unlock != nil {
			t.Errorf("%s: an account that is not locked was offered a way to unlock it: %s",
				s.name, mustEncode(t, report.Unlock))
		}
	})
}

// TestALockTheServerAppliedItselfIsCleared.
//
// The case above fabricates the lock by writing the attribute, which is a
// state neither overlay ever produces on its own -- and that hid a defect
// that made the whole feature useless on OpenLDAP.
//
// A real ppolicy lock carries pwdFailureTime beside pwdAccountLockedTime,
// because counting the failures is how the overlay decided to lock. The first
// version of the unlock deleted both. pwdFailureTime is NO-USER-MODIFICATION:
// the server refused the modify with a constraint violation, and a modify is
// atomic, so the lock was not cleared either. Every account the overlay had
// locked -- which is every account locked in production -- could not be
// unlocked, while this suite stayed green.
//
// So this one makes the server do the locking.
func TestALockTheServerAppliedItselfIsCleared(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		if s.name != "openldap" {
			t.Skip("this is the ppolicy overlay's own lock; 389 DS's is the case below")
		}
		client, base := alderSession(t, s, false)
		// The one entry the harness holds to cn=strict, pwdMaxFailure 3.
		target := "uid=user0004,ou=people," + suffix

		t.Cleanup(func() {
			// pwdFailureTime cannot be deleted -- that is the whole point of
			// this case -- so the cleanup clears the lock and lets the
			// overlay discard the rest.
			_ = sess.Apply(ctx(t), directory.ChangeRecord{
				DN: mustDN(t, target), Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModDelete, Name: "pwdAccountLockedTime"}},
			})
		})

		// Let the server lock it, the way it really happens.
		for range 5 {
			_ = bindAs(t, s, target, "not-the-password")
		}
		locked := policyOf(t, client, base, target)
		if !locked.State.Locked {
			t.Skipf("%s: the overlay did not lock the account after five bad binds", s.name)
		}
		if locked.Unlock == nil {
			t.Fatalf("%s: locked by the overlay and offered no way out: %s", s.name, mustEncode(t, locked))
		}

		// The change must not name an attribute the directory owns. This is
		// the assertion the first version failed: pwdFailureTime is there on
		// the entry, and putting it in the modify makes the server refuse
		// the whole thing.
		for _, a := range locked.Unlock.Attributes {
			if strings.EqualFold(a, "pwdFailureTime") {
				t.Errorf("%s: the unlock names pwdFailureTime, which is NO-USER-MODIFICATION; "+
					"the server refuses the whole modify and the lock survives", s.name)
			}
		}

		planAndApplyChanges(t, client, base, []api.ChangeRequest{locked.Unlock.Change})

		after := policyOf(t, client, base, target)
		if after.State.Locked {
			t.Fatalf("%s: applied the offered change and the account is still locked: %s",
				s.name, mustEncode(t, after.State))
		}
		// And the account can bind again, which is the only thing the
		// operator actually wanted.
		if err := bindAs(t, s, target, "password-user0004"); err != nil {
			t.Logf("%s: the lock is gone; the bind still fails for another reason: %v", s.name, err)
		}
		t.Logf("%s: the overlay locked it, the report's own change cleared it (%v)",
			s.name, locked.Unlock.Attributes)
	})
}

// TestAnAutomaticLockoutIsReportedAndCleared.
//
// 389 DS's commonest lock, and the one the report used to miss entirely: five
// bad binds and the server sets accountUnlockTime to when it will release
// itself. Until then a bind is refused, which is what anybody means by
// locked -- but the state reader only looked at nsAccountLock, so the screen
// said the account was not locked and offered nothing. The unlock table had
// an entry for it that could never be reached.
func TestAnAutomaticLockoutIsReportedAndCleared(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		if s.name == "openldap" {
			t.Skip("an automatic lockout with a release time is 389 DS's")
		}
		client, base := alderSession(t, s, false)
		target := "uid=user0107,ou=people," + suffix

		t.Cleanup(func() {
			for _, attr := range []string{"accountUnlockTime", "passwordRetryCount", "retryCountResetTime"} {
				_ = sess.Apply(ctx(t), directory.ChangeRecord{
					DN: mustDN(t, target), Type: directory.ChangeModify,
					Mods: []directory.Mod{{Op: directory.ModDelete, Name: attr}},
				})
			}
		})

		for range 6 {
			_ = bindAs(t, s, target, "not-the-password")
		}
		locked := policyOf(t, client, base, target)
		if !locked.State.Locked {
			t.Fatalf("%s: six bad binds and the report says the account is not locked: %s",
				s.name, mustEncode(t, locked.State))
		}
		if locked.State.LockedDetail == nil || !strings.Contains(*locked.State.LockedDetail, "failed binds") {
			t.Errorf("%s: locked by failed binds and the report does not say so: %v",
				s.name, locked.State.LockedDetail)
		}
		if locked.Unlock == nil {
			t.Fatalf("%s: locked out and offered no way back: %s", s.name, mustEncode(t, locked))
		}

		planAndApplyChanges(t, client, base, []api.ChangeRequest{locked.Unlock.Change})

		after := policyOf(t, client, base, target)
		if after.State.Locked {
			t.Fatalf("%s: the lockout survived the change the report offered: %s",
				s.name, mustEncode(t, after.State))
		}
		t.Logf("%s: locked out by failed binds, cleared by %v", s.name, locked.Unlock.Attributes)
	})
}
