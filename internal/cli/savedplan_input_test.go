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

const bobDN = "uid=bob,ou=people,dc=example,dc=test"

// inputPlanStub answers alder plan as a directory where alice's title reads
// "Before" and the LDIF sets it to "After", and bob is deleted. A plan of
// changes carrying expectations is a saved plan's, answered as holding or,
// with holds false, with alice's expectation no longer met.
func inputPlanStub(t *testing.T, holds bool) *stub {
	t.Helper()
	return inputPlanStubNamed(t, holds, "title")
}

// inputPlanStubNamed is inputPlanStub with alice's modification naming the
// title attribute as title does: by a name, or by its OID.
func inputPlanStubNamed(t *testing.T, holds bool, titleName string) *stub {
	t.Helper()
	s := newStub(t)
	stubAttributeNames(s)
	exact := func(index int, dn, action string, record map[string]any, baseline string) map[string]any {
		return map[string]any{"index": index, "dn": dn, "action": action, "exists": true, "intent": "exact",
			"kind": "data", "record": record, "baseline": baseline}
	}
	alice := map[string]any{"dn": aliceDN, "type": "modify", "mods": []any{
		map[string]any{"op": "replace", "name": titleName, "values": []any{map[string]any{"text": "After"}}},
	}}
	bob := map[string]any{"dn": bobDN, "type": "delete"}
	planned := planJSON(t, counts{modify: 1, delete: 1},
		exact(0, aliceDN, "modify", alice, "plan-alice"), exact(1, bobDN, "delete", bob, "plan-bob"))
	holding := planJSON(t, counts{modify: 1, delete: 1},
		exact(0, aliceDN, "modify", alice, "saved-alice"), exact(1, bobDN, "delete", bob, "saved-bob"))
	moved := planJSON(t, counts{delete: 1, conflict: 1},
		map[string]any{"index": 0, "dn": aliceDN, "action": "conflict", "exists": true, "intent": "exact",
			"problem": map[string]any{"code": "expected_state_differs"}},
		exact(1, bobDN, "delete", bob, "saved-bob"))
	s.on("POST /api/v1/plan", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case !strings.Contains(string(body), `"expect"`):
			_, _ = io.WriteString(w, planned)
		case holds:
			_, _ = io.WriteString(w, holding)
		default:
			_, _ = io.WriteString(w, moved)
		}
	})
	s.on("GET /api/v1/entry", func(w http.ResponseWriter, r *http.Request) {
		dn := r.URL.Query().Get("dn")
		attrs := []any{
			map[string]any{"name": "cn", "kind": map[string]any{"name": "cn", "kind": "string", "known": true},
				"values": []any{map[string]any{"text": strings.TrimPrefix(strings.Split(dn, ",")[0], "uid=")}}},
			map[string]any{"name": "title", "kind": map[string]any{"name": "title", "kind": "string", "known": true},
				"values": []any{map[string]any{"text": "Before"}}},
			map[string]any{"name": "userPassword", "kind": map[string]any{"name": "userPassword", "kind": "string", "known": true},
				"withheld": true, "values": []any{map[string]any{"size": 26}}},
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, mustJSON(t, map[string]any{"dn": dn, "attributes": attrs}))
	})
	s.reply("POST /api/v1/changeset/apply", http.StatusOK,
		`{"appliedCount":2,"outcomes":[{"index":0,"dn":"`+aliceDN+`","applied":true,"summary":"modified"},`+
			`{"index":1,"dn":"`+bobDN+`","applied":true,"summary":"deleted"}]}`)
	return s
}

// stubAttributeNames is GET /schema, reduced to the names of the attribute
// types these tests use.
func stubAttributeNames(s *stub) {
	types := []map[string]any{
		{"name": "cn", "names": []string{"cn", "commonName"}, "oid": "2.5.4.3"},
		{"name": "title", "oid": "2.5.4.12"},
		{"name": "userPassword", "oid": "2.5.4.35"},
		// Kept by the directory, which a desired-state plan never replaces:
		// one operational, one that only says no user modifies it.
		{"name": "nsAccountLock", "oid": "2.16.840.1.113730.3.1.610", "operational": true},
		{"name": "stampedBy", "oid": "1.3.6.1.4.1.99999.1", "noUserModification": true},
	}
	body, _ := json.Marshal(map[string]any{"subschemaDn": "cn=Subschema", "counts": map[string]any{}, "attributeTypes": types})
	s.reply("GET /api/v1/schema", http.StatusOK, string(body))
}

const inputLDIF = "dn: " + aliceDN + "\nchangetype: modify\nreplace: title\ntitle: After\n-\n\n" +
	"dn: " + bobDN + "\nchangetype: delete\n"

func saveInput(t *testing.T, s *stub) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "change.plan.json")
	r := s.run(t, runOpts{}, "plan", writeTemp(t, dir, "change.ldif", inputLDIF), "--out", out)
	expectCode(t, r, ExitOK)
	return out
}

func TestPlanOutSavesAnLDIFPlanWithWhatEachChangeExpects(t *testing.T) {
	s := inputPlanStub(t, true)
	out := saveInput(t, s)
	doc := readSaved(t, out)

	if doc["project"] != nil {
		t.Errorf("an LDIF plan names a project: %v", doc["project"])
	}
	input, _ := doc["input"].(map[string]any)
	if input["kind"] != "ldif" || input["mode"] != "changes" || !strings.HasPrefix(input["digest"].(string), "sha256:") {
		t.Errorf("input = %v", input)
	}
	environment, _ := doc["environment"].(map[string]any)
	if _, named := environment["name"]; named || environment["host"] != "ldap.example.test:636" ||
		environment["bindDn"] != "cn=admin,dc=example,dc=test" {
		t.Errorf("environment = %v, want the host and bind and no name", environment)
	}
	changes, _ := doc["changes"].([]any)
	if len(changes) != 2 {
		t.Fatalf("changes = %v", doc["changes"])
	}
	modify, _ := changes[0].(map[string]any)["expect"].(map[string]any)
	if attrs, _ := modify["attributes"].([]any); len(attrs) != 1 || attrs[0].(map[string]any)["name"] != "title" {
		t.Errorf("the modification expects %v, want title alone", modify)
	}
	del, _ := changes[1].(map[string]any)["expect"].(map[string]any)
	if del["exhaustive"] != true {
		t.Errorf("the deletion's expectation is not exhaustive: %v", del)
	}
	text, _ := json.Marshal(del)
	if !strings.Contains(string(text), `"cn"`) || strings.Contains(string(text), "userPassword") {
		t.Errorf("the deletion expects %s; want the entry's readable attributes, and no sensitive one", text)
	}
}

func TestApplyPlanAppliesTheSavedChangesAndAsksForDeletes(t *testing.T) {
	s := inputPlanStub(t, true)
	saved := saveInput(t, s)
	before := len(s.called("POST /api/v1/plan"))

	// It deletes, so --yes alone does not apply it.
	r := s.run(t, runOpts{}, "apply", "--plan", saved, "--yes")
	expectCode(t, r, ExitNotConfirmed)
	if len(s.called("POST /api/v1/changeset/apply")) != 0 {
		t.Fatal("a plan that deletes was applied without --allow-deletes")
	}

	r = s.run(t, runOpts{}, "apply", "--plan", saved, "--yes", "--allow-deletes")
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stderr, "applying the plan saved in "+saved) {
		t.Errorf("stderr does not say which saved plan is applied:\n%s", r.stderr)
	}
	for _, p := range s.called("POST /api/v1/plan")[before:] {
		if strings.Contains(string(p.Body), `"ldif"`) {
			t.Errorf("the LDIF was planned again instead of the saved changes: %s", p.Body)
		}
	}
	var sent struct {
		Changes []struct {
			Baseline string          `json:"baseline"`
			Expect   json.RawMessage `json:"expect"`
		} `json:"changes"`
	}
	_ = json.Unmarshal(s.called("POST /api/v1/changeset/apply")[0].Body, &sent)
	if len(sent.Changes) != 2 || sent.Changes[0].Baseline != "saved-alice" || len(sent.Changes[1].Expect) == 0 {
		t.Errorf("did not apply the saved changes with their expectations and the new baselines: %s",
			s.called("POST /api/v1/changeset/apply")[0].Body)
	}
}

func TestApplyPlanThatNoLongerHoldsWritesNothing(t *testing.T) {
	s := inputPlanStub(t, true)
	saved := saveInput(t, s)
	moved := inputPlanStub(t, false)
	r := moved.run(t, runOpts{}, "apply", "--plan", saved, "--yes", "--allow-deletes")
	expectCode(t, r, ExitStale)
	if len(moved.called("POST /api/v1/changeset/apply")) != 0 {
		t.Error("a saved plan that no longer holds was applied")
	}
}

func TestApplyPlanRefusesAnotherDirectoryBeforeConnecting(t *testing.T) {
	s := inputPlanStub(t, true)
	saved := saveInput(t, s)
	before := len(s.calls)

	r := s.run(t, runOpts{}, "apply", "--plan", saved, "--yes", "--host", "ldap.other.test")
	expectCode(t, r, ExitNotApplicable)
	if !strings.Contains(r.stderr, "was made against ldap.example.test:636") {
		t.Errorf("stderr does not say which directory the plan is for:\n%s", r.stderr)
	}
	r = s.run(t, runOpts{}, "apply", "--plan", saved, "--yes", "--bind-dn", "cn=someone,dc=example,dc=test")
	expectCode(t, r, ExitNotApplicable)
	if len(s.calls) != before {
		t.Errorf("requests were sent for a saved plan that was refused")
	}
}

func TestPlanOutRefusals(t *testing.T) {
	dir := t.TempDir()
	ldif := writeTemp(t, dir, "a.ldif", inputLDIF)
	out := filepath.Join(dir, "a.plan.json")

	s := newStub(t)
	stubAttributeNames(s)
	// A change carrying a value the server withheld is not saved: the file
	// would hold nothing to apply, and a secret must never be in it.
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
	s.reply("GET /api/v1/entry", http.StatusOK, mustJSON(t, map[string]any{"dn": aliceDN, "attributes": []any{}}))
	r := s.run(t, runOpts{}, "plan", ldif, "--out", out)
	// As alder project plan --out says it: the plan was shown, the file is
	// what failed.
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "apply this one with alder apply") {
		t.Errorf("stderr does not say what to do instead:\n%s", r.stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("a plan with a withheld value was saved")
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"standard output", []string{"plan", ldif, "--out", "-"}},
		{"force with no file", []string{"plan", ldif, "--force"}},
		{"a recovery bundle", []string{"plan", "--recovery", ldif, "--out", out}},
		{"apply with an input as well", []string{"apply", ldif, "--plan", out, "--yes"}},
		{"apply from standard input", []string{"apply", "--plan", "-", "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectCode(t, newStub(t).run(t, runOpts{}, tc.args...), ExitUsage)
		})
	}
}

func TestSavedPlansStayWithTheirOwnCommand(t *testing.T) {
	s := inputPlanStub(t, true)
	saved := saveInput(t, s)

	// An input's plan is refused by alder project apply.
	ps := savedPlanStub(t, true)
	dir := writeProject(t, ps, nil)
	r := ps.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--plan", saved, "--yes")
	expectCode(t, r, ExitNotApplicable)
	if !strings.Contains(r.stderr, "apply it with alder apply --plan") {
		t.Errorf("stderr does not point at alder apply:\n%s", r.stderr)
	}

	// And a project's plan by alder apply.
	projectPlan := savePlanFor(t, ps, dir)
	r = s.run(t, runOpts{}, "apply", "--plan", projectPlan, "--yes")
	expectCode(t, r, ExitNotApplicable)
	if !strings.Contains(r.stderr, "alder project apply --env dev --plan") {
		t.Errorf("stderr does not point at alder project apply:\n%s", r.stderr)
	}
}

func TestASavedInputPlanIsReadStrictly(t *testing.T) {
	s := inputPlanStub(t, true)
	for _, tc := range []struct{ name, from, to, want string }{
		{"an environment name", `"host":`, `"name": "prod", "host":`, "names an environment"},
		{"no input kind", `"kind": "ldif"`, `"kind": "package"`, "which input"},
		{"a rename that does not expect its entry's identity", `"type": "delete"`, `"type": "modrdn"`,
			"does not expect the entry's identity"},
		{"a deletion that is not exact", `"exhaustive": true`, `"exhaustive": false`, "does not expect the entry exactly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := saveInput(t, s)
			edit(t, saved, tc.from, tc.to)
			r := s.run(t, runOpts{}, "apply", "--plan", saved, "--yes", "--allow-deletes")
			expectCode(t, r, ExitNotApplicable)
			if !strings.Contains(r.stderr, tc.want) {
				t.Errorf("stderr does not say %q:\n%s", tc.want, r.stderr)
			}
		})
	}
}

func TestPlanOutWithJSONWritesOneDocument(t *testing.T) {
	s := inputPlanStub(t, true)
	dir := t.TempDir()
	out := filepath.Join(dir, "a.plan.json")
	r := s.run(t, runOpts{}, "plan", writeTemp(t, dir, "a.ldif", inputLDIF), "--out", out, "--json")
	expectCode(t, r, ExitOK)
	oneJSONDocument(t, r.stdout)
	if _, err := os.Stat(out); err != nil {
		t.Errorf("the plan was not saved: %v", err)
	}

	// Saving fails because the directory moved: the error is the one document.
	moved := inputPlanStub(t, false)
	r = moved.run(t, runOpts{}, "plan", writeTemp(t, dir, "b.ldif", inputLDIF), "--out", filepath.Join(dir, "b.plan.json"), "--json")
	expectCode(t, r, ExitStale)
	oneJSONDocument(t, r.stdout)
}

// An expectation is checked by the name the entry returns its attribute
// under. One written under an OID or another name would find nothing there,
// expect the attribute absent, and always hold.
func TestASavedModificationExpectsItsAttributeByTheNameTheEntryReturns(t *testing.T) {
	s := inputPlanStubNamed(t, true, "2.5.4.12")
	doc := readSaved(t, saveInput(t, s))
	changes, _ := doc["changes"].([]any)
	expect, _ := changes[0].(map[string]any)["expect"].(map[string]any)
	attrs, _ := expect["attributes"].([]any)
	if len(attrs) != 1 {
		t.Fatalf("expect = %v", expect)
	}
	got, _ := json.Marshal(attrs[0])
	if want := `{"name":"title","values":[{"text":"Before"}]}`; string(got) != want {
		t.Errorf("the modification of 2.5.4.12 expects %s, want %s", got, want)
	}
}

// The saved change is the plan's record, which has no room for an expect the
// input wrote itself; saving it would drop the guard the plan showed.
func TestPlanOutRefusesChangesThatCarryTheirOwnExpectation(t *testing.T) {
	s := inputPlanStub(t, true)
	dir := t.TempDir()
	changes := writeTemp(t, dir, "changes.json", `[{"dn":"`+aliceDN+`","type":"modify",`+
		`"mods":[{"op":"replace","name":"title","values":[{"text":"After"}]}],`+
		`"expect":{"attributes":[{"name":"employeeType","values":[{"text":"contractor"}]}]}}]`)
	out := filepath.Join(dir, "p.json")
	r := s.run(t, runOpts{}, "plan", "--changes", changes, "--out", out)
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "carries its own expect") {
		t.Errorf("stderr = %s", r.stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("the plan was saved without the expectation the input gave")
	}
}

// A file for another directory is refused as such (exit 3) before anything
// asks for a confirmation there is no terminal for.
func TestApplyPlanRefusesAnotherDirectoryBeforeAskingToConfirm(t *testing.T) {
	s := inputPlanStub(t, true)
	saved := saveInput(t, s)
	r := s.run(t, runOpts{}, "apply", "--plan", saved, "--host", "ldap.other.test")
	expectCode(t, r, ExitNotApplicable)
}

// Only a plan that no longer holds is stale. Failing to ask is reported as
// itself, so a script retrying on exit 4 does not retry a network failure.
func TestPlanOutReportsAFailedCheckAsItselfNotAsStale(t *testing.T) {
	s := inputPlanStub(t, true)
	s.on("POST /api/v1/plan", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), `"expect"`) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"code":"internal","message":"boom"}`)
			return
		}
		_, _ = io.WriteString(w, planJSON(t, counts{modify: 1},
			map[string]any{"index": 0, "dn": aliceDN, "action": "modify", "exists": true, "intent": "exact", "kind": "data",
				"record": map[string]any{"dn": aliceDN, "type": "modify", "mods": []any{
					map[string]any{"op": "replace", "name": "title", "values": []any{map[string]any{"text": "After"}}},
				}}, "baseline": "b"}))
	})
	dir := t.TempDir()
	r := s.run(t, runOpts{}, "plan", writeTemp(t, dir, "c.ldif", "dn: "+aliceDN+"\nchangetype: modify\nreplace: title\ntitle: After\n-\n"),
		"--out", filepath.Join(dir, "p.json"))
	if r.code == ExitStale || r.code == ExitOK {
		t.Errorf("exit %d for a failed check, want neither 0 nor 4\nstderr: %s", r.code, r.stderr)
	}
}
