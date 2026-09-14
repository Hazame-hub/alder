package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPlanSendsTheDocumentAsItIsWithTheModeAsked(t *testing.T) {
	dir := t.TempDir()
	crlf := strings.ReplaceAll(restoreLDIF, "\n", "\r\n")
	cases := []struct {
		name  string
		opts  runOpts
		args  []string
		check func(t *testing.T, req map[string]any)
	}{
		{"a file, changes by default", runOpts{}, []string{"plan", writeTemp(t, dir, "a.ldif", crlf)},
			func(t *testing.T, req map[string]any) {
				if req["ldif"] != crlf || req["mode"] != "changes" || req["reconcile"] != false {
					t.Errorf("request = %q", req)
				}
			}},
		{"desired state", runOpts{}, []string{"plan", writeTemp(t, dir, "b.ldif", "dn: uid=a\nuid: a\n"), "--mode", "desired"},
			func(t *testing.T, req map[string]any) {
				if req["mode"] != "desired" {
					t.Errorf("request = %v", req)
				}
			}},
		{"standard input", runOpts{stdin: restoreLDIF}, []string{"plan", "-"},
			func(t *testing.T, req map[string]any) {
				if req["ldif"] != restoreLDIF {
					t.Errorf("request = %v", req)
				}
			}},
		{"change requests", runOpts{}, []string{"plan", "--changes", writeTemp(t, dir, "c.json", `[{"dn":"`+aliceDN+`","type":"delete"}]`)},
			func(t *testing.T, req map[string]any) {
				changes, _ := req["changes"].([]any)
				if len(changes) != 1 || req["ldif"] != nil || req["mode"] != nil {
					t.Errorf("request = %v", req)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub(t)
			s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
			expectCode(t, s.run(t, tc.opts, tc.args...), ExitOK)
			var req map[string]any
			_ = json.Unmarshal(s.called("POST /api/v1/plan")[0].Body, &req)
			tc.check(t, req)
		})
	}
}

func TestPlanShowsWhatAlderPlannedAndWithholdsWhatItWithheld(t *testing.T) {
	s := newStub(t)
	body := planJSON(t, counts{modify: 1}, exactModify("b0"))
	s.reply("POST /api/v1/plan", http.StatusOK, body)
	ldifFile := writeTemp(t, t.TempDir(), "a.ldif", restoreLDIF+"replace: userPassword\nuserPassword: "+testSecret+"\n-\n")

	r := s.run(t, runOpts{}, "plan", ldifFile)
	expectCode(t, r, ExitOK)
	for _, want := range []string{"Plan: 1 change examined", "1 modify", "modify       " + aliceDN,
		"    replace: title", "userPassword: withheld (26 bytes)"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %q in:\n%s", want, r.stdout)
		}
	}

	r = s.run(t, runOpts{}, "plan", ldifFile, "--json")
	expectCode(t, r, ExitOK)
	oneJSONDocument(t, r.stdout)
	if strings.TrimSpace(r.stdout) != body {
		t.Errorf("stdout is not Alder's plan:\n%s", r.stdout)
	}
}

func TestAPlanThatCannotBeAppliedExitsThree(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{conflict: 1}, map[string]any{
		"index": 0, "dn": aliceDN, "action": "conflict", "exists": true, "problem": map[string]any{"code": "entry_exists"},
	}))
	r := s.run(t, runOpts{}, "plan", writeTemp(t, t.TempDir(), "a.ldif", restoreLDIF), "--json")
	expectCode(t, r, ExitNotApplicable)
	oneJSONDocument(t, r.stdout)
}

func TestMalformedInputIsAlderRefusalKeptWhole(t *testing.T) {
	s := newStub(t)
	refusal := apiErrorJSON("bad_request", "The LDIF could not be parsed.", map[string]any{"detail": "line 3: missing a colon"})
	s.reply("POST /api/v1/plan", http.StatusBadRequest, refusal)
	ldifFile := writeTemp(t, t.TempDir(), "a.ldif", "dn: uid=a\nnonsense\n")
	r := s.run(t, runOpts{}, "plan", ldifFile)
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "line 3: missing a colon") {
		t.Errorf("stderr = %s", r.stderr)
	}
	r = s.run(t, runOpts{}, "plan", ldifFile, "--json")
	doc := oneJSONDocument(t, r.stdout)
	if e := doc["error"].(map[string]any); e["error"] != "bad_request" || e["detail"] != "line 3: missing a colon" {
		t.Errorf("stdout = %s", r.stdout)
	}
}

func TestChangeRequestsWithUnknownFieldsAreRefusedNotTrimmed(t *testing.T) {
	s := newStub(t)
	file := writeTemp(t, t.TempDir(), "c.json", `[{"dn":"`+aliceDN+`","type":"delete","recursive":true}]`)
	r := s.run(t, runOpts{}, "plan", "--changes", file)
	expectCode(t, r, ExitFailed)
	if len(s.called("POST /api/v1/session")) != 0 {
		t.Error("sent a change with part of it dropped")
	}
}

// --- apply ------------------------------------------------------------------

func applyStub(t *testing.T, plan string) *stub {
	t.Helper()
	s := newStub(t)
	s.reply("POST /api/v1/import/ldif", http.StatusOK, importJSON(t))
	s.reply("POST /api/v1/plan", http.StatusOK, plan)
	s.reply("POST /api/v1/changeset/apply", http.StatusOK, `{"appliedCount":1,"outcomes":[{"index":0,"dn":"`+aliceDN+`","applied":true,"summary":"modified"}]}`)
	return s
}

func TestApplyAsksAndTheDefaultIsNo(t *testing.T) {
	for name, answer := range map[string]string{"enter": "\n", "no": "no\n", "end of input": "", "anything else": "sure\n"} {
		t.Run(name, func(t *testing.T) {
			s := applyStub(t, planJSON(t, counts{modify: 1}, exactModify("b0")))
			r := s.run(t, runOpts{interactive: true, stdin: answer}, "apply", writeTemp(t, t.TempDir(), "a.ldif", restoreLDIF))
			expectCode(t, r, ExitNotConfirmed)
			if !strings.Contains(r.stderr, "Apply 1 change to ldap.example.test:636? [y/N]") {
				t.Errorf("stderr = %s", r.stderr)
			}
			if len(s.called("POST /api/v1/changeset/apply")) != 0 {
				t.Fatal("applied without a yes")
			}
		})
	}
}

func TestApplyAppliesThePlanThatWasShown(t *testing.T) {
	s := applyStub(t, planJSON(t, counts{modify: 1}, exactModify("token-from-the-plan")))
	r := s.run(t, runOpts{interactive: true, stdin: "y\n"}, "apply", writeTemp(t, t.TempDir(), "a.ldif", restoreLDIF))
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stdout, "Applied 1 change.") || !strings.Contains(r.stdout, "userPassword: withheld (26 bytes)") {
		t.Errorf("stdout = %s", r.stdout)
	}
	if len(s.called("POST /api/v1/plan")) != 1 {
		t.Error("the plan was made more than once")
	}
	var sent struct {
		Changes []struct {
			Dn       string `json:"dn"`
			Baseline string `json:"baseline"`
			Mods     []struct {
				Name   string `json:"name"`
				Values []struct {
					Text *string `json:"text"`
					Size *int    `json:"size"`
				} `json:"values"`
			} `json:"mods"`
		} `json:"changes"`
	}
	_ = json.Unmarshal(s.called("POST /api/v1/changeset/apply")[0].Body, &sent)
	if len(sent.Changes) != 1 || sent.Changes[0].Baseline != "token-from-the-plan" {
		t.Fatalf("applied %s", s.called("POST /api/v1/changeset/apply")[0].Body)
	}
	// The exact change goes from the client's own copy, never the plan's
	// withheld record: a size where a password should be would be refused.
	pw := sent.Changes[0].Mods[1].Values[0]
	if pw.Text == nil || *pw.Text != testSecret || pw.Size != nil {
		t.Errorf("the password was sent as %+v, not from the client's copy", pw)
	}
}

func TestApplyWithoutATerminalNeedsYes(t *testing.T) {
	s := applyStub(t, planJSON(t, counts{modify: 1}, exactModify("b0")))
	file := writeTemp(t, t.TempDir(), "a.ldif", restoreLDIF)

	r := s.run(t, runOpts{}, "apply", file)
	expectCode(t, r, ExitNotConfirmed)
	if len(s.called("POST /api/v1/session")) != 0 {
		t.Error("connected for an apply that could not be confirmed")
	}

	// Standard input carrying the document is not a person to ask.
	r = s.run(t, runOpts{interactive: true, stdin: restoreLDIF}, "apply", "-")
	expectCode(t, r, ExitNotConfirmed)

	r = s.run(t, runOpts{}, "apply", file, "--yes")
	expectCode(t, r, ExitOK)
	if len(s.called("POST /api/v1/changeset/apply")) != 1 {
		t.Error("--yes did not apply")
	}
}

func TestDeletionsNeedTheirOwnPermissionWhenYesAnswers(t *testing.T) {
	daveDN := "uid=dave,ou=people,dc=example,dc=test"
	plan := planJSON(t, counts{delete: 1}, map[string]any{
		"index": 0, "dn": daveDN, "action": "delete", "exists": true, "intent": "exact",
		"record": map[string]any{"dn": daveDN, "type": "delete"}, "baseline": "b0",
		"preview": map[string]any{"ldif": "dn: " + daveDN + "\nchangetype: delete\n", "ansible": "", "summary": ""},
	})
	file := writeTemp(t, t.TempDir(), "d.ldif", "dn: "+daveDN+"\nchangetype: delete\n")
	s := applyStub(t, plan)
	s.reply("POST /api/v1/import/ldif", http.StatusOK, `{"changes":[],"requests":[{"dn":"`+daveDN+`","type":"delete"}]}`)

	expectCode(t, s.run(t, runOpts{}, "apply", file, "--yes"), ExitNotConfirmed)
	if len(s.called("POST /api/v1/changeset/apply")) != 0 {
		t.Fatal("--yes deleted without --allow-deletes")
	}
	expectCode(t, s.run(t, runOpts{}, "apply", file, "--yes", "--allow-deletes"), ExitOK)

	r := s.run(t, runOpts{interactive: true, stdin: "y\n"}, "apply", file)
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stderr, "deleting 1 entry?") {
		t.Errorf("the question did not say it deletes: %s", r.stderr)
	}
}

func TestAStalePlanStopsAndIsNeverPlannedAgain(t *testing.T) {
	for name, refusal := range map[string]struct {
		status int
		body   string
	}{
		"stale":    {http.StatusConflict, apiErrorJSON("conflict", "The directory changed.", map[string]any{"cause": "plan_stale", "affected": []any{map[string]any{"index": 0, "dn": aliceDN}}})},
		"mismatch": {http.StatusBadRequest, apiErrorJSON("plan_mismatch", "Not the planned operation.", nil)},
	} {
		t.Run(name, func(t *testing.T) {
			s := applyStub(t, planJSON(t, counts{modify: 1}, exactModify("b0")))
			s.reply("POST /api/v1/changeset/apply", refusal.status, refusal.body)
			r := s.run(t, runOpts{}, "apply", writeTemp(t, t.TempDir(), "a.ldif", restoreLDIF), "--yes", "--json")
			expectCode(t, r, ExitStale)
			if !strings.Contains(r.stderr, "nothing was written") || !strings.Contains(r.stderr, "review a new plan") {
				t.Errorf("stderr = %s", r.stderr)
			}
			if len(s.called("POST /api/v1/plan")) != 1 || len(s.called("POST /api/v1/changeset/apply")) != 1 {
				t.Error("a refused plan was planned or applied again")
			}
			doc := oneJSONDocument(t, r.stdout)
			if doc["plan"] == nil || doc["result"] != nil {
				t.Errorf("stdout = %s", r.stdout)
			}
			if e := doc["error"].(map[string]any); e["error"] == nil {
				t.Errorf("the typed error was lost: %v", e)
			}
		})
	}
}

func TestApplyRefusesAPlanItCannotApplyWhole(t *testing.T) {
	s := applyStub(t, planJSON(t, counts{modify: 1, conflict: 1}, exactModify("b0"), map[string]any{
		"index": 1, "dn": "uid=x,dc=example,dc=test", "action": "conflict", "exists": false, "problem": map[string]any{"code": "entry_missing"},
	}))
	expectCode(t, s.run(t, runOpts{}, "apply", writeTemp(t, t.TempDir(), "a.ldif", restoreLDIF), "--yes"), ExitNotApplicable)
	if len(s.called("POST /api/v1/changeset/apply")) != 0 {
		t.Fatal("applied part of a plan with a conflict in it")
	}
}

func TestNothingToApplyIsSuccessAndSendsNothing(t *testing.T) {
	s := applyStub(t, planJSON(t, counts{unchanged: 1}, map[string]any{"index": 0, "dn": aliceDN, "action": "unchanged", "exists": true}))
	r := s.run(t, runOpts{}, "apply", writeTemp(t, t.TempDir(), "a.ldif", restoreLDIF), "--yes")
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stdout, "Nothing to apply") || len(s.called("POST /api/v1/changeset/apply")) != 0 {
		t.Errorf("stdout = %s", r.stdout)
	}
}

func TestAnApplyThatStopsPartwaySaysHowFarItGot(t *testing.T) {
	twoItems := planJSON(t, counts{modify: 2}, exactModify("b0"), func() map[string]any {
		item := exactModify("b1")
		item["index"] = 1
		return item
	}())
	file := writeTemp(t, t.TempDir(), "a.ldif", restoreLDIF+"\n"+restoreLDIF)

	s := applyStub(t, twoItems)
	s.reply("POST /api/v1/import/ldif", http.StatusOK, `{"changes":[],"requests":[{"dn":"`+aliceDN+`","type":"modify"},{"dn":"`+aliceDN+`","type":"modify"}]}`)
	s.reply("POST /api/v1/changeset/apply", http.StatusOK, `{"appliedCount":1,"failedIndex":1,"outcomes":[`+
		`{"index":0,"dn":"`+aliceDN+`","applied":true},{"index":1,"dn":"`+aliceDN+`","applied":false,"error":{"error":"constraint_violation","message":"no"}}]}`)
	r := s.run(t, runOpts{}, "apply", file, "--yes")
	expectCode(t, r, ExitPartial)
	if !strings.Contains(r.stderr, "after applying 1 of 2") {
		t.Errorf("stderr = %s", r.stderr)
	}

	s.reply("POST /api/v1/changeset/apply", http.StatusOK, `{"appliedCount":0,"failedIndex":0,"outcomes":[`+
		`{"index":0,"dn":"`+aliceDN+`","applied":false,"error":{"error":"constraint_violation","message":"no"}},{"index":1,"dn":"`+aliceDN+`","applied":false}]}`)
	expectCode(t, s.run(t, runOpts{}, "apply", file, "--yes"), ExitFailed)
}

func TestApplyJSONHoldsThePlanAndTheResult(t *testing.T) {
	plan := planJSON(t, counts{modify: 1}, exactModify("b0"))
	s := applyStub(t, plan)
	r := s.run(t, runOpts{}, "apply", writeTemp(t, t.TempDir(), "a.ldif", restoreLDIF), "--yes", "--json")
	expectCode(t, r, ExitOK)
	doc := oneJSONDocument(t, r.stdout)
	if doc["plan"] == nil || doc["result"].(map[string]any)["appliedCount"] != float64(1) || doc["error"] != nil {
		t.Errorf("stdout = %s", r.stdout)
	}
	if !strings.Contains(r.stderr, "Plan: 1 change examined") {
		t.Errorf("the plan was not shown on standard error:\n%s", r.stderr)
	}
}

func TestADesiredStateChangeIsAppliedFromThePlansRecord(t *testing.T) {
	item := exactModify("b0")
	item["intent"] = "desired"
	s := applyStub(t, planJSON(t, counts{modify: 1}, item))
	r := s.run(t, runOpts{}, "apply", writeTemp(t, t.TempDir(), "a.ldif", "dn: "+aliceDN+"\ntitle: Before\n"), "--mode", "desired", "--yes")
	expectCode(t, r, ExitOK)
	if len(s.called("POST /api/v1/import/ldif")) != 0 {
		t.Error("desired state needs no client copy, and none should be parsed")
	}
	if !strings.Contains(string(s.called("POST /api/v1/changeset/apply")[0].Body), `"size":26`) {
		t.Error("a desired-state change was not sent from the plan's record")
	}
}

func TestAnApplyWithNoAnswerSaysTheOutcomeIsUnknown(t *testing.T) {
	s := applyStub(t, planJSON(t, counts{modify: 1}, exactModify("b0")))
	s.on("POST /api/v1/changeset/apply", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})
	r := s.run(t, runOpts{}, "apply", writeTemp(t, t.TempDir(), "a.ldif", restoreLDIF), "--yes")
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "some changes may have been written") {
		t.Errorf("stderr = %s", r.stderr)
	}
}
