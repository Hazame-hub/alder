package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// POST /project/plan, against a fake directory: the project is read by the
// client's own package from the files the request carried, planned by
// POST /plan's planner, and reported with its unmanaged entries -- against
// the session's directory, whatever alder.yaml says.

const projectYAML = `version: 1
managed:
  - base: ou=people,dc=alder,dc=test
    files: [people/*.ldif]
environments:
  prod:
    api-url: https://alder.attacker.example
    host: ldap.attacker.example
    ca-file: CA_FILE
    bind-dn: cn=alder,dc=alder,dc=test
    bind-password-file: PASSWORD_FILE
`

const projectAlice = `dn: uid=alice,ou=people,dc=alder,dc=test
objectClass: top
objectClass: person
objectClass: inetOrgPerson
uid: alice
cn: Alice Liddell
sn: Liddell
`

const projectBob = `dn: uid=bob,ou=people,dc=alder,dc=test
objectClass: top
objectClass: person
objectClass: inetOrgPerson
uid: bob
cn: Bob
sn: B
`

type sentFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func projectBody(t *testing.T, files ...sentFile) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"files": files})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func validProjectFiles() []sentFile {
	return []sentFile{
		{Path: "alder.yaml", Content: projectYAML},
		{Path: "people/b.ldif", Content: projectBob},
		{Path: "people/a.ldif", Content: projectAlice},
	}
}

// projectRig is a directory holding the people OU, Alice with an old cn, and
// Carol, whom no file describes.
func projectRig(t *testing.T, cfg Config) *testRig {
	t.Helper()
	ou := directory.NewEntry(mustParse(t, "ou=people,dc=alder,dc=test"))
	ou.Set("objectClass", [][]byte{[]byte("top"), []byte("organizationalUnit")})
	ou.Set("ou", [][]byte{[]byte("people")})
	alice := personAt(t, "uid=alice,ou=people,dc=alder,dc=test",
		[]string{"uid", "alice"}, []string{"cn", "Alice"}, []string{"sn", "Liddell"})
	carol := personAt(t, "uid=carol,ou=people,dc=alder,dc=test",
		[]string{"uid", "carol"}, []string{"cn", "Carol"}, []string{"sn", "C"})
	byDN := map[string]*directory.Entry{}
	for _, e := range []*directory.Entry{ou, alice, carol} {
		byDN[strings.ToLower(e.DN.String())] = e
	}
	return newRig(t, cfg, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t), byDN: byDN,
		entries: []*directory.Entry{ou, alice, carol},
	})
}

func TestAProjectIsPlannedAgainstTheSessionsDirectory(t *testing.T) {
	rig := projectRig(t, Config{})
	res := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, validProjectFiles()...)))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d\n%s", res.Status, res.Body)
	}
	got := decode[ProjectPlan](t, res)

	// The session's directory, never the environment's.
	if got.Host != "ldap.example.test:636" || got.BindDn != "cn=admin,dc=alder,dc=test" {
		t.Errorf("host = %q, bindDn = %q; want the session's", got.Host, got.BindDn)
	}
	if strings.Contains(res.Body, "attacker") {
		t.Errorf("a value from an environment reached the response:\n%s", res.Body)
	}

	if got.Project.File != "alder.yaml" || got.Project.Entries != 2 || len(got.Project.Subtrees) != 1 ||
		strings.Join(got.Project.Subtrees[0].Files, ",") != "people/a.ldif,people/b.ldif" ||
		strings.Join(got.Project.Environments, ",") != "prod" {
		t.Errorf("project = %+v", got.Project)
	}

	actions := map[string]PlanAction{}
	for _, item := range got.Plan.Items {
		actions[item.Dn] = item.Action
	}
	if actions["uid=alice,ou=people,dc=alder,dc=test"] != PlanActionModify ||
		actions["uid=bob,ou=people,dc=alder,dc=test"] != PlanActionAdd || len(actions) != 2 {
		t.Errorf("plan = %v", actions)
	}

	if len(got.Unmanaged) != 1 {
		t.Fatalf("unmanaged = %+v", got.Unmanaged)
	}
	u := got.Unmanaged[0]
	if u.Base != "ou=people,dc=alder,dc=test" || u.Error != nil ||
		strings.Join(u.Entries, "|") != "ou=people,dc=alder,dc=test|uid=carol,ou=people,dc=alder,dc=test" {
		t.Errorf("unmanaged = %+v", u)
	}

	// Planning writes nothing.
	if len(rig.fake.applied) != 0 {
		t.Errorf("planning a project wrote %d changes", len(rig.fake.applied))
	}
}

// The project's plan is POST /plan's plan of the same document, item for
// item: one planner, not a second one that happens to agree.
func TestAProjectsPlanIsThePlanOfItsDocument(t *testing.T) {
	rig := projectRig(t, Config{})
	res := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, validProjectFiles()...)))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d\n%s", res.Status, res.Body)
	}
	fromProject := decode[ProjectPlan](t, res).Plan

	document := projectAlice + "\n" + projectBob
	body, _ := json.Marshal(map[string]any{"ldif": document, "mode": "desired", "reconcile": false})
	fromPlan := planFor(t, rig, string(body))

	strip := func(p Plan) string {
		for i := range p.Items {
			p.Items[i].Baseline = nil
		}
		raw, _ := json.Marshal(p)
		return string(raw)
	}
	if a, b := strip(fromProject), strip(fromPlan); a != b {
		t.Errorf("the project's plan differs from POST /plan's:\n%s\n%s", a, b)
	}
}

func TestAnInvalidProjectListsEveryProblemAndPlansNothing(t *testing.T) {
	rig := projectRig(t, Config{})
	files := validProjectFiles()
	files[1].Content = projectBob + "userPassword: {SSHA}abc\n"
	files[2].Content = strings.Replace(projectAlice, "ou=people", "ou=elsewhere", 1)
	res := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, files...)))
	if res.Status != http.StatusBadRequest {
		t.Fatalf("status = %d\n%s", res.Status, res.Body)
	}
	got := decode[Error](t, res)
	if got.Error != ErrorErrorProjectInvalid || got.Problems == nil || len(*got.Problems) < 2 {
		t.Fatalf("error = %+v", got)
	}
	var secret bool
	for _, p := range *got.Problems {
		if p.File != nil && *p.File == "people/b.ldif" && p.Line != nil && *p.Line == 1 &&
			strings.Contains(p.Message, "carries userPassword, a secret") {
			secret = true
		}
	}
	if !secret {
		t.Errorf("the secret is not reported by file and line: %+v", *got.Problems)
	}
	if strings.Contains(res.Body, "{SSHA}abc") {
		t.Error("the secret's value is in the response")
	}
	if len(rig.fake.readDNs) != 0 {
		t.Errorf("an invalid project read the directory: %v", rig.fake.readDNs)
	}
}

// An environment's files are never read and its address never dialled: the
// fake has one session and nothing else, and a password file on the
// server's disk named by alder.yaml stays unread.
func TestAProjectsEnvironmentsAreValidatedAndNeverUsed(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(secretFile, []byte("SERVER-SIDE-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml := strings.NewReplacer("PASSWORD_FILE", secretFile, "CA_FILE", secretFile).Replace(projectYAML)

	rig := projectRig(t, Config{})
	files := validProjectFiles()
	files[0].Content = yaml
	res := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, files...)))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d\n%s", res.Status, res.Body)
	}
	if strings.Contains(res.Body, "SERVER-SIDE-SECRET") || strings.Contains(res.Body, secretFile) {
		t.Errorf("the response carries what a file on the server holds:\n%s", res.Body)
	}

	// Validated all the same: a bad environment refuses the project, as
	// alder project validate does.
	files[0].Content = strings.Replace(yaml, "    host: ldap.attacker.example\n", "", 1)
	res = rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, files...)))
	if res.Status != http.StatusBadRequest || !strings.Contains(res.Body, "host is required") {
		t.Errorf("an environment with no host: status %d\n%s", res.Status, res.Body)
	}
}

func TestAProjectsFilesMustBeCleanUniqueAndRooted(t *testing.T) {
	rig := projectRig(t, Config{})
	for name, files := range map[string][]sentFile{
		"a path that climbs":    append(validProjectFiles(), sentFile{Path: "../x.ldif", Content: "x"}),
		"an absolute path":      append(validProjectFiles(), sentFile{Path: "/etc/passwd", Content: "x"}),
		"a backslash":           append(validProjectFiles(), sentFile{Path: "people\\c.ldif", Content: "x"}),
		"the same path twice":   append(validProjectFiles(), sentFile{Path: "people/a.ldif", Content: projectAlice}),
		"no alder.yaml on top":  {{Path: "sub/alder.yaml", Content: projectYAML}, {Path: "people/a.ldif", Content: projectAlice}},
		"a file and a dir":      append(validProjectFiles(), sentFile{Path: "people", Content: "x"}),
		"no files at all":       {},
		"a dot path at the top": append(validProjectFiles(), sentFile{Path: "./people/c.ldif", Content: "x"}),
	} {
		t.Run(name, func(t *testing.T) {
			res := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, files...)))
			if res.Status != http.StatusBadRequest {
				t.Fatalf("status = %d\n%s", res.Status, res.Body)
			}
			if got := decode[Error](t, res); got.Error != ErrorErrorBadRequest {
				t.Errorf("error = %q, want bad_request: %s", got.Error, got.Message)
			}
		})
	}

	many := validProjectFiles()
	for i := len(many); i <= 1000; i++ {
		many = append(many, sentFile{Path: "extra/" + strings.Repeat("x", 1+i%40) + string(rune('a'+i%26)) + "-" +
			string(rune('a'+(i/26)%26)) + ".ldif"})
	}
	res := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, many...)))
	if res.Status != http.StatusBadRequest || !strings.Contains(res.Body, "at most 1000 files") {
		t.Errorf("1001 files: status %d\n%s", res.Status, res.Body)
	}
}

func TestAProjectNeedsASession(t *testing.T) {
	rig := projectRig(t, Config{})
	if res := rig.anonymous(t, http.MethodPost, "/api/v1/project/plan"); res.Status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.Status)
	}
}

// Nothing is written, so a read-only Alder plans a project like any other.
func TestAProjectPlansOnAReadOnlyAlder(t *testing.T) {
	rig := projectRig(t, Config{ReadOnly: true})
	res := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, validProjectFiles()...)))
	if res.Status != http.StatusOK {
		t.Errorf("status = %d\n%s", res.Status, res.Body)
	}
}

// A subtree read short -- here, referrals to entries held elsewhere -- is
// reported unreadable with the reason, never half listed, and the plan
// beside it stands.
func TestASubtreeThatCannotBeReadWholeIsReportedNotHalfListed(t *testing.T) {
	rig := projectRig(t, Config{})
	rig.fake.referrals = []string{"ldap://elsewhere.example/ou=people,dc=alder,dc=test"}
	res := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, validProjectFiles()...)))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d\n%s", res.Status, res.Body)
	}
	got := decode[ProjectPlan](t, res)
	u := got.Unmanaged[0]
	if u.Error == nil || !strings.Contains(*u.Error, "could not be read whole") || len(u.Entries) != 0 {
		t.Errorf("unmanaged = %+v", u)
	}
	if len(got.Plan.Items) != 2 {
		t.Errorf("the plan did not stand: %+v", got.Plan.Items)
	}
}

// A managed base the directory does not hold has nothing unmanaged in it,
// and no error: the plan is what creates it.
func TestAManagedBaseTheDirectoryLacksHasNothingUnmanaged(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), sch: testSchema(t), byDN: map[string]*directory.Entry{}})
	res := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, validProjectFiles()...)))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d\n%s", res.Status, res.Body)
	}
	u := decode[ProjectPlan](t, res).Unmanaged[0]
	if u.Error != nil || len(u.Entries) != 0 || u.Entries == nil {
		t.Errorf("unmanaged = %+v", u)
	}
}
