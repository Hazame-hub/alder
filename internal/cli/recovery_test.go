package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/recovery"
)

// A real bundle, made by the package that makes them, so the client's check
// of what it writes is exercised against the format and not a hand-written
// imitation of it.
func testBundle(t *testing.T) string {
	t.Helper()
	target := dn.MustParse(aliceDN)
	pre := directory.NewEntry(target)
	pre.Set("title", [][]byte{[]byte("Before")})
	record := directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModReplace, Name: "title", Values: [][]byte{[]byte("After")}}}}
	b, err := recovery.New(recovery.Origin{Vendor: "Stub", NamingContexts: []string{"dc=example,dc=test"}},
		[]recovery.Step{recovery.Derive(0, record, pre, nil, recovery.KindData)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return mustJSON(t, b)
}

func appliedJSON(t *testing.T, applied int, failed *int, bundle string) string {
	t.Helper()
	outcomes := []any{map[string]any{"index": 0, "dn": aliceDN, "applied": applied > 0, "summary": "modify alice"}}
	doc := map[string]any{"appliedCount": applied, "outcomes": outcomes}
	if failed != nil {
		doc["failedIndex"] = *failed
		if applied > 0 {
			outcomes = append(outcomes, map[string]any{"index": 1, "dn": aliceDN, "applied": false,
				"error": map[string]any{"error": "upstream", "message": "No Such Attribute"}})
			doc["outcomes"] = outcomes
		} else {
			outcomes[0].(map[string]any)["error"] = map[string]any{"error": "upstream", "message": "No Such Attribute"}
		}
	}
	raw := mustJSON(t, doc)
	if bundle != "" {
		raw = strings.TrimSuffix(raw, "}") + `,"recovery":` + bundle + "}"
	}
	return raw
}

const recoveryChanges = `[{"dn":"` + aliceDN + `","type":"modify","mods":[{"op":"replace","name":"title","values":[{"text":"After"}]}]}]`

func recoveryApplyStub(t *testing.T, applyReply string) *stub {
	t.Helper()
	s := newStub(t)
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
	s.reply("POST /api/v1/changeset/apply", http.StatusOK, applyReply)
	return s
}

func TestApplyWritesARecoveryBundleForWhatWasApplied(t *testing.T) {
	bundle := testBundle(t)
	s := recoveryApplyStub(t, appliedJSON(t, 1, nil, bundle))
	dir := t.TempDir()
	changes := writeTemp(t, dir, "c.json", recoveryChanges)
	out := filepath.Join(dir, "recovery.json")

	r := s.run(t, runOpts{}, "apply", "--changes", changes, "--yes", "--recovery-out", out)
	expectCode(t, r, ExitOK)
	var sent map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/changeset/apply")[0].Body, &sent)
	if sent["recovery"] != true {
		t.Errorf("the apply did not ask for a bundle: %v", sent)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("no bundle was written: %v", err)
	}
	if _, integrity, err := recovery.Decode(data); err != nil || integrity != recovery.IntegrityVerified {
		t.Errorf("the written bundle does not verify: %v %s", err, integrity)
	}
	if !strings.Contains(r.stderr, "recovery bundle for 1 applied change written to") {
		t.Errorf("stderr = %s", r.stderr)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(out); info.Mode().Perm() != 0o600 {
			t.Errorf("the bundle is readable by others: %v", info.Mode().Perm())
		}
	}

	second := filepath.Join(dir, "second.json")
	r = s.run(t, runOpts{}, "apply", "--changes", changes, "--yes", "--recovery-out", second, "--json")
	expectCode(t, r, ExitOK)
	if doc := oneJSONDocument(t, r.stdout); doc["recoveryFile"] != second {
		t.Errorf("the envelope does not say where the bundle went: %s", r.stdout)
	}
}

func TestRecoveryOutIsNeverReplacedOrPrintedWithoutBeingAsked(t *testing.T) {
	bundle := testBundle(t)
	dir := t.TempDir()
	changes := writeTemp(t, dir, "c.json", recoveryChanges)
	existing := writeTemp(t, dir, "existing.json", "keep me")

	cases := []struct {
		name string
		args []string
	}{
		{"an existing file", []string{"apply", "--changes", changes, "--yes", "--recovery-out", existing}},
		{"standard output", []string{"apply", "--changes", changes, "--yes", "--recovery-out", "-"}},
		{"force with no file", []string{"apply", "--changes", changes, "--yes", "--force"}},
		{"origin mismatch with no bundle", []string{"apply", "--changes", changes, "--yes", "--allow-origin-mismatch"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := recoveryApplyStub(t, appliedJSON(t, 1, nil, bundle))
			expectCode(t, s.run(t, runOpts{}, tc.args...), ExitUsage)
			if len(s.called("POST /api/v1/session")) != 0 {
				t.Error("connected before refusing the command line")
			}
		})
	}
	if data, _ := os.ReadFile(existing); string(data) != "keep me" {
		t.Error("the existing file was changed")
	}

	s := recoveryApplyStub(t, appliedJSON(t, 1, nil, bundle))
	expectCode(t, s.run(t, runOpts{}, "apply", "--changes", changes, "--yes", "--recovery-out", existing, "--force"), ExitOK)
	if data, _ := os.ReadFile(existing); !strings.Contains(string(data), `"alder-recovery"`) {
		t.Error("--force did not replace the file")
	}

	// The flag is the command line's alone.
	envOut := filepath.Join(dir, "from-env.json")
	s = recoveryApplyStub(t, appliedJSON(t, 1, nil, bundle))
	expectCode(t, s.run(t, runOpts{env: map[string]string{"ALDER_RECOVERY_OUT": envOut}},
		"apply", "--changes", changes, "--yes"), ExitOK)
	if _, err := os.Stat(envOut); err == nil {
		t.Error("an environment variable asked for a bundle")
	}
}

func TestAPartialApplyStillWritesTheBundleForWhatRan(t *testing.T) {
	failed := 1
	s := recoveryApplyStub(t, appliedJSON(t, 1, &failed, testBundle(t)))
	dir := t.TempDir()
	out := filepath.Join(dir, "recovery.json")
	r := s.run(t, runOpts{}, "apply", "--changes", writeTemp(t, dir, "c.json", recoveryChanges), "--yes", "--recovery-out", out)
	expectCode(t, r, ExitPartial)
	if _, err := os.Stat(out); err != nil {
		t.Errorf("no bundle after a partial apply: %v", err)
	}
	if !strings.Contains(r.stderr, "recovery bundle for 1 applied change written") {
		t.Errorf("stderr = %s", r.stderr)
	}
}

func TestNothingAppliedWritesNoBundle(t *testing.T) {
	failed := 0
	s := recoveryApplyStub(t, appliedJSON(t, 0, &failed, ""))
	dir := t.TempDir()
	out := filepath.Join(dir, "recovery.json")
	r := s.run(t, runOpts{}, "apply", "--changes", writeTemp(t, dir, "c.json", recoveryChanges), "--yes", "--recovery-out", out)
	expectCode(t, r, ExitFailed)
	if _, err := os.Stat(out); err == nil {
		t.Error("a bundle was written for nothing")
	}
	if !strings.Contains(r.stderr, "no recovery bundle was written") {
		t.Errorf("stderr = %s", r.stderr)
	}
}

func TestABundleThatDoesNotVerifyIsNeverWritten(t *testing.T) {
	corrupt := strings.Replace(testBundle(t), `"After"`, `"Planted"`, 1)
	s := recoveryApplyStub(t, appliedJSON(t, 1, nil, corrupt))
	dir := t.TempDir()
	out := filepath.Join(dir, "recovery.json")
	r := s.run(t, runOpts{}, "apply", "--changes", writeTemp(t, dir, "c.json", recoveryChanges), "--yes", "--recovery-out", out)
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "1 change was applied, but the recovery bundle was not written") {
		t.Errorf("stderr = %s", r.stderr)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "c.json" {
			t.Errorf("left behind %s", e.Name())
		}
	}
}

func inspectionJSON(t *testing.T, matches bool, changes []any) string {
	t.Helper()
	var bundle map[string]any
	_ = json.Unmarshal([]byte(testBundle(t)), &bundle)
	drift := []any{}
	for i := range changes {
		drift = append(drift, map[string]any{"index": i, "dn": aliceDN, "state": "ready"})
	}
	doc := map[string]any{
		"version": 1, "createdAt": bundle["createdAt"], "origin": bundle["origin"], "originMatches": matches,
		"recoverability": "exact", "integrity": "verified", "checksum": bundle["checksum"], "steps": bundle["steps"],
		"changes": changes, "drift": drift,
	}
	if !matches {
		doc["originDifferences"] = []string{"namingContexts"}
	}
	return mustJSON(t, doc)
}

func compensation() []any {
	return []any{map[string]any{"dn": aliceDN, "type": "modify",
		"mods":   []any{map[string]any{"op": "replace", "name": "title", "values": []any{map[string]any{"text": "Before"}}}},
		"expect": map[string]any{"attributes": []any{map[string]any{"name": "title", "values": []any{map[string]any{"text": "After"}}}}},
	}}
}

func TestPlanRecoverySendsTheBundleAsItWasRead(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/recovery/inspect", http.StatusOK, inspectionJSON(t, true, compensation()))
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
	// A field this client does not know is sent, not dropped: refusing it is
	// the server's job, and dropping it would hide a tampered file.
	raw := strings.Replace(testBundle(t), `"format":"alder-recovery"`, `"format":"alder-recovery","extra":true`, 1)
	file := writeTemp(t, t.TempDir(), "recovery.json", raw)

	r := s.run(t, runOpts{}, "plan", "--recovery", file)
	expectCode(t, r, ExitOK)
	if got := string(s.called("POST /api/v1/recovery/inspect")[0].Body); got != raw {
		t.Errorf("the bundle was not sent as read:\n%s", got)
	}
	var req map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/plan")[0].Body, &req)
	changes, _ := req["changes"].([]any)
	if len(changes) != 1 || changes[0].(map[string]any)["expect"] == nil {
		t.Errorf("the plan was not of the bundle's changes: %v", req)
	}
	for _, want := range []string{"Recovery bundle created", "exact recovery available", "1. modify " + aliceDN} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %q in:\n%s", want, r.stdout)
		}
	}

	r = s.run(t, runOpts{}, "plan", "--recovery", file, "--json")
	expectCode(t, r, ExitOK)
	oneJSONDocument(t, r.stdout)
}

func TestApplyRecoveryAgainstAnotherOriginNeedsToBeAllowed(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/recovery/inspect", http.StatusOK, inspectionJSON(t, false, compensation()))
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
	s.reply("POST /api/v1/changeset/apply", http.StatusOK, appliedJSON(t, 1, nil, ""))
	file := writeTemp(t, t.TempDir(), "recovery.json", testBundle(t))

	r := s.run(t, runOpts{}, "apply", "--recovery", file, "--yes")
	expectCode(t, r, ExitNotConfirmed)
	if len(s.called("POST /api/v1/plan")) != 0 || !strings.Contains(r.stderr, "--allow-origin-mismatch") {
		t.Errorf("planned or said nothing: %s", r.stderr)
	}
	r = s.run(t, runOpts{}, "apply", "--recovery", file, "--yes", "--allow-origin-mismatch")
	expectCode(t, r, ExitOK)
	if len(s.called("POST /api/v1/changeset/apply")) != 1 {
		t.Error("the allowed recovery was not applied")
	}
}

func TestARecoveryWithNothingToCompensate(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/recovery/inspect", http.StatusOK, inspectionJSON(t, true, []any{}))
	file := writeTemp(t, t.TempDir(), "recovery.json", testBundle(t))
	r := s.run(t, runOpts{}, "plan", "--recovery", file)
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stdout, "Nothing in this bundle can be compensated") || len(s.called("POST /api/v1/plan")) != 0 {
		t.Errorf("stdout = %s", r.stdout)
	}
	r = s.run(t, runOpts{}, "plan", "--recovery", file, "--json")
	expectCode(t, r, ExitOK)
	if items, _ := oneJSONDocument(t, r.stdout)["items"].([]any); items == nil || len(items) != 0 {
		t.Errorf("stdout = %s", r.stdout)
	}
}

func TestRecoveryIsOneInputAmongThree(t *testing.T) {
	dir := t.TempDir()
	file := writeTemp(t, dir, "recovery.json", testBundle(t))
	ldifFile := writeTemp(t, dir, "a.ldif", restoreLDIF)
	changes := writeTemp(t, dir, "c.json", recoveryChanges)
	for _, args := range [][]string{
		{"plan", "--recovery", file, ldifFile},
		{"plan", "--recovery", file, "--changes", changes},
		{"plan", "--recovery", file, "--mode", "desired"},
	} {
		s := newStub(t)
		expectCode(t, s.run(t, runOpts{}, args...), ExitUsage)
	}
}
