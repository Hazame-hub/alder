//go:build conformance

package conformance

import (
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
