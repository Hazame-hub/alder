// Package recovery derives compensating changes for the changes Alder applies,
// and carries them in a recovery bundle.
//
// Recovery is compensation, not rollback. LDAP has no transactions, and nothing
// here pretends otherwise: a bundle describes changes that would bring the
// entries an apply touched back to the state they were read in immediately
// before it, for the parts of that state Alder can know. Each step says how
// much that is -- exact, partial or unavailable -- and why.
//
// A bundle is never executed. It turns into ordinary change requests, which go
// through the plan like any other change: read against the directory as it is
// then, reviewed, and applied with the plan's tokens. Every compensating change
// carries the state it expects the entry to be in, so a directory somebody has
// changed since shows up as a conflict in that plan rather than being
// overwritten.
//
// Secrets are never captured. A password change cannot be recovered, and a
// deleted entry's password is not restored.
package recovery

import (
	"encoding/base64"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Recoverability is how much of a change's effect its compensation undoes.
type Recoverability string

const (
	// Exact: applied to the state the original change left, the compensation
	// returns every user attribute the change touched to its earlier values,
	// or removes an entry the change created. It still goes through a plan, and
	// is refused if the directory has moved on.
	Exact Recoverability = "exact"
	// Partial: some of the effect can be compensated and some cannot; the
	// reasons say which.
	Partial Recoverability = "partial"
	// Unavailable: nothing about the change can be compensated.
	Unavailable Recoverability = "unavailable"
)

// ReasonCode says why a change is not exactly recoverable.
type ReasonCode string

const (
	// ReasonPasswordNotCaptured: a password change. The previous password is
	// not known, and is never captured.
	ReasonPasswordNotCaptured ReasonCode = "password_not_captured"
	// ReasonSensitiveNotCaptured: a sensitive attribute was modified. Its
	// earlier values are never captured.
	ReasonSensitiveNotCaptured ReasonCode = "sensitive_value_not_captured"
	// ReasonServerOwnedAttribute: an operational or NO-USER-MODIFICATION
	// attribute, which the server maintains.
	ReasonServerOwnedAttribute ReasonCode = "server_owned_attribute"
	// ReasonIdentityRegenerated: a deleted entry recreated gets a new identity
	// (entryUUID, nsUniqueId) and new timestamps.
	ReasonIdentityRegenerated ReasonCode = "identity_regenerated"
	// ReasonHiddenAttributesUnknown: attributes the bind could not read were
	// not captured, and cannot be restored.
	ReasonHiddenAttributesUnknown ReasonCode = "hidden_attributes_unknown"
	// ReasonSensitiveNotRestored: a deleted entry held a sensitive attribute,
	// which is not restored.
	ReasonSensitiveNotRestored ReasonCode = "sensitive_values_not_restored"
	// ReasonSchemaOrConfig: a write to the schema or the server's
	// configuration, which this version does not compensate.
	ReasonSchemaOrConfig ReasonCode = "schema_or_config_not_supported"
	// ReasonPreStateUnavailable: the entry could not be read before the
	// change, so there is nothing to derive a compensation from.
	ReasonPreStateUnavailable ReasonCode = "pre_state_unavailable"
)

var knownReasons = map[ReasonCode]bool{
	ReasonPasswordNotCaptured: true, ReasonSensitiveNotCaptured: true, ReasonServerOwnedAttribute: true,
	ReasonIdentityRegenerated: true, ReasonHiddenAttributesUnknown: true, ReasonSensitiveNotRestored: true,
	ReasonSchemaOrConfig: true, ReasonPreStateUnavailable: true,
}

// Reason is one limitation of a step.
type Reason struct {
	Code      ReasonCode `json:"code"`
	Attribute string     `json:"attribute,omitempty"`
}

// Kinds of target, as the plan classifies them.
const (
	KindData   = "data"
	KindSchema = "schema"
	KindConfig = "config"
)

// Value is one attribute value: exactly one of Text or Base64.
type Value = snapshot.Value

// Attribute is an attribute with values.
type Attribute struct {
	Name   string  `json:"name"`
	Values []Value `json:"values"`
}

// Mod is one modification of a compensating change.
type Mod struct {
	Op     string  `json:"op"`
	Name   string  `json:"name"`
	Values []Value `json:"values,omitempty"`
}

// Expect is the state a compensating change requires: what the original change
// left behind.
type Expect struct {
	Attributes []Attribute `json:"attributes"`
	Exhaustive bool        `json:"exhaustive,omitempty"`
}

// Change is one compensating change. Its types are the canonical operations a
// plan takes, less the password change, which can never be a compensation.
type Change struct {
	DN           string      `json:"dn"`
	Type         string      `json:"type"`
	Mods         []Mod       `json:"mods,omitempty"`
	Attributes   []Attribute `json:"attributes,omitempty"`
	NewRDN       string      `json:"newRdn,omitempty"`
	DeleteOldRDN *bool       `json:"deleteOldRdn,omitempty"`
	NewSuperior  string      `json:"newSuperior,omitempty"`
	Expect       *Expect     `json:"expect,omitempty"`
}

// Original describes the change that was applied, without its values.
type Original struct {
	Type       string   `json:"type"`
	DN         string   `json:"dn"`
	TargetDN   string   `json:"targetDn,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
}

// Step is the recovery of one applied change.
type Step struct {
	// Index is the change's position in the set that was applied.
	Index          int            `json:"index"`
	Original       Original       `json:"original"`
	Kind           string         `json:"kind"`
	Recoverability Recoverability `json:"recoverability"`
	Reasons        []Reason       `json:"reasons,omitempty"`
	// Compensation runs in the order given, and after the compensation of every
	// later step.
	Compensation []Change `json:"compensation"`
}

// Derive works out the compensation of one change from the entry as it was
// immediately before the change ran.
//
// pre is that entry, or nil when it did not exist. For a modification it must
// hold at least the attributes the change touches; for a delete or a rename,
// every user attribute the bind can read. kind is the target kind the plan
// classified the change as.
func Derive(index int, record directory.ChangeRecord, pre *directory.Entry, sch *schema.Schema, kind string) Step {
	step := Step{Index: index, Original: original(record), Kind: kind, Compensation: []Change{}}
	unavailable := func(code ReasonCode, attribute string) Step {
		step.Recoverability = Unavailable
		step.Reasons = append(step.Reasons, Reason{Code: code, Attribute: attribute})
		step.Compensation = []Change{}
		return step
	}

	if kind == KindSchema || kind == KindConfig {
		return unavailable(ReasonSchemaOrConfig, "")
	}
	switch record.Type {
	case directory.ChangeSetPassword:
		return unavailable(ReasonPasswordNotCaptured, "")
	case directory.ChangeAdd:
		if pre != nil {
			return unavailable(ReasonPreStateUnavailable, "")
		}
		return deriveAdd(step, record, sch)
	case directory.ChangeModify:
		if pre == nil {
			return unavailable(ReasonPreStateUnavailable, "")
		}
		return deriveModify(step, record, pre, sch)
	case directory.ChangeDelete:
		if pre == nil {
			return unavailable(ReasonPreStateUnavailable, "")
		}
		return deriveDelete(step, record, pre, sch)
	case directory.ChangeModRDN:
		if pre == nil {
			return unavailable(ReasonPreStateUnavailable, "")
		}
		return deriveRename(step, record, pre)
	}
	return unavailable(ReasonPreStateUnavailable, "")
}

func original(record directory.ChangeRecord) Original {
	o := Original{Type: string(record.Type), DN: record.DN.String()}
	if record.Type == directory.ChangeModRDN {
		if target, err := record.Target(); err == nil {
			o.TargetDN = target.String()
		}
	}
	o.Attributes = record.AffectedAttributes()
	return o
}

// deriveAdd: an entry that did not exist is deleted, if it is still what the
// add created.
func deriveAdd(step Step, record directory.ChangeRecord, sch *schema.Schema) Step {
	expect := &Expect{Exhaustive: true}
	seen := map[string]bool{}
	for _, a := range record.Attrs {
		base := strings.ToLower(schema.BaseName(a.Name))
		if seen[base] || strings.EqualFold(a.Name, "objectClass") || !comparable(sch, a.Name) {
			continue
		}
		seen[base] = true
		expect.Attributes = append(expect.Attributes, Attribute{Name: a.Name, Values: encode(a.Values)})
	}
	// The naming attribute is stored even when the add did not list it.
	for _, ava := range record.DN.RDN() {
		base := strings.ToLower(schema.BaseName(ava.Type))
		if !seen[base] && comparable(sch, ava.Type) {
			seen[base] = true
			expect.Attributes = append(expect.Attributes, Attribute{Name: ava.Type, Values: encode([][]byte{[]byte(ava.Value)})})
		}
	}
	step.Recoverability = Exact
	step.Compensation = []Change{{DN: record.DN.String(), Type: string(directory.ChangeDelete), Expect: expect}}
	return step
}

// deriveModify: for each attribute the change touched, the values it would
// have after the modifications are worked out from the values it had, by the
// attribute's equality rule, and the compensation removes what was added and
// restores what was removed -- value by value, so the entry must still hold
// what the change left for it to apply.
func deriveModify(step Step, record directory.ChangeRecord, pre *directory.Entry, sch *schema.Schema) Step {
	type touched struct {
		name string
		mods []directory.Mod
	}
	var order []string
	byName := map[string]*touched{}
	for _, m := range record.Mods {
		base := strings.ToLower(schema.BaseName(m.Name))
		t, ok := byName[base]
		if !ok {
			t = &touched{name: m.Name}
			byName[base] = t
			order = append(order, base)
		}
		t.mods = append(t.mods, m)
	}

	change := Change{DN: record.DN.String(), Type: string(directory.ChangeModify), Expect: &Expect{}}
	skipped := 0
	for _, base := range order {
		t := byName[base]
		if schema.IsSensitive(t.name) {
			step.Reasons = append(step.Reasons, Reason{Code: ReasonSensitiveNotCaptured, Attribute: t.name})
			skipped++
			continue
		}
		if serverOwned(sch, t.name) {
			step.Reasons = append(step.Reasons, Reason{Code: ReasonServerOwnedAttribute, Attribute: t.name})
			skipped++
			continue
		}
		keyer := keyerFor(sch, t.name)
		before := pre.Get(t.name)
		after := applyMods(before, t.mods, keyer)
		if sameSet(before, after, keyer) {
			if !sameSet(before, after, snapshot.ExactKey) {
				// Equal by the rule, stored differently: "Alice" replaced by
				// "alice". Put the earlier form back as it was written.
				change.Mods = append(change.Mods, Mod{Op: string(directory.ModReplace), Name: t.name, Values: encode(before)})
				change.Expect.Attributes = append(change.Expect.Attributes, Attribute{Name: t.name, Values: encode(after)})
			}
			continue
		}
		added, removed := difference(after, before, keyer), difference(before, after, keyer)
		switch {
		case len(before) == 0:
			change.Mods = append(change.Mods, Mod{Op: string(directory.ModDelete), Name: t.name, Values: encode(after)})
		case len(after) == 0:
			change.Mods = append(change.Mods, Mod{Op: string(directory.ModAdd), Name: t.name, Values: encode(before)})
		default:
			if len(added) > 0 {
				change.Mods = append(change.Mods, Mod{Op: string(directory.ModDelete), Name: t.name, Values: encode(added)})
			}
			if len(removed) > 0 {
				change.Mods = append(change.Mods, Mod{Op: string(directory.ModAdd), Name: t.name, Values: encode(removed)})
			}
		}
		change.Expect.Attributes = append(change.Expect.Attributes, Attribute{Name: t.name, Values: encode(after)})
	}

	switch {
	case skipped == len(order):
		step.Recoverability = Unavailable
	case skipped > 0:
		step.Recoverability = Partial
	default:
		step.Recoverability = Exact
	}
	if len(change.Mods) > 0 {
		step.Compensation = []Change{change}
	}
	return step
}

// deriveDelete: the entry is added back from what was read. That is never the
// same entry: its identity and timestamps are new, and anything the bind could
// not read, or Alder would not hold, is missing.
func deriveDelete(step Step, record directory.ChangeRecord, pre *directory.Entry, sch *schema.Schema) Step {
	change := Change{DN: record.DN.String(), Type: string(directory.ChangeAdd)}
	step.Reasons = append(step.Reasons, Reason{Code: ReasonIdentityRegenerated}, Reason{Code: ReasonHiddenAttributesUnknown})
	names := append([]string(nil), pre.Order...)
	for _, name := range names {
		values := pre.Get(name)
		if len(values) == 0 {
			continue
		}
		base := strings.ToLower(schema.BaseName(name))
		switch {
		case schema.IsSensitive(name):
			step.Reasons = append(step.Reasons, Reason{Code: ReasonSensitiveNotRestored, Attribute: name})
			continue
		case identity[base] || serverOwned(sch, name):
			continue
		}
		change.Attributes = append(change.Attributes, Attribute{Name: name, Values: encode(values)})
	}
	step.Recoverability = Partial
	step.Compensation = []Change{change}
	return step
}

// deriveRename: the entry is moved back to its old name and parent. The new
// naming value is removed on the way back only if the entry did not hold it
// before the rename.
func deriveRename(step Step, record directory.ChangeRecord, pre *directory.Entry) Step {
	target, err := record.Target()
	if err != nil {
		step.Recoverability = Unavailable
		step.Reasons = append(step.Reasons, Reason{Code: ReasonPreStateUnavailable})
		return step
	}
	newRDN := target.RDN()
	heldNew := true
	for _, ava := range newRDN {
		if !holds(pre.Get(ava.Type), ava.Value) {
			heldNew = false
		}
	}
	deleteNew := !heldNew
	change := Change{
		DN:           target.String(),
		Type:         string(directory.ChangeModRDN),
		NewRDN:       record.DN.RDN().String(),
		DeleteOldRDN: &deleteNew,
	}
	if !target.Parent().Equal(record.DN.Parent()) {
		change.NewSuperior = record.DN.Parent().String()
	}
	step.Recoverability = Exact
	step.Compensation = []Change{change}
	return step
}

var identity = map[string]bool{"entryuuid": true, "nsuniqueid": true}

func holds(values [][]byte, want string) bool {
	for _, v := range values {
		if strings.EqualFold(string(v), want) {
			return true
		}
	}
	return false
}

// comparable is an attribute an expectation can state: not a secret, and not
// maintained by the server.
func comparable(sch *schema.Schema, name string) bool {
	base := strings.ToLower(schema.BaseName(name))
	return !schema.IsSensitive(name) && !identity[base] && !serverOwned(sch, name)
}

func serverOwned(sch *schema.Schema, name string) bool {
	if sch == nil {
		return false
	}
	at := sch.AttributeType(schema.BaseName(name))
	return at != nil && (sch.EffectiveUsage(at).Operational() || sch.EffectiveNoUserModification(at))
}

func keyerFor(sch *schema.Schema, name string) snapshot.Keyer {
	if sch != nil {
		if at := sch.AttributeType(schema.BaseName(name)); at != nil {
			if k, known := snapshot.KeyerForRule(sch.EffectiveEquality(at)); known {
				return k
			}
		}
	}
	return snapshot.ExactKey
}

// applyMods is what an attribute holds after modifications, as LDAP applies
// them: in order, values compared by the attribute's equality rule.
func applyMods(before [][]byte, mods []directory.Mod, keyer snapshot.Keyer) [][]byte {
	current := append([][]byte(nil), before...)
	for _, m := range mods {
		switch m.Op {
		case directory.ModReplace:
			current = dedupe(m.Values, keyer)
		case directory.ModAdd:
			current = dedupe(append(current, m.Values...), keyer)
		case directory.ModDelete:
			if len(m.Values) == 0 {
				current = nil
				continue
			}
			current = difference(current, m.Values, keyer)
		}
	}
	return current
}

func dedupe(values [][]byte, keyer snapshot.Keyer) [][]byte {
	seen := map[string]bool{}
	out := make([][]byte, 0, len(values))
	for _, v := range values {
		k := keyer(v)
		if !seen[k] {
			seen[k] = true
			out = append(out, v)
		}
	}
	return out
}

// difference is the values of a that b does not hold.
func difference(a, b [][]byte, keyer snapshot.Keyer) [][]byte {
	have := map[string]bool{}
	for _, v := range b {
		have[keyer(v)] = true
	}
	var out [][]byte
	for _, v := range a {
		if !have[keyer(v)] {
			out = append(out, v)
		}
	}
	return out
}

func sameSet(a, b [][]byte, keyer snapshot.Keyer) bool {
	return len(difference(a, b, keyer)) == 0 && len(difference(b, a, keyer)) == 0
}

func encode(values [][]byte) []Value {
	out := make([]Value, 0, len(values))
	for _, v := range values {
		if utf8.Valid(v) && !controlBytes(v) {
			text := string(v)
			out = append(out, Value{Text: &text})
			continue
		}
		b := base64.StdEncoding.EncodeToString(v)
		out = append(out, Value{Base64: &b})
	}
	return out
}

func controlBytes(v []byte) bool {
	for _, c := range v {
		if c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// Overall is the recoverability of a set of steps.
func Overall(steps []Step) Recoverability {
	if len(steps) == 0 {
		return Unavailable
	}
	exact, unavailable := 0, 0
	for _, s := range steps {
		switch s.Recoverability {
		case Exact:
			exact++
		case Unavailable:
			unavailable++
		}
	}
	switch {
	case exact == len(steps):
		return Exact
	case unavailable == len(steps):
		return Unavailable
	}
	return Partial
}

// ReadAttributes is what Derive needs read of an entry before a change.
func ReadAttributes(record directory.ChangeRecord) []string {
	switch record.Type {
	case directory.ChangeDelete:
		return []string{"*"}
	case directory.ChangeModRDN:
		// Whether the entry already held its new naming values.
		out := []string{"objectClass"}
		if target, err := record.Target(); err == nil {
			for _, ava := range target.RDN() {
				out = append(out, ava.Type)
			}
		}
		return out
	case directory.ChangeModify:
		seen := map[string]bool{}
		out := []string{"objectClass"}
		for _, m := range record.Mods {
			base := strings.ToLower(schema.BaseName(m.Name))
			if !seen[base] {
				seen[base] = true
				out = append(out, m.Name)
			}
		}
		return out
	}
	return []string{"objectClass"}
}

// Covers reports whether an entry read with got holds everything reading with
// want would: a read of "*" covers any list of user attributes.
func Covers(got, want []string) bool {
	have := map[string]bool{}
	for _, name := range got {
		have[strings.ToLower(schema.BaseName(name))] = true
	}
	if have["*"] {
		return true
	}
	for _, name := range want {
		if !have[strings.ToLower(schema.BaseName(name))] {
			return false
		}
	}
	return true
}

// Touched is every DN a change reads or writes, so a cache of pre-states can
// forget what a change makes stale.
func Touched(record directory.ChangeRecord) []dn.DN {
	out := []dn.DN{record.DN}
	if record.Type == directory.ChangeModRDN {
		if target, err := record.Target(); err == nil {
			out = append(out, target)
		}
	}
	return out
}

func sortedReasons(reasons []Reason) []Reason {
	out := append([]Reason(nil), reasons...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Assess is how recoverable a change would be, before it runs: what a plan
// shows beside the change. It needs no values, and for a delete it cannot yet
// say whether the entry holds a secret -- the bundle made at apply does.
func Assess(record directory.ChangeRecord, sch *schema.Schema, kind string) (Recoverability, []Reason) {
	pre := directory.NewEntry(record.DN)
	if record.Type == directory.ChangeAdd {
		pre = nil
	}
	step := Derive(0, record, pre, sch, kind)
	return step.Recoverability, sortedReasons(step.Reasons)
}
