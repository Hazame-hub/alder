//go:build conformance

package conformance

import (
	"context"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/policy"
)

// 1.21: the password policy in force on an account, and what the server
// records about it.
//
// The harness carries the same intent on both servers in two mechanisms that
// have nothing in common: a default policy everybody gets, and a stricter one
// attached to uid=user0004. OpenLDAP keeps both as entries in the data tree
// and names the default from slapd.conf; 389 DS keeps the default on cn=config
// and the per-entry one as a subentry. Alder reports each in its own words and
// translates neither.

func policyFor(t *testing.T, sess directory.Session, target string) *policy.Report {
	t.Helper()
	parsed, err := dn.Parse(target)
	if err != nil {
		t.Fatalf("dn %q: %v", target, err)
	}
	report, err := policy.For(ctx(t), sess, parsed)
	if err != nil {
		t.Fatalf("reading the policy for %s: %v", target, err)
	}
	return report
}

func settingOf(p *policy.Policy, keys ...string) (policy.Setting, bool) {
	for _, want := range keys {
		for _, s := range p.Settings {
			if strings.EqualFold(s.Key, want) {
				return s, true
			}
		}
	}
	return policy.Setting{}, false
}

func TestPolicyDefaultIsFoundWhereTheServerHasOne(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		report := policyFor(t, sess, "uid=user0003,ou=people,"+suffix)
		if !s.defaultPolicy {
			// A server with no default policy must be reported as having
			// none, in those words: "no policy found" and "no policy in
			// force" are different claims and Alder may only make the first.
			if report.Policy == nil || report.Policy.Source != policy.SourceNone {
				t.Fatalf("%s has no default policy and the report says %+v", s.name, report.Policy)
			}
			if !strings.Contains(report.Policy.Why, "not the same") {
				t.Errorf("%s: the report must not read as \"there is no policy\": %q", s.name, report.Policy.Why)
			}
			return
		}
		if report.Policy == nil || report.Policy.Source != policy.SourceDefault {
			t.Fatalf("%s: no default policy found (%+v, unread %+v)", s.name, report.Policy, report.Unread)
		}
		if report.Policy.Why == "" {
			t.Errorf("%s: a policy with no account of where it came from", s.name)
		}
		// The harness sets the same intent on both, in each server's attribute.
		length, ok := settingOf(report.Policy, "pwdMinLength", "passwordMinLength")
		if !ok {
			t.Fatalf("%s: the default policy has no minimum length: %+v", s.name, report.Policy.Settings)
		}
		if length.Values[0] != "8" {
			t.Errorf("%s: minimum length %q, harness sets 8", s.name, length.Values[0])
		}
		if length.Label == "" {
			t.Errorf("%s: %s is reported without saying what it means", s.name, length.Key)
		}
		// A duration is rendered for a person as well as kept as the server
		// wrote it: 7776000 is not a number anybody reads.
		age, ok := settingOf(report.Policy, "pwdMaxAge", "passwordMaxAge")
		if !ok || age.Detail == "" {
			t.Errorf("%s: maximum age %+v (ok=%v) has no readable form", s.name, age, ok)
		}
		t.Logf("%s: default policy at %s, %d settings", s.name, report.Policy.DN, len(report.Policy.Settings))
	})
}

func TestPolicyNamedByTheEntryWins(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		// user0004 is the account the harness holds to the stricter policy, on
		// both servers, by each server's own pointer attribute.
		report := policyFor(t, sess, "uid=user0004,ou=people,"+suffix)
		if report.Policy == nil {
			t.Fatalf("%s: no policy (unread %+v)", s.name, report.Unread)
		}
		if report.Policy.Source != policy.SourceEntry {
			t.Fatalf("%s: the entry names its own policy and the report says %q (%s)",
				s.name, report.Policy.Source, report.Policy.Why)
		}
		length, ok := settingOf(report.Policy, "pwdMinLength", "passwordMinLength")
		if !ok || length.Values[0] != "16" {
			t.Errorf("%s: the stricter policy sets 16, report says %+v (ok=%v)", s.name, length, ok)
		}
		if report.Policy.DN == "" {
			t.Errorf("%s: a policy nobody can find: %+v", s.name, report.Policy)
		}
		t.Logf("%s: entry policy at %s", s.name, report.Policy.DN)
	})
}

func TestPolicyReportsWhatTheServerRecordsAboutTheAccount(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		// A fresh account records almost nothing: the state half of this
		// report is only interesting once something has happened to the
		// account, and on a seeded harness nothing has. So the test makes
		// something happen -- the one thing an operator most often needs to
		// see -- and puts it back.
		target := "uid=user0007,ou=people," + suffix
		parsed, err := dn.Parse(target)
		if err != nil {
			t.Fatalf("dn: %v", err)
		}

		before := policyFor(t, sess, target)
		if before.State.Locked {
			t.Fatalf("%s: %s is locked before the test locks it", s.name, target)
		}

		lock := directory.ChangeRecord{DN: parsed, Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: s.lockAttr,
				Values: [][]byte{[]byte(s.lockValue)}}}}
		if err := sess.Apply(ctx(t), lock); err != nil {
			t.Fatalf("%s: locking %s with %s: %v", s.name, target, s.lockAttr, err)
		}
		t.Cleanup(func() {
			unlock := directory.ChangeRecord{DN: parsed, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModDelete, Name: s.lockAttr}}}
			if err := sess.Apply(context.Background(), unlock); err != nil {
				t.Errorf("%s: %s is still locked: %v", s.name, target, err)
			}
		})

		locked := policyFor(t, sess, target)
		if !locked.State.Locked {
			t.Fatalf("%s: the server holds %s on this account and the report says it is not locked: %+v",
				s.name, s.lockAttr, locked.State)
		}
		if locked.State.LockedDetail == "" {
			t.Errorf("%s: locked, with nothing said about it", s.name)
		}
		found := false
		for _, a := range locked.State.Attributes {
			if strings.EqualFold(a.Key, s.lockAttr) {
				found = true
				if a.Label == "" {
					t.Errorf("%s: %s is reported without saying what it means", s.name, a.Key)
				}
				if len(a.Values) == 0 || a.Values[0] != s.lockValue {
					t.Errorf("%s: %s reported as %v, server holds %q", s.name, a.Key, a.Values, s.lockValue)
				}
			}
		}
		if !found {
			t.Errorf("%s: the attribute that locks the account is not in the report: %+v",
				s.name, locked.State.Attributes)
		}
		t.Logf("%s: locked via %s, reported as %q", s.name, s.lockAttr, locked.State.LockedDetail)
	})
}
