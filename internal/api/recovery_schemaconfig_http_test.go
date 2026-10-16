package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
)

// Recovery of schema and configuration changes, through the HTTP layer.
//
// The directory here stores ordered values the way a server that keeps their
// order does, because that is the property the derivation has to survive: what
// the server stores is not what it was sent, and a compensation built from what
// was sent would name values the server does not hold. The conformance suite
// runs the same round trip against OpenLDAP and 389 DS.

const (
	scConfig   = "cn=config"
	scDatabase = "olcDatabase={1}mdb,cn=config"
	scSchema   = "cn={4}alder,cn=schema,cn=config"
	scOther    = "( 1.3.6.1.4.1.99997.1.8 NAME 'alderOther' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"
	scProbe    = "( 1.3.6.1.4.1.99997.1.7 NAME 'alderProbe' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"
	scProbeOID = "1.3.6.1.4.1.99997.1.7"
)

// orderedDirectory keeps the values of the attributes it names behind their
// positions, "{n}", renumbered when one goes. A value given with a position is
// inserted there and one without is appended; a value is matched with its
// position or without one.
type orderedDirectory struct {
	*memDirectory
	ordered map[string]bool
}

func position(v []byte) (int, string) {
	s := string(v)
	if strings.HasPrefix(s, "{") {
		if end := strings.IndexByte(s, '}'); end > 0 {
			if n, err := strconv.Atoi(s[1:end]); err == nil {
				return n, s[end+1:]
			}
		}
	}
	return -1, s
}

func (o *orderedDirectory) Apply(ctx context.Context, ch directory.ChangeRecord) error {
	e, exists := o.byDN[memKey(ch.DN)]
	if ch.Type != directory.ChangeModify || !exists {
		return o.memDirectory.Apply(ctx, ch)
	}
	lists := map[string][]string{}
	var order []string
	var rest []directory.Mod
	for _, m := range ch.Mods {
		key := strings.ToLower(m.Name)
		if !o.ordered[key] {
			rest = append(rest, m)
			continue
		}
		list, seen := lists[key]
		if !seen {
			order = append(order, m.Name)
			for _, v := range e.Get(m.Name) {
				_, text := position(v)
				list = append(list, text)
			}
		}
		switch m.Op {
		case directory.ModReplace:
			list = nil
			for _, v := range m.Values {
				_, text := position(v)
				list = append(list, text)
			}
		case directory.ModAdd:
			for _, v := range m.Values {
				at, text := position(v)
				if at < 0 || at > len(list) {
					at = len(list)
				}
				list = append(list[:at], append([]string{text}, list[at:]...)...)
			}
		case directory.ModDelete:
			if len(m.Values) == 0 {
				list = nil
			}
			for _, v := range m.Values {
				at, text := position(v)
				found := -1
				for i, have := range list {
					if have == text && (at < 0 || at == i) {
						found = i
					}
				}
				if found < 0 {
					return ldapErr(16, "No Such Attribute")
				}
				list = append(list[:found], list[found+1:]...)
			}
		}
		lists[key] = list
	}
	for _, name := range order {
		values := make([][]byte, 0, len(lists[strings.ToLower(name)]))
		for i, text := range lists[strings.ToLower(name)] {
			values = append(values, []byte("{"+strconv.Itoa(i)+"}"+text))
		}
		rest = append(rest, directory.Mod{Op: directory.ModReplace, Name: name, Values: values})
	}
	ch.Mods = rest
	return o.memDirectory.Apply(ctx, ch)
}

// serverStateRig is a directory with a configuration tree and a schema collection
// in it, announced the way a server that keeps its schema in its configuration
// announces them.
func serverStateRig(t *testing.T) (*testRig, *orderedDirectory) {
	t.Helper()
	global := directory.NewEntry(mustParse(t, scConfig))
	global.Set("olcIdleTimeout", [][]byte{[]byte("0")})
	database := directory.NewEntry(mustParse(t, scDatabase))
	database.Set("olcLimits", [][]byte{[]byte("{0}dn.exact=\"cn=a\" size=1"), []byte("{1}dn.exact=\"cn=b\" size=2")})
	database.Set("olcRootPW", [][]byte{[]byte(oldHash)})
	collection := directory.NewEntry(mustParse(t, scSchema))
	collection.Set("olcAttributeTypes", [][]byte{[]byte("{0}" + scOther)})

	byDN := map[string]*directory.Entry{}
	for _, e := range []*directory.Entry{global, database, collection} {
		byDN[memKey(e.DN)] = e
	}
	caps := defaultCaps()
	caps.ConfigContext = scConfig
	caps.SchemaWrite = directory.SchemaWrite{
		Style: directory.SchemaStyleConfig, ObjectClassAttr: "olcObjectClasses", AttributeTypeAttr: "olcAttributeTypes",
		Targets: []directory.SchemaTarget{{DN: scSchema, Name: "alder"}},
	}
	// No schema: the planner does not judge which attributes the server's
	// own entries may hold, as it would not for attributes it cannot see.
	mem := &orderedDirectory{
		memDirectory: &memDirectory{fakeSession: &fakeSession{caps: caps, byDN: byDN}},
		ordered:      map[string]bool{"olclimits": true, "olcattributetypes": true, "olcobjectclasses": true},
	}

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

func TestRecoveryReturnsSettingsAndASchemaAddToS0(t *testing.T) {
	rig, mem := serverStateRig(t)
	s0 := mem.state(t)

	changes := parseChanges(t,
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"olcIdleTimeout","values":[{"text":"1800"}]}]}`, scConfig),
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"olcLimits","values":[{"text":"{0}dn.exact=\"cn=c\" size=3"}]}]}`, scDatabase),
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"olcAttributeTypes","values":[{"text":%q}]}]}`, scSchema, scProbe),
	)
	p, result := applyThroughPlan(t, rig, changes, true)
	if result.AppliedCount != len(changes) {
		t.Fatalf("applied %d of %d: %+v", result.AppliedCount, len(changes), result.Outcomes)
	}
	// What the plan said before, and what the bundle says after, agree.
	wantLevel := []RecoveryRecoverability{RecoverabilityExact, RecoverabilityExact, RecoverabilityPartial}
	for i, item := range p.Items {
		if item.Recovery == nil || item.Recovery.Recoverability != wantLevel[i] {
			t.Errorf("plan item %d: recovery %+v, want %s", i, item.Recovery, wantLevel[i])
		}
	}
	b := decodeBundle(t, result.Recovery)
	wantKind := []PlanTargetKind{PlanTargetConfig, PlanTargetConfig, PlanTargetSchema}
	for i, step := range b.Steps {
		if step.Kind != wantKind[i] || step.Recoverability != wantLevel[i] {
			t.Errorf("step %d: %s %s, want %s %s", i, step.Kind, step.Recoverability, wantKind[i], wantLevel[i])
		}
	}
	if r := b.Steps[2].Reasons; r == nil || len(*r) != 1 || (*r)[0].Code != RecoveryReasonDefinitionMayBeInUse {
		t.Errorf("schema step reasons %+v", r)
	}

	inspection := inspect(t, rig, bundleJSON(t, result.Recovery))
	for _, d := range inspection.Drift {
		if d.State != RecoveryDriftReady {
			t.Fatalf("an undisturbed directory reports drift: %+v", d)
		}
	}
	// The schema compensation names the value the server stored, position and
	// all, and expects that one definition rather than the whole collection.
	removal := inspection.Changes[0]
	if removal.Dn != scSchema || removal.Mods == nil || len(*removal.Mods) != 1 {
		t.Fatalf("schema compensation %+v", removal)
	}
	if values := (*removal.Mods)[0].Values; values == nil || len(*values) != 1 || *(*values)[0].Text != "{1}"+scProbe {
		t.Errorf("schema compensation removes %+v, want the stored value", values)
	}
	if removal.Expect == nil || removal.Expect.Definitions == nil || len(*removal.Expect.Definitions) != 1 ||
		(*removal.Expect.Definitions)[0].Oid != scProbeOID || len(removal.Expect.Attributes) != 0 {
		t.Errorf("schema expectation %+v", removal.Expect)
	}

	_, recovered := applyThroughPlan(t, rig, inspection.Changes, false)
	if recovered.AppliedCount != len(inspection.Changes) {
		t.Fatalf("recovery applied %d of %d: %+v", recovered.AppliedCount, len(inspection.Changes), recovered.Outcomes)
	}
	if got := mem.state(t); got != s0 {
		t.Errorf("not back at S0\n--- S0\n%s\n--- now\n%s", s0, got)
	}
}

func TestRecoveryOfARemovedDefinitionPutsItBackInItsPlace(t *testing.T) {
	rig, mem := serverStateRig(t)
	mem.byDN[memKey(mustParse(t, scSchema))].Set("olcAttributeTypes",
		[][]byte{[]byte("{0}" + scProbe), []byte("{1}" + scOther)})
	s0 := mem.state(t)

	_, result := applyThroughPlan(t, rig, parseChanges(t, fmt.Sprintf(
		`{"dn":%q,"type":"modify","mods":[{"op":"delete","name":"olcAttributeTypes","values":[{"text":%q}]}]}`,
		scSchema, "{0}"+scProbe)), true)
	if b := decodeBundle(t, result.Recovery); b.Recoverability != RecoverabilityExact {
		t.Errorf("bundle %s, want exact: a definition put back cannot be in use", b.Recoverability)
	}
	inspection := inspect(t, rig, bundleJSON(t, result.Recovery))
	applyThroughPlan(t, rig, inspection.Changes, false)
	// Byte for byte, positions included: it is first again.
	if got := mem.state(t); got != s0 {
		t.Errorf("not back at S0\n--- S0\n%s\n--- now\n%s", s0, got)
	}
}

func TestRecoveryOfAnEditedDefinitionIsAConflict(t *testing.T) {
	rig, mem := serverStateRig(t)
	_, result := applyThroughPlan(t, rig, parseChanges(t, fmt.Sprintf(
		`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"olcAttributeTypes","values":[{"text":%q}]}]}`, scSchema, scProbe)), true)

	// Somebody edits the definition afterwards.
	edited := strings.Replace(scProbe, "NAME 'alderProbe'", "NAME 'alderProbe' DESC 'theirs'", 1)
	mem.byDN[memKey(mustParse(t, scSchema))].Set("olcAttributeTypes",
		[][]byte{[]byte("{0}" + scOther), []byte("{1}" + edited)})

	inspection := inspect(t, rig, bundleJSON(t, result.Recovery))
	if inspection.Drift[0].State != RecoveryDriftDrifted || inspection.Drift[0].Problem == nil ||
		inspection.Drift[0].Problem.Code != PlanProblemExpectedStateDiffers {
		t.Fatalf("drift %+v, want drifted: expected_state_differs", inspection.Drift[0])
	}
	if got := mem.byDN[memKey(mustParse(t, scSchema))].Get("olcAttributeTypes"); len(got) != 2 {
		t.Errorf("the edited definition was touched: %q", got)
	}
}

func TestRecoveryOfASettingNeverCarriesASecret(t *testing.T) {
	// Until 1.39 this case also changed olcAccess and held it back with
	// access_rule_not_written. Access rules are compensated now; see
	// TestRecoveryPutsAccessRulesBackWithTheLockoutWarning.
	rig, _ := serverStateRig(t)
	_, result := applyThroughPlan(t, rig, parseChanges(t, fmt.Sprintf(
		`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"olcRootPW","values":[{"text":%q}]}]}`,
		scDatabase, sentinelPassword)), true)
	raw := bundleJSON(t, result.Recovery)
	for _, leak := range []string{sentinelPassword, oldHash} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Errorf("the bundle carries %q", leak)
		}
	}
	b := decodeBundle(t, result.Recovery)
	if b.Recoverability != RecoverabilityUnavailable || len(b.Steps[0].Compensation) != 0 {
		t.Errorf("bundle %+v, want unavailable with nothing to apply", b)
	}
}

func TestRecoveryPutsAccessRulesBackWithTheLockoutWarning(t *testing.T) {
	rig, mem := serverStateRig(t)
	mem.ordered["olcaccess"] = true
	rig.fake.caps.Config.DN = scConfig
	mem.byDN[memKey(mustParse(t, scDatabase))].Set("olcAccess", [][]byte{
		[]byte("{0}to attrs=userPassword by self write by anonymous auth by * none"),
		[]byte("{1}to * by self write by users read by * read"),
	})
	s0 := mem.state(t)

	// A rule put first, which renumbers every rule after it.
	p, result := applyThroughPlan(t, rig, parseChanges(t, fmt.Sprintf(
		`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"olcAccess","values":[{"text":"{0}to dn.exact=\"cn=probe\" by * none"}]}]}`,
		scDatabase)), true)
	if item := p.Items[0]; item.Recovery == nil || item.Recovery.Recoverability != RecoverabilityExact {
		t.Errorf("the plan says recovery %+v, want exact", item.Recovery)
	}
	if b := decodeBundle(t, result.Recovery); b.Recoverability != RecoverabilityExact || b.Steps[0].Reasons != nil {
		t.Fatalf("bundle %+v, want exact with no reasons", b)
	}

	inspection := inspect(t, rig, bundleJSON(t, result.Recovery))
	if len(inspection.Changes) != 1 || inspection.Drift[0].State != RecoveryDriftReady {
		t.Fatalf("changes %+v drift %+v", inspection.Changes, inspection.Drift)
	}

	// The compensation is planned like the change was, and carries the same
	// warning: it is the same kind of write.
	body, _ := json.Marshal(map[string]any{"changes": inspection.Changes})
	recoveryPlan := planFor(t, rig, string(body))
	if !hasWarning(p.Items[0].Preview, "olcAccess: ") {
		t.Fatalf("the forward change carries no lockout warning: %+v", p.Items[0].Preview)
	}
	if !hasWarning(recoveryPlan.Items[0].Preview, "olcAccess: ") {
		t.Errorf("the compensation carries no lockout warning: %+v", recoveryPlan.Items[0].Preview)
	}

	_, recovered := applyThroughPlan(t, rig, inspection.Changes, false)
	if recovered.AppliedCount != 1 {
		t.Fatalf("recovery applied %d: %+v", recovered.AppliedCount, recovered.Outcomes)
	}
	// Byte for byte, positions included.
	if got := mem.state(t); got != s0 {
		t.Errorf("not back at S0\n--- S0\n%s\n--- now\n%s", s0, got)
	}

	// Applied twice, the second finds what it expects gone: a conflict, not a
	// second write.
	again := inspect(t, rig, bundleJSON(t, result.Recovery))
	if again.Drift[0].State == RecoveryDriftReady {
		t.Errorf("a bundle already applied is still ready: %+v", again.Drift[0])
	}
}

func hasWarning(preview *ChangePreview, prefix string) bool {
	if preview == nil || preview.Warnings == nil {
		return false
	}
	for _, w := range *preview.Warnings {
		if strings.HasPrefix(w, prefix) {
			return true
		}
	}
	return false
}

func TestABadExpectedDefinitionIsRefused(t *testing.T) {
	rig, _ := serverStateRig(t)
	for name, def := range map[string]string{
		"an OID that is not numeric": `{"attribute":"olcAttributeTypes","oid":"alderProbe"}`,
		"a value of another OID":     fmt.Sprintf(`{"attribute":"olcAttributeTypes","oid":%q,"value":{"text":%q}}`, scProbeOID, scOther),
		"a sensitive attribute":      fmt.Sprintf(`{"attribute":"olcRootPW","oid":%q}`, scProbeOID),
	} {
		body, _ := json.Marshal(map[string]any{"changes": []json.RawMessage{json.RawMessage(fmt.Sprintf(
			`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"olcAttributeTypes","values":[{"text":%q}]}],
			  "expect":{"attributes":[],"definitions":[%s]}}`, scSchema, scProbe, def))}})
		if res := rig.do(t, http.MethodPost, "/api/v1/plan", bytes.NewReader(body)); res.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d: %s", name, res.Status, res.Body)
		}
	}
}
