package schema

import "strings"

// ValueKind classifies an attribute value for the UI. It is the bridge between
// RFC 4517 syntaxes, of which there are dozens, and the handful of input
// controls an editor can sensibly offer.
//
// This is presentation, not validation. The server is the authority on whether
// a value is legal for its syntax, and Alder does not duplicate that judgement;
// the kind only decides which control the user is given and how a value is
// rendered when read back.
type ValueKind string

// The value kinds. Add one only when it earns a distinct input control.
const (
	KindString      ValueKind = "string"      // single-line directory string
	KindText        ValueKind = "text"        // multi-line: descriptions, postal addresses
	KindInteger     ValueKind = "integer"     //
	KindBoolean     ValueKind = "boolean"     // TRUE / FALSE
	KindDN          ValueKind = "dn"          // renders as a link to the entry
	KindTime        ValueKind = "time"        // GeneralizedTime / UTCTime
	KindBinary      ValueKind = "binary"      // shown as base64, never as text
	KindPassword    ValueKind = "password"    // masked, never logged, never echoed
	KindCertificate ValueKind = "certificate" // binary, but decodable for display
	KindImage       ValueKind = "image"       // JPEG and friends
	KindOID         ValueKind = "oid"         // links into the schema browser
)

// Syntax OIDs from RFC 4517 section 3.3 and the few vendor syntaxes both target
// servers publish. The map is the single place a syntax gets an opinion
// attached to it.
var syntaxKinds = map[string]ValueKind{
	"1.3.6.1.4.1.1466.115.121.1.1":  KindBinary,      // ACI Item
	"1.3.6.1.4.1.1466.115.121.1.3":  KindString,      // Attribute Type Description
	"1.3.6.1.4.1.1466.115.121.1.5":  KindBinary,      // Binary
	"1.3.6.1.4.1.1466.115.121.1.6":  KindString,      // Bit String
	"1.3.6.1.4.1.1466.115.121.1.7":  KindBoolean,     // Boolean
	"1.3.6.1.4.1.1466.115.121.1.8":  KindCertificate, // Certificate
	"1.3.6.1.4.1.1466.115.121.1.9":  KindCertificate, // Certificate List
	"1.3.6.1.4.1.1466.115.121.1.10": KindCertificate, // Certificate Pair
	"1.3.6.1.4.1.1466.115.121.1.11": KindString,      // Country String
	"1.3.6.1.4.1.1466.115.121.1.12": KindDN,          // DN
	"1.3.6.1.4.1.1466.115.121.1.14": KindString,      // Delivery Method
	"1.3.6.1.4.1.1466.115.121.1.15": KindString,      // Directory String
	"1.3.6.1.4.1.1466.115.121.1.16": KindString,      // DIT Content Rule Description
	"1.3.6.1.4.1.1466.115.121.1.17": KindString,      // DIT Structure Rule Description
	"1.3.6.1.4.1.1466.115.121.1.21": KindString,      // Enhanced Guide
	"1.3.6.1.4.1.1466.115.121.1.22": KindString,      // Facsimile Telephone Number
	"1.3.6.1.4.1.1466.115.121.1.23": KindBinary,      // Fax
	"1.3.6.1.4.1.1466.115.121.1.24": KindTime,        // Generalized Time
	"1.3.6.1.4.1.1466.115.121.1.25": KindString,      // Guide
	"1.3.6.1.4.1.1466.115.121.1.26": KindString,      // IA5 String
	"1.3.6.1.4.1.1466.115.121.1.27": KindInteger,     // INTEGER
	"1.3.6.1.4.1.1466.115.121.1.28": KindImage,       // JPEG
	"1.3.6.1.4.1.1466.115.121.1.30": KindString,      // Matching Rule Description
	"1.3.6.1.4.1.1466.115.121.1.31": KindString,      // Matching Rule Use Description
	"1.3.6.1.4.1.1466.115.121.1.34": KindString,      // Name And Optional UID
	"1.3.6.1.4.1.1466.115.121.1.35": KindString,      // Name Form Description
	"1.3.6.1.4.1.1466.115.121.1.36": KindString,      // Numeric String
	"1.3.6.1.4.1.1466.115.121.1.37": KindString,      // Object Class Description
	"1.3.6.1.4.1.1466.115.121.1.38": KindOID,         // OID
	"1.3.6.1.4.1.1466.115.121.1.39": KindString,      // Other Mailbox
	"1.3.6.1.4.1.1466.115.121.1.40": KindBinary,      // Octet String
	"1.3.6.1.4.1.1466.115.121.1.41": KindText,        // Postal Address
	"1.3.6.1.4.1.1466.115.121.1.44": KindString,      // Printable String
	"1.3.6.1.4.1.1466.115.121.1.45": KindString,      // Subtree Specification
	"1.3.6.1.4.1.1466.115.121.1.49": KindBinary,      // Supported Algorithm
	"1.3.6.1.4.1.1466.115.121.1.50": KindString,      // Telephone Number
	"1.3.6.1.4.1.1466.115.121.1.51": KindString,      // Teletex Terminal Identifier
	"1.3.6.1.4.1.1466.115.121.1.52": KindString,      // Telex Number
	"1.3.6.1.4.1.1466.115.121.1.53": KindTime,        // UTC Time
	"1.3.6.1.4.1.1466.115.121.1.54": KindString,      // LDAP Syntax Description
	"1.3.6.1.4.1.1466.115.121.1.58": KindString,      // Substring Assertion

	// Vendor syntaxes the harness servers publish.
	"1.3.6.1.4.1.1466.115.121.1.4":   KindBinary, // Audio
	"1.3.6.1.4.1.4203.666.11.10.2.1": KindString, // OpenLDAP CSN
	"1.3.6.1.1.16.1":                 KindString, // UUID
	"1.3.6.1.4.1.1466.115.121.1.13":  KindBinary, // Data Quality Syntax
}

// syntaxNames labels the syntaxes servers publish without a DESC. Both target
// servers do supply DESC, so this is a fallback rather than the usual path.
var syntaxNames = map[string]string{
	"1.3.6.1.4.1.1466.115.121.1.7":  "Boolean",
	"1.3.6.1.4.1.1466.115.121.1.12": "DN",
	"1.3.6.1.4.1.1466.115.121.1.15": "Directory String",
	"1.3.6.1.4.1.1466.115.121.1.24": "Generalized Time",
	"1.3.6.1.4.1.1466.115.121.1.26": "IA5 String",
	"1.3.6.1.4.1.1466.115.121.1.27": "INTEGER",
	"1.3.6.1.4.1.1466.115.121.1.38": "OID",
	"1.3.6.1.4.1.1466.115.121.1.40": "Octet String",
	"1.3.6.1.1.16.1":                "UUID",
}

// sensitiveAttrs are attribute types whose values are never rendered, never
// logged, and never returned to the browser in readable form. Rule 6 of the
// project charter is enforced here and in the logging deny list, in both places
// deliberately: one is presentation, the other is defence.
var sensitiveAttrs = map[string]bool{
	"userpassword":            true,
	"unicodepwd":              true,
	"nsslapd-rootpw":          true,
	"krbprincipalkey":         true,
	"sambantpassword":         true,
	"sambalmpassword":         true,
	"nssymmetrickey":          true,
	"nsds5replicacredentials": true,
	"pwdhistory":              true,
	"passwordhistory":         true,
}

// IsSensitive reports whether an attribute description names a secret. Options
// are ignored, so "userPassword;binary" is caught too.
func IsSensitive(attrDescription string) bool {
	return sensitiveAttrs[fold(attrDescription)]
}

// SensitiveAttributeNames returns the built-in sensitive attribute list, for
// the config layer to extend and for tests to assert against.
func SensitiveAttributeNames() []string {
	out := make([]string, 0, len(sensitiveAttrs))
	for name := range sensitiveAttrs {
		out = append(out, name)
	}
	return out
}

// SyntaxKind returns the value kind for a syntax OID, defaulting to a
// single-line string for syntaxes this catalogue does not know.
func SyntaxKind(oid string) ValueKind {
	if k, ok := syntaxKinds[strings.TrimSpace(oid)]; ok {
		return k
	}
	return KindString
}

// SyntaxLabel returns a human name for a syntax OID: the server's DESC where
// there is one, then the built-in table, then the OID itself.
func (s *Schema) SyntaxLabel(oid string) string {
	if sy := s.Syntax(oid); sy != nil && sy.Desc != "" {
		return sy.Desc
	}
	if n, ok := syntaxNames[oid]; ok {
		return n
	}
	return oid
}

// AttributeKind is the whole presentation opinion about one attribute type,
// assembled from its schema definition. The API hands one of these to the UI
// per attribute so the editor never has to reason about syntax OIDs itself.
type AttributeKind struct {
	Name        string    `json:"name"`
	OID         string    `json:"oid"`
	Desc        string    `json:"desc,omitempty"`
	Kind        ValueKind `json:"kind"`
	Syntax      string    `json:"syntax,omitempty"`
	SyntaxLabel string    `json:"syntaxLabel,omitempty"`
	// MaxLength is the advisory {len} from the syntax, or 0 for no limit.
	MaxLength int `json:"maxLength,omitempty"`
	// SingleValue, ReadOnly and Operational drive whether the editor offers an
	// "add value" control, disables the field, or files it under the
	// operational section.
	SingleValue bool `json:"singleValue"`
	ReadOnly    bool `json:"readOnly"`
	Operational bool `json:"operational"`
	Sensitive   bool `json:"sensitive"`
	Obsolete    bool `json:"obsolete,omitempty"`
	// Known is false when the attribute is not in the schema at all. The editor
	// still shows it, as a plain string, and marks it unrecognised.
	Known bool `json:"known"`
}

// KindOf assembles the presentation opinion for an attribute description.
func (s *Schema) KindOf(attrDescription string) AttributeKind {
	base := BaseName(attrDescription)
	at := s.AttributeType(base)
	if at == nil {
		return AttributeKind{
			Name:      base,
			Kind:      KindString,
			Sensitive: IsSensitive(base),
			Known:     false,
		}
	}
	syn := s.EffectiveSyntax(at)
	kind := SyntaxKind(syn)
	sensitive := IsSensitive(base)
	if sensitive {
		kind = KindPassword
	}
	// A ";binary" option means the value arrives as bytes whatever the syntax
	// says, so it is rendered as binary regardless.
	for _, opt := range Options(attrDescription) {
		if strings.EqualFold(opt, "binary") {
			kind = KindBinary
		}
	}
	usage := s.EffectiveUsage(at)
	maxLen := at.SyntaxLen
	if maxLen == 0 {
		for _, sup := range s.SuperTypes(at) {
			if sup.SyntaxLen != 0 {
				maxLen = sup.SyntaxLen
				break
			}
		}
	}
	return AttributeKind{
		Name:        at.Name(),
		OID:         at.OID,
		Desc:        at.Desc,
		Kind:        kind,
		Syntax:      syn,
		SyntaxLabel: s.SyntaxLabel(syn),
		MaxLength:   maxLen,
		SingleValue: s.EffectiveSingleValue(at),
		ReadOnly:    s.EffectiveNoUserModification(at),
		Operational: usage.Operational(),
		Sensitive:   sensitive,
		Obsolete:    at.Obsolete,
		Known:       true,
	}
}
