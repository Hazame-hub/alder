package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const passwordChanges = `[{"dn":"` + aliceDN + `","type":"setpassword"},{"dn":"` + bobDN + `","type":"setpassword"}]`

// passwordStub answers alder plan for two password changes, alice's and bob's,
// planned without their passwords, and collects each plan request's body and
// what was sent to apply.
func passwordStub(t *testing.T, plans, applied *[]string) *stub {
	t.Helper()
	s := newStub(t)
	stubAttributeNames(s)
	item := func(index int, dn string, atApply bool) map[string]any {
		it := map[string]any{"index": index, "dn": dn, "action": "set_password", "exists": true, "intent": "exact",
			"kind": "data", "record": map[string]any{"dn": dn, "type": "setpassword"}, "baseline": "b-" + dn}
		if atApply {
			it["passwordSuppliedAtApply"] = true
		}
		return it
	}
	s.on("POST /api/v1/plan", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if plans != nil {
			*plans = append(*plans, string(body))
		}
		atApply := !strings.Contains(string(body), "newPassword")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, planJSON(t, counts{setPassword: 2}, item(0, aliceDN, atApply), item(1, bobDN, atApply)))
	})
	s.on("GET /api/v1/entry", func(w http.ResponseWriter, r *http.Request) {
		dn := r.URL.Query().Get("dn")
		attrs := []any{map[string]any{"name": "cn", "values": []any{map[string]any{"text": "x"}}}}
		if r.URL.Query().Get("includeOperational") == "true" {
			attrs = append(attrs, map[string]any{"name": "entryUUID", "values": []any{map[string]any{"text": "uuid-" + dn}}})
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, mustJSON(t, map[string]any{"dn": dn, "attributes": attrs}))
	})
	s.on("POST /api/v1/changeset/apply", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if applied != nil {
			*applied = append(*applied, string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"appliedCount":2,"outcomes":[{"index":0,"dn":"`+aliceDN+`","applied":true,"summary":"set"},`+
			`{"index":1,"dn":"`+bobDN+`","applied":true,"summary":"set"}]}`)
	})
	return s
}

func savePasswords(t *testing.T, s *stub) (string, string) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "reset.plan.json")
	r := s.run(t, runOpts{}, "plan", "--changes", writeTemp(t, dir, "reset.json", passwordChanges), "--out", out)
	expectCode(t, r, ExitOK)
	return dir, out
}

func TestPlanOutSavesAPasswordChangeWithoutThePassword(t *testing.T) {
	var plans []string
	s := passwordStub(t, &plans, nil)
	_, out := savePasswords(t, s)
	data, _ := os.ReadFile(out)
	if strings.Contains(string(data), "newPassword") {
		t.Fatalf("the saved plan holds a password field:\n%s", data)
	}
	changes, _ := readSaved(t, out)["changes"].([]any)
	if len(changes) != 2 {
		t.Fatalf("changes = %v", changes)
	}
	for _, c := range changes {
		change := c.(map[string]any)
		expect, _ := change["expect"].(map[string]any)
		attrs, _ := expect["attributes"].([]any)
		if change["type"] != "setpassword" || len(attrs) != 1 || attrs[0].(map[string]any)["name"] != "entryUUID" ||
			expect["subordinates"] != nil {
			t.Errorf("saved change = %v; want a password change expecting its entry's identity only", change)
		}
	}
}

func TestPlanOutRefusesAPasswordInItsInput(t *testing.T) {
	s := passwordStub(t, nil, nil)
	dir := t.TempDir()
	out := filepath.Join(dir, "reset.plan.json")
	in := writeTemp(t, dir, "reset.json", `[{"dn":"`+aliceDN+`","type":"setpassword","newPassword":"hunter2-hunter2"}]`)
	r := s.run(t, runOpts{}, "plan", "--changes", in, "--out", out)
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "leave newPassword out") || strings.Contains(r.stderr+r.stdout, "hunter2") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("the plan was saved")
	}
}

func TestApplyPlanTakesThePasswordsFromAFileInPlanOrder(t *testing.T) {
	var plans, applied []string
	s := passwordStub(t, &plans, &applied)
	dir, out := savePasswords(t, s)
	plans = nil
	secrets := writeTemp(t, dir, "secrets", "first-secret-1\r\nsecond-secret-2\n")
	r := s.run(t, runOpts{}, "apply", "--plan", out, "--new-password-file", secrets, "--yes", "--json")
	expectCode(t, r, ExitOK)
	if len(plans) != 1 || !strings.Contains(plans[0], `"newPassword":"first-secret-1"`) {
		t.Fatalf("apply did not plan with the passwords: %v", plans)
	}
	var body struct {
		Changes []struct {
			Dn          string `json:"dn"`
			NewPassword string `json:"newPassword"`
		} `json:"changes"`
	}
	if len(applied) != 1 || json.Unmarshal([]byte(applied[0]), &body) != nil || len(body.Changes) != 2 {
		t.Fatalf("applied = %v", applied)
	}
	if body.Changes[0].Dn != aliceDN || body.Changes[0].NewPassword != "first-secret-1" ||
		body.Changes[1].Dn != bobDN || body.Changes[1].NewPassword != "second-secret-2" {
		t.Errorf("applied %+v; want alice's then bob's password, in plan order", body.Changes)
	}
	if strings.Contains(r.stdout+r.stderr, "secret-") {
		t.Errorf("a password reached the output:\n%s\n%s", r.stdout, r.stderr)
	}
}

func TestApplyPlanTakesThePasswordsFromStandardInput(t *testing.T) {
	var applied []string
	s := passwordStub(t, nil, &applied)
	_, out := savePasswords(t, s)
	r := s.run(t, runOpts{stdin: "a-secret\nb-secret\n"}, "apply", "--plan", out, "--new-password-stdin", "--yes")
	expectCode(t, r, ExitOK)
	if len(applied) != 1 || !strings.Contains(applied[0], "b-secret") {
		t.Fatalf("applied = %v", applied)
	}
}

func TestApplyPlanAsksForEachPasswordTwiceAtTheTerminal(t *testing.T) {
	var applied []string
	s := passwordStub(t, nil, &applied)
	_, out := savePasswords(t, s)
	var prompts []string
	ask := func(_ context.Context, prompt string) (string, error) {
		prompts = append(prompts, prompt)
		if strings.Contains(prompt, "bob") {
			return "bob-secret", nil
		}
		return "alice-secret", nil
	}
	r := s.run(t, runOpts{interactive: true, ask: ask, stdin: "y\n"}, "apply", "--plan", out)
	expectCode(t, r, ExitOK)
	if len(prompts) != 4 || !strings.Contains(prompts[0], aliceDN) || !strings.Contains(prompts[3], bobDN) {
		t.Errorf("prompts = %q", prompts)
	}
	if len(applied) != 1 || !strings.Contains(applied[0], "alice-secret") {
		t.Fatalf("applied = %v", applied)
	}

	// Two different answers are refused, and nothing is sent.
	applied = nil
	n := 0
	differ := func(context.Context, string) (string, error) { n++; return strings.Repeat("x", n), nil }
	r = s.run(t, runOpts{interactive: true, ask: differ, stdin: "y\n"}, "apply", "--plan", out)
	expectCode(t, r, ExitUsage)
	if !strings.Contains(r.stderr, "differ") || len(applied) != 0 {
		t.Errorf("stderr:\n%s", r.stderr)
	}
}

func TestApplyPlanRefusesTheWrongNumberOfPasswordsBeforeReadingTheDirectory(t *testing.T) {
	var plans, applied []string
	s := passwordStub(t, &plans, &applied)
	dir, out := savePasswords(t, s)
	for _, tc := range []struct{ name, content, want string }{
		{"one too few", "only-one\n", "sets 2 passwords, and 1 was given"},
		{"one too many", "a\nb\nc\n", "sets 2 passwords, and 3 were given"},
		{"an empty line", "a\n\nb\n", "line 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plans, applied = nil, nil
			r := s.run(t, runOpts{}, "apply", "--plan", out, "--new-password-file", writeTemp(t, dir, "s", tc.content), "--yes")
			expectCode(t, r, ExitUsage)
			if !strings.Contains(r.stderr, tc.want) {
				t.Errorf("stderr does not say %q:\n%s", tc.want, r.stderr)
			}
			if len(plans) != 0 || len(applied) != 0 {
				t.Error("the directory was asked")
			}
		})
	}
}

func TestApplyPlanPasswordUsage(t *testing.T) {
	s := passwordStub(t, nil, nil)
	dir, out := savePasswords(t, s)
	secrets := writeTemp(t, dir, "s", "a\nb\n")
	for _, tc := range []struct {
		name string
		args []string
		opts runOpts
		want string
	}{
		{"none given with --yes", []string{"apply", "--plan", out, "--yes"}, runOpts{}, "--new-password-file or --new-password-stdin"},
		{"both sources", []string{"apply", "--plan", out, "--yes", "--new-password-file", secrets, "--new-password-stdin"}, runOpts{},
			"two answers to one question"},
		{"stdin for both", []string{"apply", "--plan", out, "--yes", "--new-password-stdin", "--bind-dn", "cn=admin",
			"--bind-password-stdin"}, runOpts{stdin: "x\n"}, "both the bind password and the new passwords"},
		{"without a plan", []string{"apply", "--changes", secrets, "--new-password-file", secrets}, runOpts{},
			"supply the passwords of a --plan file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := s.run(t, tc.opts, tc.args...)
			expectCode(t, r, ExitUsage)
			if !strings.Contains(r.stderr, tc.want) {
				t.Errorf("stderr does not say %q:\n%s", tc.want, r.stderr)
			}
		})
	}
}

func TestASavedPasswordChangeIsReadStrictly(t *testing.T) {
	s := passwordStub(t, nil, nil)
	dir, out := savePasswords(t, s)
	secrets := writeTemp(t, dir, "s", "a\nb\n")
	for _, tc := range []struct{ name, from, to, want string }{
		{"a password in the file", `"type": "setpassword"`, `"type": "setpassword", "newPassword": "x"`, "carries a password"},
		{"no identity", `"name": "entryUUID"`, `"name": "cn"`, "does not expect the entry's identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out := savePasswords(t, s)
			edit(t, out, tc.from, tc.to)
			r := s.run(t, runOpts{}, "apply", "--plan", out, "--new-password-file", secrets, "--yes")
			expectCode(t, r, ExitNotApplicable)
			if !strings.Contains(r.stderr, tc.want) {
				t.Errorf("stderr:\n%s", r.stderr)
			}
		})
	}
	_ = out
}

func TestApplyRefusesAPasswordChangeWithoutItsPassword(t *testing.T) {
	var applied []string
	s := passwordStub(t, nil, &applied)
	dir := t.TempDir()
	r := s.run(t, runOpts{}, "apply", "--changes", writeTemp(t, dir, "c.json", passwordChanges), "--yes")
	expectCode(t, r, ExitNotApplicable)
	if !strings.Contains(r.stderr, "alder apply --plan") || len(applied) != 0 {
		t.Errorf("stderr:\n%s", r.stderr)
	}
}
