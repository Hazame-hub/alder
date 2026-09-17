package plan

import (
	"fmt"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Checking a change against the schema before the directory does.
//
// Only rules the schema states outright. A plan that calls a change invalid
// stops it being applied, so a false positive here is worse than a miss: the
// miss is still refused by the directory, with its own result code and Alder's
// hint beside it, while the false positive silently withholds a change the
// directory would have accepted. Everything below is therefore a rule a
// directory enforces the same way on both target servers, and anything that
// depends on server behaviour -- RDN values some servers add for you, attributes
// an overlay supplies -- is left for the directory to decide.

// validate returns the first schema rule an operation breaks, or nil.
func validate(sch *schema.Schema, op directory.ChangeRecord, live *directory.Entry) *Problem {
	if sch == nil {
		return nil
	}

	classes := resultingClasses(op, live)
	// With no object classes known, which attributes are permitted and which
	// are required cannot be judged at all -- and "not known" is a real state,
	// not only a malformed record: a delegated bind the access rules forbid to
	// read objectClass sees an entry with none. Treating that as "nothing is
	// permitted" would mark every change such a session plans as invalid.
	judgeClasses := len(classes) > 0
	req := sch.Requirements(classes)
	if len(req.Unknown) > 0 {
		return &Problem{Code: ProblemObjectClassUndefined, Attribute: req.Unknown[0]}
	}

	permitted := map[string]bool{}
	for _, name := range req.Must {
		permitted[strings.ToLower(name)] = true
	}
	for _, name := range req.May {
		permitted[strings.ToLower(name)] = true
	}
	extensible := false
	for _, c := range classes {
		if strings.EqualFold(c, "extensibleObject") {
			extensible = true
		}
	}

	// check applies to one attribute and the number of values it would end
	// up holding.
	check := func(name string, count int) *Problem {
		at := sch.AttributeType(schema.BaseName(name))
		if at == nil {
			return &Problem{Code: ProblemAttributeUndefined, Attribute: name}
		}
		canonical := strings.ToLower(at.Name())
		// objectClass is permitted by top, and an operational attribute is not
		// governed by the entry's classes at all -- nsAccountLock and
		// pwdAccountLockedTime are settable and appear in no MAY list.
		if judgeClasses && canonical != "objectclass" && !sch.EffectiveUsage(at).Operational() &&
			!extensible && !permitted[canonical] {
			return &Problem{Code: ProblemAttributeNotPermitted, Attribute: at.Name()}
		}
		if count > 1 && sch.EffectiveSingleValue(at) {
			return &Problem{Code: ProblemSingleValue, Attribute: at.Name()}
		}
		return nil
	}

	switch op.Type {
	case directory.ChangeAdd:
		present := map[string]bool{}
		for _, a := range op.Attrs {
			if p := check(a.Name, len(a.Values)); p != nil {
				return p
			}
			if at := sch.AttributeType(schema.BaseName(a.Name)); at != nil {
				present[strings.ToLower(at.Name())] = true
			}
		}
		// The RDN's own attribute counts as present. Some servers add it to the
		// entry from the name when the record omits it; refusing that here
		// would be refusing something one of the target servers accepts.
		for _, name := range rdnAttributes(sch, op.DN) {
			present[name] = true
		}
		for _, must := range req.Must {
			if judgeClasses && !present[strings.ToLower(must)] {
				return &Problem{Code: ProblemMissingRequired, Attribute: must}
			}
		}
	case directory.ChangeModify:
		for name, count := range resultingCounts(op, live) {
			if p := check(name, count); p != nil {
				return p
			}
		}
	}
	return nil
}

// resultingClasses is the object classes the entry would carry after the
// operation.
func resultingClasses(op directory.ChangeRecord, live *directory.Entry) []string {
	var current [][]byte
	switch op.Type {
	case directory.ChangeAdd:
		for _, a := range op.Attrs {
			if strings.EqualFold(schema.BaseName(a.Name), "objectClass") {
				current = append(current, a.Values...)
			}
		}
	default:
		if live != nil {
			current = live.Get("objectClass")
		}
		for _, m := range op.Mods {
			if strings.EqualFold(schema.BaseName(m.Name), "objectClass") {
				current = applyMod(current, m)
			}
		}
	}
	out := make([]string, 0, len(current))
	for _, v := range current {
		out = append(out, string(v))
	}
	return out
}

// resultingCounts is how many values each attribute a modify touches would hold
// once every modification has been applied in order.
func resultingCounts(op directory.ChangeRecord, live *directory.Entry) map[string]int {
	state := map[string][][]byte{}
	names := map[string]string{}
	for _, m := range op.Mods {
		key := strings.ToLower(schema.BaseName(m.Name))
		if _, seen := state[key]; !seen {
			if live != nil {
				state[key] = live.Get(m.Name)
			} else {
				state[key] = nil
			}
			names[key] = m.Name
		}
		state[key] = applyMod(state[key], m)
	}
	out := make(map[string]int, len(state))
	for key, values := range state {
		out[names[key]] = len(values)
	}
	return out
}

// applyMod is one modification's effect on a set of values, as a directory
// applies it: byte-exact membership, order of first appearance kept.
func applyMod(current [][]byte, m directory.Mod) [][]byte {
	switch m.Op {
	case directory.ModReplace:
		return append([][]byte(nil), m.Values...)
	case directory.ModAdd:
		seen := make(map[string]bool, len(current))
		out := append([][]byte(nil), current...)
		for _, v := range current {
			seen[string(v)] = true
		}
		for _, v := range m.Values {
			if !seen[string(v)] {
				seen[string(v)] = true
				out = append(out, v)
			}
		}
		return out
	case directory.ModDelete:
		if len(m.Values) == 0 {
			return nil
		}
		drop := make(map[string]bool, len(m.Values))
		for _, v := range m.Values {
			drop[string(v)] = true
		}
		out := make([][]byte, 0, len(current))
		for _, v := range current {
			if !drop[string(v)] {
				out = append(out, v)
			}
		}
		return out
	}
	return current
}

// rdnAttributes is the canonical, folded names of the attributes in a DN's RDN.
func rdnAttributes(sch *schema.Schema, target dn.DN) []string {
	if target.IsEmpty() {
		return nil
	}
	var out []string
	for _, ava := range target.RDN() {
		name := ava.Type
		if at := sch.AttributeType(name); at != nil {
			name = at.Name()
		}
		out = append(out, strings.ToLower(name))
	}
	return out
}

// problemReason is the prose beside a code.
func problemReason(p Problem) string {
	switch p.Code {
	case ProblemObjectClassUndefined:
		return fmt.Sprintf("The schema does not define the object class %s.", p.Attribute)
	case ProblemAttributeUndefined:
		return fmt.Sprintf("The schema does not define the attribute %s.", p.Attribute)
	case ProblemAttributeNotPermitted:
		return fmt.Sprintf("No object class on this entry permits %s.", p.Attribute)
	case ProblemSingleValue:
		return fmt.Sprintf("%s is single-valued, and this change would leave it with more than one value.", p.Attribute)
	case ProblemMissingRequired:
		return fmt.Sprintf("The entry's object classes require %s, and this change does not supply it.", p.Attribute)
	}
	return "The schema does not permit this change."
}

// SchemaProblem returns the first rule of the schema an operation breaks, or
// nil. It is the same check a plan makes, for the callers that have to judge
// content against a schema without planning anything: a migration preflight
// asks whether an entry would be accepted, and must not issue a baseline to
// find out.
func SchemaProblem(sch *schema.Schema, op directory.ChangeRecord, live *directory.Entry) *Problem {
	return validate(sch, op, live)
}
