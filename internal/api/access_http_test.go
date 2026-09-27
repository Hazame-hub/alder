package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// GET /access over HTTP: the rules that bear on an entry, in the server's
// order, with the raw value always there. Nothing here evaluates anything.

func accessRig(t *testing.T, style string) *testRig {
	t.Helper()
	caps := defaultCaps()
	entries := []*directory.Entry{
		configEntry(t, "uid=alice,ou=people,dc=alder,dc=test", "objectClass", "person", "uid", "alice"),
		configEntry(t, "ou=people,dc=alder,dc=test", "objectClass", "organizationalUnit", "ou", "people"),
		configEntry(t, "dc=alder,dc=test", "objectClass", "domain", "dc", "alder"),
	}
	switch style {
	case "aci":
		// 389 DS keeps them on the entries themselves; one here, one above.
		entries[0].Set("aci", [][]byte{[]byte(`(targetattr="alderTeam")(version 3.0; acl "alderTeam is not for svc-alder"; deny (read,search,compare) userdn = "ldap:///cn=svc-alder,ou=services,dc=alder,dc=test";)`)})
		entries[2].Set("aci", [][]byte{[]byte(`(targetattr!="userPassword")(version 3.0; acl "svc-alder administers people"; allow (read,search,compare) userdn = "ldap:///cn=svc-alder,ou=services,dc=alder,dc=test";)`)})
	case "olcAccess":
		caps.VendorName = "OpenLDAP"
		caps.ConfigContext = "cn=config"
		caps.Config = directory.ConfigAccess{DN: "cn=config", Readable: true}
		db := configEntry(t, "olcDatabase={1}mdb,cn=config",
			"objectClass", "olcMdbConfig", "olcDatabase", "{1}mdb", "olcSuffix", "dc=alder,dc=test")
		db.Set("olcAccess", [][]byte{
			[]byte(`{0}to attrs=userPassword  by self write  by anonymous auth  by * none`),
			[]byte(`{1}to dn.subtree="ou=services,dc=alder,dc=test"  by users read  by * read`),
			[]byte(`{2}to *  by self write  by users read  by * read`),
		})
		other := configEntry(t, "olcDatabase={2}mdb,cn=config",
			"objectClass", "olcMdbConfig", "olcDatabase", "{2}mdb", "olcSuffix", "dc=other,dc=test")
		other.Set("olcAccess", [][]byte{[]byte(`{0}to *  by * none`)})
		// An overlay on the database that holds the entry can carry rules of
		// its own, two levels down. A one-level search found the database and
		// dropped these without saying so.
		overlay := configEntry(t, "olcOverlay={0}memberof,olcDatabase={1}mdb,cn=config",
			"objectClass", "olcOverlayConfig", "olcOverlay", "{0}memberof")
		overlay.Set("olcAccess", [][]byte{[]byte(`{0}to attrs=memberOf  by * read`)})
		entries = append(entries, db, other, overlay)
	case "unreadableConfig":
		caps.VendorName = "OpenLDAP"
		caps.ConfigContext = "cn=config"
		caps.Config = directory.ConfigAccess{DN: "cn=config", Readable: false,
			Reason: "this identity may not read cn=config"}
	}
	byDN := map[string]*directory.Entry{}
	for _, e := range entries {
		byDN[strings.ToLower(e.DN.String())] = e
	}
	return newRig(t, Config{}, &fakeSession{caps: caps, entries: entries, byDN: byDN})
}

func accessReport(t *testing.T, rig *testRig) AccessReport {
	t.Helper()
	res := rig.do(t, http.MethodGet, "/api/v1/access?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, res.Body)
	}
	return decode[AccessReport](t, res)
}

func TestAccessReportsACIsFromTheEntryAndAbove(t *testing.T) {
	report := accessReport(t, accessRig(t, "aci"))
	if len(report.Rules) != 2 {
		t.Fatalf("two acis bear on this entry, got %d: %+v", len(report.Rules), report.Rules)
	}
	own, inherited := report.Rules[0], report.Rules[1]
	if own.Inherited != nil && *own.Inherited {
		t.Error("the entry's own aci is not inherited")
	}
	if inherited.Inherited == nil || !*inherited.Inherited {
		t.Error("an aci written on the suffix must be marked inherited, or nobody will find it")
	}
	if inherited.Source != "dc=alder,dc=test" {
		t.Errorf("source %q: a rule is only useful if you can find where it is written", inherited.Source)
	}
	if own.Raw == "" || inherited.Raw == "" {
		t.Error("the raw rule is the thing actually in force and must always be there")
	}
	if report.Disclaimer == "" || !strings.Contains(report.Disclaimer, "does not evaluate") {
		t.Errorf("every report says what it is not: %q", report.Disclaimer)
	}
}

func TestAccessReportsOlcAccessInServerOrder(t *testing.T) {
	report := accessReport(t, accessRig(t, "olcAccess"))
	if len(report.Rules) != 4 {
		t.Fatalf("three rules on the database and one on its overlay, got %d: %+v", len(report.Rules), report.Rules)
	}
	for i, rule := range report.Rules[:3] {
		if rule.Index == nil || *rule.Index != i {
			t.Fatalf("rule %d is out of the server's order: %+v", i, rule)
		}
	}
	// The overlay's rule is reported, after the database's and with its own
	// source, because an operator cannot fix a rule they were never shown.
	last := report.Rules[3]
	if !strings.Contains(last.Source, "olcOverlay") {
		t.Errorf("the overlay's own rule is missing: %+v", report.Rules)
	}
	// A rule about a subtree this entry is not in is reported, and reported as
	// not bearing on it: what an operator needs is the whole ordered list with
	// the relevant ones marked, not a filtered one.
	if report.Rules[1].Applies != AccessAppliesNo {
		t.Errorf("rule {1} is about ou=services: applies %q", report.Rules[1].Applies)
	}
	if report.Rules[2].Applies != AccessAppliesYes {
		t.Errorf("rule {2} is about everything: applies %q", report.Rules[2].Applies)
	}
	// Another database's rules are not this entry's.
	for _, rule := range report.Rules {
		if strings.Contains(rule.Source, "{2}mdb") {
			t.Errorf("a rule from the database that does not hold this entry: %+v", rule)
		}
	}
}

func TestAccessSaysWhenItCouldNotReadWhereRulesLive(t *testing.T) {
	report := accessReport(t, accessRig(t, "unreadableConfig"))
	if report.Unread == nil || len(*report.Unread) == 0 {
		t.Fatal("the configuration tree could not be read, and a report of no rules would be a lie")
	}
	if !strings.Contains((*report.Unread)[0].Reason, "may not read") {
		t.Errorf("the reason must be the server's own: %+v", (*report.Unread)[0])
	}
	if len(report.Rules) != 0 {
		t.Errorf("no rules were readable, so none should be reported: %+v", report.Rules)
	}
}

// A session whose server answers what an identity may do, and one whose
// server does not. Both are ordinary: only 389 Directory Server publishes the
// control, so the second case is most servers.
func rightsRig(t *testing.T, answer *directory.EffectiveRights, err error) (*testRig, *fakeSession) {
	t.Helper()
	caps := defaultCaps()
	caps.EffectiveRights = true
	entry := configEntry(t, "uid=alice,ou=people,dc=alder,dc=test", "objectClass", "person", "uid", "alice")
	fake := &fakeSession{
		caps: caps, entries: []*directory.Entry{entry},
		byDN:      map[string]*directory.Entry{strings.ToLower(entry.DN.String()): entry},
		rights:    answer,
		rightsErr: err,
	}
	return newRig(t, Config{}, fake), fake
}

func TestAccessCarriesTheServersOwnVerdict(t *testing.T) {
	rig, sess := rightsRig(t, &directory.EffectiveRights{
		Entry: "v",
		Attributes: []directory.AttributeRights{
			{Name: "cn", Rights: "rsc"},
			{Name: "userPassword", Rights: "none"},
		},
	}, nil)

	res := rig.do(t, http.MethodGet,
		"/api/v1/access?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest&as=cn%3Dsvc-alder%2Cou%3Dservices%2Cdc%3Dalder%2Cdc%3Dtest", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, res.Body)
	}
	report := decode[AccessReport](t, res)

	if sess.rightsAsked != "cn=svc-alder,ou=services,dc=alder,dc=test" {
		t.Errorf("the server was asked about %q", sess.rightsAsked)
	}
	if report.Effective == nil {
		t.Fatalf("no verdict: %v", report.RightsNote)
	}
	if report.Effective.Entry != "v" {
		t.Errorf("entry rights %q", report.Effective.Entry)
	}
	if report.Effective.EntryWords == nil || (*report.Effective.EntryWords)[0] != "view this entry" {
		t.Errorf("the letters must be glossed as well as kept: %+v", report.Effective.EntryWords)
	}
	attrs := *report.Effective.Attributes
	if len(attrs) != 2 || attrs[1].Rights != "none" {
		t.Fatalf("attributes: %+v", attrs)
	}
	// "none" glosses to nothing at all, which is how the screen tells a denial
	// from a right it does not recognise.
	if attrs[1].Words != nil && len(*attrs[1].Words) != 0 {
		t.Errorf("none should carry no words: %+v", attrs[1].Words)
	}
}

func TestAccessSaysWhyThereIsNoVerdict(t *testing.T) {
	// A server that declines the question. "No rights" would be a verdict,
	// and this is not one.
	rig, _ := rightsRig(t, nil, directory.ErrRightsUnanswered)
	res := rig.do(t, http.MethodGet, "/api/v1/access?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest", nil)
	report := decode[AccessReport](t, res)
	if report.Effective != nil {
		t.Fatalf("a verdict from a declined question: %+v", report.Effective)
	}
	if report.RightsNote == nil || !strings.Contains(*report.RightsNote, "declined") {
		t.Errorf("the note must say the server declined: %v", report.RightsNote)
	}

	// And a server that cannot answer at all, which is most of them.
	plain := accessReport(t, accessRig(t, "aci"))
	if plain.RightsNote == nil || !strings.Contains(*plain.RightsNote, "does not answer") {
		t.Errorf("a server without the control must say so: %v", plain.RightsNote)
	}
}

func TestAccessNeedsAnEntryAndASession(t *testing.T) {
	rig := accessRig(t, "aci")
	res := rig.anonymous(t, http.MethodGet, "/api/v1/access?dn=dc%3Dalder%2Cdc%3Dtest")
	if res.Status != http.StatusUnauthorized {
		t.Errorf("status %d: reading access rules needs a session", res.Status)
	}
}

// GET /policy: which policy applies, where it is written, and what the server
// records about the account.

func policyRig(t *testing.T) *testRig {
	t.Helper()
	caps := defaultCaps()
	caps.VendorName = "OpenLDAP"
	caps.ConfigContext = "cn=config"
	caps.Config = directory.ConfigAccess{DN: "cn=config", Readable: true}

	account := configEntry(t, "uid=alice,ou=people,dc=alder,dc=test",
		"objectClass", "person", "uid", "alice",
		"pwdPolicySubentry", "cn=strict,ou=policies,dc=alder,dc=test",
		"pwdAccountLockedTime", "20260101000000Z",
		"pwdChangedTime", "20251201090000Z")
	strict := configEntry(t, "cn=strict,ou=policies,dc=alder,dc=test",
		"objectClass", "pwdPolicy", "cn", "strict",
		"pwdMinLength", "16", "pwdMaxAge", "2592000", "pwdLockout", "TRUE")
	plain := configEntry(t, "uid=bob,ou=people,dc=alder,dc=test", "objectClass", "person", "uid", "bob")
	def := configEntry(t, "cn=default,ou=policies,dc=alder,dc=test",
		"objectClass", "pwdPolicy", "cn", "default", "pwdMinLength", "8")
	overlay := configEntry(t, "olcOverlay={0}ppolicy,olcDatabase={1}mdb,cn=config",
		"objectClass", "olcPPolicyConfig", "olcPPolicyDefault", "cn=default,ou=policies,dc=alder,dc=test")

	entries := []*directory.Entry{account, strict, plain, def, overlay}
	byDN := map[string]*directory.Entry{}
	for _, e := range entries {
		byDN[strings.ToLower(e.DN.String())] = e
	}
	return newRig(t, Config{}, &fakeSession{caps: caps, entries: entries, byDN: byDN})
}

func TestPolicyReportsTheOneTheEntryNames(t *testing.T) {
	rig := policyRig(t)
	res := rig.do(t, http.MethodGet, "/api/v1/policy?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, res.Body)
	}
	report := decode[PolicyReport](t, res)

	if report.Policy == nil || report.Policy.Source != PolicySourceEntry {
		t.Fatalf("the entry names its own policy: %+v", report.Policy)
	}
	if report.Policy.Dn == nil || *report.Policy.Dn != "cn=strict,ou=policies,dc=alder,dc=test" {
		t.Errorf("a policy nobody can find: %+v", report.Policy.Dn)
	}
	// The value as the server holds it, and a reading of it beside.
	var age PolicySetting
	for _, s := range report.Policy.Settings {
		if strings.EqualFold(s.Key, "pwdMaxAge") {
			age = s
		}
	}
	if age.Values[0] != "2592000" {
		t.Errorf("maximum age %v", age.Values)
	}
	if age.Detail == nil || *age.Detail != "30 days" {
		t.Errorf("30 days is what 2592000 seconds is: %v", age.Detail)
	}

	if !report.State.Locked || report.State.LockedDetail == nil {
		t.Errorf("the account carries pwdAccountLockedTime and the report says %+v", report.State)
	}
	if report.State.Changed == nil {
		t.Error("the server records when the password changed and the report drops it")
	}
	if !strings.Contains(report.Disclaimer, "not a decision") {
		t.Errorf("every report says what it is not: %q", report.Disclaimer)
	}
}

func TestPolicyFallsBackToTheServersDefault(t *testing.T) {
	rig := policyRig(t)
	res := rig.do(t, http.MethodGet, "/api/v1/policy?dn=uid%3Dbob%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest", nil)
	report := decode[PolicyReport](t, res)
	if report.Policy == nil || report.Policy.Source != PolicySourceDefault {
		t.Fatalf("an account naming no policy gets the server's: %+v", report.Policy)
	}
	if report.Policy.Dn == nil || *report.Policy.Dn != "cn=default,ou=policies,dc=alder,dc=test" {
		t.Errorf("the default is found through the overlay that names it: %+v", report.Policy.Dn)
	}
	if report.State.Locked {
		t.Error("bob is not locked")
	}
}

func TestPolicyDoesNotPresentTheDefaultWhenTheNamedOneIsUnreadable(t *testing.T) {
	// An account that names a policy Alder cannot read is not governed by the
	// server's default, and saying it is would be a confident wrong answer
	// about the one thing this report exists to get right.
	caps := defaultCaps()
	caps.VendorName = "OpenLDAP"
	caps.ConfigContext = "cn=config"
	caps.Config = directory.ConfigAccess{DN: "cn=config", Readable: true}

	account := configEntry(t, "uid=carol,ou=people,dc=alder,dc=test",
		"objectClass", "person", "uid", "carol",
		"pwdPolicySubentry", "cn=gone,ou=policies,dc=alder,dc=test")
	def := configEntry(t, "cn=default,ou=policies,dc=alder,dc=test",
		"objectClass", "pwdPolicy", "cn", "default", "pwdMinLength", "8")
	overlay := configEntry(t, "olcOverlay={0}ppolicy,olcDatabase={1}mdb,cn=config",
		"objectClass", "olcPPolicyConfig", "olcPPolicyDefault", "cn=default,ou=policies,dc=alder,dc=test")

	entries := []*directory.Entry{account, def, overlay}
	byDN := map[string]*directory.Entry{}
	for _, e := range entries {
		byDN[strings.ToLower(e.DN.String())] = e
	}
	rig := newRig(t, Config{}, &fakeSession{caps: caps, entries: entries, byDN: byDN})

	res := rig.do(t, http.MethodGet, "/api/v1/policy?dn=uid%3Dcarol%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest", nil)
	report := decode[PolicyReport](t, res)

	if report.Policy == nil || report.Policy.Source != PolicySourceEntry {
		t.Fatalf("the entry names its own policy, readable or not: %+v", report.Policy)
	}
	if len(report.Policy.Settings) != 0 {
		t.Errorf("no settings were read, so none should be reported: %+v", report.Policy.Settings)
	}
	if !strings.Contains(report.Policy.Why, "not the server's default") {
		t.Errorf("the report must say the default does not apply here: %q", report.Policy.Why)
	}
	if report.Unread == nil || len(*report.Unread) == 0 {
		t.Error("the policy it names could not be read, and the report does not say so")
	}
}
