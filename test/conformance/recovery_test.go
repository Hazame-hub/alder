//go:build conformance

package conformance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// 1.9: recovery bundles, against both real directories.
//
// The unit and HTTP tests hold the derivation to its rules against a directory
// written for them. These hold it to the servers: that what OpenLDAP and 389 DS
// actually store after a change, and actually accept as its compensation, brings
// the entries back -- through Alder's own endpoints, a plan and an apply, and
// nothing else.

const (
	recOU      = "ou=alder-recovery," + suffix
	recOtherOU = "ou=alder-recovery-b," + suffix
	recSecret  = "Recovery-Conformance-Secret-7c1f"
)

func recDN(rdn string) string { return rdn + "," + recOU }

var recEntries = []string{
	recDN("uid=rec-probe"), recDN("uid=rec-gone"), recDN("uid=rec-new"), recDN("uid=rec-move"),
	recDN("uid=rec-moved"), "uid=rec-move," + recOtherOU, "uid=rec-moved," + recOtherOU,
}

func clearRecovery(t *testing.T, sess directory.Session) {
	t.Helper()
	for _, d := range recEntries {
		_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, d), Type: directory.ChangeDelete})
	}
	for _, d := range []string{recOU, recOtherOU} {
		_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, d), Type: directory.ChangeDelete})
	}
}

func seedRecovery(t *testing.T, sess directory.Session) {
	t.Helper()
	clearRecovery(t, sess)
	t.Cleanup(func() { clearRecovery(t, sess) })
	for _, ou := range []struct{ dn, name string }{{recOU, "alder-recovery"}, {recOtherOU, "alder-recovery-b"}} {
		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, ou.dn), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("organizationalUnit")}},
			{Name: "ou", Values: [][]byte{[]byte(ou.name)}},
		}})
	}
	mustApply(t, sess, person(t, recDN("uid=rec-probe"), "rec-probe",
		[2]string{"title", "Before"}, [2]string{"description", "first"}, [2]string{"userPassword", recSecret}))
	mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, recDN("uid=rec-probe")), Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModAdd, Name: "description", Values: [][]byte{[]byte("second")}}}})
	mustApply(t, sess, person(t, recDN("uid=rec-gone"), "rec-gone",
		[2]string{"title", "Leaving"}, [2]string{"userPassword", recSecret}))
	mustApply(t, sess, person(t, recDN("uid=rec-move"), "rec-move", [2]string{"title", "Moving"}))
}

// recoveryState is the user data of the test's entries: every attribute a read
// of "*" returns, values sorted, userPassword left out -- a recovery never
// restores one, and a server stores its own hash of the one it was given.
func recoveryState(t *testing.T, sess directory.Session) string {
	t.Helper()
	var lines []string
	for _, d := range recEntries {
		e, err := sess.Read(ctx(t), mustDN(t, d), []string{"*"})
		if err != nil {
			continue
		}
		for _, name := range e.Order {
			if strings.EqualFold(name, "userPassword") {
				continue
			}
			var values []string
			for _, v := range e.Attributes[name] {
				values = append(values, string(v))
			}
			sort.Strings(values)
			lines = append(lines, strings.ToLower(d)+" | "+strings.ToLower(name)+" = "+strings.Join(values, " ; "))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

type recChange = map[string]any

func textValues(values ...string) []map[string]string {
	out := make([]map[string]string, 0, len(values))
	for _, v := range values {
		out = append(out, map[string]string{"text": v})
	}
	return out
}

// recoveryPlanAndApply plans changes over HTTP and applies what the plan would apply,
// with its baselines. It fails the test unless every change is applicable.
func recoveryPlanAndApply(t *testing.T, client *http.Client, base string, changes []recChange, withRecovery bool) api.ChangesetResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"changes": changes, "reconcile": false})
	res := post(t, client, base+"/plan", string(body))
	if res.status != http.StatusOK {
		t.Fatalf("planning: status %d\n%s", res.status, res.body)
	}
	plan := decodeInto[api.Plan](t, res)
	var send []recChange
	for _, item := range plan.Items {
		if item.Baseline == nil {
			t.Fatalf("change %d (%s) would not apply: %s %v %v", item.Index, item.Dn, item.Action, item.Problem, item.Reason)
		}
		change := recChange{}
		for k, v := range changes[item.Index] {
			change[k] = v
		}
		change["baseline"] = *item.Baseline
		send = append(send, change)
	}
	applyBody, _ := json.Marshal(map[string]any{"changes": send, "recovery": withRecovery})
	applied := post(t, client, base+"/changeset/apply", string(applyBody))
	if applied.status != http.StatusOK {
		t.Fatalf("applying: status %d\n%s", applied.status, applied.body)
	}
	return decodeInto[api.ChangesetResult](t, applied)
}

func recoveryOf(t *testing.T, raw *json.RawMessage) api.RecoveryBundle {
	t.Helper()
	if raw == nil {
		t.Fatal("no recovery bundle was returned")
	}
	var out api.RecoveryBundle
	if err := json.Unmarshal(*raw, &out); err != nil {
		t.Fatalf("the bundle does not decode: %v", err)
	}
	return out
}

func inspectBundle(t *testing.T, client *http.Client, base string, bundle *json.RawMessage) api.RecoveryInspection {
	t.Helper()
	if bundle == nil {
		t.Fatal("no recovery bundle was returned")
	}
	raw := []byte(*bundle)
	res := post(t, client, base+"/recovery/inspect", string(raw))
	if res.status != http.StatusOK {
		t.Fatalf("inspecting: status %d\n%s", res.status, res.body)
	}
	return decodeInto[api.RecoveryInspection](t, res)
}

func asChanges(t *testing.T, requests []api.ChangeRequest) []recChange {
	t.Helper()
	raw, _ := json.Marshal(requests)
	var out []recChange
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Every kind of change in one set, recovered through a plan, and the directory
// back at S0 -- less the one thing recovery never restores.
func TestRecoveryReturnsTheDirectoryToWhereItWas(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		seedRecovery(t, sess)
		client, base := alder(t, s)
		s0 := recoveryState(t, sess)

		changes := []recChange{
			{"dn": recDN("uid=rec-probe"), "type": "modify", "mods": []recChange{
				{"op": "replace", "name": "description", "values": textValues("second", "third")},
				{"op": "add", "name": "telephoneNumber", "values": textValues("+1 555 0100")},
				{"op": "delete", "name": "title"},
			}},
			{"dn": recDN("uid=rec-new"), "type": "add", "attributes": []recChange{
				{"name": "objectClass", "values": textValues("top", "person", "organizationalPerson", "inetOrgPerson")},
				{"name": "cn", "values": textValues("rec-new")}, {"name": "sn", "values": textValues("New")},
				{"name": "uid", "values": textValues("rec-new")},
			}},
			{"dn": recDN("uid=rec-move"), "type": "modrdn", "newRdn": "uid=rec-moved", "deleteOldRdn": true, "newSuperior": recOtherOU},
			{"dn": recDN("uid=rec-gone"), "type": "delete"},
			{"dn": recDN("uid=rec-probe"), "type": "setpassword", "newPassword": recSecret + "-new"},
		}
		result := recoveryPlanAndApply(t, client, base, changes, true)
		if result.AppliedCount != len(changes) {
			t.Fatalf("applied %d of %d: %+v", result.AppliedCount, len(changes), result.Outcomes)
		}
		bundle := recoveryOf(t, result.Recovery)
		raw := []byte(*result.Recovery)
		for _, leak := range []string{recSecret, s.bindPW, s.bindDN, "baseline"} {
			if strings.Contains(string(raw), leak) {
				t.Errorf("the bundle contains %q", leak)
			}
		}
		if bundle.Recoverability != api.RecoverabilityPartial || len(bundle.Steps) != len(changes) {
			t.Fatalf("bundle %s with %d steps", bundle.Recoverability, len(bundle.Steps))
		}
		want := []api.RecoveryRecoverability{api.RecoverabilityExact, api.RecoverabilityExact, api.RecoverabilityExact,
			api.RecoverabilityPartial, api.RecoverabilityUnavailable}
		for i, step := range bundle.Steps {
			if step.Recoverability != want[i] {
				t.Errorf("step %d (%s): %s, want %s", i, step.Original.Type, step.Recoverability, want[i])
			}
		}

		inspection := inspectBundle(t, client, base, result.Recovery)
		if !inspection.OriginMatches || inspection.Integrity != api.SnapshotIntegrityVerified {
			t.Errorf("origin %v, integrity %s", inspection.OriginMatches, inspection.Integrity)
		}
		for _, d := range inspection.Drift {
			if d.State != api.RecoveryDriftReady {
				t.Errorf("drift on an untouched directory: %+v", d)
			}
		}
		recovered := recoveryPlanAndApply(t, client, base, asChanges(t, inspection.Changes), false)
		if recovered.FailedIndex != nil {
			t.Fatalf("recovery stopped: %+v", recovered.Outcomes)
		}
		if got := recoveryState(t, sess); got != s0 {
			t.Errorf("not back at S0\n--- S0\n%s\n--- now\n%s", s0, got)
		}
		gone, err := sess.Read(ctx(t), mustDN(t, recDN("uid=rec-gone")), []string{"*"})
		if err != nil {
			t.Fatalf("the deleted entry was not recreated: %v", err)
		}
		if len(gone.Get("userPassword")) != 0 {
			t.Error("the recreated entry has a password")
		}
	})
}

// A run that stops covers what it applied, and only that.
func TestRecoveryOfAPartialApply(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		seedRecovery(t, sess)
		client, base := alder(t, s)
		s0 := recoveryState(t, sess)

		// The second change has no plan and fails in the directory: there is no
		// such value to delete.
		body, _ := json.Marshal(map[string]any{"recovery": true, "changes": []recChange{
			{"dn": recDN("uid=rec-probe"), "type": "modify", "mods": []recChange{
				{"op": "replace", "name": "title", "values": textValues("During")}}},
			{"dn": recDN("uid=rec-move"), "type": "modify", "mods": []recChange{
				{"op": "delete", "name": "description", "values": textValues("not there")}}},
			{"dn": recDN("uid=rec-gone"), "type": "delete"},
		}})
		res := post(t, client, base+"/changeset/apply", string(body))
		if res.status != http.StatusOK {
			t.Fatalf("status %d: %s", res.status, res.body)
		}
		result := decodeInto[api.ChangesetResult](t, res)
		if result.FailedIndex == nil || *result.FailedIndex != 1 || result.AppliedCount != 1 {
			t.Fatalf("result %+v", result)
		}
		if result.Recovery == nil || len(recoveryOf(t, result.Recovery).Steps) != 1 || recoveryOf(t, result.Recovery).Steps[0].Index != 0 {
			t.Fatalf("bundle %+v, want exactly the applied change", result.Recovery)
		}
		inspection := inspectBundle(t, client, base, result.Recovery)
		recoveryPlanAndApply(t, client, base, asChanges(t, inspection.Changes), false)
		if got := recoveryState(t, sess); got != s0 {
			t.Errorf("not back at S0\n--- S0\n%s\n--- now\n%s", s0, got)
		}
	})
}

// A later change to what a compensation would touch is a conflict in its plan,
// never overwritten.
func TestRecoveryAfterDriftIsAConflict(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		seedRecovery(t, sess)
		client, base := alder(t, s)
		result := recoveryPlanAndApply(t, client, base, []recChange{
			{"dn": recDN("uid=rec-probe"), "type": "modify", "mods": []recChange{
				{"op": "replace", "name": "title", "values": textValues("Applied")}}},
		}, true)

		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, recDN("uid=rec-probe")), Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: "title", Values: [][]byte{[]byte("Somebody else")}}}})

		inspection := inspectBundle(t, client, base, result.Recovery)
		if len(inspection.Drift) != 1 || inspection.Drift[0].State != api.RecoveryDriftDrifted ||
			inspection.Drift[0].Problem == nil || inspection.Drift[0].Problem.Code != api.PlanProblemExpectedStateDiffers {
			t.Fatalf("drift %+v", inspection.Drift)
		}
		body, _ := json.Marshal(map[string]any{"changes": inspection.Changes})
		plan := decodeInto[api.Plan](t, post(t, client, base+"/plan", string(body)))
		if plan.Items[0].Action != api.PlanActionConflict || plan.Items[0].Baseline != nil {
			t.Errorf("plan %+v, want a conflict with nothing to apply", plan.Items[0])
		}
		if e, _ := sess.Read(ctx(t), mustDN(t, recDN("uid=rec-probe")), []string{"title"}); string(e.Get("title")[0]) != "Somebody else" {
			t.Error("the later change was overwritten")
		}
	})
}

// The command line: apply with --recovery-out, then apply --recovery.
func TestCLIRecoveryOutThenRecovery(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		seedRecovery(t, sess)
		base := startAlder(t)
		dir := t.TempDir()
		run := func(args ...string) cliResult { return alderCLI(t, base, s, cliOpts{}, args...) }
		s0 := recoveryState(t, sess)

		changes := filepath.Join(dir, "changes.json")
		doc := fmt.Sprintf(`[{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"title","values":[{"text":"Via the CLI"}]}]},`+
			`{"dn":%q,"type":"modrdn","newRdn":"uid=rec-moved","deleteOldRdn":true}]`, recDN("uid=rec-probe"), recDN("uid=rec-move"))
		if err := os.WriteFile(changes, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		bundle := filepath.Join(dir, "recovery.json")
		r := run("apply", "--changes", changes, "--yes", "--recovery-out", bundle, "--json")
		wantExit(t, r, 0, "apply --recovery-out")
		if decodeMap(t, r.stdout)["recoveryFile"] != bundle || !strings.Contains(r.stderr, "recovery bundle for 2 applied changes written") {
			t.Errorf("stdout %s\nstderr %s", r.stdout, r.stderr)
		}
		if got := recoveryState(t, sess); got == s0 {
			t.Fatal("nothing changed")
		}

		r = run("apply", "--changes", changes, "--yes", "--recovery-out", bundle)
		if r.code != 7 || !strings.Contains(r.stderr, "already exists") {
			t.Errorf("an existing bundle was not protected: exit %d\n%s", r.code, r.stderr)
		}

		r = run("plan", "--recovery", bundle)
		wantExit(t, r, 0, "plan --recovery")
		if !strings.Contains(r.stdout, "exact recovery available") {
			t.Errorf("plan --recovery:\n%s", r.stdout)
		}
		r = run("apply", "--recovery", bundle, "--yes")
		wantExit(t, r, 0, "apply --recovery")
		if got := recoveryState(t, sess); got != s0 {
			t.Errorf("not back at S0\n--- S0\n%s\n--- now\n%s", s0, got)
		}

		// Applied again, it has nothing left to do: the renamed entry is back
		// and the title is what it was, so the plan is a set of conflicts.
		r = run("apply", "--recovery", bundle, "--yes")
		if r.code != 3 {
			t.Errorf("a recovery applied twice: exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
		}
	})
}
