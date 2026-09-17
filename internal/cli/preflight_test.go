package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// 1.12: alder preflight.
//
// The client reads a file, sends its bytes, prints the report and exits with a
// code a pipeline can branch on. It decides nothing about compatibility.

func preflightReportJSON(overall string, complete bool, findings string) string {
	return `{"reportVersion":1,"createdAt":"2026-09-17T12:00:00Z",
	"source":{"type":"change_package","format":"alder-change-package","version":1,"id":"3f1b8a52","title":"team schema and a user",
	          "integrity":"verified","vendor":"OpenLDAP","objects":2},
	"target":{"vendor":"389 Project","namingContexts":["dc=example,dc=test"],"schemaEntry":"cn=schema","schemaWritable":true,"crossVendor":true},
	"overall":"` + overall + `","complete":` + map[bool]string{true: "true", false: "false"}[complete] + `,
	"reasons":` + map[bool]string{true: "[]", false: `["reference_unknown"]`}[complete] + `,
	"counts":{"portable":1,"alreadySatisfied":0,"prerequisiteRequired":0,"incompatible":0,"unsupported":0,"unknown":0,"excluded":0},
	"sections":[{"category":"schema","counts":{"portable":1,"alreadySatisfied":0,"prerequisiteRequired":0,"incompatible":0,"unsupported":0,"unknown":0,"excluded":0}}],
	"capabilities":[{"capability":"schema_write","available":true,"requiredBy":"schema definitions","explanation":"x"}],
	"notEvaluated":[{"area":"access_control","reason":"not compared"},{"area":"server_configuration","reason":"not captured"}],
	"findings":[` + findings + `],"truncated":false}`
}

const portableFinding = `{"id":"f1","code":"definition_portable","classification":"portable","category":"schema","scope":"definition",
 "source":{"item":"c1","element":"attributeType","oid":"1.3.6.1.4.1.99999.9.1","name":"alderCliTeam"},
 "explanation":"It can be added.","blocksPortability":false,"blocksPlan":false,"manualAction":false,"validationStatus":"ready"}`

const conflictFinding = `{"id":"f2","code":"definition_conflict","classification":"incompatible","category":"schema","scope":"definition",
 "source":{"item":"c2","element":"attributeType","oid":"1.3.6.1.4.1.99999.9.2","name":"alderCliOther"},
 "target":{"fact":"defined_differently","detail":"singleValue"},
 "explanation":"The target already defines it with a different meaning.","blocksPortability":true,"blocksPlan":true,"manualAction":false,
 "causes":["f1"],"prerequisites":[{"type":"schema","element":"attributeType","name":"x","providedBy":"f1"}]}`

func TestPreflightExitsWithWhatTheReportConcludes(t *testing.T) {
	file := writeTemp(t, t.TempDir(), "package.json", packageDoc)
	cases := map[string]struct {
		body string
		code int
	}{
		"compatible":         {preflightReportJSON("compatible", true, portableFinding), ExitOK},
		"with prerequisites": {preflightReportJSON("compatible_with_prerequisites", true, portableFinding), ExitDifferences},
		"incomplete":         {preflightReportJSON("incomplete", false, portableFinding), ExitIncomplete},
		"incompatible":       {preflightReportJSON("incompatible", true, portableFinding+","+conflictFinding), ExitNotApplicable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			var sent map[string]json.RawMessage
			s.on("POST /api/v1/preflight", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&sent)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			})
			r := s.run(t, runOpts{}, "preflight", file)
			expectCode(t, r, tc.code)
			if !strings.Contains(r.stdout, "Migration preflight: change package from OpenLDAP -> dc=example,dc=test (389 Project)") ||
				!strings.Contains(r.stdout, "Not evaluated") || !strings.Contains(r.stdout, "access control") {
				t.Errorf("output:\n%s", r.stdout)
			}
			// The artifact went as it was: the same JSON, not a re-encoding.
			var original, carried any
			_ = json.Unmarshal([]byte(packageDoc), &original)
			_ = json.Unmarshal(sent["artifact"], &carried)
			a, _ := json.Marshal(original)
			b, _ := json.Marshal(carried)
			if string(a) != string(b) {
				t.Errorf("the artifact was changed on the way")
			}
		})
	}
}

func TestPreflightListsWhatNeedsAttention(t *testing.T) {
	file := writeTemp(t, t.TempDir(), "package.json", packageDoc)
	s := newStub(t)
	s.reply("POST /api/v1/preflight", http.StatusOK, preflightReportJSON("incompatible", true, portableFinding+","+conflictFinding))
	r := s.run(t, runOpts{}, "preflight", file)
	expectCode(t, r, ExitNotApplicable)
	for _, want := range []string{"definition_conflict", "because of f1", "needs schema attributeType x (supplied by f1)"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "definition_portable") {
		t.Errorf("a portable finding was listed without --all:\n%s", r.stdout)
	}
	all := newStub(t)
	all.reply("POST /api/v1/preflight", http.StatusOK, preflightReportJSON("incompatible", true, portableFinding+","+conflictFinding))
	r = all.run(t, runOpts{}, "preflight", file, "--all")
	if !strings.Contains(r.stdout, "definition_portable") {
		t.Errorf("--all did not list every finding:\n%s", r.stdout)
	}
}

func TestPreflightJSONIsAldersAnswer(t *testing.T) {
	file := writeTemp(t, t.TempDir(), "package.json", packageDoc)
	s := newStub(t)
	body := preflightReportJSON("compatible_with_prerequisites", true, portableFinding)
	s.reply("POST /api/v1/preflight", http.StatusOK, body)
	r := s.run(t, runOpts{}, "preflight", file, "--json")
	expectCode(t, r, ExitDifferences)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &parsed); err != nil || parsed["overall"] != "compatible_with_prerequisites" {
		t.Fatalf("--json is not the report: %v\n%s", err, r.stdout)
	}
	if strings.Contains(r.stdout, "Migration preflight") {
		t.Error("human text leaked into --json output")
	}
}

func TestPreflightReadsStandardInput(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/preflight", http.StatusOK, preflightReportJSON("compatible", true, portableFinding))
	r := s.run(t, runOpts{stdin: packageDoc}, "preflight", "-")
	expectCode(t, r, ExitOK)
}

func TestPreflightRefusesWhatIsNotJSONBeforeSendingIt(t *testing.T) {
	file := writeTemp(t, t.TempDir(), "package.ldif", "dn: cn=x,dc=example,dc=test\nchangetype: add\n")
	s := newStub(t)
	sent := false
	s.on("POST /api/v1/preflight", func(w http.ResponseWriter, _ *http.Request) {
		sent = true
		w.WriteHeader(http.StatusInternalServerError)
	})
	r := s.run(t, runOpts{}, "preflight", file)
	expectCode(t, r, ExitFailed)
	if sent {
		t.Fatal("a document that is not JSON was sent")
	}
}

func TestPreflightRefusalCarriesAldersCode(t *testing.T) {
	file := writeTemp(t, t.TempDir(), "package.json", packageDoc)
	s := newStub(t)
	s.reply("POST /api/v1/preflight", http.StatusBadRequest,
		`{"error":"package_checksum_mismatch","message":"The package does not match its checksum."}`)
	r := s.run(t, runOpts{}, "preflight", file)
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "checksum") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
}
