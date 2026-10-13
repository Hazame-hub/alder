package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// writeSuffixProject is writeProject with the files written under
// dc=alder,dc=test and the dev environment under dc=dev,dc=alder,dc=test.
// Alice's manager is Bob, a name; her description only looks like one.
func writeSuffixProject(t *testing.T, s *stub) string {
	t.Helper()
	return writeProject(t, s, map[string]string{
		"alder.yaml": `version: 1
suffix: dc=alder,dc=test
managed:
  - base: ou=people,dc=alder,dc=test
    files: [people/*.ldif]
environments:
  dev:
    api-url: ` + s.srv.URL + `
    host: ldap.dev.test
    bind-dn: cn=alder,ou=services,dc=dev,dc=alder,dc=test
    bind-password-env: ALDER_DEV_BIND_PASSWORD
    suffix: dc=dev,dc=alder,dc=test
  prod:
    api-url: ` + s.srv.URL + `
    host: ldap.prod.test
    bind-dn: cn=alder,ou=services,dc=alder,dc=test
    bind-password-env: ALDER_DEV_BIND_PASSWORD
`,
		"people/alice.ldif": projectAlice + "manager: uid=bob,ou=people,dc=alder,dc=test\n" +
			"description: uid=bob,ou=people,dc=alder,dc=test\n",
	})
}

// stubSchema is GET /schema as Alder answers it, reduced to the attribute
// types the project uses: the syntax is what decides, never the name.
func stubSchema(s *stub) {
	const dirString, dnSyntax, oid = "1.3.6.1.4.1.1466.115.121.1.15", "1.3.6.1.4.1.1466.115.121.1.12", "1.3.6.1.4.1.1466.115.121.1.38"
	types := []map[string]any{
		{"name": "objectClass", "oid": "2.5.4.0", "syntax": oid},
		{"name": "uid", "names": []string{"uid", "userid"}, "oid": "0.9.2342.19200300.100.1.1", "syntax": dirString},
		{"name": "cn", "oid": "2.5.4.3", "syntax": dirString},
		{"name": "sn", "oid": "2.5.4.4", "syntax": dirString},
		{"name": "description", "oid": "2.5.4.13", "syntax": dirString},
		{"name": "manager", "oid": "0.9.2342.19200300.100.1.10", "syntax": dnSyntax},
	}
	body, _ := json.Marshal(map[string]any{"subschemaDn": "cn=Subschema", "counts": map[string]any{}, "attributeTypes": types})
	s.reply("GET /api/v1/schema", http.StatusOK, string(body))
}

func TestAProjectPlanForAnEnvironmentWithItsOwnSuffix(t *testing.T) {
	s := newStub(t)
	stubSchema(s)
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
	s.reply("POST /api/v1/snapshots/capture", http.StatusOK, snapshotOf("uid=alice,ou=people,dc=dev,dc=alder,dc=test"))
	dir := writeSuffixProject(t, s)

	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--detailed-exitcode")
	expectCode(t, r, ExitPending)
	if !strings.Contains(r.stderr, "alder: environment dev names the suffix dc=dev,dc=alder,dc=test: "+
		"the project's dc=alder,dc=test is rebased onto it\n") {
		t.Errorf("stderr does not announce the rebase:\n%s", r.stderr)
	}
	if !strings.HasPrefix(r.stdout, "Environment dev (ldap.dev.test:636)\n"+
		"Suffix dc=alder,dc=test -> dc=dev,dc=alder,dc=test: 2 entries and 1 DN value rebased from the files\n\n") {
		t.Errorf("stdout does not say the plan was rebased:\n%s", r.stdout)
	}
	if !strings.Contains(r.stderr, "description of uid=alice,ou=people,dc=dev,dc=alder,dc=test reads as a DN below "+
		"dc=alder,dc=test and was left as written") {
		t.Errorf("stderr does not name the value left as written:\n%s", r.stderr)
	}

	var req map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/plan")[0].Body, &req)
	ldifText, _ := req["ldif"].(string)
	for _, want := range []string{
		"dn: uid=alice,ou=people,dc=dev,dc=alder,dc=test\n",
		"dn: uid=bob,ou=people,dc=dev,dc=alder,dc=test\n",
		"manager: uid=bob,ou=people,dc=dev,dc=alder,dc=test\n",
		"description: uid=bob,ou=people,dc=alder,dc=test\n",
	} {
		if !strings.Contains(ldifText, want) {
			t.Errorf("the plan request lacks %q:\n%s", want, ldifText)
		}
	}
	// The unmanaged report reads the environment's subtree, and compares
	// with the environment's names: Bob is described, and only absent.
	var capture map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/snapshots/capture")[0].Body, &capture)
	if capture["base"] != "ou=people,dc=dev,dc=alder,dc=test" {
		t.Errorf("captured %v, want the rebased base", capture["base"])
	}
	if !strings.HasSuffix(r.stdout, "Unmanaged: none. Every entry in the managed subtrees is one the project describes.\n") {
		t.Errorf("stdout:\n%s", r.stdout)
	}

	r = s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--json")
	expectCode(t, r, ExitOK)
	doc := oneJSONDocument(t, r.stdout)
	rebase, _ := doc["rebase"].(map[string]any)
	left, _ := rebase["leftAsWritten"].([]any)
	if rebase["from"] != "dc=alder,dc=test" || rebase["to"] != "dc=dev,dc=alder,dc=test" ||
		rebase["entries"] != float64(2) || rebase["values"] != float64(1) || len(left) != 1 {
		t.Errorf("rebase = %v", doc["rebase"])
	}
	if sub := doc["unmanaged"].([]any)[0].(map[string]any); sub["base"] != "ou=people,dc=dev,dc=alder,dc=test" {
		t.Errorf("unmanaged[0].base = %v", sub["base"])
	}
}

// An environment without a suffix plans the files as written, and needs no
// schema to do it.
func TestAProjectPlanWithoutAnEnvironmentSuffixIsAsWritten(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
	dir := writeSuffixProject(t, s)
	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "prod", "--json")
	expectCode(t, r, ExitOK)
	if _, ok := oneJSONDocument(t, r.stdout)["rebase"]; ok {
		t.Error("a plan as written carries a rebase")
	}
	if n := len(s.called("GET /api/v1/schema")); n != 0 {
		t.Errorf("read the schema %d times for a plan that moves nothing", n)
	}
	var req map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/plan")[0].Body, &req)
	if ldifText, _ := req["ldif"].(string); strings.Contains(ldifText, "dc=dev") {
		t.Errorf("prod was rebased:\n%s", ldifText)
	}
}

// A type the directory's schema does not define cannot be said to hold DNs
// or not, so nothing is planned rather than guessed at.
func TestAProjectThatCannotBeRebasedIsNotPlanned(t *testing.T) {
	s := newStub(t)
	s.reply("GET /api/v1/schema", http.StatusOK, `{"subschemaDn":"cn=Subschema","counts":{},"attributeTypes":[]}`)
	dir := writeSuffixProject(t, s)
	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev")
	expectCode(t, r, ExitNotApplicable)
	if !strings.Contains(r.stderr, "the directory's schema does not define") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	if n := len(s.called("POST /api/v1/plan")); n != 0 {
		t.Errorf("planned %d times", n)
	}
}

func TestAProjectApplyForAnEnvironmentWithItsOwnSuffix(t *testing.T) {
	s := projectApplyStub(t, planJSON(t, counts{modify: 1}, desiredModify("b0")))
	stubSchema(s)
	dir := writeSuffixProject(t, s)
	r := s.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--yes", "--json")
	expectCode(t, r, ExitOK)
	doc := oneJSONDocument(t, r.stdout)
	if rebase, _ := doc["rebase"].(map[string]any); rebase["to"] != "dc=dev,dc=alder,dc=test" {
		t.Errorf("rebase = %v", doc["rebase"])
	}
	if !strings.Contains(r.stderr, "Suffix dc=alder,dc=test -> dc=dev,dc=alder,dc=test") {
		t.Errorf("stderr does not say the plan was rebased:\n%s", r.stderr)
	}
	var req map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/plan")[0].Body, &req)
	if ldifText, _ := req["ldif"].(string); !strings.Contains(ldifText, "dn: uid=alice,ou=people,dc=dev,dc=alder,dc=test\n") {
		t.Errorf("applied the files as written:\n%s", ldifText)
	}
}

func TestProjectValidateListsTheEnvironmentsThatRebase(t *testing.T) {
	s := newStub(t)
	dir := writeSuffixProject(t, s)
	r := s.run(t, projectRun, "project", "validate", "--project", dir)
	expectCode(t, r, ExitOK)
	if !strings.HasSuffix(r.stdout, "environments: dev, prod\n  environment dev: suffix dc=alder,dc=test -> dc=dev,dc=alder,dc=test\n") {
		t.Errorf("stdout:\n%s", r.stdout)
	}
	if len(s.calls) != 0 {
		t.Errorf("validate sent %d request(s)", len(s.calls))
	}
}
