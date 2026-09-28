//go:build conformance

package conformance

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// What an export writes when the operator asks for sensitive attributes.
//
// A UI audit on 2026-09-28 ticked "include sensitive attributes" on an
// OpenLDAP database entry and got a file containing the root password in
// plain text and the replication bind password inside an olcSyncrepl value
// -- from a screen that had just said "a secret; Alder never sends it to the
// browser", by way of a button that same screen recommended: "Sensitive
// attributes are omitted; use Export if you need them."
//
// The option exists for password digests, which is a real need: a hash can
// be carried to another server and a directory rebuilt from it. The 1.26
// security fix added the configuration tree's secrets to the same set the
// option releases, and so handed them to the same checkbox.
//
// These ask the running servers for the export the audit asked for.

// pwLine is a userPassword *value* line, as opposed to the attribute name
// appearing in the export's own header comment about what it omitted.
const pwLine = "\nuserPassword:"

func exportWith(t *testing.T, client *http.Client, base, target string, sensitive bool) string {
	t.Helper()
	want := "false"
	if sensitive {
		want = "true"
	}
	q := "?dn=" + url.QueryEscape(target) + "&scope=base&includeOperational=true&includeSensitive=" + want
	res := get(t, client, base+"/export/ldif"+q)
	if res.status != http.StatusOK {
		t.Fatalf("GET /export/ldif: %d\n%s", res.status, res.body)
	}
	return res.body
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func TestAnExportNeverCarriesACredentialInTheClear(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		if s.schemaBindDN == "" {
			t.Skipf("%s keeps no configuration tree Alder binds to separately", s.name)
		}
		client, base := alderSession(t, s, true)

		// The database entry: on OpenLDAP this is where olcRootPW and
		// olcSyncrepl live, and the audit's file came from here.
		body := exportWith(t, client, base, "olcDatabase={1}mdb,cn=config", true)

		// The harness's own root password, stored unhashed, exactly as many
		// real deployments leave it.
		if strings.Contains(body, "alder-admin") {
			t.Error("the export carries a cleartext password; it can be attached to a ticket")
		}
		for _, marker := range []string{"credentials=", "\nolcRootPW:"} {
			if strings.Contains(body, marker) {
				t.Errorf("the export carries %q", marker)
			}
		}
		// And it says what it held back, rather than being quietly shorter
		// than the directory.
		if !strings.Contains(body, "were not written") {
			t.Errorf("the export withheld values and does not say so:\n%s", firstLines(body, 20))
		}
		t.Logf("%s: the configuration export names what it withheld and carries none of it", s.name)
	})
}

func TestWhatAnExportReleasesFollowsHowTheServerStoredIt(t *testing.T) {
	// The other half, and the rule rather than the fixture.
	//
	// The entry viewer reads the RFC 2307 storage scheme off each withheld
	// value and shows it as a badge -- or says "stored in the clear" when
	// there is no prefix, because an unprefixed value *is* the password.
	// The export follows the same reading: a digest may travel, the password
	// itself may not. So what the viewer says decides what the file holds,
	// which is what this checks, against whatever each server happens to
	// store. The harness servers differ, which is the point.
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		client, base := alderSession(t, s, false)
		target := "uid=user0001,ou=people," + suffix

		view := entryOf(t, client, base, target)
		held := attributeOf(view, "userPassword")
		if held == nil || !isTrue(held.Withheld) {
			t.Fatalf("%s: userPassword is not withheld in the entry view, so this proves nothing", s.name)
		}
		schemes := strs(held.ValueSchemes)
		if len(schemes) == 0 {
			t.Fatalf("%s: the entry view reports no storage scheme at all", s.name)
		}
		hashed := schemes[0] != ""

		if plain := exportWith(t, client, base, target, false); strings.Contains(plain, pwLine) {
			t.Error("a userPassword value appears in an export that did not ask for secrets")
		}

		body := exportWith(t, client, base, target, true)
		if hashed {
			if !strings.Contains(body, pwLine) {
				t.Errorf("%s stores a %s digest and the export withheld it, so the option does nothing",
					s.name, schemes[0])
			}
			t.Logf("%s: stored as %s, and the digest travels -- which is what the option is for",
				s.name, schemes[0])
			return
		}
		// The audit's finding in its other form: this server holds the
		// password itself, and no checkbox turns that into a restore.
		if strings.Contains(body, pwLine) {
			t.Error("the server stores this password in the clear and the export wrote it to a file")
		}
		if !strings.Contains(body, "were not written") {
			t.Errorf("%s: the export withheld the password and does not say so:\n%s",
				s.name, firstLines(body, 20))
		}
		t.Logf("%s: stored with no scheme -- the password itself -- so the export names it and leaves it",
			s.name)
	})
}
