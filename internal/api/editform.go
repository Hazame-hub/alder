package api

import (
	"github.com/hazame-hub/alder/internal/schema"
)

// Turning a parsed definition back into the form a change posts.
//
// This is the inverse of objectClassFrom and attributeTypeFrom, and it exists
// because the summary beside it cannot do the job. The summary reports
// *effective* values — syntax, equality and single-valuedness resolved through
// SUP — which is what somebody reading an attribute wants to know, and exactly
// what an editor must not write back: a value inherited from a superior would
// silently become one declared here.
//
// It is also complete, which the summary is not. An editor prefilled from a
// summary carrying no SUBSTR writes a definition with no SUBSTR, the server
// accepts it, and substring search on that attribute stops working with nothing
// said. Round-tripping through the declared form is what stops an edit changing
// anything nobody asked it to.

func objectClassEditForm(oc *schema.ObjectClass) ObjectClassInput {
	in := ObjectClassInput{
		Oid:      oc.OID,
		Names:    oc.Names,
		Kind:     ptr(ObjectClassInputKind(oc.Kind.String())),
		Desc:     ptrIfSet(oc.Desc),
		Obsolete: ptrIfTrue(oc.Obsolete),
	}
	if len(oc.SuperNames) > 0 {
		in.SuperNames = ptr(oc.SuperNames)
	}
	// Only what this class declares. The inherited attributes are reported
	// separately, and writing them back here would redeclare them on the class
	// itself — a different definition that happens to behave the same until the
	// superior changes.
	if len(oc.Must) > 0 {
		in.Must = ptr(oc.Must)
	}
	if len(oc.May) > 0 {
		in.May = ptr(oc.May)
	}
	if ext := extensionsOut(oc.Extensions); ext != nil {
		in.Extensions = ext
	}
	return in
}

func attributeTypeEditForm(at *schema.AttributeType) AttributeTypeInput {
	in := AttributeTypeInput{
		Oid:                at.OID,
		Names:              at.Names,
		Desc:               ptrIfSet(at.Desc),
		Obsolete:           ptrIfTrue(at.Obsolete),
		SuperName:          ptrIfSet(at.SuperName),
		Equality:           ptrIfSet(at.Equality),
		Ordering:           ptrIfSet(at.Ordering),
		Substr:             ptrIfSet(at.Substr),
		Syntax:             ptrIfSet(at.Syntax),
		SingleValue:        ptrIfTrue(at.SingleValue),
		Collective:         ptrIfTrue(at.Collective),
		NoUserModification: ptrIfTrue(at.NoUserModification),
	}
	if at.SyntaxLen > 0 {
		in.SyntaxLen = ptr(at.SyntaxLen)
	}
	if at.Usage != schema.UsageUserApplications {
		in.Usage = ptr(AttributeTypeInputUsage(at.Usage.String()))
	}
	if ext := extensionsOut(at.Extensions); ext != nil {
		in.Extensions = ext
	}
	return in
}

// extensionsOut copies the X- extensions across, or returns nil when there are
// none so the field is absent rather than an empty object.
func extensionsOut(ext schema.Extensions) *map[string][]string {
	if len(ext) == 0 {
		return nil
	}
	out := make(map[string][]string, len(ext))
	for k, v := range ext {
		out[k] = v
	}
	return &out
}

// ptrIfTrue omits a false boolean, so a definition that says nothing about
// OBSOLETE does not come back asserting it is not obsolete.
func ptrIfTrue(b bool) *bool {
	if !b {
		return nil
	}
	return &b
}
