package api

import (
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
)

// Where to look, when the directory says no.
//
// ldapHint already writes the sentence: what a result code usually means for
// what was attempted. Since 1.19 Alder can also answer the question that
// sentence raises. "The account this session is bound as may read this but not
// change it" is true and unhelpful on its own; the access report says which
// rule does that and, on a server that will answer, what this bind may
// actually do. The same for a password refused by a policy, an entry that
// breaks its object classes, and a parent that is not there.
//
// So a refusal carries a second thing beside the sentence: which screen
// answers it, and for which entry. It is a pointer, not a diagnosis -- Alder
// is not claiming to know that the access rules are the reason, only that they
// are where somebody would look next.

// What a remedy points at.
const (
	remedyAccess = "access"
	remedyPolicy = "policy"
	remedySchema = "schema"
	remedyParent = "parent"
	// remedyConfigIdentity is not a screen: it is a different connection.
	remedyConfigIdentity = "config-identity"
)

// ldapRemedy returns where to look for a refusal, or nil when there is nowhere
// better than the sentence already given.
func ldapRemedy(code uint16, record directory.ChangeRecord, caps directory.Capabilities) *Remedy {
	target := record.DN.String()
	inConfig := caps.Config.DN != "" && withinConfigTree(target, caps.Config.DN)

	switch int(code) {
	case rcInsufficientAccess:
		if inConfig && !caps.Config.SeparateBind {
			// A screen will not help: this needs a second identity, supplied
			// at connect time.
			return &Remedy{Kind: remedyConfigIdentity,
				Label: "Reconnect with configuration credentials"}
		}
		if target == "" {
			return nil
		}
		return &Remedy{Kind: remedyAccess, Dn: ptrIfSet(target),
			Label: "See the access rules on this entry"}

	case rcConstraintViolation:
		attribute := passwordAttribute(record)
		if attribute == "" || target == "" {
			return nil
		}
		return &Remedy{Kind: remedyPolicy, Dn: ptrIfSet(target), Attribute: ptrIfSet(attribute),
			Label: "See the password policy in force here"}

	case rcObjectClassViolation, rcUndefinedType, rcInvalidSyntax, rcNoSuchAttribute:
		if target == "" {
			return nil
		}
		remedy := &Remedy{Kind: remedySchema, Dn: ptrIfSet(target),
			Label: "See what this entry's object classes require"}
		if attrs := record.AffectedAttributes(); len(attrs) == 1 {
			remedy.Attribute = ptrIfSet(attrs[0])
			remedy.Label = "See what the schema says about " + attrs[0]
		}
		return remedy

	case rcNoSuchObject:
		if record.Type != directory.ChangeAdd || target == "" {
			return nil
		}
		// The parent, from the DN type rather than by cutting a string at the
		// first comma: an RDN can contain one.
		parent := record.DN.Parent()
		if len(parent) == 0 {
			return nil
		}
		return &Remedy{Kind: remedyParent, Dn: ptrIfSet(parent.String()),
			Label: "Look at the parent this entry needs"}
	}
	return nil
}

// passwordAttribute is the password-ish attribute a change touched, if one.
//
// The name is the test, deliberately: a server that refuses a value on policy
// grounds says only "constraint violation", and the attribute is the one piece
// of evidence Alder has for which constraint it was.
func passwordAttribute(record directory.ChangeRecord) string {
	for _, name := range record.AffectedAttributes() {
		if strings.Contains(strings.ToLower(name), "password") ||
			strings.EqualFold(name, "userPassword") ||
			strings.EqualFold(name, "unicodePwd") {
			return name
		}
	}
	return ""
}
