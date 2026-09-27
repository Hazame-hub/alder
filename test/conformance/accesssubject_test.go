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

// Asking the access view about another identity, over HTTP.
//
// The Go level was proved in 1.19; what was never exercised is the query
// parameter, and the parameter is the whole feature as far as the interface is
// concerned. The assertions that matter are the two that keep the screen
// honest: on a server that answers, the verdict is about the identity named
// and not about the session; on either server, the rules are unchanged by the
// question, because they are about the entry.

func accessAs(t *testing.T, client *http.Client, base, target, subject string) (api.AccessReport, httpResult) {
	t.Helper()
	path := base + "/access?dn=" + url.QueryEscape(target)
	if subject != "" {
		path += "&as=" + url.QueryEscape(subject)
	}
	res := get(t, client, path)
	if res.status != http.StatusOK {
		return api.AccessReport{}, res
	}
	var report api.AccessReport
	if err := json.Unmarshal([]byte(res.body), &report); err != nil {
		t.Fatalf("decoding the access report: %v\n%s", err, res.body)
	}
	return report, res
}

func TestAskingAboutAnotherIdentityChangesOnlyTheVerdict(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alderSession(t, s, true)
		// An entry the delegated account is deliberately held short of: the
		// harness denies it alderTeam on this one, on both servers.
		target := "uid=user0002,ou=people," + suffix

		mine, _ := accessAs(t, client, base, target, "")
		theirs, _ := accessAs(t, client, base, target, s.restrictedDN)

		// The rules are about the entry. Whoever is asked about, the list and
		// the marks on it are identical -- and a screen that implied
		// otherwise would be the failure this view exists to avoid.
		if len(mine.Rules) != len(theirs.Rules) {
			t.Fatalf("%s: %d rules asked as myself, %d asked about somebody else",
				s.name, len(mine.Rules), len(theirs.Rules))
		}
		for i := range mine.Rules {
			if mine.Rules[i].Applies != theirs.Rules[i].Applies || mine.Rules[i].Raw != theirs.Rules[i].Raw {
				t.Errorf("%s: rule %d changed when the subject did: %s vs %s",
					s.name, i, mustEncode(t, mine.Rules[i]), mustEncode(t, theirs.Rules[i]))
			}
		}

		if !sess.Capabilities().EffectiveRights {
			// The honest case: nothing about the report is identity-scoped,
			// so nothing may change at all, and the note has to say why.
			if theirs.Effective != nil {
				t.Errorf("%s answers no effective rights and produced a verdict: %s",
					s.name, mustEncode(t, theirs.Effective))
			}
			if theirs.RightsNote == nil || *theirs.RightsNote == "" {
				t.Errorf("%s: no verdict and no reason given", s.name)
			}
			t.Logf("%s: no effective-rights control, and the report says so", s.name)
			return
		}

		// On a server that answers, the verdict is the one thing that moves.
		if theirs.Effective == nil {
			t.Fatalf("%s publishes the control and gave no verdict for %s: %s",
				s.name, s.restrictedDN, mustEncode(t, theirs))
		}
		if !strings.EqualFold(theirs.Effective.Subject, s.restrictedDN) {
			t.Errorf("%s: the verdict is attributed to %q, want %q",
				s.name, theirs.Effective.Subject, s.restrictedDN)
		}
		if mine.Effective != nil && strings.EqualFold(mine.Effective.Subject, theirs.Effective.Subject) {
			t.Errorf("%s: asking about somebody else returned the session's own verdict", s.name)
		}

		// And it is a different answer, not the same one relabelled: the
		// delegated account is denied alderTeam on this entry and the
		// administrator is not.
		denied := deniedAttributes(theirs.Effective)
		if len(denied) == 0 {
			t.Errorf("%s: the delegated account is denied an attribute here and the verdict shows none: %s",
				s.name, mustEncode(t, theirs.Effective))
		}
		t.Logf("%s: %s is denied %v here", s.name, s.restrictedDN, denied)
	})
}

func TestAnIdentityThatIsNotADistinguishedNameIsRefused(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, _ directory.Session) {
		client, base := alderSession(t, s, true)
		// The only authzID the driver builds is "dn: <DN>", so anything else
		// is a question no server can be asked. 389 DS answers such a control
		// with an error code where the rights letters go, which would reach
		// the reader as "the server declined to say" -- a sentence about
		// access, describing a typo.
		_, res := accessAs(t, client, base, "uid=user0002,ou=people,"+suffix, "not a dn")
		if res.status != http.StatusBadRequest {
			t.Fatalf("%s: an identity that is not a DN answered %d, want 400\n%s", s.name, res.status, res.body)
		}
		if !strings.Contains(res.body, "distinguished name") {
			t.Errorf("%s: the refusal does not say what was wrong: %s", s.name, res.body)
		}
	})
}

// TestAnEntryThatCannotBeReadAnswersWithTheDirectorysRefusal pins the answer
// an operator gets for the commonest mistake there is: a DN that is not there.
//
// It was "Something went wrong" with a 500 for both of these views, because
// the handler called errors.Unwrap on a value built with errors.Join -- whose
// Unwrap returns nil -- so the directory's own result code never reached the
// mapping that turns it into a 404. A generic internal error also makes a typo
// indistinguishable from a permissions problem, which on these two screens in
// particular is the distinction the reader came for.
func TestAnEntryThatCannotBeReadAnswersWithTheDirectorysRefusal(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		client, base := alderSession(t, s, false)
		missing := url.QueryEscape("uid=nobody-is-here,ou=people," + suffix)
		for _, path := range []string{"/access?dn=" + missing, "/policy?dn=" + missing} {
			res := get(t, client, base+path)
			if res.status == http.StatusInternalServerError {
				t.Errorf("%s: %s answered 500 for an entry that is not there: %s", s.name, path, res.body)
				continue
			}
			if res.status != http.StatusNotFound {
				t.Errorf("%s: %s answered %d, want 404: %s", s.name, path, res.status, res.body)
				continue
			}
			if !strings.Contains(strings.ToLower(res.body), "no such entry") {
				t.Errorf("%s: %s does not say the entry is not there: %s", s.name, path, res.body)
			}
		}
	})
}

func deniedAttributes(e *api.EffectiveRights) []string {
	var out []string
	if e.Attributes == nil {
		return nil
	}
	for _, a := range *e.Attributes {
		if a.Words == nil || len(*a.Words) == 0 {
			out = append(out, a.Name)
		}
	}
	return out
}

// TestAskingWhatAnUnauthenticatedClientMayDo.
//
// The exposure question, and the one that could not be asked until 1.28: it
// has no DN to name, because the effective-rights control identifies an
// unauthenticated requester by an authorization identity carrying no DN at
// all. `as=anonymous` is the one reserved value of the parameter, and it
// cannot collide with a DN because a DN always contains an equals sign.
//
// What makes it worth having: on the harness, anonymous can read nothing at
// all here, and the only way to be sure of that was to try binding as nobody
// and looking.
func TestAskingWhatAnUnauthenticatedClientMayDo(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alderSession(t, s, true)
		target := "uid=user0002,ou=people," + suffix

		report, res := accessAs(t, client, base, target, "anonymous")
		if res.status != 0 && res.status != http.StatusOK {
			t.Fatalf("%s: the anonymous question answered %d\n%s", s.name, res.status, res.body)
		}

		// Alder says what it asked, whatever the server echoes -- and for this
		// question the server correctly echoes nothing, because there is no DN.
		if report.AskedAbout == nil || *report.AskedAbout != "anonymous" {
			t.Errorf("%s: the report does not say it asked anonymously: %s", s.name, mustEncode(t, report.AskedAbout))
		}
		// A plain question about oneself still names the bind DN, so the field
		// distinguishes the three cases rather than only marking this one.
		mine, _ := accessAs(t, client, base, target, "")
		if mine.AskedAbout == nil || !strings.EqualFold(*mine.AskedAbout, s.bindDN) {
			t.Errorf("%s: asking about myself reports %v, want the bind DN", s.name, mine.AskedAbout)
		}

		if !sess.Capabilities().EffectiveRights {
			if report.Effective != nil {
				t.Errorf("%s answers no effective rights and produced a verdict", s.name)
			}
			t.Logf("%s: no effective-rights control, so the question has the same honest non-answer", s.name)
			return
		}

		if report.Effective == nil {
			t.Fatalf("%s publishes the control and gave no anonymous verdict: %s", s.name, mustEncode(t, report))
		}
		// The harness lets nobody read anything without binding, which is what
		// the seeded access rules say, so this is the answer that proves the
		// question was really asked rather than defaulted to the session.
		denied := deniedAttributes(report.Effective)
		if len(denied) == 0 {
			t.Errorf("%s: anonymous is denied everything here and the verdict shows nothing denied: %s",
				s.name, mustEncode(t, report.Effective))
		}
		if mine.Effective != nil && len(deniedAttributes(mine.Effective)) >= len(denied) {
			t.Errorf("%s: the anonymous answer is no narrower than the administrator's, so it was not asked",
				s.name)
		}
		t.Logf("%s: anonymous is denied %d attributes here; entry rights %q",
			s.name, len(denied), report.Effective.Entry)
	})
}
