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
