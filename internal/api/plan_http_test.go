package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// The plan endpoint, and the property the whole feature rests on: what a plan
// says will be applied is what Apply is given.

func planRig(t *testing.T, entries ...*directory.Entry) *testRig {
	t.Helper()
	byDN := map[string]*directory.Entry{}
	for _, e := range entries {
		byDN[strings.ToLower(e.DN.String())] = e
	}
	return newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t), byDN: byDN,
	})
}

func personAt(t *testing.T, d string, pairs ...[]string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(mustParse(t, d))
	e.Set("objectClass", [][]byte{[]byte("top"), []byte("person"), []byte("inetOrgPerson")})
	for _, p := range pairs {
		values := make([][]byte, 0, len(p)-1)
		for _, v := range p[1:] {
			values = append(values, []byte(v))
		}
		e.Set(p[0], values)
	}
	return e
}

func planFor(t *testing.T, rig *testRig, body string) Plan {
	t.Helper()
	res := rig.do(t, http.MethodPost, "/api/v1/plan", strings.NewReader(body))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", res.Status, res.Body)
	}
	return decode[Plan](t, res)
}

const planAlice = "uid=alice,ou=people,dc=alder,dc=test"

func TestAPlanCountsWhatItWouldDo(t *testing.T) {
	rig := planRig(t,
		personAt(t, planAlice, []string{"cn", "Alice"}, []string{"mail", "old@alder.test"}),
		personAt(t, "uid=bob,ou=people,dc=alder,dc=test", []string{"cn", "Bob"}),
	)

	body := fmt.Sprintf(`{"changes":[
      {"dn":%q,"type":"modify","mods":[{"op":"replace","name":"mail","values":[{"text":"new@alder.test"}]}]},
      {"dn":%q,"type":"modify","mods":[{"op":"replace","name":"cn","values":[{"text":"Bob"}]}]},
      {"dn":"uid=carol,ou=people,dc=alder,dc=test","type":"add","attributes":[
        {"name":"objectClass","values":[{"text":"person"}]},
        {"name":"cn","values":[{"text":"Carol"}]},
        {"name":"sn","values":[{"text":"Carroll"}]}]},
      {"dn":"uid=nobody,ou=people,dc=alder,dc=test","type":"delete"}
    ]}`, planAlice, "uid=bob,ou=people,dc=alder,dc=test")

	p := planFor(t, rig, body)

	if p.Counts.Examined != 4 {
		t.Errorf("examined = %d, want 4", p.Counts.Examined)
	}
	if p.Counts.Modify != 1 {
		t.Errorf("modify = %d, want 1", p.Counts.Modify)
	}
	if p.Counts.Add != 1 {
		t.Errorf("add = %d, want 1", p.Counts.Add)
	}
	// Bob's cn is already Bob, and there is no uid=nobody to delete.
	if p.Counts.Unchanged != 2 {
		t.Errorf("unchanged = %d, want 2", p.Counts.Unchanged)
	}
	if p.Counts.Conflict != 0 {
		t.Errorf("conflict = %d, want 0", p.Counts.Conflict)
	}
}

// An item that would do nothing carries no record and no baseline: there is
// nothing to apply and nothing to check.
func TestAnUnchangedItemCarriesNothingToApply(t *testing.T) {
	rig := planRig(t, personAt(t, planAlice, []string{"mail", "same@alder.test"}))
	p := planFor(t, rig, fmt.Sprintf(
		`{"changes":[{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"mail",`+
			`"values":[{"text":"same@alder.test"}]}]}]}`, planAlice))

	item := p.Items[0]
	if item.Action != PlanActionUnchanged {
		t.Fatalf("action = %q, want unchanged", item.Action)
	}
	if item.Record != nil {
		t.Error("an unchanged item carries a record")
	}
	if item.Baseline != nil {
		t.Error("an unchanged item carries a baseline")
	}
	if item.Reason == nil || *item.Reason == "" {
		t.Error("an unchanged item does not say why")
	}
}

// The reconcile case: what was sent is an add, what would run is a modify of
// only the attribute that differs, and the plan shows the second.
func TestAPlanShowsTheRecordThatWouldRunNotTheOneThatWasSent(t *testing.T) {
	rig := planRig(t, personAt(t, planAlice,
		[]string{"cn", "Alice"}, []string{"mail", "old@alder.test"}))

	body := fmt.Sprintf(`{"reconcile":true,"changes":[{"dn":%q,"type":"add","attributes":[
      {"name":"objectClass","values":[{"text":"top"},{"text":"person"},{"text":"inetOrgPerson"}]},
      {"name":"cn","values":[{"text":"Alice"}]},
      {"name":"mail","values":[{"text":"new@alder.test"}]}]}]}`, planAlice)

	p := planFor(t, rig, body)
	item := p.Items[0]

	if item.Action != PlanActionModify {
		t.Fatalf("action = %q, want modify\nreason: %v", item.Action, item.Reason)
	}
	if item.Record == nil {
		t.Fatal("a modify item carries no record")
	}
	if item.Record.Type != "modify" {
		t.Errorf("the planned record is a %q, not a modify", item.Record.Type)
	}
	if item.Record.Mods == nil || len(*item.Record.Mods) != 1 {
		t.Fatalf("the planned record has mods %+v; only mail differs", item.Record.Mods)
	}
	if got := (*item.Record.Mods)[0].Name; !strings.EqualFold(got, "mail") {
		t.Errorf("the planned mod touches %q, want mail", got)
	}
	// And the preview is the exact LDIF of that record, not of what was sent.
	if item.Preview == nil || !strings.Contains(item.Preview.Ldif, "replace: mail") {
		t.Errorf("the preview does not show the modification: %+v", item.Preview)
	}
	if item.Preview != nil && strings.Contains(item.Preview.Ldif, "changetype: add") {
		t.Error("the preview shows the add that was sent rather than the modify that would run")
	}
}

// The invariant, end to end: take the plan's record, apply it, and the
// directory receives exactly that.
func TestApplyingWhatThePlanReturnedSendsExactlyThatRecord(t *testing.T) {
	rig := planRig(t, personAt(t, planAlice,
		[]string{"cn", "Alice"}, []string{"mail", "old@alder.test"}))

	body := fmt.Sprintf(`{"reconcile":true,"changes":[{"dn":%q,"type":"add","attributes":[
      {"name":"objectClass","values":[{"text":"top"},{"text":"person"},{"text":"inetOrgPerson"}]},
      {"name":"cn","values":[{"text":"Alice"}]},
      {"name":"mail","values":[{"text":"new@alder.test"}]}]}]}`, planAlice)
	p := planFor(t, rig, body)

	item := p.Items[0]
	if item.Record == nil || item.Baseline == nil {
		t.Fatal("the plan returned nothing to apply")
	}

	// Hand the plan's own record straight back, baseline and all.
	applyBody := fmt.Sprintf(`{"changes":[%s]}`, mustJSON(t, withBaseline(*item.Record, *item.Baseline)))
	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply", strings.NewReader(applyBody))
	if res.Status != http.StatusOK {
		t.Fatalf("apply status = %d\nbody: %s", res.Status, res.Body)
	}

	applied := rig.fake.applied
	if len(applied) != 1 {
		t.Fatalf("%d changes reached the directory, want 1", len(applied))
	}
	// The directory received a modify of mail, which is what the plan said --
	// not the add that was planned from.
	got := applied[0]
	if got.Type != directory.ChangeModify {
		t.Errorf("the directory received a %q", got.Type)
	}
	if len(got.Mods) != 1 || !strings.EqualFold(got.Mods[0].Name, "mail") {
		t.Errorf("the directory received %+v", got.Mods)
	}
	// And the LDIF the plan showed is the LDIF of what ran.
	if item.Preview != nil && item.Preview.Ldif != got.LDIF() {
		t.Errorf("the plan previewed:\n%s\nthe directory received:\n%s",
			item.Preview.Ldif, got.LDIF())
	}
}

// --- drift -------------------------------------------------------------------

func TestApplyRefusesAStalePlan(t *testing.T) {
	alice := personAt(t, planAlice, []string{"mail", "old@alder.test"})
	rig := planRig(t, alice)

	p := planFor(t, rig, fmt.Sprintf(
		`{"changes":[{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"mail",`+
			`"values":[{"text":"new@alder.test"}]}]}]}`, planAlice))
	item := p.Items[0]

	// Somebody else edits the entry in the meantime.
	alice.Set("mail", [][]byte{[]byte("someone-else@alder.test")})

	applyBody := fmt.Sprintf(`{"changes":[%s]}`, mustJSON(t, withBaseline(*item.Record, *item.Baseline)))
	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply", strings.NewReader(applyBody))

	if res.Status != http.StatusConflict {
		t.Fatalf("status = %d, want 409\nbody: %s", res.Status, res.Body)
	}
	if len(rig.fake.applied) != 0 {
		t.Errorf("%d changes were applied despite the refusal", len(rig.fake.applied))
	}
	body := decode[Error](t, res)
	if body.Error != ErrorErrorConflict {
		t.Errorf("error = %q, want conflict", body.Error)
	}
}

// A stale item anywhere in a set stops the whole set, before anything runs.
func TestOneStaleChangeStopsTheWholeChangeset(t *testing.T) {
	alice := personAt(t, planAlice, []string{"mail", "old@alder.test"})
	bob := personAt(t, "uid=bob,ou=people,dc=alder,dc=test", []string{"mail", "bob@alder.test"})
	rig := planRig(t, alice, bob)

	p := planFor(t, rig, fmt.Sprintf(`{"changes":[
      {"dn":%q,"type":"modify","mods":[{"op":"replace","name":"mail","values":[{"text":"a@alder.test"}]}]},
      {"dn":%q,"type":"modify","mods":[{"op":"replace","name":"mail","values":[{"text":"b@alder.test"}]}]}
    ]}`, "uid=bob,ou=people,dc=alder,dc=test", planAlice))

	// The second one goes stale.
	alice.Set("mail", [][]byte{[]byte("moved@alder.test")})

	first := withBaseline(*p.Items[0].Record, *p.Items[0].Baseline)
	second := withBaseline(*p.Items[1].Record, *p.Items[1].Baseline)
	applyBody := fmt.Sprintf(`{"changes":[%s,%s]}`, mustJSON(t, first), mustJSON(t, second))

	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply", strings.NewReader(applyBody))
	if res.Status != http.StatusConflict {
		t.Fatalf("status = %d, want 409\nbody: %s", res.Status, res.Body)
	}
	// Nothing ran, including the change that was still fine. That is the point
	// of checking the whole set first.
	if len(rig.fake.applied) != 0 {
		t.Errorf("%d changes ran before the stale one was found", len(rig.fake.applied))
	}
}

// A change with no baseline applies exactly as it did before the field existed.
func TestAChangeWithoutABaselineIsUnaffected(t *testing.T) {
	alice := personAt(t, planAlice, []string{"mail", "old@alder.test"})
	rig := planRig(t, alice)

	// No baseline, and the entry moves anyway.
	alice.Set("mail", [][]byte{[]byte("moved@alder.test")})
	body := fmt.Sprintf(
		`{"changes":[{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"mail",`+
			`"values":[{"text":"new@alder.test"}]}]}]}`, planAlice)

	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply", strings.NewReader(body))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", res.Status, res.Body)
	}
	if len(rig.fake.applied) != 1 {
		t.Errorf("%d changes applied, want 1", len(rig.fake.applied))
	}
}

// Planning writes nothing, so a read-only Alder still answers it. That is the
// deployment where "what would this do" is most worth asking.
func TestAReadOnlyAlderStillPlans(t *testing.T) {
	rig := newRig(t, Config{ReadOnly: true}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		byDN: map[string]*directory.Entry{
			strings.ToLower(planAlice): personAt(t, planAlice, []string{"mail", "old@alder.test"}),
		},
	})
	res := rig.do(t, http.MethodPost, "/api/v1/plan", strings.NewReader(fmt.Sprintf(
		`{"changes":[{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"mail",`+
			`"values":[{"text":"new@alder.test"}]}]}]}`, planAlice)))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", res.Status, res.Body)
	}
	if len(rig.fake.applied) != 0 {
		t.Error("planning wrote to the directory")
	}
}

// Rule 6 at the plan endpoint: a password change is planned and previewed, and
// the password is in neither.
func TestAPlannedPasswordChangeNeverEchoesThePassword(t *testing.T) {
	const secret = "correct-horse-battery-staple"
	rig := planRig(t, personAt(t, planAlice, []string{"cn", "Alice"}))

	res := rig.do(t, http.MethodPost, "/api/v1/plan", strings.NewReader(fmt.Sprintf(
		`{"changes":[{"dn":%q,"type":"setpassword","newPassword":%q}]}`, planAlice, secret)))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d\nbody: %s", res.Status, res.Body)
	}
	if strings.Contains(res.Body, secret) {
		t.Fatalf("the plan echoed the new password:\n%s", res.Body)
	}
	p := decode[Plan](t, res)
	if p.Items[0].Action != PlanActionSetPassword {
		t.Errorf("action = %q, want set_password", p.Items[0].Action)
	}
}

// The set-as-a-whole findings the changeset preview already made are reported
// here too, because a plan is where somebody looks before applying.
func TestAPlanReportsOrderingWarnings(t *testing.T) {
	rig := planRig(t)
	body := `{"changes":[
      {"dn":"uid=x,ou=new,dc=alder,dc=test","type":"add","attributes":[
        {"name":"objectClass","values":[{"text":"person"}]},
        {"name":"cn","values":[{"text":"x"}]},
        {"name":"sn","values":[{"text":"x"}]}]},
      {"dn":"ou=new,dc=alder,dc=test","type":"add","attributes":[
        {"name":"objectClass","values":[{"text":"organizationalUnit"}]},
        {"name":"ou","values":[{"text":"new"}]}]}
    ]}`
	p := planFor(t, rig, body)
	if p.Warnings == nil || len(*p.Warnings) == 0 {
		t.Fatal("a child created before its parent produced no warning")
	}
	if !strings.Contains(strings.Join(*p.Warnings, " "), "parent") {
		t.Errorf("warnings = %v", *p.Warnings)
	}
}

// withBaseline attaches a baseline to a change request, which is what a client
// applying a plan does.
func withBaseline(req ChangeRequest, baseline string) ChangeRequest {
	req.Baseline = &baseline
	return req
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	return string(raw)
}
