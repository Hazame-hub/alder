package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 1.40: --min-signers, a two-person rule in plain CI. Two reviewers sign the
// saved plan, and the apply refuses it with fewer valid signatures by
// distinct trusted keys.

func TestMinSigners(t *testing.T) {
	keys := t.TempDir()
	alicePriv, alicePub := generateKey(t, keys, "alice")
	bobPriv, bobPub := generateKey(t, keys, "bob")
	_, carolPub := generateKey(t, keys, "carol")

	trustAll := []string{"--trusted-keys", alicePub, "--trusted-keys", bobPub, "--trusted-keys", carolPub}

	project := func(t *testing.T) (*stub, func(plan string, extra ...string) result, string) {
		s := savedPlanStub(t, true)
		dir := writeSavedPlanProject(t, s)
		saved := savePlanFor(t, s, dir)
		return s, func(plan string, extra ...string) result {
			return s.run(t, projectRun, append([]string{"project", "apply", "--project", dir, "--env", "dev",
				"--plan", plan, "--yes"}, extra...)...)
		}, saved
	}
	input := func(t *testing.T) (*stub, func(plan string, extra ...string) result, string) {
		s := inputPlanStub(t, true)
		saved := saveInput(t, s)
		return s, func(plan string, extra ...string) result {
			return s.run(t, runOpts{}, append([]string{"apply", "--plan", plan, "--yes", "--allow-deletes"}, extra...)...)
		}, saved
	}

	for name, setup := range map[string]func(t *testing.T) (*stub, func(string, ...string) result, string){
		"project apply": project, "apply": input,
	} {
		t.Run(name, func(t *testing.T) {
			t.Run("two signatures by distinct trusted keys apply", func(t *testing.T) {
				s, apply, saved := setup(t)
				twice := signFile(t, signFile(t, saved, alicePriv, "alice"), bobPriv, "bob")
				r := apply(twice, append(trustAll, "--min-signers", "2", "--json")...)
				expectCode(t, r, ExitOK)
				if len(s.called("POST /api/v1/changeset/apply")) != 1 {
					t.Fatal("the plan was not applied")
				}
				sig, _ := oneJSONDocument(t, r.stdout)["signature"].(map[string]any)
				if signers, _ := sig["signers"].([]any); sig["status"] != "verified" || len(signers) != 2 {
					t.Errorf("signature = %v", sig)
				}
			})

			t.Run("one trusted signature is refused before connecting", func(t *testing.T) {
				s, apply, saved := setup(t)
				once := signFile(t, saved, alicePriv, "alice")
				before := len(s.calls)
				r := apply(once, append(trustAll, "--min-signers", "2", "--json")...)
				expectCode(t, r, ExitNotApplicable)
				if !strings.Contains(r.stdout+r.stderr, "signatures_too_few") {
					t.Errorf("no signatures_too_few in:\n%s\n%s", r.stdout, r.stderr)
				}
				if len(s.calls) != before {
					t.Errorf("%d request(s) sent for a refused plan", len(s.calls)-before)
				}
			})

			t.Run("a second signature by an untrusted key does not count", func(t *testing.T) {
				_, apply, saved := setup(t)
				malloryPriv, _ := generateKey(t, t.TempDir(), "mallory")
				twice := signFile(t, signFile(t, saved, alicePriv, "alice"), malloryPriv, "bob")
				expectCode(t, apply(twice, append(trustAll, "--min-signers", "2")...), ExitNotApplicable)
			})

			t.Run("one key's signature twice counts once", func(t *testing.T) {
				_, apply, saved := setup(t)
				once := signFile(t, saved, alicePriv, "alice")
				data, err := os.ReadFile(once)
				if err != nil {
					t.Fatal(err)
				}
				var doc map[string]json.RawMessage
				var sigs []json.RawMessage
				if err := json.Unmarshal(data, &doc); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(doc["signatures"], &sigs); err != nil || len(sigs) != 1 {
					t.Fatalf("signatures = %s (%v)", doc["signatures"], err)
				}
				doc["signatures"], _ = json.Marshal([]json.RawMessage{sigs[0], sigs[0]})
				twice, _ := json.Marshal(doc)
				dup := writeTemp(t, t.TempDir(), "dup.json", string(twice))
				expectCode(t, apply(dup, append(trustAll, "--min-signers", "2")...), ExitNotApplicable)
			})

			for _, tc := range []struct {
				name string
				args []string
			}{
				{"without --trusted-keys", []string{"--min-signers", "2"}},
				{"more than the keys named", []string{"--trusted-keys", alicePub, "--min-signers", "2"}},
				{"negative", append(trustAll, "--min-signers", "-1")},
			} {
				t.Run("usage: "+tc.name, func(t *testing.T) {
					_, apply, saved := setup(t)
					expectCode(t, apply(signFile(t, saved, alicePriv, "alice"), tc.args...), ExitUsage)
				})
			}
		})
	}
}
