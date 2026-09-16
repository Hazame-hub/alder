package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 1.11: change packages on the command line.
//
// The client carries bytes and prints what Alder answers. What matters here is
// that it never invents a way around the plan: --package goes through
// validation and a plan, and there is no command that applies a package.

const packageDoc = `{
  "format": "alder-change-package",
  "version": 1,
  "id": "3f1b8a52-6a0e-4f1e-9a3e-77a4e1f0c111",
  "createdAt": "2026-09-16T10:00:00Z",
  "title": "team schema and a user",
  "source": {"method": "changeset", "alderVersion": "1.11.0"},
  "assumptions": {"namingContexts": ["dc=example,dc=test"]},
  "counts": {"changes": 2, "data": 1, "schema": 1, "destructive": 0, "omitted": 1},
  "changes": [
    {"id": "c1", "kind": "schema", "destructive": false,
     "schema": {"element": "attributeType", "op": "add", "oid": "1.3.6.1.4.1.99999.9.1",
                "definition": "( 1.3.6.1.4.1.99999.9.1 NAME 'alderCliTeam' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"}},
    {"id": "c2", "kind": "data", "destructive": false, "dependsOn": ["c1"],
     "data": {"dn": "uid=alice,ou=people,dc=example,dc=test", "type": "modify",
              "mods": [{"op": "replace", "name": "alderCliTeam", "values": [{"text": "platform"}]}]}}
  ],
  "omitted": [{"subject": "userPassword", "kind": "data", "reason": "secret_not_portable",
               "detail": "a package never carries a secret"}],
  "checksum": "sha256:not-checked-by-the-client"
}`

func packageInspectionJSON() string {
	return `{"packageId":"3f1b8a52-6a0e-4f1e-9a3e-77a4e1f0c111","version":1,"createdAt":"2026-09-16T10:00:00Z",
	"title":"team schema and a user","integrity":"verified",
	"source":{"method":"changeset","alderVersion":"1.11.0"},
	"assumptions":{"namingContexts":["dc=example,dc=test"]},
	"counts":{"changes":2,"data":1,"schema":1,"destructive":0,"omitted":1},
	"changes":[
	 {"id":"c1","kind":"schema","destructive":false,
	  "schema":{"element":"attributeType","op":"add","oid":"1.3.6.1.4.1.99999.9.1",
	            "definition":"( 1.3.6.1.4.1.99999.9.1 NAME 'alderCliTeam' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"}},
	 {"id":"c2","kind":"data","destructive":false,"dependsOn":["c1"],
	  "data":{"dn":"uid=alice,ou=people,dc=example,dc=test","type":"modify",
	          "mods":[{"op":"replace","name":"alderCliTeam","values":[{"text":"platform"}]}]}}],
	"omitted":[{"subject":"userPassword","kind":"data","reason":"secret_not_portable"}],
	"order":["c1","c2"]}`
}

// validationJSON is what a target answers: one change ready, and whatever else
// the caller asks for.
func validationJSON(extra string) string {
	body := `{"packageId":"3f1b8a52-6a0e-4f1e-9a3e-77a4e1f0c111","integrity":"verified",
	"target":{"vendor":"389 Project","namingContexts":["dc=example,dc=test"],"schemaWritable":true},
	"assumptions":[{"kind":"namingContext","value":"dc=example,dc=test","satisfied":true}],
	"counts":{"ready":1,"alreadySatisfied":1,"noOp":0,"conflict":CONFLICT,"dependencyMissing":0,
	          "unsupported":0,"targetIncompatible":0,"unknown":UNKNOWN},
	"items":[
	 {"id":"c1","kind":"schema","destructive":false,"status":"ready",
	  "changes":[{"dn":"cn=schema","type":"modify","mods":[{"op":"add","name":"attributeTypes",
	              "values":[{"text":"( 1.3.6.1.4.1.99999.9.1 NAME 'alderCliTeam' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"}]}]}]},
	 {"id":"c2","kind":"data","destructive":false,"status":"already_satisfied"}EXTRA],
	"order":["c1"]}`
	switch extra {
	case "conflict":
		return strings.NewReplacer("CONFLICT", "1", "UNKNOWN", "0", "EXTRA",
			`,{"id":"c3","kind":"data","destructive":false,"status":"conflict",
			  "problems":[{"code":"entry_missing","subject":"uid=bob,ou=people,dc=example,dc=test"}]}`).Replace(body)
	case "unknown":
		return strings.NewReplacer("CONFLICT", "0", "UNKNOWN", "1", "EXTRA",
			`,{"id":"c3","kind":"data","destructive":false,"status":"unknown",
			  "problems":[{"code":"read_failed","subject":"uid=bob,ou=people,dc=example,dc=test"}]}`).Replace(body)
	default:
		return strings.NewReplacer("CONFLICT", "0", "UNKNOWN", "0", "EXTRA", "").Replace(body)
	}
}

// planOfOneSchemaChange is what Alder answers when the package's ready change
// is planned: one modification, with the token only a plan issues.
const planOfOneSchemaChange = `{"counts":{"examined":1,"add":0,"modify":1,"delete":0,"rename":0,"setPassword":0,
 "unchanged":0,"conflict":0,"invalid":0},
 "items":[{"index":0,"dn":"cn=schema","action":"modify","exists":true,"baseline":"token-issued-here",
  "record":{"dn":"cn=schema","type":"modify","mods":[{"op":"add","name":"attributeTypes",
   "values":[{"text":"( 1.3.6.1.4.1.99999.9.1 NAME 'alderCliTeam' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"}]}]}}]}`

func changesFile(t *testing.T) string {
	t.Helper()
	return writeTemp(t, t.TempDir(), "changes.json", `[
	 {"dn":"uid=alice,ou=people,dc=example,dc=test","type":"modify",
	  "mods":[{"op":"replace","name":"title","values":[{"text":"Senior Engineer"}]}]}]`)
}

func TestPackageCreateWritesWhatAlderBuilt(t *testing.T) {
	s := newStub(t)
	var sent map[string]any
	s.on("POST /api/v1/packages/build", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(packageDoc))
	})
	out := filepath.Join(t.TempDir(), "package.json")
	r := s.run(t, runOpts{}, "package", "create", "--changes", changesFile(t), "--title", "team schema", "--output", out)
	expectCode(t, r, ExitOK)

	if sent["title"] != "team schema" || sent["method"] != "cli" {
		t.Errorf("request %v", sent)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != packageDoc {
		t.Errorf("the file is not what Alder sent:\n%s", data)
	}
	for _, want := range []string{"3f1b8a52-6a0e-4f1e-9a3e-77a4e1f0c111", "2 changes", "1 schema", "1 data",
		"left out: data userPassword (secret_not_portable)"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.stderr)
		}
	}

	// An existing file is never replaced without --force, as everywhere else.
	again := newStub(t)
	again.reply("POST /api/v1/packages/build", http.StatusOK, packageDoc)
	expectCode(t, again.run(t, runOpts{}, "package", "create", "--changes", changesFile(t), "--output", out), ExitUsage)
	forced := newStub(t)
	forced.reply("POST /api/v1/packages/build", http.StatusOK, packageDoc)
	expectCode(t, forced.run(t, runOpts{}, "package", "create", "--changes", changesFile(t), "--output", out, "--force"), ExitOK)
}

func TestPackageInspectNeedsNoDirectory(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/packages/inspect", http.StatusOK, packageInspectionJSON())
	file := writeTemp(t, t.TempDir(), "package.json", packageDoc)

	// No directory flags at all: only --api-url.
	r := s.run(t, runOpts{noConnection: true}, "package", "inspect", file, "--api-url", s.srv.URL)
	expectCode(t, r, ExitOK)
	for _, want := range []string{"team schema and a user", "2 changes", "add attributeType 1.3.6.1.4.1.99999.9.1",
		"modify uid=alice,ou=people,dc=example,dc=test", "after c1", "Left out of this package"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}

	j := s.run(t, runOpts{noConnection: true}, "package", "inspect", file, "--api-url", s.srv.URL, "--json")
	expectCode(t, j, ExitOK)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(j.stdout), &parsed); err != nil || parsed["packageId"] == nil {
		t.Errorf("--json is not Alder's answer: %v\n%s", err, j.stdout)
	}
}

func TestPackageValidateSaysWhatThisTargetMakesOfIt(t *testing.T) {
	file := writeTemp(t, t.TempDir(), "package.json", packageDoc)
	cases := map[string]struct {
		body string
		code int
	}{
		"ready here":        {validationJSON(""), ExitOK},
		"something refused": {validationJSON("conflict"), ExitNotApplicable},
		"something unknown": {validationJSON("unknown"), ExitIncomplete},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			s.reply("POST /api/v1/packages/validate", http.StatusOK, tc.body)
			r := s.run(t, runOpts{}, "package", "validate", file)
			expectCode(t, r, tc.code)
			for _, want := range []string{"Package 3f1b8a52", "1 ready", "1 already satisfied", "Nothing has been applied"} {
				if !strings.Contains(r.stdout, want) {
					t.Errorf("output lacks %q:\n%s", want, r.stdout)
				}
			}
		})
	}
}

func TestPlanningAPackageGoesThroughValidationAndAPlan(t *testing.T) {
	file := writeTemp(t, t.TempDir(), "package.json", packageDoc)
	s := newStub(t)
	s.reply("POST /api/v1/packages/validate", http.StatusOK, validationJSON(""))
	var planned map[string]any
	s.on("POST /api/v1/plan", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&planned)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(planOfOneSchemaChange))
	})
	r := s.run(t, runOpts{}, "plan", "--package", file)
	expectCode(t, r, ExitOK)

	// What was planned is what this target's validation prepared -- not
	// anything the package carried.
	changes, _ := planned["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("planned %v", planned)
	}
	first, _ := changes[0].(map[string]any)
	if first["dn"] != "cn=schema" {
		t.Errorf("planned change %v", first)
	}
	if strings.Contains(r.stdout, "token-issued-here") && !strings.Contains(r.stdout, "modify") {
		t.Errorf("the plan was not shown:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "1 ready") {
		t.Errorf("the validation was not shown:\n%s", r.stdout)
	}
}

func TestAPackageIsNeverAppliedWithoutAPlan(t *testing.T) {
	file := writeTemp(t, t.TempDir(), "package.json", packageDoc)
	s := newStub(t)
	s.reply("POST /api/v1/packages/validate", http.StatusOK, validationJSON(""))
	s.reply("POST /api/v1/plan", http.StatusOK, planOfOneSchemaChange)
	var applied map[string]any
	s.on("POST /api/v1/changeset/apply", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&applied)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"appliedCount":1,"outcomes":[{"index":0,"dn":"cn=schema","status":"applied"}]}`))
	})
	expectCode(t, s.run(t, runOpts{}, "apply", "--package", file, "--yes"), ExitOK)

	// The apply carried the plan's own token, which only the plan issues.
	changes, _ := applied["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("applied %v", applied)
	}
	first, _ := changes[0].(map[string]any)
	if first["baseline"] != "token-issued-here" {
		t.Errorf("applied change %v", first)
	}

	// And there is no such thing as applying a package directly: the verb does
	// not exist, so nothing can bypass the plan.
	out := s.run(t, runOpts{}, "package", "apply", file)
	if out.code == ExitOK || !strings.Contains(out.stderr, "unknown") {
		t.Errorf("package apply: exit %d\n%s", out.code, out.stderr)
	}
}

func TestPackageUsageIsRefusedBeforeAnythingIsRead(t *testing.T) {
	file := writeTemp(t, t.TempDir(), "package.json", packageDoc)
	changes := changesFile(t)
	cases := map[string][]string{
		"a package and changes together":    {"plan", "--package", file, "--changes", changes},
		"a package and an LDIF document":    {"plan", "--package", file, changes},
		"a schema target without a package": {"plan", "--changes", changes, "--schema-target", "cn=schema"},
		"a mode with a package":             {"plan", "--package", file, "--mode", "desired"},
		"creating with no output":           {"package", "create", "--changes", changes},
		"creating with no changes":          {"package", "create", "--output", "p.json"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			expectCode(t, newStub(t).run(t, runOpts{}, args...), ExitUsage)
		})
	}
}
