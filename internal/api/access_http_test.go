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
		entries = append(entries, db, other)
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
	if len(report.Rules) != 3 {
		t.Fatalf("the database holding this entry has three rules, got %d: %+v", len(report.Rules), report.Rules)
	}
	for i, rule := range report.Rules {
		if rule.Index == nil || *rule.Index != i {
			t.Fatalf("rule %d is out of the server's order: %+v", i, rule)
		}
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

func TestAccessNeedsAnEntryAndASession(t *testing.T) {
	rig := accessRig(t, "aci")
	res := rig.anonymous(t, http.MethodGet, "/api/v1/access?dn=dc%3Dalder%2Cdc%3Dtest")
	if res.Status != http.StatusUnauthorized {
		t.Errorf("status %d: reading access rules needs a session", res.Status)
	}
}
