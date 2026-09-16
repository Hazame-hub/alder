package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Schema snapshots.
//
// A schema snapshot is the same format as a data snapshot -- "alder-snapshot",
// version 1 -- with kind "schema" and a field set of its own. Version 1 was
// written to admit new kinds: a reader refuses a kind it does not know, and
// each kind refuses fields it does not know, so neither can be mistaken for
// the other. Releases before 1.10 refuse a schema snapshot as an unsupported
// kind.
//
// What it holds is the published subschema, parsed:
//
//   - attribute types and object classes, compared semantically and the only
//     definitions a comparison proposes changes for, because they are the only
//     ones Alder can change;
//   - LDAP syntaxes, matching rules, matching rule uses, DIT content rules and
//     name forms, kept as context -- what a definition refers to -- and never
//     compared or changed;
//   - every definition that could not be parsed, verbatim, which makes the
//     snapshot partial rather than silently smaller.
//
// DIT structure rules are not captured: Alder has never read them.
//
// Each definition keeps the server's own text beside its parsed form, and the
// parsed form is not trusted on reading: it is derived from the text again and
// must match. It holds no server configuration, no credential and no address.

// KindSchema is the kind of a schema snapshot.
const KindSchema = "schema"

// Completeness of a schema snapshot.
const (
	// SchemaComplete means every published definition was parsed.
	SchemaComplete = "complete"
	// SchemaPartial means some were not, and are listed in unparsed.
	SchemaPartial = "partial"
)

// MaxSchemaDefinitions bounds a schema snapshot: the definitions of every
// kind together, parsed or not. A real server publishes a few thousand.
const MaxSchemaDefinitions = 50000

// maxDefinitionBytes bounds one definition's text.
const maxDefinitionBytes = 64 << 10

// The element kinds a schema snapshot captures, by the subschema attribute
// that publishes them.
var (
	SchemaCompared    = []string{schema.AttrAttributeTypes, schema.AttrObjectClasses}
	SchemaContextKind = []string{schema.AttrLDAPSyntaxes, schema.AttrMatchingRules, schema.AttrMatchingRuleUse,
		schema.AttrDITContentRules, schema.AttrNameForms}
	SchemaNotCaptured = []string{schema.AttrDITStructureRules}
)

// SchemaSource is where a schema snapshot was captured.
type SchemaSource struct {
	Vendor        string `json:"vendor,omitempty"`
	VendorVersion string `json:"vendorVersion,omitempty"`
	// SubschemaEntry is the DN the server published its schema at.
	SubschemaEntry string `json:"subschemaEntry"`
	// Collections reports that the server keeps its schema in configuration
	// collections and the capturing session could read them, so each
	// definition records the collection holding it. Without it, no definition
	// carries one, which says nothing about where they came from.
	Collections bool `json:"collections"`
}

// SchemaCoverage names what the snapshot captured, and how.
type SchemaCoverage struct {
	Compared    []string `json:"compared"`
	Context     []string `json:"context"`
	NotCaptured []string `json:"notCaptured"`
}

// SchemaCounts counts what the snapshot holds.
type SchemaCounts struct {
	AttributeTypes  int `json:"attributeTypes"`
	ObjectClasses   int `json:"objectClasses"`
	LDAPSyntaxes    int `json:"ldapSyntaxes"`
	MatchingRules   int `json:"matchingRules"`
	MatchingRuleUse int `json:"matchingRuleUse"`
	DITContentRules int `json:"ditContentRules"`
	NameForms       int `json:"nameForms"`
	Unparsed        int `json:"unparsed"`
}

// SchemaAttributeType is an attribute type as a schema snapshot holds it.
//
// Every field but Collection is the parsed Definition, canonicalised; see
// canonicalAttributeType.
type SchemaAttributeType struct {
	OID                string              `json:"oid"`
	Names              []string            `json:"names,omitempty"`
	Desc               string              `json:"desc,omitempty"`
	Obsolete           bool                `json:"obsolete,omitempty"`
	Sup                string              `json:"sup,omitempty"`
	Equality           string              `json:"equality,omitempty"`
	Ordering           string              `json:"ordering,omitempty"`
	Substr             string              `json:"substr,omitempty"`
	Syntax             string              `json:"syntax,omitempty"`
	SyntaxLength       int                 `json:"syntaxLength,omitempty"`
	SingleValue        bool                `json:"singleValue,omitempty"`
	Collective         bool                `json:"collective,omitempty"`
	NoUserModification bool                `json:"noUserModification,omitempty"`
	Usage              string              `json:"usage"`
	Extensions         map[string][]string `json:"extensions,omitempty"`
	Unrecognized       []string            `json:"unrecognized,omitempty"`
	// Collection is the configuration collection holding the definition, when
	// the server keeps schema in collections and they were readable.
	Collection string `json:"collection,omitempty"`
	// Definition is the text the server published.
	Definition string `json:"definition"`
}

// SchemaObjectClass is an object class as a schema snapshot holds it.
type SchemaObjectClass struct {
	OID          string              `json:"oid"`
	Names        []string            `json:"names,omitempty"`
	Desc         string              `json:"desc,omitempty"`
	Obsolete     bool                `json:"obsolete,omitempty"`
	Sup          []string            `json:"sup,omitempty"`
	Kind         string              `json:"kind"`
	Must         []string            `json:"must,omitempty"`
	May          []string            `json:"may,omitempty"`
	Extensions   map[string][]string `json:"extensions,omitempty"`
	Unrecognized []string            `json:"unrecognized,omitempty"`
	Collection   string              `json:"collection,omitempty"`
	Definition   string              `json:"definition"`
}

// SchemaContextElement is a definition kept for reference only.
type SchemaContextElement struct {
	OID        string   `json:"oid"`
	Names      []string `json:"names,omitempty"`
	Desc       string   `json:"desc,omitempty"`
	Syntax     string   `json:"syntax,omitempty"`
	Definition string   `json:"definition"`
}

// SchemaContext holds the definitions compared definitions refer to.
type SchemaContext struct {
	LDAPSyntaxes    []SchemaContextElement `json:"ldapSyntaxes"`
	MatchingRules   []SchemaContextElement `json:"matchingRules"`
	MatchingRuleUse []SchemaContextElement `json:"matchingRuleUse"`
	DITContentRules []SchemaContextElement `json:"ditContentRules"`
	NameForms       []SchemaContextElement `json:"nameForms"`
}

// SchemaUnparsed is a published definition that could not be parsed, or that
// repeats an OID already captured.
type SchemaUnparsed struct {
	// Attribute is the subschema attribute that published it.
	Attribute  string `json:"attribute"`
	Definition string `json:"definition"`
	Error      string `json:"error"`
}

// SchemaSnapshot is a schema snapshot document.
type SchemaSnapshot struct {
	Format         string                `json:"format"`
	Version        int                   `json:"version"`
	Kind           string                `json:"kind"`
	CreatedAt      string                `json:"createdAt"`
	Source         SchemaSource          `json:"source"`
	Completeness   string                `json:"completeness"`
	Coverage       SchemaCoverage        `json:"coverage"`
	Counts         SchemaCounts          `json:"counts"`
	AttributeTypes []SchemaAttributeType `json:"attributeTypes"`
	ObjectClasses  []SchemaObjectClass   `json:"objectClasses"`
	Context        SchemaContext         `json:"context"`
	Unparsed       []SchemaUnparsed      `json:"unparsed"`
	// Checksum is "sha256:<hex>" over everything but CreatedAt and Checksum,
	// with the same meaning as a data snapshot's.
	Checksum string `json:"checksum,omitempty"`
}

// SchemaCapture describes a schema capture for BuildSchema.
type SchemaCapture struct {
	Vendor         string
	VendorVersion  string
	SubschemaEntry string
	// Collections maps a definition's OID to the configuration collection
	// holding it, when the server has collections and they were readable; nil
	// otherwise.
	Collections map[string]string
	CreatedAt   time.Time
}

// BuildSchema turns a parsed subschema into a canonical schema snapshot.
func BuildSchema(c SchemaCapture, sch *schema.Schema) (*SchemaSnapshot, error) {
	if sch == nil {
		return nil, &Error{Code: CodeInvalid, Detail: "there is no schema to capture"}
	}
	total := len(sch.AttributeTypes) + len(sch.ObjectClasses) + len(sch.Syntaxes) + len(sch.MatchingRules) +
		len(sch.MatchingRuleUses) + len(sch.DITContentRules) + len(sch.NameForms) + len(sch.Errors)
	if total > MaxSchemaDefinitions {
		return nil, &Error{Code: CodeTooLarge,
			Detail: fmt.Sprintf("the schema publishes %d definitions, and a schema snapshot holds at most %d", total, MaxSchemaDefinitions)}
	}
	s := &SchemaSnapshot{
		Format: Format, Version: Version, Kind: KindSchema, CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		Source: SchemaSource{Vendor: c.Vendor, VendorVersion: c.VendorVersion, SubschemaEntry: c.SubschemaEntry,
			Collections: c.Collections != nil},
		Coverage:       schemaCoverage(),
		AttributeTypes: []SchemaAttributeType{},
		ObjectClasses:  []SchemaObjectClass{},
		Context: SchemaContext{LDAPSyntaxes: []SchemaContextElement{}, MatchingRules: []SchemaContextElement{},
			MatchingRuleUse: []SchemaContextElement{}, DITContentRules: []SchemaContextElement{}, NameForms: []SchemaContextElement{}},
		Unparsed: []SchemaUnparsed{},
	}
	for _, e := range sch.Errors {
		s.Unparsed = append(s.Unparsed, SchemaUnparsed{Attribute: e.Attribute, Definition: strings.TrimSpace(e.Definition), Error: errorText(e.Err)})
	}

	// A repeated OID is not a second definition of the same thing Alder could
	// choose between; it is an ambiguity, and it is recorded as one.
	seenAT := map[string]bool{}
	for _, at := range sch.AttributeTypes {
		key := strings.ToLower(at.OID)
		if seenAT[key] {
			s.Unparsed = append(s.Unparsed, SchemaUnparsed{Attribute: schema.AttrAttributeTypes, Definition: at.Raw,
				Error: "another attribute type already has OID " + at.OID})
			continue
		}
		seenAT[key] = true
		s.AttributeTypes = append(s.AttributeTypes, canonicalAttributeType(at, c.Collections[at.OID]))
	}
	seenOC := map[string]bool{}
	for _, oc := range sch.ObjectClasses {
		key := strings.ToLower(oc.OID)
		if seenOC[key] {
			s.Unparsed = append(s.Unparsed, SchemaUnparsed{Attribute: schema.AttrObjectClasses, Definition: oc.Raw,
				Error: "another object class already has OID " + oc.OID})
			continue
		}
		seenOC[key] = true
		s.ObjectClasses = append(s.ObjectClasses, canonicalObjectClass(oc, c.Collections[oc.OID]))
	}
	for _, sy := range sch.Syntaxes {
		s.Context.LDAPSyntaxes = append(s.Context.LDAPSyntaxes, SchemaContextElement{OID: sy.OID, Desc: sy.Desc, Definition: sy.Raw})
	}
	for _, mr := range sch.MatchingRules {
		s.Context.MatchingRules = append(s.Context.MatchingRules,
			SchemaContextElement{OID: mr.OID, Names: nonEmpty(mr.Names), Desc: mr.Desc, Syntax: mr.Syntax, Definition: mr.Raw})
	}
	for _, mu := range sch.MatchingRuleUses {
		s.Context.MatchingRuleUse = append(s.Context.MatchingRuleUse,
			SchemaContextElement{OID: mu.OID, Names: nonEmpty(mu.Names), Desc: mu.Desc, Definition: mu.Raw})
	}
	for _, cr := range sch.DITContentRules {
		s.Context.DITContentRules = append(s.Context.DITContentRules,
			SchemaContextElement{OID: cr.OID, Names: nonEmpty(cr.Names), Desc: cr.Desc, Definition: cr.Raw})
	}
	for _, nf := range sch.NameForms {
		s.Context.NameForms = append(s.Context.NameForms,
			SchemaContextElement{OID: nf.OID, Names: nonEmpty(nf.Names), Desc: nf.Desc, Definition: nf.Raw})
	}

	s.canonicaliseSchema()
	if err := s.validateSchema(); err != nil {
		return nil, err
	}
	sum, err := s.schemaChecksum()
	if err != nil {
		return nil, err
	}
	s.Checksum = sum
	return s, nil
}

func errorText(err error) string {
	if err == nil {
		return "the definition could not be parsed"
	}
	return err.Error()
}

func schemaCoverage() SchemaCoverage {
	return SchemaCoverage{
		Compared:    append([]string{}, SchemaCompared...),
		Context:     append([]string{}, SchemaContextKind...),
		NotCaptured: append([]string{}, SchemaNotCaptured...),
	}
}

func nonEmpty(names []string) []string {
	var out []string
	for _, n := range names {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// canonicalAttributeType is the parsed form a snapshot holds.
//
// NAME keeps the definition's order, because the first name is the one
// servers and tools use; a comparison treats the names as a set. Matching
// rules, syntaxes and SUP keep their spelling; a comparison resolves them.
// Usage is always written out, so a default is not the absence of a field.
func canonicalAttributeType(at *schema.AttributeType, collection string) SchemaAttributeType {
	return SchemaAttributeType{
		OID: at.OID, Names: nonEmpty(at.Names), Desc: at.Desc, Obsolete: at.Obsolete, Sup: at.SuperName,
		Equality: at.Equality, Ordering: at.Ordering, Substr: at.Substr, Syntax: at.Syntax, SyntaxLength: at.SyntaxLen,
		SingleValue: at.SingleValue, Collective: at.Collective, NoUserModification: at.NoUserModification,
		Usage: at.Usage.String(), Extensions: canonicalExtensions(at.Extensions), Unrecognized: sortedUnique(at.Unrecognized),
		Collection: collection, Definition: strings.TrimSpace(at.Raw),
	}
}

// canonicalObjectClass is the parsed form a snapshot holds. SUP, MUST and MAY
// are sets, so they are held sorted, case-insensitively, without repeats.
func canonicalObjectClass(oc *schema.ObjectClass, collection string) SchemaObjectClass {
	return SchemaObjectClass{
		OID: oc.OID, Names: nonEmpty(oc.Names), Desc: oc.Desc, Obsolete: oc.Obsolete, Sup: sortedUnique(oc.SuperNames),
		Kind: oc.Kind.String(), Must: sortedUnique(oc.Must), May: sortedUnique(oc.May),
		Extensions: canonicalExtensions(oc.Extensions), Unrecognized: sortedUnique(oc.Unrecognized),
		Collection: collection, Definition: strings.TrimSpace(oc.Raw),
	}
}

// canonicalExtensions keeps each extension's values in the order written:
// an X-ORIGIN of two values is a list, not a set.
func canonicalExtensions(ext schema.Extensions) map[string][]string {
	if len(ext) == 0 {
		return nil
	}
	out := make(map[string][]string, len(ext))
	for k, v := range ext {
		out[strings.ToUpper(k)] = append([]string{}, v...)
	}
	return out
}

// sortedUnique sorts case-insensitively and drops case-insensitive repeats,
// keeping the first spelling.
func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if v == "" || seen[strings.ToLower(v)] {
			continue
		}
		seen[strings.ToLower(v)] = true
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// OIDLess orders OIDs numerically arc by arc, with non-numeric OIDs after the
// numeric ones, case-insensitively.
func OIDLess(a, b string) bool {
	na, nb := IsNumericOID(a), IsNumericOID(b)
	if na != nb {
		return na
	}
	if !na {
		return strings.ToLower(a) < strings.ToLower(b)
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] == pb[i] {
			continue
		}
		if len(pa[i]) != len(pb[i]) {
			return len(pa[i]) < len(pb[i])
		}
		return pa[i] < pb[i]
	}
	return len(pa) < len(pb)
}

// IsNumericOID reports an RFC 4512 numericoid: two or more arcs of digits,
// none with a leading zero.
func IsNumericOID(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 1 && p[0] == '0' {
			return false
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
	}
	return true
}

// validIdentity accepts a numericoid or the descriptor-style OID some servers
// publish for their own definitions ("nsHost-oid"). A comparison treats the
// second kind as identity it cannot act on.
func validIdentity(s string) bool {
	if IsNumericOID(s) {
		return true
	}
	if s == "" || len(s) > 256 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		digit := c >= '0' && c <= '9'
		if !letter && !digit && c != '-' && c != '.' && c != '_' && c != ';' {
			return false
		}
	}
	return true
}

func (s *SchemaSnapshot) canonicaliseSchema() {
	sort.SliceStable(s.AttributeTypes, func(i, j int) bool { return OIDLess(s.AttributeTypes[i].OID, s.AttributeTypes[j].OID) })
	sort.SliceStable(s.ObjectClasses, func(i, j int) bool { return OIDLess(s.ObjectClasses[i].OID, s.ObjectClasses[j].OID) })
	for _, list := range []*[]SchemaContextElement{&s.Context.LDAPSyntaxes, &s.Context.MatchingRules,
		&s.Context.MatchingRuleUse, &s.Context.DITContentRules, &s.Context.NameForms} {
		l := *list
		sort.SliceStable(l, func(i, j int) bool {
			if l[i].OID != l[j].OID {
				return OIDLess(l[i].OID, l[j].OID)
			}
			return l[i].Definition < l[j].Definition
		})
	}
	sort.SliceStable(s.Unparsed, func(i, j int) bool {
		if s.Unparsed[i].Attribute != s.Unparsed[j].Attribute {
			return s.Unparsed[i].Attribute < s.Unparsed[j].Attribute
		}
		return s.Unparsed[i].Definition < s.Unparsed[j].Definition
	})
	s.Counts = SchemaCounts{
		AttributeTypes: len(s.AttributeTypes), ObjectClasses: len(s.ObjectClasses),
		LDAPSyntaxes: len(s.Context.LDAPSyntaxes), MatchingRules: len(s.Context.MatchingRules),
		MatchingRuleUse: len(s.Context.MatchingRuleUse), DITContentRules: len(s.Context.DITContentRules),
		NameForms: len(s.Context.NameForms), Unparsed: len(s.Unparsed),
	}
	s.Completeness = SchemaComplete
	if len(s.Unparsed) > 0 {
		s.Completeness = SchemaPartial
	}
}

func (s *SchemaSnapshot) schemaChecksum() (string, error) {
	content := *s
	content.CreatedAt = ""
	content.Checksum = ""
	raw, err := json.Marshal(content)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// EncodeSchema writes the snapshot as its canonical document.
func EncodeSchema(w io.Writer, s *SchemaSnapshot) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// KindOf reads the kind a snapshot document claims, without validating the
// document. It is how a caller decides which decoder to hand it to.
func KindOf(data []byte) (string, error) {
	var probe struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
		Kind    string `json:"kind"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&probe); err != nil || probe.Format != Format {
		return "", &Error{Code: CodeNotSnapshot, Detail: "the document is not an Alder snapshot (no \"format\": \"alder-snapshot\")"}
	}
	return probe.Kind, nil
}

// DecodeSchema reads and validates a schema snapshot document.
//
// Everything is checked before it is used, as for a data snapshot: the format,
// version and kind, that no field is unknown and nothing follows the document,
// every OID, that no OID is captured twice, that the counts and coverage are
// what the document holds, and that every parsed form is what its definition
// parses to. A parsed form that disagrees with its text is refused rather than
// believed, since the comparison reads the parsed form and a person reads the
// text. Inheritance must not cycle. A checksum that is present must match.
func DecodeSchema(data []byte) (*SchemaSnapshot, Integrity, error) {
	var probe struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
		Kind    string `json:"kind"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&probe); err != nil || probe.Format != Format {
		return nil, "", &Error{Code: CodeNotSnapshot, Detail: "the document is not an Alder snapshot (no \"format\": \"alder-snapshot\")"}
	}
	if probe.Version > Version {
		return nil, "", &Error{Code: CodeUnsupportedVersion,
			Detail: fmt.Sprintf("snapshot version %d is newer than this Alder reads (%d); use a newer Alder", probe.Version, Version)}
	}
	if probe.Version < 1 {
		return nil, "", &Error{Code: CodeUnsupportedVersion, Detail: fmt.Sprintf("snapshot version %d does not exist", probe.Version)}
	}
	if probe.Kind != KindSchema {
		return nil, "", &Error{Code: CodeInvalid, Detail: fmt.Sprintf("kind %q is not a schema snapshot", probe.Kind)}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s SchemaSnapshot
	if err := dec.Decode(&s); err != nil {
		return nil, "", &Error{Code: CodeInvalid, Detail: err.Error()}
	}
	if dec.More() {
		return nil, "", &Error{Code: CodeInvalid, Detail: "the document continues after the snapshot"}
	}
	claimedCompleteness, claimedCounts := s.Completeness, s.Counts
	if err := s.validateSchema(); err != nil {
		return nil, "", err
	}
	claimed := s.Checksum
	s.canonicaliseSchema()
	if s.Completeness != claimedCompleteness {
		return nil, "", &Error{Code: CodeInvalid,
			Detail: fmt.Sprintf("completeness says %q and the document is %q", claimedCompleteness, s.Completeness)}
	}
	if s.Counts != claimedCounts {
		return nil, "", &Error{Code: CodeInvalid, Detail: "counts do not match what the document holds"}
	}
	sum, err := s.schemaChecksum()
	if err != nil {
		return nil, "", err
	}
	s.Checksum = sum
	if claimed == "" {
		return &s, IntegrityUnverified, nil
	}
	if claimed != sum {
		return nil, "", &Error{Code: CodeChecksumMismatch,
			Detail: "the content does not match its checksum: the file was corrupted or edited after capture"}
	}
	return &s, IntegrityVerified, nil
}

func (s *SchemaSnapshot) validateSchema() error {
	invalid := func(format string, args ...any) error {
		return &Error{Code: CodeInvalid, Detail: fmt.Sprintf(format, args...)}
	}
	if s.Format != Format || s.Version != Version || s.Kind != KindSchema {
		return invalid("not a version %d schema snapshot", Version)
	}
	if _, err := time.Parse(time.RFC3339, s.CreatedAt); err != nil {
		return invalid("createdAt %q is not an RFC 3339 time", s.CreatedAt)
	}
	if s.Completeness != SchemaComplete && s.Completeness != SchemaPartial {
		return invalid("completeness %q is not complete or partial", s.Completeness)
	}
	if _, err := dn.Parse(s.Source.SubschemaEntry); err != nil || strings.TrimSpace(s.Source.SubschemaEntry) == "" {
		return invalid("source.subschemaEntry %q is not a DN", s.Source.SubschemaEntry)
	}
	if !reflect.DeepEqual(s.Coverage, schemaCoverage()) {
		return invalid("coverage is not what a version 1 schema snapshot captures")
	}
	total := len(s.AttributeTypes) + len(s.ObjectClasses) + len(s.Context.LDAPSyntaxes) + len(s.Context.MatchingRules) +
		len(s.Context.MatchingRuleUse) + len(s.Context.DITContentRules) + len(s.Context.NameForms) + len(s.Unparsed)
	if total > MaxSchemaDefinitions {
		return &Error{Code: CodeTooLarge, Detail: fmt.Sprintf("%d definitions, and a schema snapshot holds at most %d", total, MaxSchemaDefinitions)}
	}
	if s.AttributeTypes == nil || s.ObjectClasses == nil || s.Unparsed == nil || s.Context.LDAPSyntaxes == nil ||
		s.Context.MatchingRules == nil || s.Context.MatchingRuleUse == nil || s.Context.DITContentRules == nil || s.Context.NameForms == nil {
		return invalid("a definition list is missing")
	}
	checkText := func(what, text string) error {
		if len(text) > maxDefinitionBytes {
			return invalid("%s is longer than %d bytes", what, maxDefinitionBytes)
		}
		if !utf8.ValidString(text) {
			return invalid("%s is not valid UTF-8", what)
		}
		return nil
	}

	seen := map[string]bool{}
	for i, stored := range s.AttributeTypes {
		where := fmt.Sprintf("attributeTypes[%d]", i)
		if !validIdentity(stored.OID) {
			return invalid("%s: %q is not an OID", where, stored.OID)
		}
		if seen[strings.ToLower(stored.OID)] {
			return invalid("%s: OID %s is captured twice", where, stored.OID)
		}
		seen[strings.ToLower(stored.OID)] = true
		if err := checkText(where+".definition", stored.Definition); err != nil {
			return err
		}
		if err := checkText(where+".collection", stored.Collection); err != nil {
			return err
		}
		parsed, err := schema.ParseAttributeType(stored.Definition)
		if err != nil {
			return invalid("%s: the definition does not parse: %v", where, err)
		}
		if want := canonicalAttributeType(parsed, stored.Collection); !reflect.DeepEqual(normaliseAT(want), normaliseAT(stored)) {
			return invalid("%s (%s): the parsed fields are not what the definition says", where, stored.OID)
		}
	}
	seen = map[string]bool{}
	for i, stored := range s.ObjectClasses {
		where := fmt.Sprintf("objectClasses[%d]", i)
		if !validIdentity(stored.OID) {
			return invalid("%s: %q is not an OID", where, stored.OID)
		}
		if seen[strings.ToLower(stored.OID)] {
			return invalid("%s: OID %s is captured twice", where, stored.OID)
		}
		seen[strings.ToLower(stored.OID)] = true
		if err := checkText(where+".definition", stored.Definition); err != nil {
			return err
		}
		if err := checkText(where+".collection", stored.Collection); err != nil {
			return err
		}
		parsed, err := schema.ParseObjectClass(stored.Definition)
		if err != nil {
			return invalid("%s: the definition does not parse: %v", where, err)
		}
		if want := canonicalObjectClass(parsed, stored.Collection); !reflect.DeepEqual(normaliseOC(want), normaliseOC(stored)) {
			return invalid("%s (%s): the parsed fields are not what the definition says", where, stored.OID)
		}
	}
	for name, list := range map[string][]SchemaContextElement{schema.AttrLDAPSyntaxes: s.Context.LDAPSyntaxes,
		schema.AttrMatchingRules: s.Context.MatchingRules, schema.AttrMatchingRuleUse: s.Context.MatchingRuleUse,
		schema.AttrDITContentRules: s.Context.DITContentRules, schema.AttrNameForms: s.Context.NameForms} {
		for i, e := range list {
			where := fmt.Sprintf("context.%s[%d]", name, i)
			if !validIdentity(e.OID) {
				return invalid("%s: %q is not an OID", where, e.OID)
			}
			for _, text := range append([]string{e.Definition, e.Desc, e.Syntax}, e.Names...) {
				if err := checkText(where, text); err != nil {
					return err
				}
			}
		}
	}
	for i, u := range s.Unparsed {
		where := fmt.Sprintf("unparsed[%d]", i)
		known := false
		for _, a := range append(append([]string{}, SchemaCompared...), SchemaContextKind...) {
			known = known || a == u.Attribute
		}
		if !known {
			return invalid("%s: %q is not a captured subschema attribute", where, u.Attribute)
		}
		for _, text := range []string{u.Definition, u.Error} {
			if err := checkText(where, text); err != nil {
				return err
			}
		}
	}
	if cycle := s.InheritanceCycle(); cycle != "" {
		return invalid("inheritance cycles: %s", cycle)
	}
	return nil
}

// normaliseAT and normaliseOC make empty and absent lists equal, which JSON
// does not distinguish in a way that matters here.
func normaliseAT(a SchemaAttributeType) SchemaAttributeType {
	if len(a.Names) == 0 {
		a.Names = nil
	}
	if len(a.Extensions) == 0 {
		a.Extensions = nil
	}
	if len(a.Unrecognized) == 0 {
		a.Unrecognized = nil
	}
	return a
}

func normaliseOC(o SchemaObjectClass) SchemaObjectClass {
	for _, l := range []*[]string{&o.Names, &o.Sup, &o.Must, &o.May, &o.Unrecognized} {
		if len(*l) == 0 {
			*l = nil
		}
	}
	if len(o.Extensions) == 0 {
		o.Extensions = nil
	}
	return o
}

// InheritanceCycle reports the first SUP cycle among attribute types or among
// object classes, as the OIDs around it, or "" when there is none. References
// resolve by OID or by any name; a reference to nothing ends a chain.
func (s *SchemaSnapshot) InheritanceCycle() string {
	atIndex := map[string]*SchemaAttributeType{}
	for i := range s.AttributeTypes {
		at := &s.AttributeTypes[i]
		atIndex[strings.ToLower(at.OID)] = at
		for _, n := range at.Names {
			if _, taken := atIndex[strings.ToLower(n)]; !taken {
				atIndex[strings.ToLower(n)] = at
			}
		}
	}
	for i := range s.AttributeTypes {
		seen := map[string]bool{}
		var path []string
		for at := &s.AttributeTypes[i]; at != nil; {
			key := strings.ToLower(at.OID)
			if seen[key] {
				return "attribute types " + strings.Join(append(path, at.OID), " -> ")
			}
			seen[key] = true
			path = append(path, at.OID)
			if at.Sup == "" {
				break
			}
			at = atIndex[strings.ToLower(at.Sup)]
		}
	}

	ocIndex := map[string]*SchemaObjectClass{}
	for i := range s.ObjectClasses {
		oc := &s.ObjectClasses[i]
		ocIndex[strings.ToLower(oc.OID)] = oc
		for _, n := range oc.Names {
			if _, taken := ocIndex[strings.ToLower(n)]; !taken {
				ocIndex[strings.ToLower(n)] = oc
			}
		}
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(oc *SchemaObjectClass) string
	visit = func(oc *SchemaObjectClass) string {
		key := strings.ToLower(oc.OID)
		switch state[key] {
		case visiting:
			return "object classes " + strings.Join(append(stack, oc.OID), " -> ")
		case done:
			return ""
		}
		state[key] = visiting
		stack = append(stack, oc.OID)
		for _, sup := range oc.Sup {
			if parent := ocIndex[strings.ToLower(sup)]; parent != nil {
				if found := visit(parent); found != "" {
					return found
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[key] = done
		return ""
	}
	for i := range s.ObjectClasses {
		if found := visit(&s.ObjectClasses[i]); found != "" {
			return found
		}
	}
	return ""
}

// ElementCount is how many definitions the snapshot holds, parsed or not.
func (s *SchemaSnapshot) ElementCount() int {
	c := s.Counts
	return c.AttributeTypes + c.ObjectClasses + c.LDAPSyntaxes + c.MatchingRules + c.MatchingRuleUse +
		c.DITContentRules + c.NameForms + c.Unparsed
}
