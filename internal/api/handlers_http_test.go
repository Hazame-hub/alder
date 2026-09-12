package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/directory"
)

// Every read endpoint refuses a request with no session.
//
// The guard is one shared function, but the routes are wired one at a time, and
// a route that forgets it is a route that serves directory contents to anybody
// who can reach the port. This is the test that notices a new endpoint wired
// without it.
func TestEveryEndpointRequiresASession(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps()})

	for _, target := range []string{
		"/api/v1/tree?dn=dc%3Dalder%2Cdc%3Dtest",
		"/api/v1/count?dn=dc%3Dalder%2Cdc%3Dtest",
		"/api/v1/entry?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest",
		"/api/v1/views",
		"/api/v1/references?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest",
		"/api/v1/members?dn=cn%3Dteam%2Cou%3Dgroups%2Cdc%3Dalder%2Cdc%3Dtest",
		"/api/v1/compare?left=uid%3Da%2Cdc%3Dtest&right=uid%3Db%2Cdc%3Dtest",
		"/api/v1/schema",
		"/api/v1/schema/requirements?class=person",
		"/api/v1/export/ldif?dn=dc%3Dalder%2Cdc%3Dtest",
		"/api/v1/export/ansible?dn=dc%3Dalder%2Cdc%3Dtest",
	} {
		t.Run(target, func(t *testing.T) {
			res := rig.anonymous(t, http.MethodGet, target)
			if res.Status != fiber.StatusUnauthorized {
				t.Errorf("got %d, want 401 — this endpoint serves directory "+
					"contents without a session", res.Status)
			}
		})
	}
}

// The source offer is the one endpoint that must answer without a session:
// AGPL section 13 runs to whoever is looking at the running instance.
//
// It moved into openapi.yaml at 1.0 and is now routed by the generated handler
// rather than by hand, so this checks the offer itself and not merely a 200: a
// route wired to the wrong thing answers 200 with nothing useful in it.
func TestTheSourceOfferNeedsNoSession(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps()})
	res := rig.anonymous(t, http.MethodGet, "/api/v1/source")
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d, want 200; the offer has to reach a user who is not "+
			"connected to a directory", res.Status)
	}

	var offer SourceOffer
	if err := json.Unmarshal([]byte(res.Body), &offer); err != nil {
		t.Fatalf("the offer is not JSON: %v -- %s", err, res.Body)
	}
	if offer.License != "AGPL-3.0-only" {
		t.Errorf("license is %q", offer.License)
	}
	if offer.SourceUrl == "" {
		t.Error("the offer names nowhere to get the source, which is the whole obligation")
	}
	if offer.Notice == "" {
		t.Error("the offer carries no notice for a person to read")
	}
}

// Read-only is a seatbelt at the API layer, whatever the directory would have
// allowed. It is worthless if a write route does not consult it.
func TestReadOnlyRefusesEveryWrite(t *testing.T) {
	rig := newRig(t, Config{ReadOnly: true}, &fakeSession{
		caps: defaultCaps(), entry: entryFixture(t),
	})

	body := `{"dn":"uid=alice,ou=people,dc=alder,dc=test","type":"delete"}`
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodPost, "/api/v1/changes/apply", body},
		{http.MethodPost, "/api/v1/changeset/apply", `{"changes":[` + body + `]}`},
	} {
		t.Run(tc.target, func(t *testing.T) {
			res := rig.do(t, tc.method, tc.target, strings.NewReader(tc.body))
			if res.Status != fiber.StatusForbidden {
				t.Errorf("got %d, want 403 in read-only mode", res.Status)
			}
			if len(rig.fake.applied) != 0 {
				t.Errorf("a write reached the directory in read-only mode: %+v",
					rig.fake.applied)
			}
		})
	}
}

// A malformed parameter is the caller's mistake, and saying so is worth more
// than a 500 that reads as "the directory is broken".
func TestBadParametersAreRefusedWithoutTouchingTheDirectory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
	}{
		{"a DN that is not a DN", "/api/v1/entry?dn=not%3Da%3Bdn%3D"},
		{"an unknown search scope", "/api/v1/export/ldif?dn=dc%3Dalder%2Cdc%3Dtest&scope=sideways"},
		{"a filter that is not RFC 4515", "/api/v1/export/ldif?dn=dc%3Dalder%2Cdc%3Dtest&filter=%28%28%28"},
		{"a filter that is not RFC 4515, on the playbook", "/api/v1/export/ansible?dn=dc%3Dalder%2Cdc%3Dtest&filter=%28%28%28"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps()})
			res := rig.do(t, http.MethodGet, tc.target, nil)
			if res.Status != fiber.StatusBadRequest {
				t.Errorf("got %d, want 400 for %s", res.Status, tc.name)
			}
			if rig.fake.last() != nil {
				t.Error("the directory was searched despite the request being invalid")
			}
		})
	}
}

// The rule that a password never reaches the browser, asserted where it would
// actually leak: the response body.
func TestTheEntryEndpointWithholdsSecrets(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), entry: entryFixture(t)})

	res := rig.do(t, http.MethodGet,
		"/api/v1/entry?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest", nil)
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d, want 200", res.Status)
	}
	body := res.Body

	if strings.Contains(body, "averyrealsecret") {
		t.Error("the password hash reached the browser")
	}
	// The attribute is still reported as present — "set, and withheld" is the
	// answer, not silence, or nobody knows a password exists.
	if !strings.Contains(body, "userPassword") {
		t.Error("userPassword vanished entirely; it should be reported as set and withheld")
	}
	if !strings.Contains(body, "alice@alder.test") {
		t.Error("withholding the secret also withheld the ordinary attributes")
	}
}

// The search handler builds the SearchRequest, and a parameter dropped there is
// invisible: the search still succeeds, against a different question.
func TestSearchPassesItsParametersToTheDirectory(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps()})

	res := rig.do(t, http.MethodPost, "/api/v1/search", strings.NewReader(
		`{"baseDn":"ou=people,dc=alder,dc=test","scope":"one",`+
			`"filter":"(uid=alice)","limit":7,"attributes":["cn","mail"]}`))
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d, want 200: %s", res.Status, res.Body)
	}

	got := rig.fake.last()
	if got == nil {
		t.Fatal("the handler never searched")
	}
	if got.BaseDN.String() != "ou=people,dc=alder,dc=test" {
		t.Errorf("base is %q", got.BaseDN.String())
	}
	if got.Scope != directory.ScopeOneLevel {
		t.Errorf("scope is %v, want one level", got.Scope)
	}
	if got.Limit != 7 {
		t.Errorf("limit is %d, want 7", got.Limit)
	}
	rendered, err := got.Filter.Render()
	if err != nil || rendered != "(uid=alice)" {
		t.Errorf("filter is %q (err %v)", rendered, err)
	}
}

// The command rides on the search response. It is the newest field on the
// newest surface, and until now nothing checked it crossed the wire at all.
func TestSearchReturnsTheCommandAndNeverThePassword(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps()})

	res := rig.do(t, http.MethodPost, "/api/v1/search", strings.NewReader(
		`{"baseDn":"ou=people,dc=alder,dc=test","scope":"sub","filter":"(uid=alice)"}`))
	out := decode[SearchResponse](t, res)

	if out.Command == nil || *out.Command == "" {
		t.Fatal("no command came back with the search")
	}
	cmd := *out.Command
	for _, want := range []string{"ldapsearch -x", "-W", "(uid=alice)"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the command is missing %q:\n%s", want, cmd)
		}
	}
	if strings.Contains(cmd, " -w ") {
		t.Errorf("the command carries a password inline:\n%s", cmd)
	}
}

// An error from the directory is the directory's, and saying so is different
// from claiming Alder broke.
func TestADirectoryFailureIsReportedAsUpstream(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps:      defaultCaps(),
		searchErr: errors.New("the server is on fire"),
	})

	res := rig.do(t, http.MethodPost, "/api/v1/search", strings.NewReader(
		`{"baseDn":"dc=alder,dc=test","scope":"sub","filter":"(objectClass=*)"}`))
	if res.Status < 500 {
		t.Errorf("got %d; a directory failure should not read as the caller's mistake",
			res.Status)
	}
	if body := res.Body; strings.Contains(body, "on fire") &&
		!strings.Contains(body, "upstream") {
		t.Errorf("the failure is not labelled as the directory's:\n%s", body)
	}
}

// Nothing matched is not the same as no such entry, and the export endpoints
// are where the distinction is easiest to get wrong.
func TestExportSeparatesAMissingBaseFromAnEmptyMatch(t *testing.T) {
	t.Run("no filter means the base is not there", func(t *testing.T) {
		rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps()})
		res := rig.do(t, http.MethodGet, "/api/v1/export/ldif?dn=dc%3Dalder%2Cdc%3Dtest", nil)
		if res.Status != fiber.StatusNotFound {
			t.Errorf("got %d, want 404", res.Status)
		}
	})

	t.Run("a filter means the base is there and holds nothing matching", func(t *testing.T) {
		rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps()})
		res := rig.do(t, http.MethodGet,
			"/api/v1/export/ldif?dn=dc%3Dalder%2Cdc%3Dtest&filter=%28cn%3Dnobody%29", nil)
		if res.Status != fiber.StatusBadRequest {
			t.Errorf("got %d, want 400 — the entry exists, the filter matched nothing",
				res.Status)
		}
	})
}

// The playbook export is the newest endpoint, and its whole reason for existing
// is that the tasks converge. A regression here is silent: the file still
// downloads, still parses, and no longer enforces anything.
func TestTheAnsibleExportProducesConvergingTasks(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), entries: []*directory.Entry{entryFixture(t)},
	})

	res := rig.do(t, http.MethodGet,
		"/api/v1/export/ansible?dn=ou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest&scope=sub", nil)
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d, want 200: %s", res.Status, res.Body)
	}
	body := res.Body

	for _, want := range []string{
		"community.general.ldap_entry",
		"community.general.ldap_attrs",
		"state: exact",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the playbook is missing %q", want)
		}
	}
	// No option exists to include a secret here, so none can appear.
	if strings.Contains(body, "averyrealsecret") {
		t.Error("a password hash reached the playbook")
	}
	if ct := res.Header.Get("Content-Disposition"); !strings.Contains(ct, ".yml") {
		t.Errorf("a playbook is offered as %q; it should download as .yml", ct)
	}
}

// The LDIF export offers the choice the playbook does not, and the default has
// to be the safe one.
func TestTheLdifExportOmitsSecretsUnlessAsked(t *testing.T) {
	target := "/api/v1/export/ldif?dn=ou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest&scope=sub"

	t.Run("by default", func(t *testing.T) {
		rig := newRig(t, Config{}, &fakeSession{
			caps: defaultCaps(), entries: []*directory.Entry{entryFixture(t)},
		})
		if body := rig.do(t, http.MethodGet, target, nil).Body; strings.Contains(body, "averyrealsecret") {
			t.Error("a password hash was exported without being asked for")
		}
	})

	t.Run("when explicitly asked for", func(t *testing.T) {
		rig := newRig(t, Config{}, &fakeSession{
			caps: defaultCaps(), entries: []*directory.Entry{entryFixture(t)},
		})
		body := rig.do(t, http.MethodGet, target+"&includeSensitive=true", nil).Body
		if !strings.Contains(body, "averyrealsecret") {
			t.Error("includeSensitive was set and the hash was still withheld")
		}
	})
}

// referencedByFilter and the references endpoint answer the same question, and
// the entry view is where the browser reads the first one.
func TestTheEntryViewCarriesTheReferencedByFilter(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), entry: entryFixture(t)})

	res := rig.do(t, http.MethodGet,
		"/api/v1/entry?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest", nil)
	out := decode[EntryView](t, res)

	if out.ReferencedByFilter == nil || *out.ReferencedByFilter == "" {
		t.Fatal("no referenced-by filter on the entry view")
	}
	if !strings.Contains(*out.ReferencedByFilter, "uid=alice") {
		t.Errorf("the filter does not name the subject: %q", *out.ReferencedByFilter)
	}
}

// The capabilities the driver read have to reach the browser, which is the
// mapping convert_wire_test guards in the other direction — here through HTTP.
func TestTheSessionEndpointReportsCapabilities(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps()})

	res := rig.do(t, http.MethodGet, "/api/v1/session", nil)
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d, want 200", res.Status)
	}
	out := decode[SessionInfo](t, res)

	if out.Capabilities.SubschemaSubentry != "cn=subschema" {
		t.Errorf("subschema is %q", out.Capabilities.SubschemaSubentry)
	}
	if !out.Capabilities.Paging {
		t.Error("paging was announced by the server and did not reach the browser")
	}
	if out.BindDn == nil || *out.BindDn != "cn=admin,dc=alder,dc=test" {
		t.Errorf("bind DN is %v", out.BindDn)
	}
}

// The bind password is held in the session, one struct field away from every
// response that describes the connection.
//
// This asserts on the secret itself rather than on field names: "passwordModify"
// is a capability the browser needs, so matching names would be both noisy and
// weaker than looking for the value that must never appear.
func TestTheBindPasswordNeverReachesAResponse(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), entry: entryFixture(t),
		entries: []*directory.Entry{entryFixture(t)},
	})

	for _, target := range []string{
		"/api/v1/session",
		"/api/v1/entry?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest",
		"/api/v1/export/ldif?dn=ou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest&scope=sub&includeSensitive=true",
		"/api/v1/export/ansible?dn=ou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest&scope=sub",
	} {
		t.Run(target, func(t *testing.T) {
			if body := rig.do(t, http.MethodGet, target, nil).Body; strings.Contains(body, sentinelPassword) {
				t.Errorf("the bind password reached the response:\n%s", body)
			}
		})
	}
}

// comparePair is two different entries the fake can tell apart.
func comparePair(t *testing.T) map[string]*directory.Entry {
	t.Helper()
	left := entryFixture(t)
	right := directory.NewEntry(mustParse(t, "uid=bob,ou=people,dc=alder,dc=test"))
	right.Set("objectClass", [][]byte{[]byte("top"), []byte("inetOrgPerson")})
	right.Set("cn", [][]byte{[]byte("Bob Adler")})
	right.Set("sn", [][]byte{[]byte("Adler")})
	right.Set("uid", [][]byte{[]byte("bob")})
	right.Set("mail", [][]byte{[]byte("bob@alder.test")})
	return map[string]*directory.Entry{
		strings.ToLower(left.DN.String()):  left,
		strings.ToLower(right.DN.String()): right,
	}
}

const compareTarget = "/api/v1/compare" +
	"?left=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest" +
	"&right=uid%3Dbob%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest"

// A handler that read one entry twice would compare it with itself and report
// no differences, which is a confident wrong answer rather than an error.
func TestCompareReadsBothDNs(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), byDN: comparePair(t)})

	res := rig.do(t, http.MethodGet, compareTarget, nil)
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d, want 200: %s", res.Status, res.Body)
	}
	if len(rig.fake.readDNs) < 2 {
		t.Fatalf("the handler read %v", rig.fake.readDNs)
	}
	if strings.EqualFold(rig.fake.readDNs[0], rig.fake.readDNs[1]) {
		t.Errorf("both reads were for %q; the handler compared an entry with itself",
			rig.fake.readDNs[0])
	}

	out := decode[EntryComparison](t, res)
	if out.Counts.Differs == 0 {
		t.Error("two different entries reported no differences at all")
	}
}

// Two DNs means twice the usual exposure to rule 2, and the refusal has to say
// which side was wrong or the caller cannot fix it.
func TestCompareRejectsAnUnparseableDN(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), byDN: comparePair(t)})

	res := rig.do(t, http.MethodGet,
		"/api/v1/compare?left=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest"+
			"&right=not%3Da%3Bdn%3D", nil)
	if res.Status != fiber.StatusBadRequest {
		t.Errorf("got %d, want 400 for a malformed right-hand DN", res.Status)
	}
}

// The standing rule, on the newest surface — and this one compares two entries,
// so it has two chances to leak.
func TestCompareNeverShipsAPasswordHash(t *testing.T) {
	pair := comparePair(t)
	for _, e := range pair {
		e.Set("userPassword", [][]byte{[]byte("{SSHA}averyrealsecret")})
	}
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), byDN: pair})

	body := rig.do(t, http.MethodGet, compareTarget, nil).Body
	if strings.Contains(body, "averyrealsecret") {
		t.Errorf("a password hash reached the comparison:\n%s", body)
	}
	if strings.Contains(body, sentinelPassword) {
		t.Error("the bind password reached the comparison")
	}
	// Presence is still reported: that a password is set is not the secret.
	if !strings.Contains(body, "userPassword") {
		t.Error("userPassword vanished entirely rather than being withheld")
	}
}
