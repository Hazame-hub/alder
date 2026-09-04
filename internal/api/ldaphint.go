package api

import (
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
)

// Explaining a refusal.
//
// The driver deliberately drops the server's free-text diagnostic: some servers
// name entries the caller may not be allowed to know exist, some distinguish
// "no such object" from "insufficient access" in the text while returning the
// same code for both, and some echo part of the request back. That decision
// stands. What it left behind was a result code and nothing else, and "Unwilling
// To Perform (code 53)" tells an operator nothing about what to do next.
//
// So the explanation is written here instead, from two things Alder already
// knows for certain: the standard result code, and what was being attempted.
// Nothing the server said is repeated. The result is a sentence about the
// likely cause rather than a claim about this particular failure, and it is
// phrased that way -- "usually means", not "means".

// The result codes of RFC 4511 that an operator actually meets.
const (
	rcOperationsError      = 1
	rcTimeLimitExceeded    = 3
	rcSizeLimitExceeded    = 4
	rcNoSuchAttribute      = 16
	rcUndefinedType        = 17
	rcInappropriateMatch   = 18
	rcConstraintViolation  = 19
	rcAttributeOrValueEx   = 20
	rcInvalidSyntax        = 21
	rcNoSuchObject         = 32
	rcInvalidDNSyntax      = 34
	rcInappropriateAuth    = 48
	rcInvalidCredentials   = 49
	rcInsufficientAccess   = 50
	rcBusy                 = 51
	rcUnavailable          = 52
	rcUnwillingToPerform   = 53
	rcNamingViolation      = 64
	rcObjectClassViolation = 65
	rcNotAllowedOnNonLeaf  = 66
	rcNotAllowedOnRDN      = 67
	rcEntryAlreadyExists   = 68
	rcObjectClassModsProh  = 69
	rcAffectsMultipleDSAs  = 71
)

// ldapHint returns a sentence explaining a refusal, or "" when the code speaks
// for itself.
//
// record may be the zero value, for a failure that was not a change.
func ldapHint(code uint16, record directory.ChangeRecord, caps directory.Capabilities) string {
	inConfig := caps.Config.DN != "" && withinConfigTree(record.DN.String(), caps.Config.DN)
	_, isSchemaTarget := caps.SchemaWrite.Target(record.DN.String())

	switch int(code) {
	case rcUnwillingToPerform:
		// The code that prompted this whole function. It means the server
		// understood the request perfectly well and declined, which is a
		// different situation from a malformed one and wants different advice.
		switch {
		case isSchemaTarget && record.Type == directory.ChangeDelete:
			return "Servers generally refuse to remove a schema collection while it is loaded. " +
				"Individual definitions inside it can be removed; the collection itself usually " +
				"needs the server's own configuration tooling, or a restart."
		case inConfig && record.Type == directory.ChangeDelete:
			return "Servers refuse to delete configuration entries that they are currently using. " +
				"Some parts of a running configuration can only be changed, not removed."
		case inConfig && record.Type == directory.ChangeModRDN:
			return "Configuration entries usually cannot be renamed. Where an entry's position " +
				"forms part of its name, the server owns that name."
		case inConfig:
			return "The server understood this and declined it. In a configuration tree that " +
				"usually means the value is not one it can adopt while running, rather than " +
				"that the change was malformed."
		default:
			return "The server understood this and declined it. That usually means the change " +
				"is not permitted in the server's current state, rather than that anything " +
				"about it was wrong."
		}

	case rcInsufficientAccess:
		if inConfig && !caps.Config.SeparateBind {
			return "The account this session is bound as has no rights in the configuration " +
				"tree. Connect again and supply configuration credentials, which are used only " +
				"for that tree."
		}
		return "The account this session is bound as may read this but not change it."

	case rcNoSuchObject:
		if record.Type == directory.ChangeAdd {
			return "The parent entry does not exist. Create it first — in a changeset, move the " +
				"change that creates it earlier."
		}
		return "There is no entry at that DN. It may have been removed, or renamed, since this " +
			"page was loaded."

	case rcEntryAlreadyExists:
		return "An entry already exists at that DN. To change it, edit it rather than adding it."

	case rcNotAllowedOnNonLeaf:
		return "The entry has children, and a directory removes entries one at a time from the " +
			"bottom. Delete or move what is beneath it first."

	case rcObjectClassViolation:
		return "The entry does not satisfy its object classes: an attribute they require is " +
			"missing, or one is present that they do not permit."

	case rcNamingViolation:
		return "The attribute and value in the RDN must also appear in the entry itself."

	case rcNotAllowedOnRDN:
		return "This attribute forms part of the entry's name. Rename the entry to change it."

	case rcObjectClassModsProh:
		return "A directory will not change an entry's structural object class. Create a new " +
			"entry with the class you want and move what you need across."

	case rcConstraintViolation:
		if strings.Contains(strings.ToLower(strings.Join(record.AffectedAttributes(), " ")), "password") {
			return "The server applied its password policy and refused the value — too short, " +
				"too recently used, or not complex enough by its rules."
		}
		return "A value broke a rule the server enforces beyond the schema: a length limit, a " +
			"uniqueness requirement, or a policy."

	case rcInvalidSyntax:
		return "A value does not match the syntax its attribute declares. The schema browser " +
			"shows the syntax each attribute expects."

	case rcUndefinedType:
		return "The schema defines no attribute of that name on this server. Check the spelling, " +
			"or add the attribute type to the schema first."

	case rcNoSuchAttribute:
		return "The entry does not hold the value this change removes. Someone may have changed " +
			"it since this page was loaded."

	case rcAttributeOrValueEx:
		return "The entry already holds that value. A directory keeps one copy of each."

	case rcInvalidDNSyntax:
		return "That DN is not well formed. Values containing commas, equals signs or leading " +
			"spaces must be escaped."

	case rcInvalidCredentials, rcInappropriateAuth:
		return "The bind DN and password were not accepted. A bind DN is a full DN, not a username."

	case rcSizeLimitExceeded:
		return "The server stopped before returning everything it matched. Narrow the filter or " +
			"search from a lower base."

	case rcTimeLimitExceeded:
		return "The server gave up before finishing. An unindexed attribute in the filter is the " +
			"usual cause."

	case rcBusy, rcUnavailable:
		return "The server is not accepting work at the moment. This is usually temporary."

	case rcAffectsMultipleDSAs:
		return "The change would move an entry across a boundary between servers, which a single " +
			"operation cannot do."

	case rcOperationsError, rcInappropriateMatch:
		return ""

	default:
		return ""
	}
}
