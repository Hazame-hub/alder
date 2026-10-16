package schema

import (
	"strings"
	"sync"
)

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
	// RFC 3112. A digest like userPassword, written "scheme$salt$value"
	// rather than with a {scheme} prefix, so Releasable reads it as a
	// credential and no export writes it. That is the conservative mistake.
	"authpassword": true,

	// The configuration tree's secrets.
	//
	// These were missing, and the miss was only reachable once the harness
	// grew a replicating server: an olcSyncrepl value carries the consumer's
	// bind password in the clear, inside an ordinary configuration attribute,
	// and every generic path -- the entry view, the entry's own LDIF, an LDIF
	// export -- gates on this map and this map alone. internal/config
	// withheld them from a snapshot and internal/replication strips them from
	// a report, which is exactly the shape of the bug: two features each
	// knowing a fact the shared path did not.
	//
	// olcRootPW is the OpenLDAP counterpart of nsslapd-rootpw, which was
	// here from the start.
	"olcrootpw":                   true,
	"olcsyncrepl":                 true,
	"olcdbcryptkey":               true,
	"nsds5replicabindcredentials": true,
	"nsmultiplexorcredentials":    true,
	"nsslapd-keypassword":         true,

	// The binds a proxy back end makes to the server it proxies -- back-ldap,
	// back-meta and asyncmeta -- and the one a 389 DS agreement falls back
	// to when it initialises a replica. Each holds the password in the clear:
	// inside a "credentials=" field for the two bind settings, as the whole
	// value for the other two. They were missing for the same reason
	// olcSyncrepl once was, and a recovery bundle copied them as settings.
	"olcdbidassertbind":                true,
	"olcdbaclbind":                     true,
	"olcdbaclpasswd":                   true,
	"nsds5replicabootstrapcredentials": true,
}

// sensitiveOIDs maps the numeric OID of each sensitive attribute to its name
// in sensitiveAttrs, because an attribute description may be written either
// way: "2.5.4.35: {SSHA}..." in an LDIF file is userPassword, and matching on
// names alone let it through validate and into a repository.
//
// A static table, not a schema lookup, because the paths that need it --
// validating a project offline, an export, the entry view -- must decide
// without asking a server. Each OID was checked against a published source:
//
//   - userPassword: RFC 4519 section 2.41, and both harness servers' schemas.
//   - unicodePwd: Microsoft's Active Directory schema (Unicode-Pwd).
//   - krbPrincipalKey: MIT krb5, kerberos.schema.
//   - sambaNTPassword, sambaLMPassword, nsSymmetricKey,
//     nsds5ReplicaCredentials, passwordHistory: 389 DS's published schema.
//   - pwdHistory: the password policy draft (draft-behera-ldap-password-
//     policy), as OpenLDAP's ppolicy overlay publishes it. On 389 DS the
//     name pwdHistory is an alias of passwordHistory, whose OID is listed
//     under passwordHistory.
//   - olcRootPW, olcSyncrepl: OpenLDAP's published configuration schema.
//   - authPassword: RFC 3112 section 4.4.
//
// Not listed, because no published OID could be found: nsslapd-rootpw,
// nsslapd-keypassword, nsds5ReplicaBindCredentials and
// nsMultiplexorCredentials, which 389 DS's own schema does not define, and
// olcDbCryptKey, which current OpenLDAP does not define. The proxy back ends'
// bind settings and nsDS5ReplicaBootstrapCredentials are matched by name too:
// their OIDs were not checked against a published source. They are matched by
// name, as before. A guessed OID would be worse than none: it would claim a
// coverage nobody verified.
var sensitiveOIDs = map[string]string{
	"2.5.4.35":                         "userpassword",
	"1.2.840.113556.1.4.90":            "unicodepwd",
	"2.16.840.1.113719.1.301.4.39.1":   "krbprincipalkey",
	"1.3.6.1.4.1.7165.2.1.25":          "sambantpassword",
	"1.3.6.1.4.1.7165.2.1.24":          "sambalmpassword",
	"2.16.840.1.113730.3.1.2084":       "nssymmetrickey",
	"2.16.840.1.113730.3.1.582":        "nsds5replicacredentials",
	"1.3.6.1.4.1.42.2.27.8.1.20":       "pwdhistory",
	"2.16.840.1.113730.3.1.96":         "passwordhistory",
	"1.3.6.1.4.1.4203.1.3.4":           "authpassword",
	"1.3.6.1.4.1.4203.1.12.2.3.2.0.9":  "olcrootpw",
	"1.3.6.1.4.1.4203.1.12.2.3.2.0.11": "olcsyncrepl",
}

// sensitiveName reduces an attribute description to the name the sensitive
// tables are keyed on: options dropped, case folded, and a known OID replaced
// by its name.
func sensitiveName(attrDescription string) string {
	name := fold(attrDescription)
	if byName, ok := sensitiveOIDs[name]; ok {
		return byName
	}
	return name
}

// IsSensitive reports whether an attribute description names a secret, by
// name or by OID. Options are ignored, so "userPassword;binary" and
// "2.5.4.35;binary" are caught too.
//
// A type a loaded schema declares as a subtype of a secret is one too: see
// inheritedSecrets.
func IsSensitive(attrDescription string) bool {
	name := sensitiveName(attrDescription)
	if sensitiveAttrs[name] {
		return true
	}
	_, inherited := inheritedSecrets.Load(name)
	return inherited
}

// inheritedSecrets holds the names and OIDs of attribute types that some
// schema this process has loaded declares below a sensitive type:
// "( 1.3.6.1.4.1.99999.1 NAME 'appPassword' SUP userPassword )".
//
// The tables above are keyed on names, and a subtype has a name of its own,
// so its values reached the browser, the log and every export -- the same
// hash userPassword would have withheld, one SUP away. A subtype is a
// password by the schema's own definition: a search for userPassword returns
// it, and the server applies userPassword's matching to it.
//
// Process-wide rather than per schema, because the thirty places that ask
// this question are handed an attribute name and nothing else -- a plan, an
// export, a log line -- and a fact one of them knows and the shared path does
// not is exactly how olcSyncrepl leaked before it was listed. The set only
// ever grows, and growing only withholds more, which is the failure this
// table is allowed to have: at worst a directory whose schema declares a
// standard type below userPassword has that type withheld for every session
// on this Alder until it restarts. Alder is single-tenant (section 2 of the
// charter), and a directory is already partly trusted.
//
// What it cannot do is know a subtype before a schema declaring it has been
// read, which is why a session reads its schema when it connects. Offline
// paths -- validating a project with no server -- have only the tables above.
var inheritedSecrets sync.Map

// learnSecrets records the subtypes of sensitive types this schema declares.
func (s *Schema) learnSecrets() {
	for _, at := range s.AttributeTypes {
		if s.inheritsSecret(at) {
			inheritedSecrets.Store(fold(at.OID), struct{}{})
			for _, n := range at.Names {
				inheritedSecrets.Store(fold(n), struct{}{})
			}
		}
	}
}

// inheritsSecret reports whether a type's superior chain reaches a type the
// static tables name. The type itself is not asked: if it is listed, it is
// already caught, and if it is not, its own name proves nothing.
func (s *Schema) inheritsSecret(at *AttributeType) bool {
	if at.SuperName == "" {
		return false
	}
	if sensitiveAttrs[sensitiveName(at.SuperName)] {
		return true
	}
	for _, sup := range s.SuperTypes(at) {
		if sensitiveAttrs[sensitiveName(sup.OID)] {
			return true
		}
		for _, n := range sup.Names {
			if sensitiveAttrs[sensitiveName(n)] {
				return true
			}
		}
	}
	return false
}

// neverReleased are the secrets no export writes, whatever the caller asks
// for, because their value is the credential itself rather than a digest of
// one.
//
// The distinction the export option was built on is that a password hash can
// be carried to another server and a directory rebuilt from it -- a real
// need, and a hash is not usable against anything but the account it belongs
// to. That reasoning does not survive contact with the configuration tree.
// An olcSyncrepl value carries the consumer's bind password in the clear
// inside a longer string; olcDbCryptKey is the key the database is encrypted
// with. Writing those into a file is handing over the directory, and no
// checkbox makes it the restore case.
var neverReleased = map[string]bool{
	"olcsyncrepl":                 true,
	"olcdbcryptkey":               true,
	"nsds5replicabindcredentials": true,
	"nsmultiplexorcredentials":    true,
	"nsslapd-keypassword":         true,
	"nssymmetrickey":              true,
	"nsds5replicacredentials":     true,
	"krbprincipalkey":             true,

	"olcdbidassertbind":                true,
	"olcdbaclbind":                     true,
	"olcdbaclpasswd":                   true,
	"nsds5replicabootstrapcredentials": true,
}

// Releasable reports whether an export that asked for sensitive attributes
// may write this particular value.
//
// Two gates, and the second is per value rather than per attribute because
// the attribute cannot tell you: OpenLDAP's olcRootPW holds `{SSHA}...` on
// one server and the root password in plain text on the next, and the entry
// viewer already says which -- it prints the storage scheme, or the words
// "stored in the clear", read off this same prefix.
//
// So: a value carrying an RFC 2307 {scheme} prefix is a digest and may go
// into an export. A value with no prefix is the password, and does not. That
// is the rule the viewer already shows the operator, applied where it has
// consequences.
func Releasable(attrDescription string, value []byte) bool {
	name := sensitiveName(attrDescription)
	if !sensitiveAttrs[name] {
		return true
	}
	if neverReleased[name] {
		return false
	}
	return hasSchemePrefix(value)
}

// hasSchemePrefix reports the RFC 2307 `{scheme}` marker. Deliberately the
// narrowest parse that works: up to the first closing brace and no further,
// so nothing after it can influence the answer.
func hasSchemePrefix(value []byte) bool {
	if len(value) < 3 || value[0] != '{' {
		return false
	}
	for i := 1; i < len(value); i++ {
		if value[i] == '}' {
			return i > 1
		}
	}
	return false
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
