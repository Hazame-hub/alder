package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// 1.6: a single change goes through the same plan as a set.
//
// The confirmation dialog plans the one change it was handed, shows that plan,
// and applies the reviewed change with the plan's token through
// /changes/apply. These tests drive that path over HTTP for every kind of
// single change the interface builds, and hold the operation the directory
// receives to the one the plan showed -- with the comparison written in the
// invariant test, not borrowed from the code under test.

const (
	singleSchemaDN = "cn=schema,cn=config"
	singleConfigDN = "cn=config"
	singleNewbie   = "uid=newbie,ou=people,dc=alder,dc=test"
)

// singleRig is a directory holding an ordinary entry, the schema entry and the
// configuration entry, with capabilities that say which is which.
//
// The schema and configuration entries carry no object classes, so the planner
// does not judge which attributes they may hold -- the test schema knows nothing
// of the attributes real servers keep there. The conformance suite writes the
// real ones on both servers.
func singleRig(t *testing.T) (*testRig, *directory.Entry) {
	t.Helper()
	caps := defaultCaps()
	caps.ConfigContext = singleConfigDN
	caps.SchemaWrite = directory.SchemaWrite{
		Style:   caps.SchemaWrite.Style,
		Targets: []directory.SchemaTarget{{DN: singleSchemaDN, Name: "schema"}},
	}
	alice := personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"},
		[]string{"description", "before"}, []string{"mail", "a@alder.test"})
	bob := personAt(t, bobDN, []string{"cn", "Bob"}, []string{"sn", "B"})
	rig := newRig(t, Config{}, &fakeSession{
		caps: caps, sch: testSchema(t),
		byDN: map[string]*directory.Entry{
			singleSchemaDN:             directory.NewEntry(mustParse(t, singleSchemaDN)),
			singleConfigDN:             directory.NewEntry(mustParse(t, singleConfigDN)),
			strings.ToLower(planAlice): alice,
			strings.ToLower(bobDN):     bob,
		},
	})
	return rig, alice
}

// planSingle plans one change the way the dialog does, returning the raw body
// as well so a test can check what it does not contain.
func planSingle(t *testing.T, rig *testRig, change string) (PlanItem, string) {
	t.Helper()
	res := rig.do(t, http.MethodPost, "/api/v1/plan",
		strings.NewReader(`{"reconcile":false,"changes":[`+change+`]}`))
	p := mustPlan(t, res)
	if len(p.Items) != 1 {
		t.Fatalf("planned %d items for one change: %+v", len(p.Items), p.Items)
	}
	return p.Items[0], res.Body
}

// applySingle sends the reviewed change with a plan token, as the dialog does.
func applySingle(t *testing.T, rig *testRig, change string, baseline *string) response {
	t.Helper()
	var req ChangeRequest
	if err := json.Unmarshal([]byte(change), &req); err != nil {
		t.Fatalf("decoding %s: %v", change, err)
	}
	req.Baseline = baseline
	return rig.do(t, http.MethodPost, "/api/v1/changes/apply", strings.NewReader(mustJSON(t, req)))
}

func descriptionOf(dn, value string) string {
	return fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"description","values":[{"text":%q}]}]}`,
		dn, value)
}

func TestEverySingleChangeAppliesExactlyWhatItsPlanShowed(t *testing.T) {
	const secret = "correct-horse-battery-staple"
	cases := []struct {
		name   string
		change string
		action PlanAction
		kind   PlanTargetKind
	}{
		{"create", fmt.Sprintf(`{"dn":%q,"type":"add","attributes":[
			{"name":"objectClass","values":[{"text":"top"},{"text":"person"},{"text":"inetOrgPerson"}]},
			{"name":"cn","values":[{"text":"Newbie"}]},{"name":"sn","values":[{"text":"N"}]},
			{"name":"uid","values":[{"text":"newbie"}]}]}`, singleNewbie), PlanActionAdd, PlanTargetData},
		{"modify several attributes", fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[
			{"op":"replace","name":"description","values":[{"text":"after"}]},
			{"op":"add","name":"mail","values":[{"text":"alice@alder.test"}]},
			{"op":"add","name":"telephoneNumber","values":[{"text":"+1 555 0100"}]},
			{"op":"delete","name":"mail","values":[{"text":"a@alder.test"}]}]}`, planAlice), PlanActionModify, PlanTargetData},
		{"rename", fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=alicia","deleteOldRdn":true}`, planAlice),
			PlanActionRename, PlanTargetData},
		{"move", fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=alice","deleteOldRdn":false,"newSuperior":"ou=groups,dc=alder,dc=test"}`,
			planAlice), PlanActionRename, PlanTargetData},
		{"delete", fmt.Sprintf(`{"dn":%q,"type":"delete"}`, bobDN), PlanActionDelete, PlanTargetData},
		{"schema", fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"description","values":[{"text":"a definition"}]}]}`,
			singleSchemaDN), PlanActionModify, PlanTargetSchema},
		{"config", descriptionOf(singleConfigDN, "a setting"), PlanActionModify, PlanTargetConfig},
		{"password", fmt.Sprintf(`{"dn":%q,"type":"setpassword","newPassword":%q}`, planAlice, secret),
			PlanActionSetPassword, PlanTargetData},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig, _ := singleRig(t)
			item, body := planSingle(t, rig, tc.change)
			if item.Action != tc.action {
				t.Fatalf("action = %q, want %q\nitem: %+v", item.Action, tc.action, item)
			}
			if item.Kind == nil || *item.Kind != tc.kind {
				t.Errorf("kind = %s, want %q", singleKindText(item.Kind), tc.kind)
			}
			if item.Record == nil || item.Baseline == nil || item.Preview == nil {
				t.Fatalf("an applicable single change lacks its record, token or preview: %+v", item)
			}
			if strings.Contains(body, secret) {
				t.Fatal("the plan response carries the password")
			}

			res := applySingle(t, rig, tc.change, item.Baseline)
			if res.Status != http.StatusOK {
				t.Fatalf("applying the planned change: status %d\nbody: %s", res.Status, res.Body)
			}
			if strings.Contains(res.Body, secret) {
				t.Error("the apply response carries the password")
			}
			if len(rig.fake.applied) != 1 {
				t.Fatalf("the directory received %d operations for one planned change", len(rig.fake.applied))
			}
			executedMatchesPlanned(t, 0, rig.fake.applied[0], *item.Record)
			if tc.action == PlanActionSetPassword && rig.fake.applied[0].NewPassword != secret {
				t.Error("the password set is not the one the operator typed")
			}
		})
	}
}

func singleKindText(k *PlanTargetKind) string {
	if k == nil {
		return "absent"
	}
	return string(*k)
}

// A server that announces no configContext -- 389 DS -- still has a
// configuration tree, found at the conventional location when the session
// connected. A change addressed into it is a configuration change.
func TestAChangeToAConfigurationTreeFoundByProbingIsClassifiedAsConfig(t *testing.T) {
	caps := defaultCaps()
	caps.ConfigContext = ""
	caps.Config = directory.ConfigAccess{DN: "cn=config", Readable: true}
	rig := newRig(t, Config{}, &fakeSession{
		caps: caps, sch: testSchema(t),
		byDN: map[string]*directory.Entry{
			"cn=config":               directory.NewEntry(mustParse(t, "cn=config")),
			"cn=encryption,cn=config": directory.NewEntry(mustParse(t, "cn=encryption,cn=config")),
		},
	})
	for _, target := range []string{"cn=config", "cn=encryption,cn=config"} {
		item, _ := planSingle(t, rig, descriptionOf(target, "a setting"))
		if item.Kind == nil || *item.Kind != PlanTargetConfig {
			t.Errorf("%s: kind = %s, want config", target, singleKindText(item.Kind))
		}
	}
}

// A change the directory already satisfies plans as nothing to do: no record,
// no token, and so nothing the dialog can apply.
func TestASingleChangeThatChangesNothingPlansAsNothingToDo(t *testing.T) {
	for name, change := range map[string]string{
		"replace with the value already held": descriptionOf(planAlice, "before"),
		"add a value already present": fmt.Sprintf(
			`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"mail","values":[{"text":"a@alder.test"}]}]}`, planAlice),
	} {
		t.Run(name, func(t *testing.T) {
			rig, _ := singleRig(t)
			item, _ := planSingle(t, rig, change)
			if item.Action != PlanActionUnchanged {
				t.Fatalf("action = %q, want unchanged", item.Action)
			}
			if item.Record != nil || item.Baseline != nil {
				t.Errorf("a plan that does nothing carries something to apply: %+v", item)
			}
			if len(rig.fake.applied) != 0 {
				t.Errorf("planning wrote %d operations", len(rig.fake.applied))
			}
		})
	}
}

// The directory moves between the plan and the apply: nothing is written, the
// refusal names the change, and only a new plan -- which the operator sees --
// can be applied.
func TestASingleChangeIsRefusedWhenTheDirectoryMovedAfterItWasPlanned(t *testing.T) {
	rig, alice := singleRig(t)
	change := descriptionOf(planAlice, "what was reviewed")
	item, _ := planSingle(t, rig, change)

	alice.Set("description", [][]byte{[]byte("somebody else's edit")})

	res := applySingle(t, rig, change, item.Baseline)
	if res.Status != http.StatusConflict {
		t.Fatalf("status = %d, want 409\nbody: %s", res.Status, res.Body)
	}
	refusal := decode[Error](t, res)
	if refusal.Error != ErrorErrorConflict || refusal.Cause == nil || *refusal.Cause != ErrorCausePlanStale {
		t.Errorf("refusal = %+v, want conflict with cause plan_stale", refusal)
	}
	if refusal.Affected == nil || len(*refusal.Affected) != 1 || (*refusal.Affected)[0].Dn != planAlice {
		t.Errorf("affected = %+v, want the one change", refusal.Affected)
	}
	if len(rig.fake.applied) != 0 {
		t.Fatalf("a stale plan wrote %d operations", len(rig.fake.applied))
	}

	again, _ := planSingle(t, rig, change)
	if again.Baseline == nil || *again.Baseline == *item.Baseline {
		t.Fatal("recomputing the plan returned the stale token")
	}
	if res := applySingle(t, rig, change, again.Baseline); res.Status != http.StatusOK {
		t.Fatalf("applying the recomputed plan: status %d\nbody: %s", res.Status, res.Body)
	}
}

// Whatever is sent under a plan's token has to be the operation it was issued
// for, in every family the dialog builds.
func TestASingleChangeThatIsNotItsPlanIsRefused(t *testing.T) {
	cases := []struct {
		name, planned, sent string
	}{
		{"rename with deleteOldRdn flipped",
			fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=alicia","deleteOldRdn":true}`, planAlice),
			fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=alicia","deleteOldRdn":false}`, planAlice)},
		{"move to a different parent",
			fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=alice","deleteOldRdn":false,"newSuperior":"ou=groups,dc=alder,dc=test"}`, planAlice),
			fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=alice","deleteOldRdn":false,"newSuperior":"ou=people,dc=alder,dc=test"}`, planAlice)},
		{"modifications reordered",
			fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"description","values":[{"text":"x"}]},{"op":"add","name":"mail","values":[{"text":"b@alder.test"}]}]}`, planAlice),
			fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"mail","values":[{"text":"b@alder.test"}]},{"op":"replace","name":"description","values":[{"text":"x"}]}]}`, planAlice)},
		{"a delete sent as a replace",
			fmt.Sprintf(`{"dn":%q,"type":"delete"}`, bobDN),
			descriptionOf(bobDN, "not deleted")},
		{"attribute name changed",
			descriptionOf(planAlice, "x"),
			fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"title","values":[{"text":"x"}]}]}`, planAlice)},
		{"schema definition changed after planning",
			fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"description","values":[{"text":"the reviewed definition"}]}]}`, singleSchemaDN),
			fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"description","values":[{"text":"another definition!!"}]}]}`, singleSchemaDN)},
		{"config value changed after planning",
			descriptionOf(singleConfigDN, "1800"),
			descriptionOf(singleConfigDN, "9999")},
		{"password of the same length substituted",
			fmt.Sprintf(`{"dn":%q,"type":"setpassword","newPassword":"correct-horse-battery-staple"}`, planAlice),
			fmt.Sprintf(`{"dn":%q,"type":"setpassword","newPassword":"correct-horse-battery-stapl3"}`, planAlice)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig, _ := singleRig(t)
			item, _ := planSingle(t, rig, tc.planned)
			if item.Baseline == nil {
				t.Fatalf("the planned change is not applicable: %+v", item)
			}
			res := applySingle(t, rig, tc.sent, item.Baseline)
			if res.Status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400\nbody: %s", res.Status, res.Body)
			}
			if got := decode[Error](t, res).Error; got != ErrorErrorPlanMismatch {
				t.Errorf("error = %q, want plan_mismatch", got)
			}
			if len(rig.fake.applied) != 0 {
				t.Errorf("a mismatched change wrote %d operations", len(rig.fake.applied))
			}
		})
	}
}

// A 1.x client that applies without planning is served exactly as before.
func TestAChangeWithoutAPlanTokenStillAppliesAsBefore(t *testing.T) {
	rig, _ := singleRig(t)
	if res := applySingle(t, rig, descriptionOf(planAlice, "unplanned"), nil); res.Status != http.StatusOK {
		t.Fatalf("status = %d\nbody: %s", res.Status, res.Body)
	}
	if len(rig.fake.applied) != 1 {
		t.Fatalf("applied %d operations, want 1", len(rig.fake.applied))
	}
}
