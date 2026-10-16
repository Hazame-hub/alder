//go:build conformance

package conformance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// Recovering a change to the server's configuration and to its schema, against
// both real directories.
//
// The unit and HTTP tests hold the derivation to a directory that stores
// ordered values the way OpenLDAP does. These hold it to the servers, which is
// where it can actually be wrong: each stores a setting and a definition its
// own way -- a position in front of a definition on one, an X-ORIGIN added on
// the other -- and the compensation has to name what the server holds, through
// the identity the server routes that tree to. The assertions do not know which
// server they are talking to.

const (
	recProbeOID = "1.3.6.1.4.1.99997.1.90"
	recProbeDef = "( " + recProbeOID + " NAME 'alderRecoveryProbe' DESC 'recovery' " +
		"EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )"
)

func TestRecoveryReturnsAConfigurationSettingToWhatItWas(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.configWriteDN == "" {
			t.Skip("the configuration tree is not readable")
		}
		target := mustDN(t, s.configWriteDN)
		read := func() []string {
			t.Helper()
			e, err := sess.Read(ctx(t), target, []string{s.configWriteAttr})
			if err != nil {
				t.Fatalf("reading %s: %v", s.configWriteDN, err)
			}
			return e.GetStrings(s.configWriteAttr)
		}
		was := read()
		if len(was) != 1 {
			t.Fatalf("%s holds %v for %s, want one value", s.configWriteDN, was, s.configWriteAttr)
		}
		value, ok := alternateFor(s.configWriteAttr, was)
		if !ok {
			t.Fatalf("no alternate value for %s", s.configWriteAttr)
		}
		// Put back outside Alder whatever happens, so a failure here does not
		// leave the server configured differently for the next test.
		t.Cleanup(func() {
			_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModReplace, Name: s.configWriteAttr, Values: [][]byte{[]byte(was[0])}}}})
		})

		client, base := alderSession(t, s, true)
		result := recoveryPlanAndApply(t, client, base, []recChange{
			{"dn": s.configWriteDN, "type": "modify", "mods": []recChange{
				{"op": "replace", "name": s.configWriteAttr, "values": textValues(value)}}},
		}, true)
		if got := read(); len(got) != 1 || got[0] == was[0] {
			t.Fatalf("the setting did not change: %v", got)
		}
		bundle := recoveryOf(t, result.Recovery)
		if len(bundle.Steps) != 1 || bundle.Steps[0].Kind != api.PlanTargetConfig ||
			bundle.Steps[0].Recoverability != api.RecoverabilityExact {
			t.Fatalf("bundle %+v, want one exact configuration step", bundle.Steps)
		}

		inspection := inspectBundle(t, client, base, result.Recovery)
		for _, d := range inspection.Drift {
			if d.State != api.RecoveryDriftReady {
				t.Errorf("drift on an untouched server: %+v", d)
			}
		}
		recovered := recoveryPlanAndApply(t, client, base, asChanges(t, inspection.Changes), false)
		if recovered.FailedIndex != nil {
			t.Fatalf("recovery stopped: %+v", recovered.Outcomes)
		}
		// As the server stored it before, character for character.
		if got := read(); len(got) != 1 || got[0] != was[0] {
			t.Errorf("%s is %v after recovery, and was %v", s.configWriteAttr, got, was)
		}
	})
}

// TestRecoveryPutsConfigurationAccessRulesBack: a reviewed change replaced
// the configuration tree's access rules, and the bundle puts back exactly what
// the server held before -- every rule, in its spelling, at its position --
// with the same lockout warning the change carried.
//
// The replacement puts one rule first that names an entry which does not
// exist, and keeps every other rule: it still grants the test bind everything
// it had (each server's administrator, which its rules do not bind anyway),
// and changes nothing for any other case in the suite while it is in place.
// Putting it first renumbers every ordered rule after it, which is what makes
// "positions included" something the round trip has to get right.
//
// It does not skip when the entry holds no readable rule: a compensation is
// only derived from rules that were read, so the case fails instead, and the
// harness seeds one on each server.
func TestRecoveryPutsConfigurationAccessRulesBack(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.configAccessDN == "" {
			t.Skip("the configuration tree is not readable")
		}
		target := mustDN(t, s.configAccessDN)
		attr := s.configAccessAttr
		read := func() []string {
			t.Helper()
			e, err := sess.Read(ctx(t), target, []string{attr})
			if err != nil {
				t.Fatalf("reading %s: %v", s.configAccessDN, err)
			}
			return e.GetStrings(attr)
		}
		was := read()
		if len(was) == 0 {
			// Both harness servers hold rules there: OpenLDAP from slapd.conf,
			// 389 DS from test/compose/ds389/config-access.ldif. None read
			// is a harness that was not seeded, or rules the bind cannot
			// read -- which the bundle would rightly not compensate, and so
			// would prove nothing here.
			t.Fatalf("%s holds no %s this bind can read; is the harness seeded? (task compose:up)", s.configAccessDN, attr)
		}
		// Put back outside Alder whatever happens: a failure here must not
		// leave the server with rules the rest of the suite did not expect.
		t.Cleanup(func() {
			if sameSet(read(), was) {
				return
			}
			if err := sess.Apply(ctx(t), directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModReplace, Name: attr, Values: bs(was...)}}}); err != nil {
				t.Errorf("restoring %s on %s outside Alder: %v", attr, s.configAccessDN, err)
			}
		})

		// The rules as a person would write them: the position the server
		// keeps in front of an ordered value, where it keeps one, is left for
		// the server to assign.
		replacement := []string{s.configAccessProbe}
		for _, v := range was {
			replacement = append(replacement, withoutPosition(v))
		}
		client, base := alderSession(t, s, true)
		result := recoveryPlanAndApply(t, client, base, []recChange{
			{"dn": s.configAccessDN, "type": "modify", "mods": []recChange{
				{"op": "replace", "name": attr, "values": textValues(replacement...)}}},
		}, true)
		changed := read()
		if len(changed) != len(was)+1 || sameSet(changed, was) {
			t.Fatalf("the rules did not change as asked: %q", changed)
		}

		bundle := recoveryOf(t, result.Recovery)
		if len(bundle.Steps) != 1 || bundle.Steps[0].Kind != api.PlanTargetConfig ||
			bundle.Steps[0].Recoverability != api.RecoverabilityExact || bundle.Steps[0].Reasons != nil {
			t.Fatalf("bundle %+v, want one exact configuration step with no reasons", bundle.Steps)
		}

		inspection := inspectBundle(t, client, base, result.Recovery)
		if len(inspection.Changes) != 1 || len(inspection.Drift) != 1 || inspection.Drift[0].State != api.RecoveryDriftReady {
			t.Fatalf("changes %+v drift %+v", inspection.Changes, inspection.Drift)
		}
		// What goes back is what the server held, character for character,
		// under an expectation of what the change left.
		change := inspection.Changes[0]
		if change.Mods == nil || len(*change.Mods) != 1 || (*change.Mods)[0].Op != api.ChangeModOpReplace ||
			!sameSet(requestTexts((*change.Mods)[0].Values), was) {
			t.Errorf("the compensation %+v does not put back %q", change.Mods, was)
		}
		if change.Expect == nil || len(change.Expect.Attributes) != 1 ||
			!sameSet(requestTexts(&change.Expect.Attributes[0].Values), changed) {
			t.Errorf("expectation %+v, want the rules as the change left them", change.Expect)
		}

		// Planned like the change was, with the same lockout warning.
		body, _ := json.Marshal(map[string]any{"changes": asChanges(t, inspection.Changes), "reconcile": false})
		res := post(t, client, base+"/plan", string(body))
		if res.status != http.StatusOK {
			t.Fatalf("planning the compensation: status %d\n%s", res.status, res.body)
		}
		plan := decodeInto[api.Plan](t, res)
		if len(plan.Items) != 1 || !warnsAbout(plan.Items[0].Preview, attr) {
			t.Errorf("the compensation is planned without the lockout warning for %s: %+v", attr, plan.Items)
		}

		recovered := recoveryPlanAndApply(t, client, base, asChanges(t, inspection.Changes), false)
		if recovered.FailedIndex != nil {
			t.Fatalf("recovery stopped: %+v", recovered.Outcomes)
		}
		// Identical, positions included: an ordered rule's position is part
		// of the value the server returns.
		if got := read(); len(got) != len(was) || !sameSet(got, was) {
			t.Errorf("%s is %q after recovery, and was %q", attr, got, was)
		}
	})
}

// withoutPosition is a value without the "{n}" a server that keeps ordered
// values puts in front of it, and the value unchanged when it has none.
func withoutPosition(v string) string {
	if !strings.HasPrefix(v, "{") {
		return v
	}
	end := strings.IndexByte(v, '}')
	if end < 2 {
		return v
	}
	for _, c := range v[1:end] {
		if c < '0' || c > '9' {
			return v
		}
	}
	return v[end+1:]
}

func requestTexts(values *[]api.AttributeValue) []string {
	if values == nil {
		return nil
	}
	out := make([]string, 0, len(*values))
	for _, v := range *values {
		if v.Text == nil {
			out = append(out, "")
			continue
		}
		out = append(out, *v.Text)
	}
	return out
}

func warnsAbout(preview *api.ChangePreview, attr string) bool {
	if preview == nil || preview.Warnings == nil {
		return false
	}
	for _, w := range *preview.Warnings {
		if len(w) > len(attr) && strings.EqualFold(w[:len(attr)+1], attr+":") {
			return true
		}
	}
	return false
}

func TestRecoveryRemovesADefinitionTheChangeAdded(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		target := schemaTarget(t, sess)
		attr := sess.Capabilities().SchemaWrite.AttributeTypeAttr
		probe := schemaDef{kind: directory.SchemaDefAttributeType, oid: recProbeOID}
		purgeSchemaDefinitions(t, s, sess, target, probe)
		t.Cleanup(func() { purgeSchemaDefinitions(t, s, sess, target, probe) })

		stored := func() []string {
			t.Helper()
			defs, err := sess.SchemaDefinitions(ctx(t), target, directory.SchemaDefAttributeType)
			if err != nil {
				t.Fatalf("reading the definitions at %s: %v", target, err)
			}
			var out []string
			for _, d := range defs {
				if directory.DefinitionOID(d) == recProbeOID {
					out = append(out, d)
				}
			}
			return out
		}

		client, base := alderSession(t, s, true)
		result := recoveryPlanAndApply(t, client, base, []recChange{
			{"dn": target, "type": "modify", "mods": []recChange{
				{"op": "add", "name": attr, "values": textValues(recProbeDef)}}},
		}, true)
		held := stored()
		if len(held) != 1 {
			t.Fatalf("after the add, %s holds %v", target, held)
		}

		bundle := recoveryOf(t, result.Recovery)
		if len(bundle.Steps) != 1 || bundle.Steps[0].Kind != api.PlanTargetSchema ||
			bundle.Steps[0].Recoverability != api.RecoverabilityPartial {
			t.Fatalf("bundle %+v, want one partial schema step", bundle.Steps)
		}
		if r := bundle.Steps[0].Reasons; r == nil || len(*r) != 1 || (*r)[0].Code != api.RecoveryReasonDefinitionMayBeInUse {
			t.Errorf("reasons %+v, want definition_may_be_in_use alone", r)
		}

		inspection := inspectBundle(t, client, base, result.Recovery)
		if len(inspection.Changes) != 1 || len(inspection.Drift) != 1 || inspection.Drift[0].State != api.RecoveryDriftReady {
			t.Fatalf("changes %+v drift %+v", inspection.Changes, inspection.Drift)
		}
		// What is removed is what the server stored, which is not what was
		// sent on either server, and the expectation is about that one
		// definition.
		change := inspection.Changes[0]
		if change.Mods == nil || len(*change.Mods) != 1 || (*change.Mods)[0].Values == nil ||
			len(*(*change.Mods)[0].Values) != 1 || *(*(*change.Mods)[0].Values)[0].Text != held[0] {
			t.Errorf("the compensation %+v does not remove the stored %q", change.Mods, held[0])
		}
		if change.Expect == nil || change.Expect.Definitions == nil || len(*change.Expect.Definitions) != 1 ||
			!strings.EqualFold((*change.Expect.Definitions)[0].Oid, recProbeOID) {
			t.Errorf("expectation %+v", change.Expect)
		}

		recovered := recoveryPlanAndApply(t, client, base, asChanges(t, inspection.Changes), false)
		if recovered.FailedIndex != nil {
			t.Fatalf("recovery stopped: %+v", recovered.Outcomes)
		}
		if left := stored(); len(left) != 0 {
			t.Errorf("the definition is still there after recovery: %v", left)
		}
	})
}
