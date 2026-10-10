package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const projectAlice = `dn: uid=alice,ou=people,dc=alder,dc=test
objectClass: inetOrgPerson
uid: alice
cn: Alice
sn: A
`

const projectBob = `dn: uid=bob,ou=people,dc=alder,dc=test
objectClass: inetOrgPerson
uid: bob
cn: Bob
sn: B
`

// writeProject lays out a two-entry project whose dev environment points at
// the stub, and returns its directory.
func writeProject(t *testing.T, s *stub, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"alder.yaml": `version: 1
managed:
  - base: ou=people,dc=alder,dc=test
    files: [people/*.ldif]
environments:
  dev:
    api-url: ` + s.srv.URL + `
    host: ldap.dev.test
    bind-dn: cn=alder,ou=services,dc=alder,dc=test
    bind-password-env: ALDER_DEV_BIND_PASSWORD
  prod:
    api-url: https://alder.example.invalid
    host: ldap.prod.test
    bind-dn: cn=alder,ou=services,dc=alder,dc=test
    bind-password-env: ALDER_PROD_BIND_PASSWORD
`,
		"people/alice.ldif": projectAlice,
		"people/bob.ldif":   projectBob,
	}
	for k, v := range extra {
		files[k] = v
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Project commands take their connection from alder.yaml, never from the
// connection flags the harness otherwise adds.
var projectRun = runOpts{noConnection: true, env: map[string]string{"ALDER_DEV_BIND_PASSWORD": testSecret}}

func TestProjectValidateNeedsNoServer(t *testing.T) {
	s := newStub(t)
	dir := writeProject(t, s, nil)
	r := s.run(t, projectRun, "project", "validate", "--project", dir)
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stdout, "valid. 2 entries in 1 managed subtree; environments: dev, prod") {
		t.Errorf("stdout:\n%s", r.stdout)
	}
	if len(s.calls) != 0 {
		t.Errorf("validate sent %d request(s); it must connect to nothing", len(s.calls))
	}
}

func TestProjectValidateListsProblemsAndExitsThree(t *testing.T) {
	s := newStub(t)
	dir := writeProject(t, s, map[string]string{"people/bob.ldif": projectBob + "userPassword: {SSHA}x\n"})
	r := s.run(t, projectRun, "project", "validate", "--project", filepath.Join(dir, "alder.yaml"))
	expectCode(t, r, ExitNotApplicable)
	if !strings.Contains(r.stderr, "people/bob.ldif:1: uid=bob,ou=people,dc=alder,dc=test carries userPassword, a secret") {
		t.Errorf("stderr does not locate the secret:\n%s", r.stderr)
	}
}

func TestProjectPlanSendsTheProjectAsDesiredStateAndNamesTheEnvironment(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
	dir := writeProject(t, s, nil)

	// A different password in the generic variable, which must not be used:
	// each environment names its own.
	opts := projectRun
	opts.env = map[string]string{"ALDER_DEV_BIND_PASSWORD": testSecret, "ALDER_BIND_PASSWORD": "the-wrong-one"}
	r := s.run(t, opts, "project", "plan", "--project", dir, "--env", "dev")
	expectCode(t, r, ExitOK)

	if !strings.Contains(r.stderr, "for environment dev: ldap.dev.test:636 through "+s.srv.URL) {
		t.Errorf("stderr does not say which environment and directory:\n%s", r.stderr)
	}
	if !strings.HasPrefix(r.stdout, "Environment dev (ldap.dev.test:636)\n") {
		t.Errorf("stdout does not start with the environment:\n%s", r.stdout)
	}

	var connect map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/session")[0].Body, &connect)
	if connect["host"] != "ldap.dev.test" || connect["bindPassword"] != testSecret {
		t.Errorf("session request did not use the dev environment and its password variable: host=%v", connect["host"])
	}

	var req map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/plan")[0].Body, &req)
	ldifText, _ := req["ldif"].(string)
	if req["mode"] != "desired" || req["reconcile"] != false {
		t.Errorf("plan request mode = %v, reconcile = %v", req["mode"], req["reconcile"])
	}
	alice, bob := strings.Index(ldifText, "dn: uid=alice"), strings.Index(ldifText, "dn: uid=bob")
	if alice < 0 || bob < 0 || alice > bob {
		t.Errorf("the plan request is not the project's entries in order:\n%s", ldifText)
	}
}

func TestProjectPlanJSONCarriesTheEnvironmentBesideThePlan(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
	dir := writeProject(t, s, nil)
	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--json")
	expectCode(t, r, ExitOK)
	doc := oneJSONDocument(t, r.stdout)
	if doc["environment"] != "dev" || doc["host"] != "ldap.dev.test:636" {
		t.Errorf("environment = %v, host = %v", doc["environment"], doc["host"])
	}
	if _, ok := doc["plan"].(map[string]any); !ok {
		t.Errorf("no plan beside them: %v", doc)
	}
}

// --env is the choice of which directory to act on, and an inherited
// environment variable must not make it.
func TestProjectPlanIgnoresALDER_ENV(t *testing.T) {
	s := newStub(t)
	dir := writeProject(t, s, nil)
	opts := projectRun
	opts.env = map[string]string{"ALDER_DEV_BIND_PASSWORD": testSecret, "ALDER_ENV": "dev", "ALDER_PROJECT": dir}
	r := s.run(t, opts, "project", "plan", "--project", dir)
	expectCode(t, r, ExitUsage)
	if !strings.Contains(r.stderr, "--env is required") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	if len(s.calls) != 0 {
		t.Errorf("%d request(s) sent for a command with no --env", len(s.calls))
	}
}

func TestProjectPlanNamesTheEnvironmentsItHas(t *testing.T) {
	s := newStub(t)
	dir := writeProject(t, s, nil)
	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "staging")
	expectCode(t, r, ExitUsage)
	if !strings.Contains(r.stderr, `no environment "staging"; it has dev, prod`) {
		t.Errorf("stderr:\n%s", r.stderr)
	}
}

func TestAnInvalidProjectIsNotPlanned(t *testing.T) {
	s := newStub(t)
	dir := writeProject(t, s, map[string]string{"people/bob.ldif": "dn: uid=bob,ou=people,dc=alder,dc=test\nchangetype: delete\n"})
	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev")
	expectCode(t, r, ExitNotApplicable)
	if len(s.calls) != 0 {
		t.Errorf("%d request(s) sent for a project that is not valid", len(s.calls))
	}
}

func TestAProjectPlanThatCannotBeAppliedExitsThree(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{conflict: 1}, map[string]any{
		"index": 0, "dn": "uid=alice,ou=people,dc=alder,dc=test", "action": "conflict", "exists": true,
		"problem": map[string]any{"code": "entry_exists"},
	}))
	dir := writeProject(t, s, nil)
	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev")
	expectCode(t, r, ExitNotApplicable)
}
