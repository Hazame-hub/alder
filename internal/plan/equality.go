package plan

import (
	"strings"

	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Whether an entry already holds a value, asked the way the directory asks.
//
// An add of "Platform" to an entry holding "platform" in a caseIgnoreMatch
// attribute is not a change: the directory refuses it as
// attributeOrValueExists, and a changeset that planned it as a modification
// stops there. A delete of "Platform" from the same entry is the mirror image,
// and the worse one: the directory would remove "platform", and a plan that
// compares bytes calls it unchanged and leaves the value in place.
//
// So add and delete compare under the attribute's equality rule -- but only the
// rules whose meaning is beyond doubt, and that both servers implement the same
// way for these purposes:
//
//   - distinguishedNameMatch, as dn.Equal compares names;
//   - caseIgnoreMatch, caseIgnoreIA5Match and caseIgnoreListMatch: case folded,
//     leading and trailing spaces dropped and runs of spaces collapsed, the
//     insignificant-space handling of RFC 4518.
//
// Every other rule, a rule the schema does not name, an attribute the schema
// does not define, and no schema at all compare bytes, as before. That is
// never a guess: bytes can report a difference the server would not see, and
// the directory then answers, but they cannot hide one it would. The keys
// themselves are snapshot.KeyerForRule's, so a drift comparison and a plan
// cannot disagree about what "the same value" means.
//
// A replacement is not changed. "These values become those" is satisfied only
// when the bytes already match, because replacing "platform" with "Platform"
// is a correction somebody meant, and the directory accepts it.

// ruleEqualityFor names the equality rules add and delete compare under, by
// name and by OID, folded.
var ruleEqualityFor = map[string]bool{
	"distinguishednamematch":     true,
	"2.5.13.1":                   true,
	"caseignorematch":            true,
	"2.5.13.2":                   true,
	"caseignoreia5match":         true,
	"1.3.6.1.4.1.1466.109.114.2": true,
	"caseignorelistmatch":        true,
	"2.5.13.11":                  true,
}

// valueKey returns the function an attribute's values are compared by when
// asking whether an entry holds them.
func valueKey(sch *schema.Schema, name string) snapshot.Keyer {
	if sch == nil {
		return snapshot.ExactKey
	}
	at := sch.AttributeType(schema.BaseName(name))
	if at == nil {
		return snapshot.ExactKey
	}
	rule := strings.ToLower(strings.TrimSpace(sch.EffectiveEquality(at)))
	if !ruleEqualityFor[rule] {
		return snapshot.ExactKey
	}
	keyer, known := snapshot.KeyerForRule(rule)
	if !known {
		return snapshot.ExactKey
	}
	return keyer
}
