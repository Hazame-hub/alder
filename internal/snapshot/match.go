package snapshot

import (
	"math/big"
	"strings"
	"time"

	"github.com/hazame-hub/alder/internal/dn"
)

// Keyer maps a value to the key it is compared by: two values are equal under
// an attribute's equality rule when their keys are equal.
type Keyer func(v []byte) string

// ExactKey compares bytes. It is what a value falls back to when its equality
// rule is unknown, which is the conservative choice: it can report a difference
// the server would not, never hide one it would.
func ExactKey(v []byte) string { return string(v) }

// KeyerForRule returns the key function for an equality matching rule, named or
// by OID, and whether the rule is one Alder models. The models are deliberately
// the rules whose meaning is unambiguous from RFC 4517:
//
//   - distinguishedNameMatch compares DNs, as the DN parser renders them;
//   - uniqueMemberMatch compares the DN and keeps any #UID part as written;
//   - caseIgnore rules fold case and apply RFC 4518 insignificant-space
//     handling -- leading and trailing spaces dropped, runs collapsed. This is
//     an approximation of string preparation: it does not apply Unicode
//     normalisation, so two forms of one character still differ;
//   - caseExact rules apply the same space handling without folding case;
//   - numericStringMatch ignores spaces, telephoneNumberMatch spaces and
//     hyphens;
//   - integerMatch and booleanMatch compare the number and the truth value;
//   - generalizedTimeMatch compares the instant;
//   - objectIdentifierMatch folds case, since descriptors are case-insensitive.
//     A descriptor and the numeric OID it names still differ.
//
// Any other rule, or none, returns ExactKey and false.
func KeyerForRule(rule string) (Keyer, bool) {
	switch strings.ToLower(strings.TrimSpace(rule)) {
	case "distinguishednamematch", "2.5.13.1":
		return dnKey, true
	case "uniquemembermatch", "2.5.13.23":
		return uniqueMemberKey, true
	case "caseignorematch", "2.5.13.2", "caseignoreia5match", "1.3.6.1.4.1.1466.109.114.2",
		"caseignorelistmatch", "2.5.13.11":
		return func(v []byte) string { return strings.ToLower(insignificantSpace(string(v))) }, true
	case "caseexactmatch", "2.5.13.5", "caseexactia5match", "1.3.6.1.4.1.1466.109.114.1":
		return func(v []byte) string { return insignificantSpace(string(v)) }, true
	case "numericstringmatch", "2.5.13.8":
		return func(v []byte) string { return strings.ReplaceAll(string(v), " ", "") }, true
	case "telephonenumbermatch", "2.5.13.20":
		return func(v []byte) string {
			return strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(string(v)))
		}, true
	case "integermatch", "2.5.13.14":
		return integerKey, true
	case "booleanmatch", "2.5.13.13":
		return func(v []byte) string { return strings.ToUpper(strings.TrimSpace(string(v))) }, true
	case "generalizedtimematch", "2.5.13.27":
		return timeKey, true
	case "objectidentifiermatch", "2.5.13.0":
		return func(v []byte) string { return strings.ToLower(strings.TrimSpace(string(v))) }, true
	case "octetstringmatch", "2.5.13.17":
		return ExactKey, true
	}
	return ExactKey, false
}

func insignificantSpace(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == ' ' }), " ")
}

func dnKey(v []byte) string {
	if d, err := dn.Parse(string(v)); err == nil {
		return DNKey(d)
	}
	return string(v)
}

func uniqueMemberKey(v []byte) string {
	s := string(v)
	uid := ""
	if hash := strings.LastIndex(s, "#'"); hash > 0 && strings.HasSuffix(s, "'B") {
		s, uid = s[:hash], s[hash:]
	}
	if d, err := dn.Parse(s); err == nil {
		return DNKey(d) + uid
	}
	return string(v)
}

func integerKey(v []byte) string {
	n, ok := new(big.Int).SetString(strings.TrimSpace(string(v)), 10)
	if !ok {
		return string(v)
	}
	return n.String()
}

var timeLayouts = []string{
	"20060102150405Z0700", "20060102150405.999999999Z0700",
	"200601021504Z0700", "2006010215Z0700",
}

func timeKey(v []byte) string {
	s := strings.TrimSpace(string(v))
	// A comma is an allowed decimal mark in GeneralizedTime.
	s = strings.Replace(s, ",", ".", 1)
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format("20060102150405.999999999Z")
		}
	}
	return string(v)
}
