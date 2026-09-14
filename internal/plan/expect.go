package plan

import (
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Expectation is state a change requires before it may run: attributes that
// must hold exactly the given values, and optionally nothing else.
//
// It exists for changes derived from an earlier state rather than typed by a
// person -- a compensating change from a recovery bundle above all. Such a
// change is correct only if the entry is still as the earlier operation left
// it. Without this, "delete the value that was added, add back the value that
// was removed" would plan as an ordinary modification against an entry somebody
// has changed since, and quietly overwrite them. With it, the plan says the
// entry no longer holds what the change expects, and nothing is applied.
//
// The values are compared by each attribute's equality rule, the way the
// directory compares them. Sensitive attributes cannot be expected: their
// values are never held, so there would be nothing to compare against.
type Expectation struct {
	// Attributes must hold exactly these values. An attribute with no values
	// must be absent.
	Attributes []directory.Attribute
	// Exhaustive means the entry must hold no other user attribute. Operational
	// and identity attributes, which the server maintains, and sensitive ones,
	// which cannot be compared, are not counted.
	Exhaustive bool
}

// ProblemExpectedStateDiffers: the entry no longer holds the state the change
// was derived from.
const ProblemExpectedStateDiffers ProblemCode = "expected_state_differs"

// identityAttributes are maintained by the server for every entry.
var identityAttributes = map[string]bool{"entryuuid": true, "nsuniqueid": true}

// ReadAttributes is what checking the expectation needs read.
func (e *Expectation) ReadAttributes() []string {
	out := make([]string, 0, len(e.Attributes)+1)
	for _, a := range e.Attributes {
		out = append(out, a.Name)
	}
	if e.Exhaustive {
		out = append(out, "*")
	}
	return out
}

// dependencies are the attribute names the expectation makes the plan depend
// on, so an apply notices when any of them changes after planning.
func (e *Expectation) dependencies(sch *schema.Schema, live *directory.Entry) []string {
	names := make([]string, 0, len(e.Attributes))
	for _, a := range e.Attributes {
		names = append(names, a.Name)
	}
	if e.Exhaustive && live != nil {
		for _, name := range live.Order {
			if userAttribute(sch, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// Differs reports the first attribute where live is not what e expects. live
// must have been read with at least ReadAttributes.
func (e *Expectation) Differs(sch *schema.Schema, live *directory.Entry) (string, bool) {
	expected := map[string]bool{}
	for _, a := range e.Attributes {
		base := strings.ToLower(schema.BaseName(a.Name))
		expected[base] = true
		if !sameByRule(sch, a.Name, live.Get(a.Name), a.Values) {
			return a.Name, true
		}
	}
	if e.Exhaustive {
		names := append([]string(nil), live.Order...)
		sort.Strings(names)
		for _, name := range names {
			// objectClass is not counted: a directory adds superclasses to what
			// it was given, and an entry nobody touched would read as changed.
			if !userAttribute(sch, name) || len(live.Get(name)) == 0 || strings.EqualFold(name, "objectClass") {
				continue
			}
			if !expected[strings.ToLower(schema.BaseName(name))] {
				return name, true
			}
		}
	}
	return "", false
}

// userAttribute is an attribute an expectation can speak about: not maintained
// by the server, and not a secret.
func userAttribute(sch *schema.Schema, name string) bool {
	base := strings.ToLower(schema.BaseName(name))
	if identityAttributes[base] || schema.IsSensitive(name) {
		return false
	}
	if sch != nil {
		if at := sch.AttributeType(schema.BaseName(name)); at != nil &&
			(sch.EffectiveUsage(at).Operational() || sch.EffectiveNoUserModification(at)) {
			return false
		}
	}
	return true
}

// sameByRule compares two value sets as the attribute's equality rule does,
// and byte for byte where Alder does not model the rule.
func sameByRule(sch *schema.Schema, name string, a, b [][]byte) bool {
	keyer := snapshot.ExactKey
	if sch != nil {
		if at := sch.AttributeType(schema.BaseName(name)); at != nil {
			if k, known := snapshot.KeyerForRule(sch.EffectiveEquality(at)); known {
				keyer = k
			}
		}
	}
	keys := func(values [][]byte) map[string]bool {
		set := make(map[string]bool, len(values))
		for _, v := range values {
			set[keyer(v)] = true
		}
		return set
	}
	left, right := keys(a), keys(b)
	if len(left) != len(right) {
		return false
	}
	for k := range left {
		if !right[k] {
			return false
		}
	}
	return true
}

// mergeDependencies joins two dependency lists into one sorted list of folded
// base names, which is the form the fingerprint expects.
func mergeDependencies(a, b []string) []string {
	seen := map[string]bool{}
	for _, list := range [][]string{a, b} {
		for _, name := range list {
			seen[strings.ToLower(schema.BaseName(name))] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
