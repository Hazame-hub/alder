//go:build conformance

package conformance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// 1.6: every single change the interface builds is planned, reviewed and
// applied with the plan's token -- against both servers, through the same HTTP
// endpoints the confirmation dialog calls.

func planOneOverHTTP(t *testing.T, client *http.Client, base, change string) (api.PlanItem, string) {
	t.Helper()
	res := post(t, client, base+"/plan", `{"reconcile":false,"changes":[`+change+`]}`)
	if res.status != http.StatusOK {
		t.Fatalf("planning %s: status %d\n%s", change, res.status, res.body)
	}
	p := decodeInto[api.Plan](t, res)
	if len(p.Items) != 1 {
		t.Fatalf("items = %+v, want one", p.Items)
	}
	return p.Items[0], res.body
}

func applyOneOverHTTP(t *testing.T, client *http.Client, base, change string, baseline *string) httpResult {
	t.Helper()
	var req map[string]any
	if err := json.Unmarshal([]byte(change), &req); err != nil {
		t.Fatalf("decoding %s: %v", change, err)
	}
	if baseline != nil {
		req["baseline"] = *baseline
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	return post(t, client, base+"/changes/apply", string(body))
}

// planAndApply is the dialog's whole path: plan, check it is the change
// expected, apply exactly that change with its token.
func planAndApply(t *testing.T, client *http.Client, base, change string, want api.PlanAction) api.PlanItem {
	t.Helper()
	item, _ := planOneOverHTTP(t, client, base, change)
	if item.Action != want {
		t.Fatalf("action = %q, want %q\nitem: %+v", item.Action, want, item)
	}
	if item.Baseline == nil {
		t.Fatalf("an applicable plan item carries no token: %+v", item)
	}
	if res := applyOneOverHTTP(t, client, base, change, item.Baseline); res.status != http.StatusOK {
		t.Fatalf("applying the planned %s: status %d\n%s", want, res.status, res.body)
	}
	return item
}

func personChange(dnText, uid string) string {
	return fmt.Sprintf(`{"dn":%q,"type":"add","attributes":[
		{"name":"objectClass","values":[{"text":"top"},{"text":"person"},{"text":"organizationalPerson"},{"text":"inetOrgPerson"}]},
		{"name":"uid","values":[{"text":%q}]},{"name":"cn","values":[{"text":"Alder Single"}]},
		{"name":"sn","values":[{"text":"Single"}]}]}`, dnText, uid)
}

func deleteQuietly(t *testing.T, sess directory.Session, targets ...string) {
	t.Helper()
	for _, d := range targets {
		_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, d), Type: directory.ChangeDelete})
	}
}

func TestSingleEntryChangesPlanThenApplyOverHTTP(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		var (
			created = "uid=alder-single,ou=people," + suffix
			renamed = "uid=alder-single-renamed,ou=people," + suffix
			moved   = "uid=alder-single-renamed,ou=groups," + suffix
		)
		deleteQuietly(t, sess, moved, renamed, created)
		t.Cleanup(func() { deleteQuietly(t, sess, moved, renamed, created) })

		item := planAndApply(t, client, base, personChange(created, "alder-single"), api.PlanActionAdd)
		if item.Kind == nil || *item.Kind != api.PlanTargetData {
			t.Errorf("kind = %s, want data", kindText(item.Kind))
		}
		if got := readOne(t, sess, created, "cn"); got != "Alder Single" {
			t.Fatalf("cn = %q after the planned add", got)
		}

		multi := fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[
			{"op":"replace","name":"description","values":[{"text":"reviewed"}]},
			{"op":"add","name":"mail","values":[{"text":"single@alder.test"}]},
			{"op":"add","name":"telephoneNumber","values":[{"text":"+1 555 0100"}]}]}`, created)
		planAndApply(t, client, base, multi, api.PlanActionModify)
		if got := readOne(t, sess, created, "mail"); got != "single@alder.test" {
			t.Errorf("mail = %q after the planned modify", got)
		}

		for name, noop := range map[string]string{
			"replace with the held value": fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"description","values":[{"text":"reviewed"}]}]}`, created),
			"add a present value":         fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"mail","values":[{"text":"single@alder.test"}]}]}`, created),
		} {
			nothing, _ := planOneOverHTTP(t, client, base, noop)
			if nothing.Action != api.PlanActionUnchanged || nothing.Baseline != nil || nothing.Record != nil {
				t.Errorf("%s: item = %+v, want unchanged with nothing to apply", name, nothing)
			}
		}

		planAndApply(t, client, base,
			fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=alder-single-renamed","deleteOldRdn":true}`, created),
			api.PlanActionRename)
		if got := readOne(t, sess, renamed, "uid"); got != "alder-single-renamed" {
			t.Errorf("uid = %q after the planned rename; deleteOldRdn should have removed the old value", got)
		}

		planAndApply(t, client, base,
			fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=alder-single-renamed","deleteOldRdn":false,"newSuperior":%q}`,
				renamed, "ou=groups,"+suffix),
			api.PlanActionRename)
		if got := readOne(t, sess, moved, "cn"); got != "Alder Single" {
			t.Errorf("cn = %q at the new location after the planned move", got)
		}

		planAndApply(t, client, base, fmt.Sprintf(`{"dn":%q,"type":"delete"}`, moved), api.PlanActionDelete)
		if _, err := sess.Read(ctx(t), mustDN(t, moved), []string{"cn"}); err == nil {
			t.Error("the entry is still there after the planned delete")
		}
	})
}

// The single-change lifecycle the dialog implements: plan, the directory moves,
// apply is refused and nothing is written, a new plan is made and reviewed, and
// only that applies. Then a request that is not its plan is refused too.
func TestASingleStaleChangeIsRefusedThenReplannedOverHTTP(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		target := "uid=user0013,ou=people," + suffix
		restoreDescription(t, sess, target)
		change := fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"description","values":[{"text":"what was reviewed"}]}]}`, target)

		item, _ := planOneOverHTTP(t, client, base, change)
		if err := sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, target), Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: "description", Values: [][]byte{[]byte("somebody else")}}}}); err != nil {
			t.Fatalf("the concurrent edit: %v", err)
		}

		refused := applyOneOverHTTP(t, client, base, change, item.Baseline)
		if refused.status != http.StatusConflict {
			t.Fatalf("applying a stale single change: status %d, want 409\n%s", refused.status, refused.body)
		}
		body := decodeInto[api.Error](t, refused)
		if body.Error != api.ErrorErrorConflict || body.Cause == nil || *body.Cause != api.ErrorCausePlanStale {
			t.Errorf("refusal = %+v, want conflict with cause plan_stale", body)
		}
		if got := readOne(t, sess, target, "description"); got != "somebody else" {
			t.Fatalf("description = %q: a refused plan wrote something", got)
		}

		planAndApply(t, client, base, change, api.PlanActionModify)
		if got := readOne(t, sess, target, "description"); got != "what was reviewed" {
			t.Errorf("description = %q after applying the recomputed plan", got)
		}

		planned, _ := planOneOverHTTP(t, client, base,
			fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"description","values":[{"text":"planned"}]}]}`, target))
		sent := applyOneOverHTTP(t, client, base,
			fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"description","values":[{"text":"not planned"}]}]}`, target),
			planned.Baseline)
		if sent.status != http.StatusBadRequest || decodeInto[api.Error](t, sent).Error != api.ErrorErrorPlanMismatch {
			t.Errorf("a change sent under another change's token: status %d\n%s", sent.status, sent.body)
		}
	})
}

// A password change is planned without the password, and the token binds it:
// another password of the same length is refused, the planned one applies.
func TestAPlannedPasswordIsBoundToItsValueOverHTTP(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		target := "uid=alder-single-pw,ou=people," + suffix
		deleteQuietly(t, sess, target)
		t.Cleanup(func() { deleteQuietly(t, sess, target) })
		planAndApply(t, client, base, personChange(target, "alder-single-pw"), api.PlanActionAdd)

		const planned, substitute = "Plan-bound-secret-1", "Plan-bound-secret-2"
		change := fmt.Sprintf(`{"dn":%q,"type":"setpassword","newPassword":%q}`, target, planned)
		item, body := planOneOverHTTP(t, client, base, change)
		if item.Action != api.PlanActionSetPassword || item.Baseline == nil {
			t.Fatalf("item = %+v, want an applicable password change", item)
		}
		for _, leak := range []string{planned, "Plan-bound-secret"} {
			if contains(body, leak) {
				t.Fatalf("the plan response carries the password:\n%s", body)
			}
		}

		swapped := applyOneOverHTTP(t, client, base,
			fmt.Sprintf(`{"dn":%q,"type":"setpassword","newPassword":%q}`, target, substitute), item.Baseline)
		if swapped.status != http.StatusBadRequest || decodeInto[api.Error](t, swapped).Error != api.ErrorErrorPlanMismatch {
			t.Fatalf("a substituted password of the same length: status %d\n%s", swapped.status, swapped.body)
		}
		if res := applyOneOverHTTP(t, client, base, change, item.Baseline); res.status != http.StatusOK {
			t.Fatalf("applying the planned password: status %d\n%s", res.status, res.body)
		} else if contains(res.body, planned) {
			t.Error("the apply response carries the password")
		}
	})
}

// A schema definition built by /schema/change is planned as a schema change,
// refused when the schema moved after planning, and applied once replanned.
func TestSchemaChangePlanThenApplyOverHTTP(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alderSession(t, s, true)
		target := schemaTarget(t, sess)
		_ = deleteProbe(t, sess, target)
		t.Cleanup(func() { _ = deleteProbe(t, sess, target) })

		build := func(desc string) string {
			t.Helper()
			def, err := schemaProbe(desc).Definition()
			if err != nil {
				t.Fatalf("rendering the probe: %v", err)
			}
			req, err := json.Marshal(map[string]any{"targetDn": target, "kind": "attributeType", "op": "add", "definition": def})
			if err != nil {
				t.Fatal(err)
			}
			res := post(t, client, base+"/schema/change", string(req))
			if res.status != http.StatusOK {
				t.Fatalf("building the schema change: status %d\n%s", res.status, res.body)
			}
			change, err := json.Marshal(decodeInto[api.SchemaChangeBuild](t, res).Change)
			if err != nil {
				t.Fatal(err)
			}
			return string(change)
		}

		change := build("planned probe")
		item, _ := planOneOverHTTP(t, client, base, change)
		if item.Action != api.PlanActionModify || item.Baseline == nil {
			t.Fatalf("item = %+v, want an applicable modify of the schema", item)
		}
		if item.Kind == nil || *item.Kind != api.PlanTargetSchema {
			t.Errorf("kind = %s, want schema", kindText(item.Kind))
		}

		// Somebody else changes the schema entry this plan depends on.
		def, err := schemaProbe("somebody else's probe").Definition()
		if err != nil {
			t.Fatal(err)
		}
		if err := applySchemaChange(t, sess, directory.SchemaChangeRequest{
			TargetDN: target, Kind: directory.SchemaDefAttributeType, Op: directory.SchemaOpAdd, Definition: def,
		}); err != nil {
			t.Fatalf("the concurrent schema change: %v", err)
		}
		refused := applyOneOverHTTP(t, client, base, change, item.Baseline)
		if refused.status != http.StatusConflict {
			t.Fatalf("applying a stale schema plan: status %d, want 409\n%s", refused.status, refused.body)
		}
		if err := deleteProbe(t, sess, target); err != nil {
			t.Fatalf("removing the concurrent definition: %v", err)
		}

		planAndApply(t, client, base, build("planned probe"), api.PlanActionModify)
		sch, err := connectForSchema(t, s).Schema(ctx(t))
		if err != nil {
			t.Fatalf("reading the schema back: %v", err)
		}
		if at := sch.AttributeType("alderProbeAttr"); at == nil || at.Desc != "planned probe" {
			t.Errorf("after the planned schema change the probe is %+v", at)
		}
	})
}

// A configuration setting is planned as a configuration change, refused when
// the setting moved after planning, and applied once replanned.
func TestConfigChangePlanThenApplyOverHTTP(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if s.configWriteDN == "" {
			t.Skip("no configuration setting is nominated for this server")
		}
		if !sess.Capabilities().Config.Readable {
			t.Skipf("the configuration tree is not readable: %s", sess.Capabilities().Config.Reason)
		}
		client, base := alderSession(t, s, true)
		target := mustDN(t, s.configWriteDN)
		set := func(value string) error {
			return sess.Apply(ctx(t), directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModReplace, Name: s.configWriteAttr, Values: [][]byte{[]byte(value)}}}})
		}
		before := readOne(t, sess, s.configWriteDN, s.configWriteAttr)
		if before == "" || before == s.configWriteValue {
			t.Fatalf("%s is %q, which this test cannot distinguish from its own write", s.configWriteAttr, before)
		}
		t.Cleanup(func() {
			if err := set(before); err != nil {
				t.Errorf("restoring %s to %q: %v", s.configWriteAttr, before, err)
			}
		})

		change := fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":%q,"values":[{"text":%q}]}]}`,
			s.configWriteDN, s.configWriteAttr, s.configWriteValue)
		item, _ := planOneOverHTTP(t, client, base, change)
		if item.Action != api.PlanActionModify || item.Baseline == nil {
			t.Fatalf("item = %+v, want an applicable modify", item)
		}
		if item.Kind == nil || *item.Kind != api.PlanTargetConfig {
			t.Errorf("kind = %s, want config", kindText(item.Kind))
		}

		if err := set("1799"); err != nil {
			t.Fatalf("the concurrent configuration change: %v", err)
		}
		if refused := applyOneOverHTTP(t, client, base, change, item.Baseline); refused.status != http.StatusConflict {
			t.Fatalf("applying a stale configuration plan: status %d, want 409\n%s", refused.status, refused.body)
		}
		if got := readOne(t, sess, s.configWriteDN, s.configWriteAttr); got != "1799" {
			t.Fatalf("%s = %q: a refused plan wrote something", s.configWriteAttr, got)
		}

		planAndApply(t, client, base, change, api.PlanActionModify)
		if got := readOne(t, sess, s.configWriteDN, s.configWriteAttr); got != s.configWriteValue {
			t.Errorf("%s = %q after the planned change, want %q", s.configWriteAttr, got, s.configWriteValue)
		}
	})
}

func kindText(k *api.PlanTargetKind) string {
	if k == nil {
		return "absent"
	}
	return string(*k)
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
