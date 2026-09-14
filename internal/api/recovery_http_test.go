package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Recovery, end to end through the HTTP layer, against a directory that
// actually changes.
//
// The fake the other HTTP tests use records writes and never applies them,
// which is right for asking "what was sent" and useless for asking "is the
// directory back where it was". This one applies every change the way a server
// does -- refusing an add over an entry, a delete of a parent, a value that is
// not there -- and answers a read with only the attributes asked for, so a
// recovery derived from a read that did not cover what it needed shows up here
// rather than against a real server.
//
// The property under test is the one the feature exists for: S0, apply with a
// bundle, inspect the bundle, plan its changes, apply the plan, and the
// directory's user data equals S0 again. The comparison is written out below
// rather than borrowed from the code under test.

type memDirectory struct {
	*fakeSession
}

// operationalInTest are attributes this directory maintains, so a read of "*"
// does not return them, as a server's would not.
var operationalInTest = map[string]bool{"entryuuid": true, "modifytimestamp": true}

func memKey(d dn.DN) string { return strings.ToLower(d.String()) }

func copyEntry(e *directory.Entry, attrs []string) *directory.Entry {
	all, named := false, map[string]bool{}
	for _, a := range attrs {
		switch a {
		case "*":
			all = true
		case "+":
		default:
			named[strings.ToLower(schema.BaseName(a))] = true
		}
	}
	out := directory.NewEntry(e.DN)
	for _, name := range e.Order {
		base := strings.ToLower(schema.BaseName(name))
		if !named[base] && (!all || operationalInTest[base]) {
			continue
		}
		values := make([][]byte, 0, len(e.Attributes[name]))
		for _, v := range e.Attributes[name] {
			values = append(values, append([]byte(nil), v...))
		}
		out.Set(name, values)
	}
	return out
}

func (m *memDirectory) Read(_ context.Context, target dn.DN, attrs []string) (*directory.Entry, error) {
	m.readDNs = append(m.readDNs, target.String())
	e, ok := m.byDN[memKey(target)]
	if !ok {
		return nil, &ldapdriver.Error{Code: 32, Message: "No Such Object"}
	}
	return copyEntry(e, attrs), nil
}

func (m *memDirectory) HasChildren(_ context.Context, target dn.DN) (bool, error) {
	for _, e := range m.byDN {
		if e.DN.IsChildOf(target) {
			return true, nil
		}
	}
	return false, nil
}

func ldapErr(code uint16, msg string) error { return &ldapdriver.Error{Code: code, Message: msg} }

func removeValue(values [][]byte, v []byte) ([][]byte, bool) {
	for i, have := range values {
		if bytes.Equal(have, v) {
			return append(values[:i:i], values[i+1:]...), true
		}
	}
	return values, false
}

func (m *memDirectory) Apply(_ context.Context, ch directory.ChangeRecord) error {
	if m.applyErr != nil {
		return m.applyErr
	}
	m.applied = append(m.applied, ch)
	key := memKey(ch.DN)
	e, exists := m.byDN[key]
	switch ch.Type {
	case directory.ChangeAdd:
		if exists {
			return ldapErr(68, "Already Exists")
		}
		if parent := ch.DN.Parent(); !parent.IsEmpty() && len(parent) > 2 {
			if _, ok := m.byDN[memKey(parent)]; !ok {
				return ldapErr(32, "No Such Object")
			}
		}
		added := directory.NewEntry(ch.DN)
		for _, a := range ch.Attrs {
			added.Set(a.Name, append(added.Get(a.Name), a.Values...))
		}
		for _, ava := range ch.DN.RDN() {
			if _, found := removeValue(added.Get(ava.Type), []byte(ava.Value)); !found {
				added.Set(ava.Type, append(added.Get(ava.Type), []byte(ava.Value)))
			}
		}
		added.Set("entryUUID", [][]byte{[]byte(fmt.Sprintf("uuid-%d", len(m.applied)))})
		m.byDN[key] = added
	case directory.ChangeModify:
		if !exists {
			return ldapErr(32, "No Such Object")
		}
		work := copyEntry(e, []string{"*", "entryUUID", "modifyTimestamp"})
		for _, mod := range ch.Mods {
			current := work.Get(mod.Name)
			name := mod.Name
			for _, n := range work.Order {
				if strings.EqualFold(n, mod.Name) {
					name = n
				}
			}
			switch mod.Op {
			case directory.ModReplace:
				current = append([][]byte(nil), mod.Values...)
			case directory.ModAdd:
				for _, v := range mod.Values {
					if _, found := removeValue(append([][]byte(nil), current...), v); found {
						return ldapErr(20, "Type Or Value Exists")
					}
					current = append(current, v)
				}
			case directory.ModDelete:
				if len(current) == 0 {
					return ldapErr(16, "No Such Attribute")
				}
				if len(mod.Values) == 0 {
					current = nil
				}
				for _, v := range mod.Values {
					var found bool
					if current, found = removeValue(current, v); !found {
						return ldapErr(16, "No Such Attribute")
					}
				}
			}
			if len(current) == 0 {
				delete(work.Attributes, name)
				order := work.Order[:0]
				for _, n := range work.Order {
					if !strings.EqualFold(n, name) {
						order = append(order, n)
					}
				}
				work.Order = order
				continue
			}
			work.Set(name, current)
		}
		m.byDN[key] = work
	case directory.ChangeDelete:
		if !exists {
			return ldapErr(32, "No Such Object")
		}
		if has, _ := m.HasChildren(context.Background(), ch.DN); has {
			return ldapErr(66, "Not Allowed On Non-Leaf")
		}
		delete(m.byDN, key)
	case directory.ChangeModRDN:
		if !exists {
			return ldapErr(32, "No Such Object")
		}
		target, err := ch.Target()
		if err != nil {
			return err
		}
		if _, taken := m.byDN[memKey(target)]; taken {
			return ldapErr(68, "Already Exists")
		}
		moved := copyEntry(e, []string{"*", "entryUUID", "modifyTimestamp"})
		moved.DN = target
		if ch.DeleteOldRDN {
			for _, ava := range ch.DN.RDN() {
				values, _ := removeValue(moved.Get(ava.Type), []byte(ava.Value))
				moved.Set(ava.Type, values)
			}
		}
		for _, ava := range target.RDN() {
			if _, found := removeValue(append([][]byte(nil), moved.Get(ava.Type)...), []byte(ava.Value)); !found {
				moved.Set(ava.Type, append(moved.Get(ava.Type), []byte(ava.Value)))
			}
		}
		delete(m.byDN, key)
		m.byDN[memKey(target)] = moved
	case directory.ChangeSetPassword:
		if !exists {
			return ldapErr(32, "No Such Object")
		}
		e.Set("userPassword", [][]byte{[]byte("{SSHA}c2VydmVyLWhhc2hlZA==")})
	}
	return nil
}

// state is the directory's user data as a comparable text: every entry,
// every attribute but the server's own, values sorted.
func (m *memDirectory) state(t *testing.T) string {
	t.Helper()
	var lines []string
	for key, e := range m.byDN {
		for _, name := range e.Order {
			base := strings.ToLower(schema.BaseName(name))
			if operationalInTest[base] || len(e.Attributes[name]) == 0 {
				continue
			}
			values := make([]string, 0, len(e.Attributes[name]))
			for _, v := range e.Attributes[name] {
				values = append(values, string(v))
			}
			sort.Strings(values)
			lines = append(lines, key+" | "+base+" = "+strings.Join(values, " ; "))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func recoveryRig(t *testing.T, entries ...*directory.Entry) (*testRig, *memDirectory) {
	t.Helper()
	byDN := map[string]*directory.Entry{}
	for _, e := range entries {
		byDN[memKey(e.DN)] = e
	}
	mem := &memDirectory{fakeSession: &fakeSession{caps: defaultCaps(), sch: testSchema(t), byDN: byDN}}
	mem.caps.VendorName = "Test Directory"

	s := NewServer(slog.New(slog.DiscardHandler), Config{IdleTimeout: time.Minute, MaxLifetime: time.Hour})
	t.Cleanup(s.sessions.Close)
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	s.Register(app)
	sess, err := s.sessions.Add(mem, directory.ConnConfig{
		Host: "ldap.example.test", Port: 636, TLS: directory.TLSModeLDAPS,
		BindDN: "cn=admin,dc=alder,dc=test", BindPassword: sentinelPassword,
	}, false)
	if err != nil {
		t.Fatalf("opening a session: %v", err)
	}
	return &testRig{app: app, server: s, fake: mem.fakeSession, cookie: sess.ID}, mem
}

const (
	recOU    = "ou=people,dc=alder,dc=test"
	recAlice = "uid=alice,ou=people,dc=alder,dc=test"
	recBob   = "uid=bob,ou=people,dc=alder,dc=test"
	recCarol = "uid=carol,ou=people,dc=alder,dc=test"
	recErin  = "uid=erin,ou=people,dc=alder,dc=test"
	recOld   = "ou=former,dc=alder,dc=test"
	oldHash  = "{SSHA}b2xkLWJvYi1oYXNo"
)

func ouAt(t *testing.T, d, name string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(mustParse(t, d))
	e.Set("objectClass", [][]byte{[]byte("top"), []byte("organizationalUnit")})
	e.Set("ou", [][]byte{[]byte(name)})
	return e
}

func standardDirectory(t *testing.T) []*directory.Entry {
	t.Helper()
	bob := personAt(t, recBob, []string{"cn", "Bob"}, []string{"sn", "B"}, []string{"uid", "bob"},
		[]string{"userPassword", oldHash})
	bob.Set("entryUUID", [][]byte{[]byte("bob-original-uuid")})
	return []*directory.Entry{
		ouAt(t, recOU, "people"),
		ouAt(t, recOld, "former"),
		personAt(t, recAlice, []string{"cn", "Alice"}, []string{"sn", "L"}, []string{"uid", "alice"},
			[]string{"description", "first", "second"}, []string{"mail", "alice@alder.test"}),
		bob,
		personAt(t, recCarol, []string{"cn", "Carol"}, []string{"sn", "C"}, []string{"uid", "carol"}),
	}
}

// applyThroughPlan plans changes and applies what the plan would apply, with
// its baselines, the way the interface does. It returns the plan and the
// result, and fails the test on a status other than 200.
func applyThroughPlan(t *testing.T, rig *testRig, changes []ChangeRequest, withRecovery bool) (Plan, ChangesetResult) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"changes": changes})
	if err != nil {
		t.Fatal(err)
	}
	p := planFor(t, rig, string(body))
	var send []ChangeRequest
	for _, item := range p.Items {
		if item.Baseline == nil {
			continue
		}
		send = append(send, withBaseline(changes[item.Index], *item.Baseline))
	}
	if len(send) == 0 {
		return p, ChangesetResult{}
	}
	applyBody, err := json.Marshal(map[string]any{"changes": send, "recovery": withRecovery})
	if err != nil {
		t.Fatal(err)
	}
	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply", bytes.NewReader(applyBody))
	if res.Status != http.StatusOK {
		t.Fatalf("apply status = %d\nbody: %s", res.Status, res.Body)
	}
	return p, decode[ChangesetResult](t, res)
}

func bundleJSON(t *testing.T, b *json.RawMessage) []byte {
	t.Helper()
	if b == nil {
		t.Fatal("no recovery bundle was returned")
	}
	return []byte(*b)
}

func decodeBundle(t *testing.T, b *json.RawMessage) *RecoveryBundle {
	t.Helper()
	var out RecoveryBundle
	if err := json.Unmarshal(bundleJSON(t, b), &out); err != nil {
		t.Fatalf("the bundle does not decode: %v", err)
	}
	return &out
}

func inspect(t *testing.T, rig *testRig, raw []byte) RecoveryInspection {
	t.Helper()
	res := rig.do(t, http.MethodPost, "/api/v1/recovery/inspect", bytes.NewReader(raw))
	if res.Status != http.StatusOK {
		t.Fatalf("inspect status = %d\nbody: %s", res.Status, res.Body)
	}
	return decode[RecoveryInspection](t, res)
}

func parseChanges(t *testing.T, raw ...string) []ChangeRequest {
	t.Helper()
	out := make([]ChangeRequest, 0, len(raw))
	for _, r := range raw {
		var ch ChangeRequest
		if err := json.Unmarshal([]byte(r), &ch); err != nil {
			t.Fatalf("change %s: %v", r, err)
		}
		out = append(out, ch)
	}
	return out
}

// roundTrip applies changes with a bundle, recovers through inspect, plan and
// apply, and asserts the directory is back at S0.
func roundTrip(t *testing.T, changes []ChangeRequest) (*RecoveryBundle, RecoveryInspection) {
	t.Helper()
	rig, mem := recoveryRig(t, standardDirectory(t)...)
	s0 := mem.state(t)

	_, result := applyThroughPlan(t, rig, changes, true)
	if result.AppliedCount != len(changes) {
		t.Fatalf("applied %d of %d: %+v", result.AppliedCount, len(changes), result.Outcomes)
	}
	if mem.state(t) == s0 {
		t.Fatal("the changes did not change anything, so recovering them proves nothing")
	}
	inspection := inspect(t, rig, bundleJSON(t, result.Recovery))
	for _, d := range inspection.Drift {
		if d.State != RecoveryDriftReady {
			t.Fatalf("an undisturbed directory reports drift: %+v", d)
		}
	}

	_, recovered := applyThroughPlan(t, rig, inspection.Changes, false)
	if recovered.AppliedCount != len(inspection.Changes) {
		t.Fatalf("recovery applied %d of %d: %+v", recovered.AppliedCount, len(inspection.Changes), recovered.Outcomes)
	}
	if got := mem.state(t); got != s0 {
		t.Errorf("the directory is not back at S0\n--- S0\n%s\n--- now\n%s", s0, got)
	}
	return decodeBundle(t, result.Recovery), inspection
}

// --- exact recoveries ---------------------------------------------------------

func TestRecoveryReturnsAnAttributeModificationToS0(t *testing.T) {
	b, inspection := roundTrip(t, parseChanges(t, fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[
		{"op":"replace","name":"description","values":[{"text":"second"},{"text":"third"}]},
		{"op":"add","name":"telephoneNumber","values":[{"text":"+1 555 0100"},{"text":"+1 555 0101"}]},
		{"op":"delete","name":"mail"},
		{"op":"add","name":"title","values":[{"text":"Engineer"}]}]}`, recAlice)))
	if b.Recoverability != RecoverabilityExact || inspection.Integrity != SnapshotIntegrityVerified || !inspection.OriginMatches {
		t.Errorf("bundle %s, integrity %s, origin %v", b.Recoverability, inspection.Integrity, inspection.OriginMatches)
	}
	if len(inspection.Changes) != 1 || inspection.Changes[0].Expect == nil {
		t.Fatalf("changes %+v, want one modify with an expectation", inspection.Changes)
	}
}

func TestRecoveryReturnsAnAddToS0(t *testing.T) {
	b, inspection := roundTrip(t, parseChanges(t, fmt.Sprintf(`{"dn":%q,"type":"add","attributes":[
		{"name":"objectClass","values":[{"text":"top"},{"text":"person"},{"text":"inetOrgPerson"}]},
		{"name":"cn","values":[{"text":"Erin"}]},{"name":"sn","values":[{"text":"E"}]}]}`, recErin)))
	if b.Recoverability != RecoverabilityExact || inspection.Changes[0].Type != ChangeRequestTypeDelete {
		t.Errorf("bundle %s, changes %+v", b.Recoverability, inspection.Changes)
	}
}

func TestRecoveryReturnsARenameMoveAndBothToS0(t *testing.T) {
	cases := map[string]string{
		"rename": fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=caroline","deleteOldRdn":true}`, recCarol),
		"move": fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=carol","deleteOldRdn":true,"newSuperior":%q}`,
			recCarol, recOld),
		"rename and move, keeping the old value": fmt.Sprintf(
			`{"dn":%q,"type":"modrdn","newRdn":"uid=caroline","deleteOldRdn":false,"newSuperior":%q}`, recCarol, recOld),
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			b, _ := roundTrip(t, parseChanges(t, change))
			if b.Recoverability != RecoverabilityExact {
				t.Errorf("bundle %s", b.Recoverability)
			}
		})
	}
}

// A changeset of dependent changes is compensated in the reverse of the order
// it ran in, not sorted by DN -- and the same entry changed twice is recovered
// to the state before the first change, not the one between them.
func TestRecoveryOfASetRunsInReverse(t *testing.T) {
	_, inspection := roundTrip(t, parseChanges(t,
		fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=caroline","deleteOldRdn":true}`, recCarol),
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"title","values":[{"text":"First"}]}]}`, recAlice),
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"title","values":[{"text":"Second"}]},{"op":"delete","name":"description","values":[{"text":"first"}]}]}`, recAlice),
	))
	var order []string
	for _, c := range inspection.Changes {
		order = append(order, string(c.Type)+" "+c.Dn)
	}
	want := []string{
		"modify " + recAlice,
		"modrdn uid=caroline,ou=people,dc=alder,dc=test",
	}
	if strings.Join(order, "\n") != strings.Join(want, "\n") {
		t.Errorf("order\n%s\nwant\n%s", strings.Join(order, "\n"), strings.Join(want, "\n"))
	}
}

// An entry created and then edited is recovered by removing it, expecting it
// as it is after the edit.
//
// The forward changes are applied without a plan: a plan reads each change
// against the directory as it is, where the entry the edit needs does not
// exist yet. Recovery is still planned, which is what is under test.
func TestRecoveryOfAnAddThenAnEditRemovesTheEntry(t *testing.T) {
	rig, mem := recoveryRig(t, standardDirectory(t)...)
	s0 := mem.state(t)
	forward := fmt.Sprintf(`{"recovery":true,"changes":[
		{"dn":%q,"type":"add","attributes":[{"name":"objectClass","values":[{"text":"top"},{"text":"person"},{"text":"inetOrgPerson"}]},{"name":"cn","values":[{"text":"Erin"}]},{"name":"sn","values":[{"text":"E"}]}]},
		{"dn":%q,"type":"modify","mods":[{"op":"add","name":"mail","values":[{"text":"erin@alder.test"}]},{"op":"replace","name":"sn","values":[{"text":"Edited"}]}]}]}`,
		recErin, recErin)
	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply", strings.NewReader(forward))
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, res.Body)
	}
	result := decode[ChangesetResult](t, res)
	if result.AppliedCount != 2 {
		t.Fatalf("applied %d", result.AppliedCount)
	}

	inspection := inspect(t, rig, bundleJSON(t, result.Recovery))
	if len(inspection.Changes) != 1 || inspection.Changes[0].Type != ChangeRequestTypeDelete {
		t.Fatalf("changes %+v, want the one delete", inspection.Changes)
	}
	_, recovered := applyThroughPlan(t, rig, inspection.Changes, false)
	if recovered.AppliedCount != 1 {
		t.Fatalf("recovery applied %d: %+v", recovered.AppliedCount, recovered.Outcomes)
	}
	if got := mem.state(t); got != s0 {
		t.Errorf("not back at S0\n--- S0\n%s\n--- now\n%s", s0, got)
	}
}

// --- partial and unavailable ------------------------------------------------

func TestRecoveryOfADeleteIsPartialAndNeverRestoresAPasswordOrAnIdentity(t *testing.T) {
	rig, mem := recoveryRig(t, standardDirectory(t)...)
	_, result := applyThroughPlan(t, rig, parseChanges(t, fmt.Sprintf(`{"dn":%q,"type":"delete"}`, recBob)), true)
	raw := bundleJSON(t, result.Recovery)
	if decodeBundle(t, result.Recovery).Recoverability != RecoverabilityPartial {
		t.Errorf("recoverability %s, want partial", decodeBundle(t, result.Recovery).Recoverability)
	}
	for _, leak := range []string{oldHash, "b2xkLWJvYi1oYXNo", "bob-original-uuid", sentinelPassword, "ldap.example.test", "cn=admin", "baseline"} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Errorf("the bundle contains %q:\n%s", leak, raw)
		}
	}
	codes := map[RecoveryReasonCode]bool{}
	for _, r := range deref(decodeBundle(t, result.Recovery).Steps[0].Reasons) {
		codes[r.Code] = true
	}
	for _, want := range []RecoveryReasonCode{RecoveryReasonIdentityRegenerated, RecoveryReasonHiddenAttributesUnknown, RecoveryReasonSensitiveValuesNotRestored} {
		if !codes[want] {
			t.Errorf("reasons lack %s: %+v", want, decodeBundle(t, result.Recovery).Steps[0].Reasons)
		}
	}

	inspection := inspect(t, rig, raw)
	applyThroughPlan(t, rig, inspection.Changes, false)
	restored := mem.byDN[memKey(mustParse(t, recBob))]
	if restored == nil {
		t.Fatal("bob was not recreated")
	}
	if len(restored.Get("userPassword")) != 0 {
		t.Error("the recreated entry has a password")
	}
	if restored.GetOne("entryUUID") == "bob-original-uuid" {
		t.Error("the recreated entry claims the original identity")
	}
	if restored.GetOne("cn") != "Bob" || restored.GetOne("uid") != "bob" {
		t.Errorf("the recreated entry lost its ordinary attributes: %+v", restored.Attributes)
	}
}

func TestAPasswordChangeIsUnavailableAndNeverCarried(t *testing.T) {
	rig, _ := recoveryRig(t, standardDirectory(t)...)
	change := parseChanges(t, fmt.Sprintf(`{"dn":%q,"type":"setpassword","newPassword":%q}`, recBob, sentinelPassword))

	p, result := applyThroughPlan(t, rig, change, true)
	if p.Items[0].Recovery == nil || p.Items[0].Recovery.Recoverability != RecoverabilityUnavailable {
		t.Errorf("the plan's assessment %+v, want unavailable", p.Items[0].Recovery)
	}
	raw := bundleJSON(t, result.Recovery)
	if decodeBundle(t, result.Recovery).Recoverability != RecoverabilityUnavailable || len(decodeBundle(t, result.Recovery).Steps[0].Compensation) != 0 {
		t.Errorf("bundle %s", raw)
	}
	for _, leak := range []string{sentinelPassword, oldHash, "c2VydmVyLWhhc2hlZA"} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Errorf("the bundle contains %q", leak)
		}
	}
	// Nothing to compensate is still a bundle that says so, and inspects to
	// no changes at all.
	if inspection := inspect(t, rig, raw); len(inspection.Changes) != 0 {
		t.Errorf("an unavailable recovery produced changes: %+v", inspection.Changes)
	}
}

func TestAPlanSaysHowRecoverableEachChangeIs(t *testing.T) {
	rig, _ := recoveryRig(t, standardDirectory(t)...)
	body, _ := json.Marshal(map[string]any{"changes": parseChanges(t,
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"title","values":[{"text":"x"}]}]}`, recAlice),
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"userPassword","values":[{"text":"x"}]},{"op":"replace","name":"title","values":[{"text":"y"}]}]}`, recCarol),
		fmt.Sprintf(`{"dn":%q,"type":"delete"}`, recBob),
	)})
	p := planFor(t, rig, string(body))
	want := []RecoveryRecoverability{RecoverabilityExact, RecoverabilityPartial, RecoverabilityPartial}
	for i, item := range p.Items {
		if item.Recovery == nil || item.Recovery.Recoverability != want[i] {
			t.Errorf("item %d assessment %+v, want %s", i, item.Recovery, want[i])
		}
	}
}

// A run that stops covers only what ran, and compensates it in reverse.
func TestAPartialApplyBundlesOnlyWhatRan(t *testing.T) {
	rig, mem := recoveryRig(t, standardDirectory(t)...)
	s0 := mem.state(t)
	changes := parseChanges(t,
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"title","values":[{"text":"Lead"}]}]}`, recAlice),
		fmt.Sprintf(`{"dn":%q,"type":"add","attributes":[{"name":"objectClass","values":[{"text":"top"},{"text":"person"},{"text":"inetOrgPerson"}]},{"name":"cn","values":[{"text":"Erin"}]},{"name":"sn","values":[{"text":"E"}]}]}`, recErin),
		// Planned as a modify, refused by the directory: carol has no mail to delete.
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"delete","name":"title","values":[{"text":"absent"}]},{"op":"add","name":"title","values":[{"text":"x"}]}]}`, recCarol),
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"title","values":[{"text":"never"}]}]}`, recBob),
	)
	_, result := applyThroughPlan(t, rig, changes, true)
	if result.FailedIndex == nil || *result.FailedIndex != 2 || result.AppliedCount != 2 {
		t.Fatalf("result %+v, want a stop at change 3 after 2", result)
	}
	if result.Recovery == nil || len(decodeBundle(t, result.Recovery).Steps) != 2 ||
		decodeBundle(t, result.Recovery).Steps[0].Index != 0 || decodeBundle(t, result.Recovery).Steps[1].Index != 1 {
		t.Fatalf("the bundle covers %+v, want exactly changes 0 and 1", result.Recovery)
	}
	inspection := inspect(t, rig, bundleJSON(t, result.Recovery))
	if len(inspection.Changes) != 2 || inspection.Changes[0].Type != ChangeRequestTypeDelete || inspection.Changes[0].Dn != recErin {
		t.Fatalf("changes %+v, want erin removed first", inspection.Changes)
	}
	applyThroughPlan(t, rig, inspection.Changes, false)
	if got := mem.state(t); got != s0 {
		t.Errorf("not back at S0\n--- S0\n%s\n--- now\n%s", s0, got)
	}
}

func TestNoBundleWhenNothingRan(t *testing.T) {
	rig, _ := recoveryRig(t, standardDirectory(t)...)
	changes := parseChanges(t, fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"delete","name":"title","values":[{"text":"absent"}]},{"op":"add","name":"title","values":[{"text":"x"}]}]}`, recCarol))
	_, result := applyThroughPlan(t, rig, changes, true)
	if result.AppliedCount != 0 || result.Recovery != nil {
		t.Errorf("result %+v, want nothing applied and no bundle", result)
	}
}

func TestASingleChangeCanCarryABundle(t *testing.T) {
	rig, mem := recoveryRig(t, standardDirectory(t)...)
	s0 := mem.state(t)
	change := parseChanges(t, fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"description","values":[{"text":"only"}]}]}`, recAlice))[0]
	body, _ := json.Marshal(map[string]any{"changes": []ChangeRequest{change}})
	item := planFor(t, rig, string(body)).Items[0]

	res := rig.do(t, http.MethodPost, "/api/v1/changes/apply?recovery=true",
		strings.NewReader(mustJSON(t, withBaseline(change, *item.Baseline))))
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, res.Body)
	}
	applied := decode[ApplyResult](t, res)
	inspection := inspect(t, rig, bundleJSON(t, applied.Recovery))
	applyThroughPlan(t, rig, inspection.Changes, false)
	if got := mem.state(t); got != s0 {
		t.Errorf("not back at S0\n--- S0\n%s\n--- now\n%s", s0, got)
	}

	// And without asking, there is none.
	res = rig.do(t, http.MethodPost, "/api/v1/changes/apply",
		strings.NewReader(fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"title","values":[{"text":"t"}]}]}`, recAlice)))
	if decode[ApplyResult](t, res).Recovery != nil {
		t.Error("a bundle was returned without being asked for")
	}
}

// --- drift ------------------------------------------------------------------

func TestDriftBeforeRecoveryIsAConflictNotAnOverwrite(t *testing.T) {
	rig, mem := recoveryRig(t, standardDirectory(t)...)
	_, result := applyThroughPlan(t, rig, parseChanges(t, fmt.Sprintf(
		`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"description","values":[{"text":"applied"}]}]}`, recAlice)), true)
	raw := bundleJSON(t, result.Recovery)

	// Somebody else changes the attribute afterwards.
	mem.byDN[memKey(mustParse(t, recAlice))].Set("description", [][]byte{[]byte("somebody else's")})

	inspection := inspect(t, rig, raw)
	if inspection.Drift[0].State != RecoveryDriftDrifted || inspection.Drift[0].Problem == nil ||
		inspection.Drift[0].Problem.Code != PlanProblemExpectedStateDiffers {
		t.Fatalf("drift %+v, want drifted: expected_state_differs", inspection.Drift[0])
	}
	p, recovered := applyThroughPlan(t, rig, inspection.Changes, false)
	if p.Items[0].Action != PlanActionConflict || recovered.AppliedCount != 0 {
		t.Errorf("plan %s, applied %d: a drifted recovery was not refused", p.Items[0].Action, recovered.AppliedCount)
	}
	if got := mem.byDN[memKey(mustParse(t, recAlice))].GetOne("description"); got != "somebody else's" {
		t.Errorf("the later change was overwritten: %q", got)
	}
}

// The exhaustive expectation on a delete is checked again at apply: an
// attribute added between planning the recovery and applying it is not one
// the baseline knew about.
func TestDriftBetweenPlanningAndApplyingARecoveryIsRefused(t *testing.T) {
	rig, mem := recoveryRig(t, standardDirectory(t)...)
	_, result := applyThroughPlan(t, rig, parseChanges(t, fmt.Sprintf(`{"dn":%q,"type":"add","attributes":[
		{"name":"objectClass","values":[{"text":"top"},{"text":"person"},{"text":"inetOrgPerson"}]},
		{"name":"cn","values":[{"text":"Erin"}]},{"name":"sn","values":[{"text":"E"}]}]}`, recErin)), true)
	inspection := inspect(t, rig, bundleJSON(t, result.Recovery))

	body, _ := json.Marshal(map[string]any{"changes": inspection.Changes})
	p := planFor(t, rig, string(body))
	if p.Items[0].Action != PlanActionDelete {
		t.Fatalf("plan %s", p.Items[0].Action)
	}
	mem.byDN[memKey(mustParse(t, recErin))].Set("description", [][]byte{[]byte("work somebody started")})

	applyBody, _ := json.Marshal(map[string]any{"changes": []ChangeRequest{withBaseline(inspection.Changes[0], *p.Items[0].Baseline)}})
	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply", bytes.NewReader(applyBody))
	if res.Status != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", res.Status, res.Body)
	}
	if _, still := mem.byDN[memKey(mustParse(t, recErin))]; !still {
		t.Error("the entry was deleted despite the drift")
	}
}

// --- what is refused --------------------------------------------------------

func TestAnExpectationWithoutABaselineIsRefused(t *testing.T) {
	rig, mem := recoveryRig(t, standardDirectory(t)...)
	s0 := mem.state(t)
	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply", strings.NewReader(fmt.Sprintf(
		`{"changes":[{"dn":%q,"type":"delete","expect":{"attributes":[{"name":"cn","values":[{"text":"Carol"}]}]}}]}`, recCarol)))
	if res.Status != http.StatusBadRequest || mem.state(t) != s0 {
		t.Errorf("status %d, changed %v: %s", res.Status, mem.state(t) != s0, res.Body)
	}
}

func TestASensitiveExpectationIsRefused(t *testing.T) {
	rig, _ := recoveryRig(t, standardDirectory(t)...)
	res := rig.do(t, http.MethodPost, "/api/v1/plan", strings.NewReader(fmt.Sprintf(
		`{"changes":[{"dn":%q,"type":"delete","expect":{"attributes":[{"name":"userPassword","values":[{"text":%q}]}]}}]}`,
		recBob, oldHash)))
	if res.Status != http.StatusBadRequest || strings.Contains(res.Body, oldHash) {
		t.Errorf("status %d: %s", res.Status, res.Body)
	}
}

func TestInspectRefusesAnUntrustworthyBundle(t *testing.T) {
	rig, _ := recoveryRig(t, standardDirectory(t)...)
	_, result := applyThroughPlan(t, rig, parseChanges(t, fmt.Sprintf(
		`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"description","values":[{"text":"applied"}]}]}`, recAlice)), true)
	raw := string(bundleJSON(t, result.Recovery))

	cases := []struct {
		name, body string
		code       ErrorError
	}{
		{"tampered", strings.Replace(raw, `"applied"`, `"planted"`, 1), ErrorErrorRecoveryChecksumMismatch},
		{"another version", strings.Replace(raw, `"version":1`, `"version":2`, 1), ErrorErrorRecoveryUnsupportedVersion},
		{"an unknown field", strings.Replace(raw, `"format":"alder-recovery"`, `"format":"alder-recovery","apply":true`, 1), ErrorErrorRecoveryInvalid},
		{"a snapshot", `{"format":"alder-snapshot","version":1}`, ErrorErrorRecoveryInvalid},
		{"a password as compensation", strings.Replace(strings.Replace(raw, `"type":"modify","mods"`, `"type":"setpassword","mods"`, 1), `"checksum":`, `"x-checksum-was":`, 1), ErrorErrorRecoveryInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.body == raw {
				t.Fatal("the case did not change the bundle")
			}
			res := rig.do(t, http.MethodPost, "/api/v1/recovery/inspect", strings.NewReader(tc.body))
			if res.Status != http.StatusBadRequest {
				t.Fatalf("status %d: %s", res.Status, res.Body)
			}
			if got := decode[Error](t, res).Error; got != tc.code {
				t.Errorf("code %s, want %s", got, tc.code)
			}
		})
	}

	if res := rig.anonymous(t, http.MethodPost, "/api/v1/recovery/inspect"); res.Status != http.StatusUnauthorized {
		t.Errorf("anonymous inspect: status %d", res.Status)
	}
	if len(rig.fake.applied) != 1 {
		t.Errorf("inspecting applied something: %d writes", len(rig.fake.applied))
	}
}

func TestInspectSaysWhenTheDirectoryIsNotTheOrigin(t *testing.T) {
	rig, mem := recoveryRig(t, standardDirectory(t)...)
	_, result := applyThroughPlan(t, rig, parseChanges(t, fmt.Sprintf(
		`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"description","values":[{"text":"applied"}]}]}`, recAlice)), true)
	raw := bundleJSON(t, result.Recovery)
	mem.caps.NamingContexts = []string{"dc=example,dc=com"}
	inspection := inspect(t, rig, raw)
	if inspection.OriginMatches || inspection.OriginDifferences == nil {
		t.Errorf("origin matches %v, differences %v", inspection.OriginMatches, inspection.OriginDifferences)
	}
}

// A set of modifications of different entries derives every bundle step from
// the read the plan check already made: no entry is read twice.
func TestRecoveryOfModificationsReusesThePlanCheckReads(t *testing.T) {
	rig, mem := recoveryRig(t, standardDirectory(t)...)
	changes := parseChanges(t,
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"title","values":[{"text":"a"}]}]}`, recAlice),
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"title","values":[{"text":"b"}]}]}`, recBob),
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"title","values":[{"text":"c"}]}]}`, recCarol),
		// The same entry again: this one must be read again.
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"title","values":[{"text":"d"}]}]}`, recAlice),
	)
	body, _ := json.Marshal(map[string]any{"changes": changes})
	p := planFor(t, rig, string(body))
	send := make([]ChangeRequest, 0, len(changes))
	for _, item := range p.Items {
		send = append(send, withBaseline(changes[item.Index], *item.Baseline))
	}
	applyBody, _ := json.Marshal(map[string]any{"changes": send, "recovery": true})
	mem.readDNs = nil
	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply", bytes.NewReader(applyBody))
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, res.Body)
	}
	if len(mem.readDNs) != len(changes)+1 {
		t.Errorf("%d reads for %d changes, want one each and one more for alice's second: %v", len(mem.readDNs), len(changes), mem.readDNs)
	}
	result := decode[ChangesetResult](t, res)
	inspection := inspect(t, rig, bundleJSON(t, result.Recovery))
	if len(inspection.Changes) != 3 {
		t.Errorf("changes %+v, want alice's two merged", inspection.Changes)
	}
}
