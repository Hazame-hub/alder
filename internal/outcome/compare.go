package outcome

import (
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Comparing values the way the directory would.
//
// A directory does not compare attribute values byte for byte. caseIgnoreMatch
// makes "Platform" and "platform" the same value, and an entry that holds one
// when the change wrote the other has not been changed by anybody. Comparing
// bytes here would report that as a difference and send somebody to look at a
// change that was applied exactly as they asked.
//
// So the attribute's own equality rule decides, read from the schema, which is
// the same basis internal/plan uses to decide whether a planned change still
// matches the entry it was planned against. Without a schema the comparison
// falls back to exact bytes, which is stricter than the server and is reported
// as such rather than presented as the directory's own opinion.

// keyerFor returns the function that turns a value into its comparison key
// under the attribute's equality rule.
func keyerFor(sch *schema.Schema, name string) func([]byte) string {
	if sch == nil {
		return snapshot.ExactKey
	}
	at := sch.AttributeType(schema.BaseName(name))
	if at == nil {
		return snapshot.ExactKey
	}
	if k, known := snapshot.KeyerForRule(sch.EffectiveEquality(at)); known {
		return k
	}
	return snapshot.ExactKey
}

func keySet(keyer func([]byte) string, values [][]byte) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[keyer(v)] = true
	}
	return set
}

// sameValues reports that the attribute holds exactly the given values, as
// sets: a directory attribute is unordered, so the order it reads back in is
// not a difference.
func sameValues(sch *schema.Schema, name string, live, want [][]byte) bool {
	keyer := keyerFor(sch, name)
	l, w := keySet(keyer, live), keySet(keyer, want)
	if len(l) != len(w) {
		return false
	}
	for k := range w {
		if !l[k] {
			return false
		}
	}
	return true
}

// missingAny reports that at least one of the wanted values is absent.
//
// This is the question an "add this value" modification asks: the attribute
// may hold other values that were already there, and those are not evidence
// of anything.
func missingAny(sch *schema.Schema, name string, live, want [][]byte) bool {
	keyer := keyerFor(sch, name)
	have := keySet(keyer, live)
	for _, v := range want {
		if !have[keyer(v)] {
			return true
		}
	}
	return false
}

// presentAny reports that at least one of the named values is still there.
//
// The question a "delete this value" modification asks. A delete with no
// values removes the whole attribute, so an attribute that still holds
// anything means it did not happen.
func presentAny(sch *schema.Schema, name string, live, want [][]byte) bool {
	if len(want) == 0 {
		return len(live) > 0
	}
	keyer := keyerFor(sch, name)
	have := keySet(keyer, live)
	for _, v := range want {
		if have[keyer(v)] {
			return true
		}
	}
	return false
}
