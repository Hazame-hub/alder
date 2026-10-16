package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/signing"
)

// 1.39: a saved plan, signed.
//
// alder sign wraps a saved plan as it wraps any document, and the two apply
// commands read the envelope: its payload goes through the same strict
// reader as a bare plan, a signature that does not match is refused whatever
// was asked, and --trusted-keys makes a signature by one of those keys a
// condition of applying at all.

// signFile signs path with key through the command and returns the envelope.
func signFile(t *testing.T, path, key, signer string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "signed."+filepath.Base(path))
	r := newStub(t).run(t, runOpts{noConnection: true}, "sign", path, "--key", key, "--signer", signer, "--output", out)
	expectCode(t, r, ExitOK)
	return out
}

// rewritePayload changes the plan inside an envelope, leaving its signatures
// as they were.
func rewritePayload(t *testing.T, envelope string, change func(plan map[string]any)) string {
	t.Helper()
	doc := readSaved(t, envelope)
	plan, _ := doc["payload"].(map[string]any)
	if plan == nil {
		t.Fatalf("%s carries no payload", envelope)
	}
	change(plan)
	out := filepath.Join(t.TempDir(), "edited.json")
	if err := os.WriteFile(out, []byte(mustJSON(t, doc)), 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestASavedPlanIsSignedLikeAnyDocument(t *testing.T) {
	s := inputPlanStub(t, true)
	saved := saveInput(t, s)
	keys := t.TempDir()
	private, public := generateKey(t, keys, "reviewer")
	signed := signFile(t, saved, private, "reviewer")

	r := newStub(t).run(t, runOpts{noConnection: true}, "verify", signed, "--trusted-keys", public, "--json")
	expectCode(t, r, ExitOK)
	doc := oneJSONDocument(t, r.stdout)
	if payload, _ := doc["payload"].(map[string]any); payload["format"] != "alder-plan" || payload["version"] != float64(1) {
		t.Errorf("verify does not see a saved plan inside: %v", doc["payload"])
	}
}

func TestProjectApplyOfASignedPlan(t *testing.T) {
	keys := t.TempDir()
	private, public := generateKey(t, keys, "reviewer")
	_, otherPublic := generateKey(t, keys, "someone")

	setup := func(t *testing.T) (*stub, string, string, string) {
		s := savedPlanStub(t, true)
		dir := writeSavedPlanProject(t, s)
		saved := savePlanFor(t, s, dir)
		return s, dir, saved, signFile(t, saved, private, "reviewer")
	}
	apply := func(s *stub, dir, plan string, extra ...string) result {
		return s.run(t, projectRun, append([]string{"project", "apply", "--project", dir, "--env", "dev", "--plan", plan, "--yes"}, extra...)...)
	}
	keyID := func(t *testing.T) string {
		r := newStub(t).run(t, runOpts{noConnection: true}, "key", "show", public)
		expectCode(t, r, ExitOK)
		return strings.TrimSpace(r.stdout)
	}

	t.Run("an unsigned plan applies as before when no key is named", func(t *testing.T) {
		s, dir, saved, _ := setup(t)
		r := apply(s, dir, saved, "--json")
		expectCode(t, r, ExitOK)
		if doc := oneJSONDocument(t, r.stdout); doc["signature"] != nil {
			t.Errorf("an unsigned plan reports a signature: %v", doc["signature"])
		}
	})

	t.Run("a plan signed by a named key applies, and says by whom", func(t *testing.T) {
		s, dir, _, signed := setup(t)
		r := apply(s, dir, signed, "--trusted-keys", public)
		expectCode(t, r, ExitOK)
		if len(s.called("POST /api/v1/changeset/apply")) != 1 {
			t.Fatal("the signed plan was not applied")
		}
		if id := keyID(t); !strings.Contains(r.stdout, id) || !strings.Contains(r.stdout, "reviewer") ||
			!strings.Contains(r.stdout, "verified") {
			t.Errorf("the plan shown does not name its verified signer %s:\n%s", id, r.stdout)
		}
	})

	t.Run("with --json, one document carrying the signature", func(t *testing.T) {
		s, dir, _, signed := setup(t)
		r := apply(s, dir, signed, "--trusted-keys", public, "--json")
		expectCode(t, r, ExitOK)
		doc := oneJSONDocument(t, r.stdout)
		sig, _ := doc["signature"].(map[string]any)
		signers, _ := sig["signers"].([]any)
		if sig["status"] != "verified" || len(signers) != 1 {
			t.Fatalf("signature = %v", doc["signature"])
		}
		if first := signers[0].(map[string]any); first["keyId"] != keyID(t) || first["trusted"] != true ||
			first["signer"] != "reviewer" {
			t.Errorf("signer = %v", first)
		}
	})

	t.Run("a signed plan applies with no key named, its signature unchecked", func(t *testing.T) {
		s, dir, _, signed := setup(t)
		r := apply(s, dir, signed, "--json")
		expectCode(t, r, ExitOK)
		doc := oneJSONDocument(t, r.stdout)
		if sig, _ := doc["signature"].(map[string]any); sig["status"] != "unchecked" {
			t.Errorf("signature = %v", doc["signature"])
		}
		if !strings.Contains(r.stderr, "not checked") {
			t.Errorf("the output does not say the signature was not checked:\n%s", r.stderr)
		}
	})

	t.Run("refusals, before connecting", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			plan  func(t *testing.T, saved, signed, dir string) string
			args  []string
			code  int
			local string
		}{
			{"unsigned, with a key named", func(_ *testing.T, saved, _, _ string) string { return saved },
				[]string{"--trusted-keys", public}, ExitNotApplicable, "unsigned"},
			{"signed by a key nobody named", func(_ *testing.T, _, signed, _ string) string { return signed },
				[]string{"--trusted-keys", otherPublic}, ExitNotApplicable, "signature_untrusted"},
			{"changed after it was signed", func(t *testing.T, _, signed, _ string) string {
				return rewritePayload(t, signed, func(p map[string]any) { p["createdAt"] = "2020-01-01T00:00:00Z" })
			}, []string{"--trusted-keys", public}, ExitFailed, "signature_invalid"},
			{"signed, by a trusted key, for project files that changed since", func(t *testing.T, _, signed, dir string) string {
				edit(t, filepath.Join(dir, "people/bob.ldif"), "sn: B", "sn: Bee")
				return signed
			}, []string{"--trusted-keys", public}, ExitNotApplicable, "saved_plan_mismatch"},
			{"a signed document that is not a plan", func(t *testing.T, _, _, _ string) string {
				doc := writeTemp(t, t.TempDir(), "snapshot.json", signableDoc)
				return signFile(t, doc, private, "reviewer")
			}, []string{"--trusted-keys", public}, ExitNotApplicable, "saved_plan_invalid"},
			{"a signed document that is not a plan, no key named", func(t *testing.T, _, _, _ string) string {
				doc := writeTemp(t, t.TempDir(), "snapshot.json", signableDoc)
				return signFile(t, doc, private, "reviewer")
			}, nil, ExitNotApplicable, "saved_plan_invalid"},
			{"a signed plan whose payload names a key twice", func(t *testing.T, _, signed, _ string) string {
				data, _ := os.ReadFile(signed)
				bad := strings.Replace(string(data), `"format": "alder-plan"`, `"format": "alder-plan", "format": "alder-plan"`, 1)
				if bad == string(data) {
					t.Fatalf("no format in:\n%s", data)
				}
				return writeTemp(t, t.TempDir(), "twice.json", bad)
			}, nil, ExitNotApplicable, "saved_plan_invalid"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s, dir, saved, signed := setup(t)
				plan := tc.plan(t, saved, signed, dir)
				before := len(s.calls)
				// No --yes and no terminal: a refusal of the file comes before
				// the question of who confirms it.
				r := s.run(t, projectRun, append([]string{"project", "apply", "--project", dir, "--env", "dev",
					"--plan", plan, "--json"}, tc.args...)...)
				expectCode(t, r, tc.code)
				doc := oneJSONDocument(t, r.stdout)
				if e, _ := doc["error"].(map[string]any); e["error"] != tc.local {
					t.Errorf("error = %v, want %s", doc["error"], tc.local)
				}
				if len(s.calls) != before {
					t.Errorf("%d request(s) sent for a plan that was refused", len(s.calls)-before)
				}
			})
		}
	})
}

func TestApplyOfASignedInputPlan(t *testing.T) {
	keys := t.TempDir()
	private, public := generateKey(t, keys, "reviewer")
	_, otherPublic := generateKey(t, keys, "someone")

	t.Run("signed by a named key, applied, one JSON document", func(t *testing.T) {
		s := inputPlanStub(t, true)
		signed := signFile(t, saveInput(t, s), private, "ci")
		r := s.run(t, runOpts{}, "apply", "--plan", signed, "--yes", "--allow-deletes", "--trusted-keys", public, "--json")
		expectCode(t, r, ExitOK)
		doc := oneJSONDocument(t, r.stdout)
		if sig, _ := doc["signature"].(map[string]any); sig["status"] != "verified" {
			t.Errorf("signature = %v", doc["signature"])
		}
		if len(s.called("POST /api/v1/changeset/apply")) != 1 {
			t.Error("the signed plan was not applied")
		}
	})

	t.Run("shown with its signer", func(t *testing.T) {
		s := inputPlanStub(t, true)
		signed := signFile(t, saveInput(t, s), private, "ci")
		r := s.run(t, runOpts{}, "apply", "--plan", signed, "--yes", "--allow-deletes", "--trusted-keys", public)
		expectCode(t, r, ExitOK)
		if !strings.Contains(r.stdout, "verified") || !strings.Contains(r.stdout, "ci") {
			t.Errorf("stdout does not name the signer:\n%s", r.stdout)
		}
	})

	for _, tc := range []struct {
		name string
		plan func(t *testing.T, saved, signed string) string
		args []string
		code int
	}{
		{"unsigned with a key named", func(_ *testing.T, saved, _ string) string { return saved },
			[]string{"--trusted-keys", public}, ExitNotApplicable},
		{"untrusted", func(_ *testing.T, _, signed string) string { return signed },
			[]string{"--trusted-keys", otherPublic}, ExitNotApplicable},
		{"invalid", func(t *testing.T, _, signed string) string {
			return rewritePayload(t, signed, func(p map[string]any) {
				p["changes"].([]any)[0].(map[string]any)["dn"] = "uid=mallory,ou=people,dc=example,dc=test"
			})
		}, []string{"--trusted-keys", public}, ExitFailed},
		{"not a plan", func(t *testing.T, _, _ string) string {
			return signFile(t, writeTemp(t, t.TempDir(), "snapshot.json", signableDoc), private, "ci")
		}, []string{"--trusted-keys", public}, ExitNotApplicable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := inputPlanStub(t, true)
			saved := saveInput(t, s)
			plan := tc.plan(t, saved, signFile(t, saved, private, "ci"))
			before := len(s.calls)
			r := s.run(t, runOpts{}, append([]string{"apply", "--plan", plan, "--json"}, tc.args...)...)
			expectCode(t, r, tc.code)
			oneJSONDocument(t, r.stdout)
			if len(s.calls) != before {
				t.Errorf("%d request(s) sent for a plan that was refused", len(s.calls)-before)
			}
		})
	}
}

func TestTrustedKeysIsNotReadFromTheEnvironment(t *testing.T) {
	keys := t.TempDir()
	_, otherPublic := generateKey(t, keys, "someone")
	s := inputPlanStub(t, true)
	saved := saveInput(t, s)
	// An inherited ALDER_TRUSTED_KEYS would refuse this unsigned plan if it
	// were read; it is not, so the plan applies as an unsigned one does.
	r := s.run(t, runOpts{env: map[string]string{"ALDER_TRUSTED_KEYS": otherPublic}},
		"apply", "--plan", saved, "--yes", "--allow-deletes")
	expectCode(t, r, ExitOK)
}

func TestTrustedKeysWithoutAPlanIsAUsageError(t *testing.T) {
	keys := t.TempDir()
	_, public := generateKey(t, keys, "reviewer")
	s := savedPlanStub(t, true)
	dir := writeSavedPlanProject(t, s)
	r := s.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--yes", "--trusted-keys", public)
	expectCode(t, r, ExitUsage)
	r = s.run(t, runOpts{}, "apply", writeTemp(t, t.TempDir(), "change.ldif", inputLDIF), "--yes", "--trusted-keys", public)
	expectCode(t, r, ExitUsage)
	if len(s.calls) != 0 {
		t.Errorf("%d request(s) sent for a command line that was wrong", len(s.calls))
	}
}

// A signature by a key nobody named was never checked, so its signer label is
// the file's word only, even under a verified header.
func TestAnUncheckedSignerIsNotShownAsVerified(t *testing.T) {
	var out strings.Builder
	renderSignature(&out, &verifyEnvelope{Status: string(signing.Verified), Signers: []signing.Signer{
		{KeyID: "aa", Signer: "reviewer", Trusted: true},
		{KeyID: "bb", Signer: "security-lead (approved)"},
	}})
	got := out.String()
	if !strings.Contains(got, "aa  trusted  reviewer") || !strings.Contains(got, "bb  not checked  security-lead") {
		t.Errorf("header:\n%s", got)
	}
}
