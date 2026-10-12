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

// --detailed-exitcode lets a CI job ask "does this environment match the
// project?" from the exit status alone. Without it nothing changes: the
// default is part of the 1.x promise.
func TestProjectPlanDetailedExitcode(t *testing.T) {
	unchanged := map[string]any{"index": 0, "dn": "uid=alice,ou=people,dc=alder,dc=test", "action": "unchanged", "exists": true}
	conflict := map[string]any{
		"index": 1, "dn": "uid=bob,ou=people,dc=alder,dc=test", "action": "conflict", "exists": true,
		"problem": map[string]any{"code": "entry_exists"},
	}
	for _, tc := range []struct {
		name string
		plan func(t *testing.T) string
		args []string
		env  map[string]string
		want int
	}{
		{"changes pending, without the flag, is 0 as before",
			func(t *testing.T) string { return planJSON(t, counts{modify: 1}, exactModify("b0")) }, nil, nil, ExitOK},
		{"nothing to change is 0",
			func(t *testing.T) string { return planJSON(t, counts{unchanged: 1}, unchanged) },
			[]string{"--detailed-exitcode"}, nil, ExitOK},
		{"changes pending is 2",
			func(t *testing.T) string { return planJSON(t, counts{modify: 1}, exactModify("b0")) },
			[]string{"--detailed-exitcode"}, nil, ExitPending},
		{"changes pending is 2 with --json too",
			func(t *testing.T) string { return planJSON(t, counts{modify: 1}, exactModify("b0")) },
			[]string{"--detailed-exitcode", "--json"}, nil, ExitPending},
		{"a change that cannot be applied is 3, and wins over pending changes",
			func(t *testing.T) string {
				return planJSON(t, counts{modify: 1, conflict: 1}, exactModify("b0"), conflict)
			},
			[]string{"--detailed-exitcode"}, nil, ExitNotApplicable},
		{"ALDER_DETAILED_EXITCODE is not read",
			func(t *testing.T) string { return planJSON(t, counts{modify: 1}, exactModify("b0")) },
			nil, map[string]string{"ALDER_DETAILED_EXITCODE": "true"}, ExitOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub(t)
			s.reply("POST /api/v1/plan", http.StatusOK, tc.plan(t))
			dir := writeProject(t, s, nil)
			opts := projectRun
			opts.env = map[string]string{"ALDER_DEV_BIND_PASSWORD": testSecret}
			for k, v := range tc.env {
				opts.env[k] = v
			}
			args := append([]string{"project", "plan", "--project", dir, "--env", "dev"}, tc.args...)
			r := s.run(t, opts, args...)
			expectCode(t, r, tc.want)
			if strings.Contains(strings.Join(tc.args, " "), "--json") {
				// Exit 2 sets a status; it is not an error document.
				doc := oneJSONDocument(t, r.stdout)
				if _, isError := doc["error"]; isError {
					t.Errorf("--detailed-exitcode wrote an error document: %v", doc)
				}
			}
		})
	}
}

// projectApplyStub answers a desired-state plan of one modification, and an
// apply of it.
func projectApplyStub(t *testing.T, plan string) *stub {
	t.Helper()
	s := newStub(t)
	s.reply("POST /api/v1/plan", http.StatusOK, plan)
	s.reply("POST /api/v1/changeset/apply", http.StatusOK,
		`{"appliedCount":1,"outcomes":[{"index":0,"dn":"uid=alice,ou=people,dc=alder,dc=test","applied":true,"summary":"modified"}]}`)
	return s
}

func desiredModify(baseline string) map[string]any {
	item := exactModify(baseline)
	item["intent"] = "desired"
	return item
}

func TestProjectApplyAsksNamingTheEnvironmentAndTheDefaultIsNo(t *testing.T) {
	s := projectApplyStub(t, planJSON(t, counts{modify: 1}, desiredModify("b0")))
	dir := writeProject(t, s, nil)
	opts := projectRun
	opts.interactive, opts.stdin = true, "\n"
	r := s.run(t, opts, "project", "apply", "--project", dir, "--env", "dev")
	expectCode(t, r, ExitNotConfirmed)
	if !strings.Contains(r.stderr, "Apply 1 change to environment dev (ldap.dev.test:636)? [y/N]") {
		t.Errorf("the question does not name the environment and the directory:\n%s", r.stderr)
	}
	if len(s.called("POST /api/v1/changeset/apply")) != 0 {
		t.Error("applied after the answer was no")
	}
}

func TestProjectApplyAppliesThePlanThatWasShown(t *testing.T) {
	s := projectApplyStub(t, planJSON(t, counts{modify: 1}, desiredModify("token-from-the-plan")))
	dir := writeProject(t, s, nil)
	opts := projectRun
	opts.interactive, opts.stdin = true, "y\n"
	r := s.run(t, opts, "project", "apply", "--project", dir, "--env", "dev")
	expectCode(t, r, ExitOK)
	if !strings.HasPrefix(r.stdout, "Environment dev (ldap.dev.test:636)\n") || !strings.Contains(r.stdout, "Applied 1 change.") {
		t.Errorf("stdout:\n%s", r.stdout)
	}
	if !strings.Contains(r.stderr, "applying "+filepath.Join(dir, "alder.yaml")+" to environment dev") {
		t.Errorf("stderr does not say what is being applied where:\n%s", r.stderr)
	}
	if n := len(s.called("POST /api/v1/plan")); n != 1 {
		t.Errorf("planned %d times; the plan shown is the plan applied", n)
	}
	var req map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/plan")[0].Body, &req)
	if req["mode"] != "desired" {
		t.Errorf("planned in mode %v, want desired", req["mode"])
	}
	var sent struct {
		Changes []struct {
			Baseline string `json:"baseline"`
		} `json:"changes"`
	}
	_ = json.Unmarshal(s.called("POST /api/v1/changeset/apply")[0].Body, &sent)
	if len(sent.Changes) != 1 || sent.Changes[0].Baseline != "token-from-the-plan" {
		t.Fatalf("did not apply the planned change with its baseline: %s", s.called("POST /api/v1/changeset/apply")[0].Body)
	}
}

// Without a terminal and without --yes, nothing is even sent: a run that
// will not be allowed to apply has no business opening a session.
func TestProjectApplyWithoutATerminalNeedsYesAndConnectsToNothing(t *testing.T) {
	s := projectApplyStub(t, planJSON(t, counts{modify: 1}, desiredModify("b0")))
	dir := writeProject(t, s, nil)
	opts := projectRun
	opts.env = map[string]string{"ALDER_DEV_BIND_PASSWORD": testSecret, "ALDER_YES": "true", "ALDER_ENV": "dev"}
	r := s.run(t, opts, "project", "apply", "--project", dir, "--env", "dev")
	expectCode(t, r, ExitNotConfirmed)
	if len(s.calls) != 0 {
		t.Errorf("%d request(s) sent; ALDER_YES must not confirm, and nothing should be sent unconfirmable", len(s.calls))
	}
	r = s.run(t, opts, "project", "apply", "--project", dir)
	expectCode(t, r, ExitUsage) // ALDER_ENV is not read either
}

func TestProjectApplyWithYesAndJSON(t *testing.T) {
	s := projectApplyStub(t, planJSON(t, counts{modify: 1}, desiredModify("b0")))
	dir := writeProject(t, s, nil)
	r := s.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--yes", "--json")
	expectCode(t, r, ExitOK)
	doc := oneJSONDocument(t, r.stdout)
	if doc["environment"] != "dev" || doc["host"] != "ldap.dev.test:636" {
		t.Errorf("environment = %v, host = %v", doc["environment"], doc["host"])
	}
	if doc["plan"] == nil || doc["result"] == nil {
		t.Errorf("want the plan and the result beside them: %v", doc)
	}
}

func TestProjectApplyOutcomes(t *testing.T) {
	unchanged := map[string]any{"index": 0, "dn": "uid=alice,ou=people,dc=alder,dc=test", "action": "unchanged", "exists": true, "intent": "desired"}
	conflict := map[string]any{"index": 0, "dn": "uid=alice,ou=people,dc=alder,dc=test", "action": "conflict", "exists": true,
		"intent": "desired", "problem": map[string]any{"code": "entry_exists"}}
	for _, tc := range []struct {
		name      string
		plan      func(t *testing.T) string
		refusal   string
		want      int
		wantApply bool
	}{
		{"nothing to apply", func(t *testing.T) string { return planJSON(t, counts{unchanged: 1}, unchanged) }, "", ExitOK, false},
		{"a change that cannot be applied", func(t *testing.T) string { return planJSON(t, counts{conflict: 1}, conflict) }, "", ExitNotApplicable, false},
		{"the directory moved since the plan", func(t *testing.T) string { return planJSON(t, counts{modify: 1}, desiredModify("b0")) },
			apiErrorJSON("conflict", "The directory changed.", map[string]any{"cause": "plan_stale",
				"affected": []any{map[string]any{"index": 0, "dn": "uid=alice,ou=people,dc=alder,dc=test"}}}), ExitStale, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := projectApplyStub(t, tc.plan(t))
			if tc.refusal != "" {
				s.reply("POST /api/v1/changeset/apply", http.StatusConflict, tc.refusal)
			}
			dir := writeProject(t, s, nil)
			r := s.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--yes")
			expectCode(t, r, tc.want)
			if applied := len(s.called("POST /api/v1/changeset/apply")) > 0; applied != tc.wantApply {
				t.Errorf("apply sent = %v, want %v", applied, tc.wantApply)
			}
			// A stale plan is never planned again and applied unseen.
			if n := len(s.called("POST /api/v1/plan")); n != 1 {
				t.Errorf("planned %d times", n)
			}
		})
	}
}

func TestAnInvalidProjectIsNotApplied(t *testing.T) {
	s := projectApplyStub(t, planJSON(t, counts{modify: 1}, desiredModify("b0")))
	dir := writeProject(t, s, map[string]string{"people/bob.ldif": projectBob + "userPassword: x\n"})
	r := s.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--yes")
	expectCode(t, r, ExitNotApplicable)
	if len(s.calls) != 0 {
		t.Errorf("%d request(s) sent for a project that is not valid", len(s.calls))
	}
}

func TestProjectApplyUsageErrors(t *testing.T) {
	s := projectApplyStub(t, planJSON(t, counts{modify: 1}, desiredModify("b0")))
	dir := writeProject(t, s, nil)
	for _, args := range [][]string{
		{"--env", "dev", "--force", "--yes"},
		{"--env", "dev", "--recovery-out", "-", "--yes"},
		{"--env", "staging", "--yes"},
	} {
		r := s.run(t, projectRun, append([]string{"project", "apply", "--project", dir}, args...)...)
		expectCode(t, r, ExitUsage)
	}
	if len(s.calls) != 0 {
		t.Errorf("%d request(s) sent for command lines that were wrong", len(s.calls))
	}
}

func TestProjectFmtRewritesOnlyWhatIsNotCanonical(t *testing.T) {
	s := newStub(t)
	messy := "# Alice, by hand.\r\ndn: uid=alice, ou=people, dc=alder, dc=test\r\nobjectClass:inetOrgPerson\r\nuid: alice\r\n" +
		"cn: Alice\r\nsn:: QQ==\r\n\r\n\r\n"
	dir := writeProject(t, s, map[string]string{"people/alice.ldif": messy})
	alice := filepath.Join(dir, "people", "alice.ldif")
	if err := os.Chmod(alice, 0o640); err != nil {
		t.Fatal(err)
	}
	// What the file holds after the chmod, which on Windows is not 0640.
	before, err := os.Stat(alice)
	if err != nil {
		t.Fatal(err)
	}

	r := s.run(t, projectRun, "project", "fmt", "--project", dir, "--check")
	expectCode(t, r, ExitDifferences)
	if r.stdout != alice+"\n" {
		t.Errorf("--check stdout = %q, want only the file that is not canonical", r.stdout)
	}
	if got, _ := os.ReadFile(alice); string(got) != messy {
		t.Errorf("--check wrote to the file:\n%q", got)
	}

	r = s.run(t, projectRun, "project", "fmt", "--project", dir)
	expectCode(t, r, ExitOK)
	if r.stdout != alice+"\n" {
		t.Errorf("stdout = %q, want the file it rewrote", r.stdout)
	}
	got, _ := os.ReadFile(alice)
	if want := "# Alice, by hand.\n" + projectAlice; string(got) != want {
		t.Errorf("formatted file =\n%q\nwant\n%q", got, want)
	}
	if info, _ := os.Stat(alice); info.Mode().Perm() != before.Mode().Perm() {
		t.Errorf("mode = %v, was %v; formatting must keep the file's permissions", info.Mode().Perm(), before.Mode().Perm())
	}
	if len(s.calls) != 0 {
		t.Errorf("fmt sent %d request(s); it must connect to nothing", len(s.calls))
	}

	r = s.run(t, projectRun, "project", "fmt", "--project", dir, "--check")
	expectCode(t, r, ExitOK)
	if r.stdout != "" {
		t.Errorf("a canonical project still lists files: %q", r.stdout)
	}
}

func TestProjectFmtLeavesAnInvalidProjectAlone(t *testing.T) {
	s := newStub(t)
	messy := "dn: uid=alice,ou=people,dc=alder,dc=test\nobjectClass:inetOrgPerson\nuid: alice\ncn: Alice\nsn: A\n"
	dir := writeProject(t, s, map[string]string{
		"people/alice.ldif": messy,
		"people/bob.ldif":   projectBob + "userPassword: {SSHA}x\n",
	})
	r := s.run(t, projectRun, "project", "fmt", "--project", dir)
	expectCode(t, r, ExitNotApplicable)
	if got, _ := os.ReadFile(filepath.Join(dir, "people", "alice.ldif")); string(got) != messy {
		t.Errorf("a file of an invalid project was rewritten:\n%q", got)
	}
}

// A symlink inside the project is written through, so it stays a link and
// its target takes the canonical form.
func TestProjectFmtWritesThroughASymlink(t *testing.T) {
	s := newStub(t)
	dir := writeProject(t, s, map[string]string{"shared/alice.ldif": "dn: uid=alice,ou=people,dc=alder,dc=test\nobjectClass:inetOrgPerson\nuid: alice\ncn: Alice\nsn: A\n"})
	link := filepath.Join(dir, "people", "alice.ldif")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "shared", "alice.ldif"), link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	r := s.run(t, projectRun, "project", "fmt", "--project", dir)
	expectCode(t, r, ExitOK)
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced by a file: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "shared", "alice.ldif")); string(got) != projectAlice {
		t.Errorf("target =\n%q", got)
	}
}
