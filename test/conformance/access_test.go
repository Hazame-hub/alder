//go:build conformance

package conformance

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/access"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// 1.19: the access rules a server holds, read from both servers.
//
// The harness carries the same intent on each: svc-alder may read people and
// groups, may not see the service accounts, and may not see alderTeam on one
// particular entry. The two servers express that in completely different
// mechanisms -- an ordered list in OpenLDAP's configuration tree, aci
// attributes down the data tree in 389 DS -- and the point of this suite is
// that Alder reports each one in its own terms rather than inventing a shared
// model. So these assertions are deliberately not identical: what is identical
// is that the rule an operator needs to find is found, with where it lives.

func accessFor(t *testing.T, sess directory.Session, target string) *access.Report {
	t.Helper()
	parsed, err := dn.Parse(target)
	if err != nil {
		t.Fatalf("dn %q: %v", target, err)
	}
	report, err := access.For(ctx(t), sess, parsed, access.Options{})
	if err != nil {
		t.Fatalf("reading access rules for %s: %v", target, err)
	}
	return report
}

func TestAccessRulesAreFoundOnBothServers(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		target := "uid=user0002,ou=people," + suffix
		report := accessFor(t, sess, target)

		if len(report.Rules) == 0 {
			t.Fatalf("%s: the harness has access rules and none were found (unread: %+v)", s.name, report.Unread)
		}
		if report.Applying() == 0 {
			t.Errorf("%s: no rule was found to bear on %s", s.name, target)
		}

		// The rule that names alderTeam on this entry exists on both servers,
		// written in each server's own way, and is what an operator would be
		// hunting for.
		found := false
		for _, rule := range report.Rules {
			if !strings.Contains(strings.ToLower(rule.Raw), "alderteam") {
				continue
			}
			found = true
			if rule.Applies != access.AppliesYes {
				t.Errorf("%s: the rule about alderTeam on this very entry says applies=%q (%s)",
					s.name, rule.Applies, rule.Why)
			}
			if rule.Source == "" {
				t.Errorf("%s: a rule with nowhere to find it: %+v", s.name, rule)
			}
			if rule.Raw == "" {
				t.Errorf("%s: the raw rule is missing, and it is the thing in force", s.name)
			}
			if len(rule.Grants) == 0 {
				t.Errorf("%s: no grant was read from %q", s.name, rule.Raw)
			}
			for _, grant := range rule.Grants {
				if !strings.Contains(strings.ToLower(grant.Subject), "svc-alder") {
					continue
				}
				// Each server says it its own way: 389 DS denies, OpenLDAP
				// grants the level "none". Neither is translated into the
				// other.
				switch rule.Style {
				case access.StyleACI:
					if grant.Kind != access.KindDeny {
						t.Errorf("389 DS writes this as a deny, reported as %q", grant.Kind)
					}
				case access.StyleOpenLDAP:
					if grant.Kind != access.KindLevel || grant.Access != "none" {
						t.Errorf("OpenLDAP writes this as the level none, reported as %q %q", grant.Kind, grant.Access)
					}
				}
			}
		}
		if !found {
			t.Errorf("%s: the harness's alderTeam rule was not reported at all", s.name)
		}
		t.Logf("%s: %d rules, %d bearing on the entry, styles %v",
			s.name, len(report.Rules), report.Applying(), report.Styles)
	})
}

func TestAccessSeparatesWhatBearsOnAnEntryFromWhatDoesNot(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		// The services subtree has a rule of its own on both servers: OpenLDAP
		// targets the subtree, 389 DS writes the aci on it. Either way it is
		// about ou=services, which is not the same as merely naming a service
		// account as the subject -- the rule about people does that.
		about := func(rule access.Rule) bool {
			services := "ou=services," + suffix
			if rule.Target != nil && rule.Target.DN != "" {
				return strings.EqualFold(rule.Target.DN, services)
			}
			return strings.EqualFold(rule.Source, services)
		}

		person := accessFor(t, sess, "uid=user0003,ou=people,"+suffix)
		for _, rule := range person.Rules {
			if about(rule) && rule.Applies == access.AppliesYes {
				t.Errorf("%s: a rule about ou=services was said to bear on an entry in ou=people: %q",
					s.name, rule.Raw)
			}
		}

		service := accessFor(t, sess, "cn=svc-alder,ou=services,"+suffix)
		hit := false
		for _, rule := range service.Rules {
			if about(rule) && rule.Applies == access.AppliesYes {
				hit = true
			}
		}
		if !hit {
			t.Errorf("%s: the rule about ou=services does not bear on an entry inside it", s.name)
		}
	})
}

// 1.20: where a server answers what an identity may do, Alder asks it.
//
// This is the proof that matters most in the whole access feature, and it is
// only possible against a real server: the harness's acis take alderTeam and
// userPassword away from svc-alder, and the server's own effective-rights
// answer says exactly that. The rules said it; the directory confirms it.
func TestEffectiveRightsAreTheServersOwnAnswer(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		target := "uid=user0002,ou=people," + suffix
		parsed, err := dn.Parse(target)
		if err != nil {
			t.Fatalf("dn: %v", err)
		}
		svc := "cn=svc-alder,ou=services," + suffix
		report, err := access.For(ctx(t), sess, parsed, access.Options{Subject: svc})
		if err != nil {
			t.Fatalf("reading access for %s: %v", target, err)
		}

		if !sess.Capabilities().EffectiveRights {
			// OpenLDAP has no equivalent control, and the report must say so
			// rather than leave the rules looking like a verdict.
			if report.Effective != nil {
				t.Errorf("%s: a verdict from a server that cannot give one: %+v", s.name, report.Effective)
			}
			if report.RightsNote == "" {
				t.Errorf("%s: no verdict and no reason", s.name)
			}
			return
		}

		if report.Effective == nil {
			t.Fatalf("%s: the server publishes the control and gave no answer (%s)", s.name, report.RightsNote)
		}
		rights := report.Effective
		if rights.Subject != svc {
			t.Errorf("answered about %q, asked about %q", rights.Subject, svc)
		}
		if rights.Entry == "" {
			t.Error("no entry-level answer")
		}
		byName := map[string]string{}
		for _, a := range rights.Attributes {
			byName[strings.ToLower(a.Name)] = a.Rights
		}
		// What the harness's acis take away, confirmed by the server itself.
		for _, denied := range []string{"alderteam", "userpassword"} {
			if got, ok := byName[denied]; !ok || got != "none" {
				t.Errorf("%s: the acis deny %s to svc-alder and the server answers %q (present=%v)",
					s.name, denied, got, ok)
			}
		}
		// And what they leave alone.
		if got := byName["cn"]; got == "" || got == "none" {
			t.Errorf("%s: cn should be readable by svc-alder, server says %q", s.name, got)
		}
		t.Logf("%s: entry %q, %d attributes answered", s.name, rights.Entry, len(rights.Attributes))
	})
}

func TestAccessSaysWhenItCannotReachTheRules(t *testing.T) {
	// A session with no configuration identity cannot read OpenLDAP's rules,
	// which live in the configuration tree. Saying "no rules" there would be
	// the most dangerous answer this feature could give.
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		if sess.Capabilities().Config.Readable {
			t.Skip("this session can read the configuration tree")
		}
		report := accessFor(t, sess, "uid=user0002,ou=people,"+suffix)
		if len(report.Unread) == 0 {
			t.Errorf("%s: the configuration tree was unreadable and the report does not say so", s.name)
		}
		for _, unread := range report.Unread {
			if unread.Reason == "" {
				t.Errorf("%s: an unreadable place with no reason: %+v", s.name, unread)
			}
		}
	})
}
