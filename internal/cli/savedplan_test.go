package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const projectAliceDN = "uid=alice,ou=people,dc=alder,dc=test"

// savedPlanStub answers project plans as a directory where alice's title
// reads "Before" and the project wants "After", and bob is to be added.
//
// A plan in desired mode is the project's plan; a plan of changes is a saved
// plan's, answered with exact, the third argument deciding whether it still
// holds.
func savedPlanStub(t *testing.T, savedHolds bool) *stub {
	t.Helper()
	s := newStub(t)
	stubAttributeNames(s)
	desired := planJSON(t, counts{add: 1, modify: 1},
		map[string]any{
			"index": 0, "dn": projectAliceDN, "action": "modify", "exists": true, "intent": "desired", "kind": "data",
			"record": map[string]any{"dn": projectAliceDN, "type": "modify", "mods": []any{
				map[string]any{"op": "replace", "name": "title", "values": []any{map[string]any{"text": "After"}}},
			}},
			"baseline": "desired-alice",
		},
		map[string]any{
			"index": 1, "dn": "uid=bob,ou=people,dc=alder,dc=test", "action": "add", "exists": false, "intent": "desired", "kind": "data",
			"record": map[string]any{"dn": "uid=bob,ou=people,dc=alder,dc=test", "type": "add", "attributes": []any{
				map[string]any{"name": "objectClass", "values": []any{map[string]any{"text": "inetOrgPerson"}}},
				map[string]any{"name": "cn", "values": []any{map[string]any{"text": "Bob"}}},
			}},
			"baseline": "desired-bob",
		})
	exact := func(index int, dn, action, baseline string) map[string]any {
		return map[string]any{"index": index, "dn": dn, "action": action, "exists": action == "modify",
			"intent": "exact", "kind": "data", "record": map[string]any{"dn": dn, "type": action}, "baseline": baseline}
	}
	holding := planJSON(t, counts{add: 1, modify: 1},
		exact(0, projectAliceDN, "modify", "saved-alice"), exact(1, "uid=bob,ou=people,dc=alder,dc=test", "add", "saved-bob"))
	moved := planJSON(t, counts{add: 1, conflict: 1},
		map[string]any{"index": 0, "dn": projectAliceDN, "action": "conflict", "exists": true, "intent": "exact",
			"problem": map[string]any{"code": "expected_state_differs"}},
		exact(1, "uid=bob,ou=people,dc=alder,dc=test", "add", "saved-bob"))
	s.on("POST /api/v1/plan", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		switch {
		case req["mode"] == "desired":
			_, _ = io.WriteString(w, desired)
		case savedHolds:
			_, _ = io.WriteString(w, holding)
		default:
			_, _ = io.WriteString(w, moved)
		}
	})
	s.reply("GET /api/v1/entry", http.StatusOK, mustJSON(t, map[string]any{
		"dn": projectAliceDN,
		"attributes": []any{
			map[string]any{"name": "cn", "kind": map[string]any{"name": "cn", "kind": "string", "known": true},
				"values": []any{map[string]any{"text": "Alice"}}},
			map[string]any{"name": "title", "kind": map[string]any{"name": "title", "kind": "string", "known": true},
				"values": []any{map[string]any{"text": "Before"}}},
		},
	}))
	s.reply("POST /api/v1/changeset/apply", http.StatusOK,
		`{"appliedCount":2,"outcomes":[{"index":0,"dn":"`+projectAliceDN+`","applied":true,"summary":"modified"},`+
			`{"index":1,"dn":"uid=bob,ou=people,dc=alder,dc=test","applied":true,"summary":"added"}]}`)
	return s
}

func readSaved(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s is not JSON: %v", path, err)
	}
	return doc
}

func TestProjectPlanOutSavesExactChangesWithWhatTheyExpect(t *testing.T) {
	s := savedPlanStub(t, true)
	dir := writeProject(t, s, nil)
	out := filepath.Join(t.TempDir(), "dev.plan.json")
	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--out", out, "--detailed-exitcode")
	expectCode(t, r, ExitPending) // the plan's status, saved or not
	if !strings.Contains(r.stderr, "plan for environment dev saved to "+out+": 2 changes") {
		t.Errorf("stderr does not say where the plan went:\n%s", r.stderr)
	}

	doc := readSaved(t, out)
	if doc["format"] != "alder-plan" || doc["version"] != float64(1) {
		t.Errorf("format = %v, version = %v", doc["format"], doc["version"])
	}
	environment, _ := doc["environment"].(map[string]any)
	if environment["name"] != "dev" || environment["host"] != "ldap.dev.test:636" ||
		environment["bindDn"] != "cn=alder,ou=services,dc=alder,dc=test" {
		t.Errorf("environment = %v", environment)
	}
	if digest, _ := doc["project"].(map[string]any)["digest"].(string); !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("project digest = %v", doc["project"])
	}
	changes, _ := doc["changes"].([]any)
	if len(changes) != 2 {
		t.Fatalf("changes = %v", doc["changes"])
	}
	alice := changes[0].(map[string]any)
	if _, ok := alice["baseline"]; ok {
		t.Error("a baseline was saved; it means nothing outside the process that issued it")
	}
	expect, _ := alice["expect"].(map[string]any)
	attrs, _ := expect["attributes"].([]any)
	if len(attrs) != 1 {
		t.Fatalf("alice expects %v, want title alone: the attribute the change replaces", expect)
	}
	title := attrs[0].(map[string]any)
	if values, _ := title["values"].([]any); title["name"] != "title" || len(values) != 1 ||
		values[0].(map[string]any)["text"] != "Before" {
		t.Errorf("alice expects %v, want title: Before", title)
	}
	if _, ok := changes[1].(map[string]any)["expect"]; ok {
		t.Error("an add carries an expectation; planning it where the entry exists is already a conflict")
	}

	// The saved changes were planned as written, with their expectations,
	// before the file was written.
	plans := s.called("POST /api/v1/plan")
	if len(plans) != 2 {
		t.Fatalf("planned %d times, want the project and then the saved changes", len(plans))
	}
	var check map[string]any
	_ = json.Unmarshal(plans[1].Body, &check)
	if check["ldif"] != nil || !strings.Contains(string(plans[1].Body), `"expect"`) {
		t.Errorf("the saved changes were not planned as written: %s", plans[1].Body)
	}
}

func TestProjectPlanOutIsNotWrittenWhenTheDirectoryMovesMeanwhile(t *testing.T) {
	s := savedPlanStub(t, false)
	dir := writeProject(t, s, nil)
	out := filepath.Join(t.TempDir(), "dev.plan.json")
	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--out", out)
	expectCode(t, r, ExitStale)
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("%s was written for a plan that does not hold", out)
	}
}

func TestAProjectPlanThatCannotBeAppliedIsNotSaved(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{conflict: 1}, map[string]any{
		"index": 0, "dn": projectAliceDN, "action": "conflict", "exists": true, "intent": "desired",
		"problem": map[string]any{"code": "entry_exists"}}))
	dir := writeProject(t, s, nil)
	out := filepath.Join(t.TempDir(), "dev.plan.json")
	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--out", out)
	expectCode(t, r, ExitNotApplicable)
	if !strings.Contains(r.stderr, "was not saved") {
		t.Errorf("stderr does not say the plan was not saved:\n%s", r.stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("%s was written for a plan that cannot be applied", out)
	}
}

func TestProjectPlanOutWithJSONSaysWhereInOneDocument(t *testing.T) {
	s := savedPlanStub(t, true)
	dir := writeProject(t, s, nil)
	out := filepath.Join(t.TempDir(), "dev.plan.json")
	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--out", out, "--json")
	expectCode(t, r, ExitOK)
	if doc := oneJSONDocument(t, r.stdout); doc["saved"] != out {
		t.Errorf("saved = %v, want %s", doc["saved"], out)
	}

	// An existing file is kept without --force, and refused before any work.
	before := len(s.calls)
	r = s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--out", out)
	expectCode(t, r, ExitUsage)
	if len(s.calls) != before {
		t.Error("requests were sent for a plan whose file would be refused")
	}
	r = s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--out", out, "--force")
	expectCode(t, r, ExitOK)
}

// savePlanFor plans dev against s and returns the file it saved.
func savePlanFor(t *testing.T, s *stub, dir string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "dev.plan.json")
	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--out", out)
	expectCode(t, r, ExitOK)
	return out
}

func TestProjectApplyPlanAppliesExactlyTheSavedChanges(t *testing.T) {
	s := savedPlanStub(t, true)
	dir := writeProject(t, s, nil)
	saved := savePlanFor(t, s, dir)
	before := len(s.called("POST /api/v1/plan"))

	r := s.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--plan", saved, "--yes")
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stderr, "applying the plan saved in "+saved) {
		t.Errorf("stderr does not say which saved plan is applied:\n%s", r.stderr)
	}
	plans := s.called("POST /api/v1/plan")[before:]
	if len(plans) != 1 {
		t.Fatalf("planned %d times; the saved changes are planned once, as written", len(plans))
	}
	var req map[string]any
	_ = json.Unmarshal(plans[0].Body, &req)
	if req["ldif"] != nil || req["mode"] == "desired" {
		t.Errorf("the project was planned instead of the saved plan: %s", plans[0].Body)
	}
	var sent struct {
		Changes []struct {
			Dn       string          `json:"dn"`
			Baseline string          `json:"baseline"`
			Expect   json.RawMessage `json:"expect"`
		} `json:"changes"`
	}
	_ = json.Unmarshal(s.called("POST /api/v1/changeset/apply")[0].Body, &sent)
	if len(sent.Changes) != 2 || sent.Changes[0].Baseline != "saved-alice" || len(sent.Changes[0].Expect) == 0 ||
		sent.Changes[1].Baseline != "saved-bob" {
		t.Errorf("did not apply the saved changes with their expectations and the new plan's baselines: %s",
			s.called("POST /api/v1/changeset/apply")[0].Body)
	}
}

func TestProjectApplyPlanThatNoLongerHoldsWritesNothing(t *testing.T) {
	s := savedPlanStub(t, true)
	dir := writeProject(t, s, nil)
	saved := savePlanFor(t, s, dir)

	moved := savedPlanStub(t, false)
	// The same project, pointed at a directory that has moved.
	yaml := filepath.Join(dir, "alder.yaml")
	data, _ := os.ReadFile(yaml)
	if err := os.WriteFile(yaml, []byte(strings.Replace(string(data), s.srv.URL, moved.srv.URL, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	r := moved.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--plan", saved, "--yes")
	expectCode(t, r, ExitStale)
	if !strings.Contains(r.stderr, "no longer holds") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	if len(moved.called("POST /api/v1/changeset/apply")) != 0 {
		t.Error("a saved plan that no longer holds was applied")
	}
}

func TestProjectApplyPlanRefusesAnotherEnvironmentOrProjectBeforeConnecting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		change func(t *testing.T, dir string)
	}{
		{"another environment", []string{"--env", "prod"}, nil},
		{"the environment's host changed", []string{"--env", "dev"}, func(t *testing.T, dir string) {
			edit(t, filepath.Join(dir, "alder.yaml"), "host: ldap.dev.test", "host: ldap2.dev.test")
		}},
		{"the environment binds as someone else", []string{"--env", "dev"}, func(t *testing.T, dir string) {
			edit(t, filepath.Join(dir, "alder.yaml"), "bind-dn: cn=alder,ou=services", "bind-dn: cn=other,ou=services")
		}},
		{"the project files changed", []string{"--env", "dev"}, func(t *testing.T, dir string) {
			edit(t, filepath.Join(dir, "people/bob.ldif"), "sn: B", "sn: Bee")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := savedPlanStub(t, true)
			dir := writeProject(t, s, nil)
			saved := savePlanFor(t, s, dir)
			if tc.change != nil {
				tc.change(t, dir)
			}
			before := len(s.calls)
			opts := projectRun
			opts.env = map[string]string{"ALDER_DEV_BIND_PASSWORD": testSecret, "ALDER_PROD_BIND_PASSWORD": testSecret}
			r := s.run(t, opts, append([]string{"project", "apply", "--project", dir, "--plan", saved, "--yes"}, tc.args...)...)
			expectCode(t, r, ExitNotApplicable)
			if len(s.calls) != before {
				t.Errorf("%d request(s) sent for a saved plan made elsewhere", len(s.calls)-before)
			}
		})
	}
}

func TestProjectApplyPlanReadsTheFileStrictly(t *testing.T) {
	s := savedPlanStub(t, true)
	dir := writeProject(t, s, nil)
	saved := savePlanFor(t, s, dir)
	for name, mutate := range map[string]func(doc map[string]any){
		"an unknown field": func(doc map[string]any) { doc["approvedBy"] = "someone" },
		"another version":  func(doc map[string]any) { doc["version"] = 2 },
		"a baseline": func(doc map[string]any) {
			doc["changes"].([]any)[0].(map[string]any)["baseline"] = "x.y.z"
		},
		"a modification with no expectation": func(doc map[string]any) {
			delete(doc["changes"].([]any)[0].(map[string]any), "expect")
		},
		"a deletion": func(doc map[string]any) {
			doc["changes"].([]any)[1].(map[string]any)["type"] = "delete"
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := readSaved(t, saved)
			mutate(doc)
			bad := filepath.Join(t.TempDir(), "bad.plan.json")
			if err := os.WriteFile(bad, []byte(mustJSON(t, doc)), 0o600); err != nil {
				t.Fatal(err)
			}
			before := len(s.calls)
			r := s.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--plan", bad, "--yes")
			expectCode(t, r, ExitNotApplicable)
			if len(s.calls) != before {
				t.Error("requests were sent for a file that is not a saved plan")
			}
		})
	}
}

func TestSavedPlanUsageErrors(t *testing.T) {
	s := savedPlanStub(t, true)
	dir := writeProject(t, s, nil)
	for _, args := range [][]string{
		{"plan", "--env", "dev", "--out", "-"},
		{"plan", "--env", "dev", "--force"},
		{"apply", "--env", "dev", "--plan", "-", "--yes"},
	} {
		r := s.run(t, projectRun, append([]string{"project", args[0], "--project", dir}, args[1:]...)...)
		expectCode(t, r, ExitUsage)
	}
	if len(s.calls) != 0 {
		t.Errorf("%d request(s) sent for command lines that were wrong", len(s.calls))
	}
}

func edit(t *testing.T, path, from, to string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), from) {
		t.Fatalf("%s does not contain %q", path, from)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), from, to, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}
